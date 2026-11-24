package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/agent"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

type recordingCaller struct {
	calls int
}

func (c *recordingCaller) Call(_ context.Context, tool string, _ json.RawMessage, approvalID string) (agent.Result, error) {
	c.calls++
	if tool == "admin.mock_destructive_action" && approvalID == "" {
		return agent.Result{IsError: true, Code: "approval_required", ApprovalID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
	}
	if tool == "utility.add" {
		return agent.Result{Text: "57", RequestID: "request-add"}, nil
	}
	if approvalID != "" {
		return agent.Result{Text: "Mock maintenance completed; no real data was changed.", RequestID: "request-risk"}, nil
	}
	return agent.Result{IsError: true, Code: "unauthorized", RequestID: "request-deny"}, nil
}

func newTestServer(t *testing.T) (*httptest.Server, *recordingCaller, *store.Memory) {
	t.Helper()
	db := store.NewMemory()
	caller := &recordingCaller{}
	server := &Server{DB: db, DevLogin: true, DevPassword: "dev-only-password-value", PublicURL: "http://127.0.0.1", Provider: agent.Fake{}, ProviderName: "fake", Model: "fake", Caller: caller}
	if err := server.SeedCatalog(t.Context()); err != nil || server.SeedDev(t.Context()) != nil {
		t.Fatal("seed")
	}
	return httptest.NewServer(server.Handler()), caller, db
}

type memoryJar struct {
	mu      sync.Mutex
	cookies []*http.Cookie
}

func (j *memoryJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = append(j.cookies, cookies...)
}
func (j *memoryJar) Cookies(_ *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]*http.Cookie(nil), j.cookies...)
}

func login(t *testing.T, base, username string) (*http.Client, string) {
	t.Helper()
	client := &http.Client{Jar: &memoryJar{}}
	body, _ := json.Marshal(map[string]string{"username": username, "password": "dev-only-password-value"})
	res, err := client.Post(base+"/api/v1/auth/dev-login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login %s status %d", username, res.StatusCode)
	}
	var payload struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return client, payload.CSRF
}

func authed(t *testing.T, client *http.Client, method, url, csrf string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestChatApprovalAndOwnership(t *testing.T) {
	ts, caller, db := newTestServer(t)
	defer ts.Close()
	ada, adaCSRF := login(t, ts.URL, "ada")
	agentsRes := authed(t, ada, http.MethodGet, ts.URL+"/api/v1/agents", "", nil)
	var agents struct {
		Agents []store.Agent `json:"agents"`
	}
	decode(t, agentsRes, &agents)
	var assistant store.Agent
	for _, agentRecord := range agents.Agents {
		if agentRecord.Name == "Workspace Assistant" {
			assistant = agentRecord
		}
	}
	if assistant.ID == "" {
		t.Fatal("template agent missing")
	}
	conversationRes := authed(t, ada, http.MethodPost, ts.URL+"/api/v1/conversations", adaCSRF, map[string]string{"agent_id": assistant.ID, "title": "Math"})
	var conversation store.Conversation
	decode(t, conversationRes, &conversation)
	runRes := authed(t, ada, http.MethodPost, ts.URL+"/api/v1/conversations/"+conversation.ID+"/runs", adaCSRF, map[string]string{"prompt": "Add 22 and 35"})
	var run store.Run
	decode(t, runRes, &run)
	waitStatus(t, ada, ts.URL, run.ID, "completed")
	if caller.calls != 1 {
		t.Fatalf("tool calls %d", caller.calls)
	}
	vera, veraCSRF := login(t, ts.URL, "vera")
	denied := authed(t, vera, http.MethodGet, ts.URL+"/api/v1/conversations/"+conversation.ID, "", nil)
	if denied.StatusCode != 404 {
		t.Fatalf("cross-user status %d", denied.StatusCode)
	}
	denied.Body.Close()
	noCSRF := authed(t, ada, http.MethodPost, ts.URL+"/api/v1/conversations", "", map[string]string{"agent_id": assistant.ID})
	if noCSRF.StatusCode != 403 {
		t.Fatalf("csrf status %d", noCSRF.StatusCode)
	}
	noCSRF.Body.Close()
	_ = veraCSRF
	_ = db
}

func TestApprovalPauseAndOperator(t *testing.T) {
	ts, caller, db := newTestServer(t)
	defer ts.Close()
	ada, adaCSRF := login(t, ts.URL, "ada")
	now := time.Now().UTC()
	agentRecord := store.Agent{ID: "agent-risk", OwnerUserID: "user-ada", Name: "Maintainer", Description: "high risk demo", SystemPrompt: "Use the mock tool only when asked.", Model: "fake", Tools: []string{"admin.mock_destructive_action"}, MaxSteps: 4, Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAgent(t.Context(), agentRecord); err != nil {
		t.Fatal(err)
	}
	conversation := store.Conversation{ID: "conv-risk", OwnerUserID: "user-ada", AgentID: agentRecord.ID, Title: "Risk", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateConversation(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	runRes := authed(t, ada, http.MethodPost, ts.URL+"/api/v1/conversations/"+conversation.ID+"/runs", adaCSRF, map[string]string{"prompt": "Run maintenance"})
	var run store.Run
	decode(t, runRes, &run)
	waitStatus(t, ada, ts.URL, run.ID, "waiting_for_approval")
	approval := store.Approval{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UserID: "user-ada", Role: "developer", Tool: "admin.mock_destructive_action", Arguments: json.RawMessage(`{"target":"demo"}`), Status: "pending", ExecutionOutcome: "not_run", ExpiresAt: time.Now().Add(time.Minute), CreatedAt: now}
	if err := db.CreateApproval(t.Context(), approval); err != nil {
		t.Fatal(err)
	}
	self := authed(t, ada, http.MethodPost, ts.URL+"/api/v1/approvals/"+approval.ID+"/decision", adaCSRF, map[string]string{"decision": "approve"})
	if self.StatusCode != 403 {
		t.Fatalf("self approval %d", self.StatusCode)
	}
	self.Body.Close()
	owen, owenCSRF := login(t, ts.URL, "owen")
	decision := authed(t, owen, http.MethodPost, ts.URL+"/api/v1/approvals/"+approval.ID+"/decision", owenCSRF, map[string]string{"decision": "approve"})
	if decision.StatusCode != 200 {
		t.Fatalf("operator decision %d", decision.StatusCode)
	}
	decision.Body.Close()
	resume := authed(t, ada, http.MethodPost, ts.URL+"/api/v1/runs/"+run.ID+"/resume", adaCSRF, map[string]any{})
	if resume.StatusCode != 202 {
		t.Fatalf("resume %d", resume.StatusCode)
	}
	resume.Body.Close()
	waitStatus(t, ada, ts.URL, run.ID, "completed")
	if caller.calls < 2 {
		t.Fatalf("calls %d", caller.calls)
	}
}

func waitStatus(t *testing.T, client *http.Client, base, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res := authed(t, client, http.MethodGet, base+"/api/v1/runs/"+id, "", nil)
		var run store.Run
		decode(t, res, &run)
		if run.Status == want {
			return
		}
		if run.Status == "failed" || run.Status == "cancelled" {
			t.Fatalf("run %s: %s", run.Status, run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", want)
}

func decode(t *testing.T, res *http.Response, dest any) {
	t.Helper()
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}
	if err := json.NewDecoder(res.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}
