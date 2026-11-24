package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/demo"
	"github.com/Jibrielh/secure-mcp-gateway/internal/mcpclient"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func startSecureHTTP(t *testing.T, utility, data string, limit int, env []string) (string, *lockedBuffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	sessions := map[string]*mcp.ClientSession{}
	for _, config := range []struct{ id, path string }{{"utility", utility}, {"data", data}} {
		cmd := exec.CommandContext(ctx, config.path)
		cmd.Env = mcpclient.DownstreamEnv(append(os.Environ(), env...))
		cmd.Stderr = os.Stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "security-test-downstream", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := session.Close(); err != nil && !childStopped(err) {
				t.Errorf("close %s: %v", config.id, err)
			}
		})
		sessions[config.id] = session
	}
	gate := testGate(t)
	logs := &lockedBuffer{}
	gate.Audit = security.NewAudit(logs)
	gate.Limiter = security.NewLimiter(limit, time.Minute)
	gate.ApprovalEnabled = true
	server, err := newGateway(sessions, defaultRoutes(), gate)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewUnstartedServer(gate.HTTPHandler(server))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer.Listener = listener
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return httpServer.URL, logs
}
func connectRole(t *testing.T, url, role string) *mcp.ClientSession {
	t.Helper()
	session, err := mcpclient.Connect(t.Context(), url+"/mcp", os.Getenv("GATEWAY_"+strings.ToUpper(role)+"_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close upstream: %v", err)
		}
	})
	return session
}
func securityCode(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	code, _ := fields["code"].(string)
	return code
}
func requestApproval(t *testing.T, url, token, id, decision string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/approvals/"+id, strings.NewReader(`{"decision":"`+decision+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := mcpclient.HTTPClient(token).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, err = io.Copy(io.Discard, res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode
}
func TestGatewaySecurityEndToEnd(t *testing.T) {
	utility := buildExecutable(t, "./cmd/mock-server")
	data := buildExecutable(t, "./cmd/data-server")
	endpoint, logs := startSecureHTTP(t, utility, data, 20, nil)
	for _, token := range []string{"", "invalid"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"utility.add","arguments":{"a":22,"b":35}}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("unauthenticated HTTP status %d", res.StatusCode)
		}
	}
	developer := connectRole(t, endpoint, "developer")
	result, err := developer.CallTool(t.Context(), &mcp.CallToolParams{Name: "utility.add", Arguments: map[string]any{"a": 22, "b": 35}})
	if err != nil {
		t.Fatal(err)
	}
	assertText(t, result, "57", false)
	if result.Meta[security.RequestIDMeta] == nil {
		t.Fatal("missing request ID")
	}
	result, err = developer.CallTool(t.Context(), &mcp.CallToolParams{Name: "data.lookup_record", Arguments: map[string]any{"id": 2}})
	if err != nil {
		t.Fatal(err)
	}
	assertText(t, result, "Bob", false)
	viewer := connectRole(t, endpoint, "viewer")
	result, err = viewer.CallTool(t.Context(), &mcp.CallToolParams{Name: "admin.mock_destructive_action", Arguments: map[string]any{}, Meta: mcp.Meta{security.AuthorizationMeta: "Bearer " + os.Getenv("GATEWAY_ADMIN_TOKEN")}})
	if err != nil {
		t.Fatal(err)
	}
	if securityCode(t, result) != "unauthorized" {
		t.Fatal("viewer high-risk call not denied")
	}
	result, err = developer.CallTool(t.Context(), &mcp.CallToolParams{Name: "utility.add", Arguments: map[string]any{"a": "invalid", "b": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("invalid input accepted")
	}
	if _, err := developer.CallTool(t.Context(), &mcp.CallToolParams{Name: os.Getenv("GATEWAY_ADMIN_TOKEN"), Arguments: map[string]any{}}); err == nil {
		t.Fatal("unknown tool accepted")
	}
	raw := logs.String()
	for _, key := range []string{"GATEWAY_VIEWER_TOKEN", "GATEWAY_DEVELOPER_TOKEN", "GATEWAY_ADMIN_TOKEN", "GATEWAY_APPROVER_TOKEN"} {
		if strings.Contains(raw, os.Getenv(key)) {
			t.Fatal("credential found in logs")
		}
	}
	reasons := map[string]bool{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	for {
		var event security.Event
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		reasons[event.Reason] = true
	}
	for _, reason := range []string{"unauthenticated", "rbac_allow", "rbac_deny", "tool_error", "unknown_tool"} {
		if !reasons[reason] {
			t.Fatalf("missing %s event", reason)
		}
	}
}

func TestSecurityDenialsNeverReachDownstream(t *testing.T) {
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	stopped := filepath.Join(t.TempDir(), "stopped")
	endpoint, logs := startSecureHTTP(t, helper, helper, 20, []string{"MCP_TEST_DOWNSTREAM=result", "MCP_TEST_CALLS=" + calls, "MCP_TEST_STOPPED=" + stopped})
	count := func() int {
		data, err := os.ReadFile(calls)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "\n")
	}
	viewer := connectRole(t, endpoint, "viewer")
	developer := connectRole(t, endpoint, "developer")
	invoke := func(client *mcp.ClientSession, approval string) *mcp.CallToolResult {
		t.Helper()
		result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "admin.mock_destructive_action", Arguments: map[string]any{}, Meta: mcp.Meta{security.ApprovalMeta: approval}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if securityCode(t, invoke(viewer, "")) != "unauthorized" || count() != 0 {
		t.Fatal("unauthorized tool reached downstream")
	}
	pending := invoke(developer, "")
	id, _ := pending.Meta[security.ApprovalMeta].(string)
	if securityCode(t, pending) != "approval_required" || id == "" || count() != 0 {
		t.Fatal("pending action reached downstream")
	}
	if status := requestApproval(t, endpoint, os.Getenv("GATEWAY_DEVELOPER_TOKEN"), id, "approve"); status != 401 {
		t.Fatal("agent credential could approve")
	}
	if status := requestApproval(t, endpoint, os.Getenv("GATEWAY_APPROVER_TOKEN"), id, "deny"); status != 200 {
		t.Fatalf("deny HTTP %d", status)
	}
	if securityCode(t, invoke(developer, id)) != "approval_denied" || count() != 0 {
		t.Fatal("denied approval reached downstream")
	}
	pending = invoke(developer, "")
	id = pending.Meta[security.ApprovalMeta].(string)
	if status := requestApproval(t, endpoint, os.Getenv("GATEWAY_APPROVER_TOKEN"), id, "approve"); status != 200 {
		t.Fatalf("approve HTTP %d", status)
	}
	if invoke(developer, id).IsError || count() != 1 {
		t.Fatal("approved action did not execute once")
	}
	if securityCode(t, invoke(developer, id)) != "approval_consumed" || count() != 1 {
		t.Fatal("approval replay executed")
	}
	if !strings.Contains(logs.String(), `"reason":"human_approved"`) {
		t.Fatal("missing approval audit")
	}
}

func TestRateLimitStopsDownstream(t *testing.T) {
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	stopped := filepath.Join(t.TempDir(), "stopped")
	endpoint, logs := startSecureHTTP(t, helper, helper, 2, []string{"MCP_TEST_DOWNSTREAM=result", "MCP_TEST_CALLS=" + calls, "MCP_TEST_STOPPED=" + stopped})
	viewer := connectRole(t, endpoint, "viewer")
	for i := range 3 {
		result, err := viewer.CallTool(t.Context(), &mcp.CallToolParams{Name: "utility.echo", Arguments: map[string]any{"message": "hello"}})
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			assertText(t, result, "downstream received: hello", false)
		} else if securityCode(t, result) != "rate_limited" {
			t.Fatal("third call not rate limited")
		}
	}
	content, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "\n") != 2 {
		t.Fatal("rate-limited call reached downstream")
	}
	if !strings.Contains(logs.String(), `"reason":"rate_limited"`) {
		t.Fatal("missing rate audit")
	}
}

type providerTransport func(*http.Request) (*http.Response, error)

func (f providerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAgentLoopThroughSecuredGateway(t *testing.T) {
	utility := buildExecutable(t, "./cmd/mock-server")
	data := buildExecutable(t, "./cmd/data-server")
	endpoint, logs := startSecureHTTP(t, utility, data, 20, nil)
	session := connectRole(t, endpoint, "developer")
	requests := 0
	agent := demo.Agent{APIKey: "local-provider-fixture", MCP: session, Client: &http.Client{Transport: providerTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		var payload struct {
			Tools []struct{ Name, Description string }
			Input []json.RawMessage
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		var output any
		if requests == 1 {
			name := ""
			for _, tool := range payload.Tools {
				if strings.HasPrefix(tool.Description, "utility.add:") {
					name = tool.Name
				}
			}
			if name == "" {
				t.Fatal("agent did not discover utility.add")
			}
			output = []any{map[string]string{"type": "function_call", "name": name, "call_id": "model-call-1", "arguments": `{"a":22,"b":35}`}}
		} else {
			raw := string(payload.Input[len(payload.Input)-1])
			if !strings.Contains(raw, "57") || !strings.Contains(raw, "function_call_output") {
				t.Fatal("real downstream result was not returned to model")
			}
			output = []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": "The sum is 57."}}}}
		}
		body, err := json.Marshal(map[string]any{"status": "completed", "output": output})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	answer, err := agent.Run(t.Context(), "Add 22 and 35.")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "The sum is 57." || requests != 2 || !strings.Contains(logs.String(), `"reason":"rbac_allow"`) {
		t.Fatal("agent/security/downstream loop failed")
	}
}

func TestStdioRequiresAuthentication(t *testing.T) {
	gateway := buildExecutable(t, "./cmd/gateway")
	utility := buildExecutable(t, "./cmd/mock-server")
	data := buildExecutable(t, "./cmd/data-server")
	for _, token := range []string{"", "invalid"} {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		command := exec.CommandContext(ctx, gateway, "-utility-command", utility, "-data-command", data)
		command.Stderr = os.Stderr
		client := mcpclient.New("unauthenticated-test", token)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 5 * time.Second}, nil)
		if err == nil {
			session.Close()
			t.Error("unauthenticated stdio connection accepted")
		}
		cancel()
	}
}
