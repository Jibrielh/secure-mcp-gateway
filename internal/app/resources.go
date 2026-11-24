package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	owner := user.ID
	if user.Role == "admin" {
		owner = ""
	}
	agents, err := s.DB.ListAgents(r.Context(), owner)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	if agents == nil {
		agents = []store.Agent{}
	}
	writeJSON(w, 200, map[string]any{"agents": agents})
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role == "viewer" || user.Role == "approver" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var input store.Agent
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_agent"})
		return
	}
	agent, err := s.validatedAgent(user, input, true)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	agent.ID = "agent-" + store.NewID()[:12]
	agent.CreatedAt = time.Now().UTC()
	agent.UpdatedAt = agent.CreatedAt
	agent.Version = 1
	agent.Enabled = true
	if err := s.DB.CreateAgent(r.Context(), agent); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	_ = s.auditManagement(r, user, "agent_created", agent.ID)
	writeJSON(w, 201, agent)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	agent, err := s.DB.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil || !owns(user, agent.OwnerUserID) {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	runs, _ := s.DB.ListRuns(r.Context(), agent.OwnerUserID, agent.ID)
	writeJSON(w, 200, map[string]any{"agent": agent, "runs": runs, "effective_tools": s.effective(user, agent)})
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	current, err := s.DB.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil || current.OwnerUserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	var input store.Agent
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_agent"})
		return
	}
	input.ID = current.ID
	input.OwnerUserID = current.OwnerUserID
	input.CreatedAt = current.CreatedAt
	agent, err := s.validatedAgent(user, input, false)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	agent.UpdatedAt = time.Now().UTC()
	if err := s.DB.UpdateAgent(r.Context(), agent); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	_ = s.auditManagement(r, user, "agent_updated", agent.ID)
	stored, _ := s.DB.GetAgent(r.Context(), agent.ID)
	writeJSON(w, 200, stored)
}

