package security

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func BenchmarkGatewayAllowPath(b *testing.B) {
	token := "0123456789abcdef0123456789abcdef"
	auth, err := NewAuth(map[string]string{
		"viewer":    "viewer-0123456789abcdef0123456789abcdef",
		"developer": token,
		"admin":     "admin-0123456789abcdef0123456789abcdef",
	}, "approver-0123456789abcdef0123456789abcdef")
	if err != nil {
		b.Fatal(err)
	}
	gate := NewGate(auth, NewAudit(discard{}), 1_000_000, map[string]Policy{
		"utility.add": {Server: "utility", Risk: "low", Roles: []string{"developer"}},
	})
	gate.Concurrency = NewConcurrency(1_000_000)
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "11"}}}, nil
	}
	handler := gate.Middleware(next)
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "utility.add", Arguments: json.RawMessage(`{"a":4,"b":7}`), Meta: mcp.Meta{AuthorizationMeta: "Bearer " + token}}}
	ctx := context.Background()
	var sink string
	b.ResetTimer()
	for range b.N {
		result, err := handler(ctx, "tools/call", request)
		if err != nil {
			b.Fatal(err)
		}
		sink = textOf(result)
	}
	if sink != "11" {
		b.Fatal(sink)
	}
}

func BenchmarkDirectNext(b *testing.B) {
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "11"}}}, nil
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "utility.add", Arguments: json.RawMessage(`{"a":4,"b":7}`)}}
	ctx := context.Background()
	var sink string
	b.ResetTimer()
	for range b.N {
		result, err := next(ctx, "tools/call", request)
		if err != nil {
			b.Fatal(err)
		}
		sink = textOf(result)
	}
	if sink != "11" {
		b.Fatal(sink)
	}
}

func textOf(result mcp.Result) string {
	tool, _ := result.(*mcp.CallToolResult)
	if tool == nil || len(tool.Content) == 0 {
		return ""
	}
	text, _ := tool.Content[0].(*mcp.TextContent)
	if text == nil {
		return ""
	}
	return text.Text
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
