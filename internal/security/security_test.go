package security

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testAuth(t *testing.T) (*Auth, map[string]string, string) {
	t.Helper()
	tokens := map[string]string{}
	random := func() string { var raw [32]byte; rand.Read(raw[:]); return hex.EncodeToString(raw[:]) }
	for _, role := range []string{"viewer", "developer", "admin"} {
		tokens[role] = random()
	}
	approver := random()
	auth, err := NewAuth(tokens, approver)
	if err != nil {
		t.Fatal(err)
	}
	return auth, tokens, approver
}
func TestAuthenticationAndRBAC(t *testing.T) {
	auth, tokens, approver := testAuth(t)
	for _, header := range []string{"", "Bearer invalid", "Basic " + tokens["viewer"], "Bearer " + approver} {
		if _, ok := auth.Authenticate(header); ok {
			t.Fatal("accepted invalid MCP credential")
		}
	}
	for role, token := range tokens {
		identity, ok := auth.Authenticate("Bearer " + token)
		if !ok || identity.Role != role || identity.ID != role+"-user" {
			t.Fatal("valid credential did not resolve expected identity")
		}
	}
	if !auth.IsApprover("Bearer "+approver) || auth.IsApprover("Bearer "+tokens["admin"]) {
		t.Fatal("approver boundary failed")
	}
	policy := Policy{Server: "utility", Risk: "high", Roles: []string{"developer"}}
	if Authorized(Identity{ID: "viewer-user", Role: "viewer"}, policy) {
		t.Fatal("viewer authorized for high-risk tool")
	}
	if !Authorized(Identity{ID: "admin-user", Role: "admin"}, policy) {
		t.Fatal("admin wildcard rejected")
	}
	if Authorized(Identity{ID: "someone", Role: "unknown"}, policy) || Authorized(Identity{ID: "admin-user", Role: "admin"}, Policy{Risk: "unknown"}) {
		t.Fatal("uncertain policy did not fail closed")
	}
	tokens["admin"] = tokens["viewer"]
	if _, err := NewAuth(tokens, approver); err == nil {
		t.Fatal("accepted duplicate tokens")
	}
	if _, err := NewAuth(map[string]string{}, approver); err == nil {
		t.Fatal("accepted missing tokens")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	now := time.Now()
	if !l.Allow("a", now) || !l.Allow("a", now) || l.Allow("a", now) {
		t.Fatal("limit=2 failed")
	}
	if !l.Allow("b", now) {
		t.Fatal("identities shared a limit")
	}
	if !l.Allow("a", now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
	concurrent := NewLimiter(2, time.Minute)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if concurrent.Allow("a", now) {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 2 {
		t.Fatalf("concurrent allowed count %d", allowed.Load())
	}
}

func TestApprovalBindingAndExpiry(t *testing.T) {
	a := NewApprovals()
	now := time.Now()
	identity := Identity{ID: "developer-user", Role: "developer"}
	id, err := a.Create(NewRequestID(), identity, "admin.mock", json.RawMessage(`{"a":1,"b":2}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Decide(id, "approve", now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		identity   Identity
		tool, args string
	}{
		{Identity{ID: "admin-user", Role: "admin"}, "admin.mock", `{"a":1,"b":2}`},
		{identity, "different", `{"a":1,"b":2}`},
		{identity, "admin.mock", `{"a":9,"b":2}`},
	} {
		if got := a.Consume(id, tc.identity, tc.tool, json.RawMessage(tc.args), now); got != "approval_mismatch" {
			t.Fatal("approval binding failed")
		}
	}
	var consumed atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.Consume(id, identity, "admin.mock", json.RawMessage(`{"b":2,"a":1}`), now) == "" {
				consumed.Add(1)
			}
		}()
	}
	wg.Wait()
	if consumed.Load() != 1 {
		t.Fatal("approval was not consumed exactly once")
	}
	expired, _ := a.Create(NewRequestID(), identity, "admin.mock", json.RawMessage(`{}`), now)
	if _, err := a.Decide(expired, "approve", now.Add(5*time.Minute)); err == nil {
		t.Fatal("expired approval accepted")
	}
}

func TestSecurityPipelineAndAudit(t *testing.T) {
	auth, tokens, _ := testAuth(t)
	var logs bytes.Buffer
	gate := NewGate(auth, NewAudit(&logs), 20, map[string]Policy{
		"utility.add": {Server: "utility", Risk: "low", Roles: []string{"viewer", "developer"}},
		"admin.mock":  {Server: "utility", Risk: "high", Roles: []string{"developer"}},
	})
	gate.ApprovalEnabled = true
	count := 0
	next := func(_ context.Context, _ string, req mcp.Request) (mcp.Result, error) {
		if req.(*mcp.CallToolRequest).Params.Name == "unknown" {
			return nil, errors.New("unknown tool")
		}
		count++
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "downstream"}}}, nil
	}
	call := func(token, tool, approval string) *mcp.CallToolResult {
		t.Helper()
		meta := mcp.Meta{AuthorizationMeta: "Bearer " + token}
		if approval != "" {
			meta[ApprovalMeta] = approval
		}
		result, err := gate.Middleware(next)(t.Context(), "tools/call", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Arguments: json.RawMessage(`{}`), Meta: meta}})
		if err != nil {
			t.Fatal(err)
		}
		return result.(*mcp.CallToolResult)
	}
	code := func(result *mcp.CallToolResult) string {
		fields, _ := result.StructuredContent.(map[string]any)
		value, _ := fields["code"].(string)
		return value
	}
	if code(call("", "utility.add", "")) != "unauthenticated" || code(call("invalid", "utility.add", "")) != "unauthenticated" {
		t.Fatal("auth rejection failed")
	}
	if call(tokens["viewer"], "utility.add", "").IsError || count != 1 {
		t.Fatal("allowed call did not forward")
	}
	if code(call(tokens["viewer"], "admin.mock", "")) != "unauthorized" || count != 1 {
		t.Fatal("denied call forwarded")
	}
	pending := call(tokens["developer"], "admin.mock", "")
	approval, _ := pending.Meta[ApprovalMeta].(string)
	if code(pending) != "approval_required" || approval == "" || count != 1 {
		t.Fatal("unapproved call forwarded")
	}
	if _, err := gate.Approvals.Decide(approval, "deny", gate.Now()); err != nil {
		t.Fatal(err)
	}
	if code(call(tokens["developer"], "admin.mock", approval)) != "approval_denied" || count != 1 {
		t.Fatal("denied approval forwarded")
	}
	pending = call(tokens["admin"], "admin.mock", "")
	approval = pending.Meta[ApprovalMeta].(string)
	if _, err := gate.Approvals.Decide(approval, "approve", gate.Now()); err != nil {
		t.Fatal(err)
	}
	if call(tokens["admin"], "admin.mock", approval).IsError || count != 2 {
		t.Fatal("approved admin call did not forward")
	}
	if code(call(tokens["admin"], "admin.mock", approval)) != "approval_consumed" || count != 2 {
		t.Fatal("approval replay forwarded")
	}
	gate.Limiter = NewLimiter(2, time.Minute)
	for range 2 {
		if call(tokens["viewer"], "utility.add", "").IsError {
			t.Fatal("unexpected limit")
		}
	}
	if code(call(tokens["viewer"], "utility.add", "")) != "rate_limited" || count != 4 {
		t.Fatal("rate-limited call forwarded")
	}
	reasons := map[string]bool{}
	decoder := json.NewDecoder(&logs)
	raw := logs.String()
	for _, token := range tokens {
		if strings.Contains(raw, token) {
			t.Fatal("credential in audit output")
		}
	}
	for {
		var event Event
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(event.RequestID) != 32 {
			t.Fatal("missing request ID")
		}
		reasons[event.Reason] = true
	}
	for _, reason := range []string{"unauthenticated", "rbac_allow", "rbac_deny", "approval_required", "approval_denied", "human_approved", "approval_consumed", "rate_limited"} {
		if !reasons[reason] {
			t.Fatalf("missing audit reason %s", reason)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk unavailable") }
func TestAuditFailureBlocksForwarding(t *testing.T) {
	auth, tokens, _ := testAuth(t)
	g := NewGate(auth, NewAudit(failingWriter{}), 20, map[string]Policy{"echo": {Risk: "low", Roles: []string{"viewer"}}})
	count := 0
	_, err := g.Middleware(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		count++
		return &mcp.CallToolResult{}, nil
	})(t.Context(), "tools/call", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "echo", Meta: mcp.Meta{AuthorizationMeta: "Bearer " + tokens["viewer"]}}})
	if err == nil || count != 0 {
		t.Fatal("audit failure did not block execution")
	}
}