func (s *Server) cloneAgent(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	current, err := s.DB.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil || !owns(user, current.OwnerUserID) {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	current.ID = "agent-" + store.NewID()[:12]
	current.OwnerUserID = user.ID
	current.Name += " copy"
	current.Enabled = true
	current.Version = 1
	current.CreatedAt = time.Now().UTC()
	current.UpdatedAt = current.CreatedAt
	if err := s.DB.CreateAgent(r.Context(), current); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	writeJSON(w, 201, current)
}

func (s *Server) disableAgent(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	agent, err := s.DB.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil || agent.OwnerUserID != user.ID && user.Role != "admin" {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	agent.Enabled = false
	agent.UpdatedAt = time.Now().UTC()
	if user.Role == "admin" && agent.OwnerUserID != user.ID {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	if err := s.DB.UpdateAgent(r.Context(), agent); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	writeJSON(w, 200, agent)
}

func (s *Server) validatedAgent(user store.User, agent store.Agent, creating bool) (store.Agent, error) {
	agent.OwnerUserID = user.ID
	if len(agent.Name) < 2 || len(agent.Name) > 80 || len(agent.SystemPrompt) > 4000 || len(agent.Description) > 300 {
		return store.Agent{}, errString("invalid_agent")
	}
	if agent.MaxSteps == 0 {
		agent.MaxSteps = 4
	}
	if agent.MaxSteps < 1 || agent.MaxSteps > 8 {
		return store.Agent{}, errString("invalid_limits")
	}
	if agent.Model == "" {
		agent.Model = s.Model
	}
	seen := map[string]bool{}
	var tools []string
	for _, name := range agent.Tools {
		tool, ok := policy.ByName(name)
		if !ok || seen[name] {
			return store.Agent{}, errString("unknown_tool")
		}
		identity := security.Identity{ID: user.ID, Role: user.Role}
		if !security.Authorized(identity, security.Policy{Risk: tool.Risk, Roles: tool.Roles}) {
			return store.Agent{}, errString("tool_not_permitted")
		}
		seen[name] = true
		tools = append(tools, name)
	}
	agent.Tools = tools
	if creating && len(agent.Tools) == 0 {
		return store.Agent{}, errString("tools_required")
	}
	return agent, nil
}

func (s *Server) effective(user store.User, agent store.Agent) []string {
	var names []string
	restrictions, _ := s.DB.ListRestrictions(context.Background())
	for _, name := range agent.Tools {
		tool, ok := policy.ByName(name)
		if !ok {
			continue
		}
		roles := tool.Roles
		for _, restriction := range restrictions {
			if restriction.Tool == name {
				if restriction.Disabled {
					roles = nil
				} else {
					roles = intersect(roles, restriction.Roles)
				}
			}
		}
		if security.Authorized(security.Identity{ID: user.ID, Role: user.Role}, security.Policy{Risk: tool.Risk, Roles: roles}) {
			names = append(names, name)
		}
	}
	return names
}

func intersect(a, b []string) []string {
	var result []string
	for _, item := range a {
		if slices.Contains(b, item) {
			result = append(result, item)
		}
	}
	return result
}

type errString string

func (e errString) Error() string { return string(e) }

func (s *Server) tools(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	restrictions, _ := s.DB.ListRestrictions(r.Context())
	var tools []map[string]any
	for _, tool := range policy.All() {
		disabled := false
		roles := append([]string(nil), tool.Roles...)
		for _, restriction := range restrictions {
			if restriction.Tool == tool.Name {
				disabled = restriction.Disabled
				if restriction.Roles != nil {
					roles = intersect(tool.Roles, restriction.Roles)
				}
			}
		}
		allowed := security.Authorized(security.Identity{ID: user.ID, Role: user.Role}, security.Policy{Risk: tool.Risk, Roles: roles}) && !disabled
		if user.Role != "admin" && !allowed {
			continue
		}
		tools = append(tools, map[string]any{
			"name": tool.Name, "provider_name": tool.ProviderName, "server": tool.ServerID, "risk": tool.Risk,
			"roles": roles, "description": tool.Description, "schema": jsonRaw(tool.InputSchema), "allowed": allowed, "disabled": disabled,
		})
	}
	if tools == nil {
		tools = []map[string]any{}
	}
	writeJSON(w, 200, map[string]any{"tools": tools})
}

func jsonRaw(raw []byte) any {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func (s *Server) integrations(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.require(w, r); !ok {
		return
	}
	items, err := s.DB.ListIntegrations(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	if items == nil {
		items = []store.Integration{}
	}
	writeJSON(w, 200, map[string]any{"integrations": items})
}

func (s *Server) setIntegration(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_integration"})
		return
	}
	items, _ := s.DB.ListIntegrations(r.Context())
	var current store.Integration
	found := false
	for _, item := range items {
		if item.ID == r.PathValue("id") {
			current = item
			found = true
		}
	}
	if !found {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	current.Enabled = input.Enabled
	current.UpdatedAt = time.Now().UTC()
	if err := s.DB.SaveIntegration(r.Context(), current); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	_ = s.auditManagement(r, user, "integration_updated", current.ID)
	writeJSON(w, 200, current)
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	items, err := s.DB.ListConversations(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	if items == nil {
		items = []store.Conversation{}
	}
	writeJSON(w, 200, map[string]any{"conversations": items})
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	var input struct {
		AgentID string `json:"agent_id"`
		Title   string `json:"title"`
	}
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_conversation"})
		return
	}
	agent, err := s.DB.GetAgent(r.Context(), input.AgentID)
	if err != nil || agent.OwnerUserID != user.ID || !agent.Enabled {
		writeJSON(w, 404, map[string]string{"error": "agent_not_found"})
		return
	}
	if input.Title == "" {
		input.Title = "New conversation"
	}
	if len(input.Title) > 120 {
		writeJSON(w, 400, map[string]string{"error": "invalid_conversation"})
		return
	}
	now := time.Now().UTC()
	conversation := store.Conversation{ID: "conv-" + store.NewID()[:12], OwnerUserID: user.ID, AgentID: agent.ID, Title: input.Title, CreatedAt: now, UpdatedAt: now}
	if err := s.DB.CreateConversation(r.Context(), conversation); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	writeJSON(w, 201, conversation)
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	conversation, err := s.DB.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil || conversation.OwnerUserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	messages, _ := s.DB.ListMessages(r.Context(), conversation.ID)
	if messages == nil {
		messages = []store.Message{}
	}
	writeJSON(w, 200, map[string]any{"conversation": conversation, "messages": messages})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	conversation, err := s.DB.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil || conversation.OwnerUserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if err := s.DB.DeleteConversation(r.Context(), conversation.ID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}
