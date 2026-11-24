package demo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeMCP struct {
	calls    int
	tool     string
	args     string
	result   *mcp.CallToolResult
	approval string
}

func (f *fakeMCP) ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "utility.add", Description: "Add two numbers", InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"],"additionalProperties":false}`)}}}, nil
}
func (f *fakeMCP) CallTool(_ context.Context, p *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	f.calls++
	f.tool = p.Name
	encoded, err := json.Marshal(p.Arguments)
	if err != nil {
		return nil, err
	}
	f.args = string(encoded)
	f.approval, _ = p.Meta[security.ApprovalMeta].(string)
	return f.result, nil
}
func responseBody(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestAgentFunctionCallingLoop(t *testing.T) {
	gateway := &fakeMCP{result: &mcp.CallToolResult{Meta: mcp.Meta{security.AuthorizationMeta: "private-transport-data"}, Content: []mcp.Content{&mcp.TextContent{Text: "57"}}, StructuredContent: map[string]any{"sum": 57}}}
	requests := 0
	agent := Agent{APIKey: "local-fixture-credential", MCP: gateway, Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if req.URL.String() != "https://api.openai.com/v1/responses" || req.Header.Get("Authorization") != "Bearer local-fixture-credential" {
			t.Fatal("incorrect provider request")
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if string(payload["store"]) != "false" {
			t.Fatal("response storage was not disabled")
		}
		if requests == 1 {
			if !strings.Contains(string(raw), "Add 22 and 35") || !strings.Contains(string(raw), "utility.add") || !strings.Contains(string(raw), "mcp_tool_0") {
				t.Fatal("prompt or discovered tool missing")
			}
			return responseBody(`{"status":"completed","output":[{"type":"reasoning","id":"reasoning-1","summary":[],"encrypted_content":"opaque"},{"type":"function_call","call_id":"call-1","name":"mcp_tool_0","arguments":"{\"a\":22,\"b\":35}"}]}`), nil
		}
		if !strings.Contains(string(raw), "function_call_output") || !strings.Contains(string(raw), "call-1") || !strings.Contains(string(raw), "57") || !strings.Contains(string(raw), "encrypted_content") {
			t.Fatal("tool output or reasoning state missing")
		}
		if strings.Contains(string(raw), "private-transport-data") || strings.Contains(string(raw), "local-fixture-credential") {
			t.Fatal("credential/transport metadata leaked into model input")
		}
		return responseBody(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"The sum is 57."}]}]}`), nil
	})}}
	answer, err := agent.Run(t.Context(), "Add 22 and 35.")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "The sum is 57." || requests != 2 || gateway.calls != 1 || gateway.tool != "utility.add" || gateway.args != `{"a":22,"b":35}` {
		t.Fatal("function calling loop did not execute through MCP")
	}
}

func TestAgentRejectsMissingKeyAndUnknownTool(t *testing.T) {
	gateway := &fakeMCP{}
	if _, err := (&Agent{MCP: gateway}).Run(t.Context(), "hello"); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatal("missing key not explained")
	}
	agent := Agent{APIKey: "local-fixture", MCP: gateway, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseBody(`{"status":"completed","output":[{"type":"function_call","call_id":"call-1","name":"invented","arguments":"{}"}]}`), nil
	})}}
	if _, err := agent.Run(t.Context(), "hello"); err == nil || gateway.calls != 0 {
		t.Fatal("undiscovered function executed")
	}
}
func TestAgentProviderFailureIsSanitized(t *testing.T) {
	agent := Agent{APIKey: "local-fixture", MCP: &fakeMCP{}, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("do not expose this response body"))}, nil
	})}}
	_, err := agent.Run(t.Context(), "hello")
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "response body") {
		t.Fatal("provider error was not sanitized")
	}
}
func TestAgentNeverTreatsConfirmationAsApproval(t *testing.T) {
	gateway := &fakeMCP{result: &mcp.CallToolResult{Meta: mcp.Meta{security.ApprovalMeta: "approval-id"}, IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "approval_required"}}}}
	waited := 0
	agent := Agent{APIKey: "local-fixture", MCP: gateway, WaitForApproval: func(string) error { waited++; return errors.New("operator did not confirm") }, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseBody(`{"status":"completed","output":[{"type":"function_call","call_id":"call-1","name":"mcp_tool_0","arguments":"{\"a\":1,\"b\":2}"}]}`), nil
	})}}
	if _, err := agent.Run(t.Context(), "hello"); err == nil || gateway.calls != 1 || waited != 1 {
		t.Fatal("unconfirmed approval retried")
	}
}
func TestAgentPassesDeniedResultsBackToModel(t *testing.T) {
	gateway := &fakeMCP{result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unauthorized"}}}}
	requests := 0
	agent := Agent{APIKey: "local-fixture", MCP: gateway, Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return responseBody(`{"status":"completed","output":[{"type":"function_call","call_id":"call-1","name":"mcp_tool_0","arguments":"{\"a\":1,\"b\":2}"}]}`), nil
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), "unauthorized") || !strings.Contains(string(body), `isError\":true`) {
			t.Fatal("denial omitted from model input")
		}
		return responseBody(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"The gateway denied this request."}]}]}`), nil
	})}}
	if answer, err := agent.Run(t.Context(), "hello"); err != nil || answer != "The gateway denied this request." || gateway.calls != 1 {
		t.Fatalf("denial workflow: %v", err)
	}
}
