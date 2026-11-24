package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/analytics"
	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "approver" && user.Role != "admin" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	status := r.URL.Query().Get("status")
	items, err := s.DB.ListApprovals(r.Context(), status)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	if items == nil {
		items = []store.Approval{}
	}
	if s.GatewayURL != "" && s.ApproverToken != "" && (user.Role == "approver" || user.Role == "admin") {
		remote, err := s.gatewayApprovals(r.Context())
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": "gateway_unavailable"})
			return
		}
		seen := map[string]bool{}
		for _, item := range items {
			seen[item.ID] = true
		}
		for _, item := range remote {
			if !seen[item.ID] {
				items = append(items, item)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"approvals": items})
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	item, err := s.DB.GetApproval(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if user.Role != "approver" && user.Role != "admin" && item.UserID != user.ID {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "approver" {
		writeJSON(w, 403, map[string]string{"error": "approver_required"})
		return
	}
	var input struct {
		Decision string `json:"decision"`
	}
	if err := readJSON(r, &input); err != nil || (input.Decision != "approve" && input.Decision != "deny") {
		writeJSON(w, 400, map[string]string{"error": "invalid_decision"})
		return
	}
	current, err := s.DB.GetApproval(r.Context(), r.PathValue("id"))
	if err == nil && current.UserID == user.ID {
		writeJSON(w, 403, map[string]string{"error": "self_approval_forbidden"})
		return
	}
	if s.GatewayURL != "" && s.ApproverToken != "" {
		if forwardErr := s.forwardDecision(r.Context(), r.PathValue("id"), input.Decision); forwardErr != nil {
			writeJSON(w, 502, map[string]string{"error": "gateway_decision_failed"})
			return
		}
		if reloaded, reloadErr := s.DB.GetApproval(r.Context(), r.PathValue("id")); reloadErr == nil && reloaded.Status != "pending" {
			_ = s.auditManagement(r, user, "approval_"+input.Decision, reloaded.ID)
			writeJSON(w, 200, reloaded)
			return
		}
		if err != nil {
			_ = s.auditManagement(r, user, "approval_"+input.Decision, r.PathValue("id"))
			writeJSON(w, 200, map[string]string{"approval_id": r.PathValue("id"), "status": input.Decision})
			return
		}
	}
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	updated, err := s.DB.DecideApproval(r.Context(), current.ID, input.Decision, user.ID, time.Now().UTC())
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": "approval_already_decided"})
		return
	}
	_ = s.auditManagement(r, user, "approval_"+input.Decision, updated.ID)
	writeJSON(w, 200, updated)
}

func (s *Server) gatewayEndpoint(path string) string {
	endpoint := s.GatewayURL
	if len(endpoint) > 4 && endpoint[len(endpoint)-4:] == "/mcp" {
		endpoint = endpoint[:len(endpoint)-4]
	}
	return strings.TrimRight(endpoint, "/") + path
}

func (s *Server) gatewayApprovals(ctx context.Context) ([]store.Approval, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gatewayEndpoint("/approvals"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.ApproverToken)
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, store.ErrConflict
	}
	var items []store.Approval
	if err := json.NewDecoder(res.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Server) forwardDecision(ctx context.Context, id, decision string) error {
	body, _ := json.Marshal(map[string]string{"decision": decision})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.gatewayEndpoint("/approvals/"+id), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.ApproverToken)
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return store.ErrConflict
	}
	return nil
}

func (s *Server) getPolicies(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	_ = user
	restrictions, err := s.DB.ListRestrictions(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	var catalog []map[string]any
	for _, tool := range policy.All() {
		catalog = append(catalog, map[string]any{"name": tool.Name, "risk": tool.Risk, "roles": tool.Roles, "description": tool.Description, "server": tool.ServerID})
	}
	if restrictions == nil {
		restrictions = []store.Restriction{}
	}
	writeJSON(w, 200, map[string]any{"catalog": catalog, "restrictions": restrictions, "default": "deny"})
}

func (s *Server) putPolicies(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var input struct {
		Tool     string   `json:"tool"`
		Disabled bool     `json:"disabled"`
		Roles    []string `json:"roles"`
	}
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_policy"})
		return
	}
	tool, found := policy.ByName(input.Tool)
	if !found {
		writeJSON(w, 400, map[string]string{"error": "unknown_tool"})
		return
	}
	for _, role := range input.Roles {
		known := false
		for _, allowed := range tool.Roles {
			if role == allowed {
				known = true
			}
		}
		if !known {
			writeJSON(w, 400, map[string]string{"error": "role_expansion_rejected"})
			return
		}
	}
	item := store.Restriction{Tool: tool.Name, Disabled: input.Disabled, Roles: input.Roles, UpdatedBy: user.ID, UpdatedAt: time.Now().UTC()}
	if err := s.DB.SaveRestriction(r.Context(), item); err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	_ = s.auditManagement(r, user, "policy_restricted", tool.Name)
	writeJSON(w, 200, item)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	query := store.AuditQuery{UserID: user.ID, AdminAll: user.Role == "admin", Tool: r.URL.Query().Get("tool"), Outcome: r.URL.Query().Get("outcome"), Risk: r.URL.Query().Get("risk"), AgentID: r.URL.Query().Get("agent_id")}
	if user.Role != "admin" {
		query.UserID = user.ID
		query.AdminAll = false
	}
	if raw := r.URL.Query().Get("after"); raw != "" {
		query.AfterID, _ = strconv.ParseInt(raw, 10, 64)
	}
	query.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	query.From, query.To = parseRange(r)
	page, err := s.DB.QueryAudit(r.Context(), query)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	if page.Events == nil {
		page.Events = []store.AuditEvent{}
	}
	writeJSON(w, 200, page)
}

