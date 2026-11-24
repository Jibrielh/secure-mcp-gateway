package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/agent"
	"github.com/Jibrielh/secure-mcp-gateway/internal/mcpclient"
	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	conversation, err := s.DB.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil || conversation.OwnerUserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	var input struct {
		Prompt string `json:"prompt"`
	}
	if err := readJSON(r, &input); err != nil || len(input.Prompt) == 0 || len(input.Prompt) > 8000 {
		writeJSON(w, 400, map[string]string{"error": "invalid_prompt"})
		return
	}
	agentRecord, err := s.DB.GetAgent(r.Context(), conversation.AgentID)
	if err != nil || !agentRecord.Enabled || agentRecord.OwnerUserID != user.ID {
		writeJSON(w, 409, map[string]string{"error": "agent_disabled"})
		return
	}
	now := time.Now().UTC()
	_ = s.DB.AddMessage(r.Context(), store.Message{ID: store.NewID(), ConversationID: conversation.ID, Role: "user", Body: input.Prompt, CreatedAt: now})
	if conversation.Title == "New conversation" {
		title := input.Prompt
		if len(title) > 80 {
			title = title[:80]
		}
		_ = s.DB.RenameConversation(r.Context(), conversation.ID, user.ID, title, now)
	}
	run := store.Run{ID: "run-" + store.NewID()[:12], ConversationID: conversation.ID, OwnerUserID: user.ID, AgentID: agentRecord.ID, Status: "queued", ProviderState: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := s.DB.CreateRun(r.Context(), run); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	go s.execute(context.Background(), run.ID, input.Prompt, false)
	writeJSON(w, 202, run)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	run, err := s.ownedRun(r.Context(), user, r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, 200, run)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.user(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthenticated"})
		return
	}
	run, err := s.ownedRun(r.Context(), user, r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "stream_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	after := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		after, _ = strconv.ParseInt(raw, 10, 64)
	}
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	updates := s.broker.subscribe(run.ID)
	defer s.broker.unsubscribe(run.ID, updates)
	for {
		events, err := s.DB.ListEvents(r.Context(), run.ID, after)
		if err != nil {
			return
		}
		for _, event := range events {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, event.Payload)
			after = event.Seq
		}
		flusher.Flush()
		current, err := s.DB.GetRun(r.Context(), run.ID)
		if err != nil {
			return
		}
		if current.Status == "completed" || current.Status == "failed" || current.Status == "cancelled" || current.Status == "waiting_for_approval" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-updates:
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	run, err := s.ownedRun(r.Context(), user, r.PathValue("id"))
	if err != nil || run.OwnerUserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if cancel, ok := s.cancels.Load(run.ID); ok {
		cancel.(context.CancelFunc)()
	}
	if run.Status == "queued" || run.Status == "running" || run.Status == "waiting_for_approval" {
		run.Status = "cancelled"
		run.UpdatedAt = time.Now().UTC()
		_ = s.DB.UpdateRun(r.Context(), run)
		_, _ = s.append(run.ID, "run.cancelled", map[string]any{})
	}
	writeJSON(w, 200, run)
}

func (s *Server) resumeRun(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	run, err := s.ownedRun(r.Context(), user, r.PathValue("id"))
	if err != nil || run.OwnerUserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if run.Status != "waiting_for_approval" {
		writeJSON(w, 409, map[string]string{"error": "not_waiting"})
		return
	}
	go s.execute(context.Background(), run.ID, "", true)
	writeJSON(w, 202, map[string]string{"status": "resuming"})
}

func (s *Server) ownedRun(ctx context.Context, user store.User, id string) (store.Run, error) {
	run, err := s.DB.GetRun(ctx, id)
	if err != nil || !owns(user, run.OwnerUserID) {
		return store.Run{}, store.ErrNotFound
	}
	return run, nil
}