func (s *Server) analytics(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	from, to := parseRange(r)
	if to.Sub(from) > 31*24*time.Hour {
		writeJSON(w, 400, map[string]string{"error": "range_too_wide"})
		return
	}
	result, err := s.DB.Analytics(r.Context(), store.AnalyticsQuery{UserID: user.ID, From: from, To: to, AdminAll: user.Role == "admin"})
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	writeJSON(w, 200, result)
}

func parseRange(r *http.Request) (time.Time, time.Time) {
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)
	if raw := r.URL.Query().Get("from"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			from = parsed.UTC()
		}
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			to = parsed.UTC()
		}
	}
	return from, to
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" && user.Role != "developer" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	items, err := s.DB.ListFindings(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	if items == nil {
		items = []store.Finding{}
	}
	writeJSON(w, 200, map[string]any{"findings": items})
}

func (s *Server) scanFindings(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	now := time.Now().UTC()
	page, err := s.DB.QueryAudit(r.Context(), store.AuditQuery{AdminAll: true, From: now.Add(-15 * time.Minute), To: now.Add(time.Second), Limit: 200})
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	created := 0
	for _, finding := range analytics.Scan(page.Events, now) {
		finding.ID = "finding-" + store.NewID()[:12]
		if err := s.DB.SaveFinding(r.Context(), finding); err == nil {
			created++
		}
	}
	_ = s.auditManagement(r, user, "findings_scanned", "")
	writeJSON(w, 200, map[string]any{"created": created})
}

func (s *Server) findingStatus(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_status"})
		return
	}
	finding, err := s.DB.UpdateFinding(r.Context(), r.PathValue("id"), input.Status)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	_ = s.auditManagement(r, user, "finding_"+input.Status, finding.ID)
	writeJSON(w, 200, finding)
}

func (s *Server) explainFinding(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" && user.Role != "developer" {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	items, err := s.DB.ListFindings(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store_failed"})
		return
	}
	var finding store.Finding
	found := false
	for _, item := range items {
		if item.ID == r.PathValue("id") {
			finding = item
			found = true
		}
	}
	if !found {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	explanation := analytics.FallbackExplanation(finding)
	source := "rule"
	if s.Ollama != nil && s.Model != "" {
		prompt := "Explain this security finding for an operator. Use only these fields. Do not invent incidents. Rule: " + finding.Rule + ". Summary: " + finding.Summary + ". Evidence: " + string(finding.Evidence)
		if text, err := s.Ollama.Summarize(r.Context(), s.Model, prompt); err == nil && text != "" {
			explanation = text
			source = "ollama"
		}
	}
	_ = s.DB.SetFindingExplanation(r.Context(), finding.ID, explanation)
	finding.Explanation = explanation
	writeJSON(w, 200, map[string]any{"finding": finding, "source": source, "advisory": true})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.require(w, r)
	if !ok {
		return
	}
	models := []string{}
	ollama := "disconnected"
	if s.Ollama != nil {
		if listed, err := s.Ollama.Models(r.Context()); err == nil {
			ollama = "connected"
			for _, model := range listed {
				models = append(models, model.Name)
			}
		}
	}
	if s.ProviderName == "fake" {
		ollama = "fake_provider"
	}
	writeJSON(w, 200, map[string]any{
		"provider": s.ProviderName, "model": s.Model, "models": models, "ollama": ollama,
		"user": publicUser(user), "paid_fallback": false, "rate_limit_per_minute": 20, "max_steps": 8,
	})
}

func (s *Server) auditManagement(r *http.Request, user store.User, action, target string) error {
	return s.DB.WriteAudit(r.Context(), store.AuditEvent{
		RequestID: store.NewID(), OccurredAt: time.Now().UTC(), UserID: user.ID, Role: user.Role,
		Tool: target, Service: "workspace", Decision: "management", Reason: action, Outcome: "success",
	})
}