func (s *Server) execute(ctx context.Context, runID, prompt string, resume bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s.cancels.Store(runID, cancel)
	defer s.cancels.Delete(runID)
	run, err := s.DB.GetRun(ctx, runID)
	if err != nil {
		return
	}
	user, err := s.DB.FindUser(ctx, run.OwnerUserID)
	if err != nil {
		return
	}
	agentRecord, err := s.DB.GetAgent(ctx, run.AgentID)
	if err != nil {
		return
	}
	state := agent.Run{Status: "running"}
	if resume && len(run.ProviderState) > 0 {
		_ = json.Unmarshal(run.ProviderState, &state)
	} else {
		state.Messages = []agent.Message{{Role: "user", Content: prompt}}
	}
	run.Status = "running"
	run.UpdatedAt = time.Now().UTC()
	_ = s.DB.UpdateRun(ctx, run)
	tools := []agent.ToolSchema{}
	for _, name := range s.effective(user, agentRecord) {
		tool, ok := policy.ByName(name)
		if !ok {
			continue
		}
		tools = append(tools, agent.ToolSchema{Name: tool.ProviderName, Description: tool.Description, Parameters: tool.InputSchema})
	}
	engine := agent.Engine{Provider: s.Provider, Caller: s.callerFor(user, agentRecord, run.ID), MaxSteps: agentRecord.MaxSteps}
	if engine.Provider == nil {
		engine.Provider = agent.Fake{}
	}
	finished := engine.Execute(ctx, agentRecord.Model, agentRecord.SystemPrompt, state, tools, func(event agent.Event) {
		_, _ = s.append(run.ID, event.Type, event.Payload)
		if event.Type == "tool.completed" || event.Type == "tool.blocked" || event.Type == "tool.failed" {
			name, _ := event.Payload["name"].(string)
			tool, _ := policy.ByName(name)
			if s.Metrics != nil {
				s.Metrics.Requests.WithLabelValues(tool.ServerID, event.Type).Inc()
			}
		}
	})
	encoded, _ := json.Marshal(finished)
	run.ProviderState = encoded
	run.Status = finished.Status
	run.Error = finished.Error
	run.UpdatedAt = time.Now().UTC()
	_ = s.DB.UpdateRun(context.Background(), run)
	if finished.Status == "completed" {
		var text string
		if len(finished.Messages) > 0 {
			text = finished.Messages[len(finished.Messages)-1].Content
		}
		_ = s.DB.AddMessage(context.Background(), store.Message{ID: store.NewID(), ConversationID: run.ConversationID, Role: "assistant", Body: text, CreatedAt: time.Now().UTC()})
	}
	if s.Metrics != nil {
		s.Metrics.Runs.WithLabelValues(finished.Status).Inc()
	}
}

func (s *Server) append(runID, eventType string, payload map[string]any) (store.RunEvent, error) {
	encoded, _ := json.Marshal(payload)
	event, err := s.DB.AppendEvent(context.Background(), store.RunEvent{RunID: runID, Type: eventType, Payload: encoded, CreatedAt: time.Now().UTC()})
	s.broker.notify(runID)
	return event, err
}

func (s *Server) callerFor(user store.User, agentRecord store.Agent, runID string) agent.Caller {
	if s.Caller != nil {
		return s.Caller
	}
	return mcpCaller{server: s, user: user, agent: agentRecord, runID: runID}
}

type mcpCaller struct {
	server *Server
	user   store.User
	agent  store.Agent
	runID  string
}

func (c mcpCaller) Call(ctx context.Context, tool string, args json.RawMessage, approvalID string) (agent.Result, error) {
	if c.server.Delegation == nil || c.server.GatewayURL == "" {
		return agent.Result{}, fmt.Errorf("gateway is not configured")
	}
	identity := security.Identity{ID: c.user.ID, Role: c.user.Role, UserID: c.user.ID, AgentID: c.agent.ID, RunID: c.runID, Tools: c.server.effective(c.user, c.agent)}
	token, err := c.server.Delegation.Mint(identity, 2*time.Minute)
	if err != nil {
		return agent.Result{}, err
	}
	session, err := mcpclient.Connect(ctx, c.server.GatewayURL, token)
	if err != nil {
		return agent.Result{}, err
	}
	defer session.Close()
	params := &mcp.CallToolParams{Name: tool, Arguments: args}
	if approvalID != "" {
		params.Meta = mcp.Meta{security.ApprovalMeta: approvalID}
	}
	result, err := session.CallTool(ctx, params)
	if err != nil {
		return agent.Result{}, err
	}
	return decodeToolResult(result), nil
}

func decodeToolResult(result *mcp.CallToolResult) agent.Result {
	var text bytes.Buffer
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text.WriteString(item.Text)
		}
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	code, _ := fields["code"].(string)
	requestID, _ := fields["request_id"].(string)
	if requestID == "" && result.Meta != nil {
		requestID, _ = result.Meta[security.RequestIDMeta].(string)
	}
	approval, _ := fields["approval_id"].(string)
	if approval == "" && result.Meta != nil {
		approval, _ = result.Meta[security.ApprovalMeta].(string)
	}
	return agent.Result{Text: text.String(), IsError: result.IsError, Code: code, ApprovalID: approval, RequestID: requestID}
}
