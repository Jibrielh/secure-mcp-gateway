package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func startUtility(t *testing.T) (context.Context, *mcp.ClientSession) {
	t.Helper()
	buildCtx, cancelBuild := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancelBuild()
	binary := filepath.Join(t.TempDir(), "mock-server")
	if output, err := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build mock server: %v\n%s", err, output)
	}

	// testing cancels t.Context before cleanup callbacks; keep the process
	// alive until the session-close callback has completed.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, binary)
	command.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-server-test", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{
		Command: command, TerminateDuration: time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("connect to mock server: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return ctx, session
}

func TestEchoStdio(t *testing.T) {
	ctx, session := startUtility(t)

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if len(listed.Tools) != 3 || !names["echo"] || !names["add"] || !names["mock_destructive_action"] {
		t.Fatalf("expected echo, add, and mock_destructive_action, got %+v", listed.Tools)
	}

	for _, tc := range []struct{ name, message string }{
		{"hello", "hello"},
		{"empty", ""},
		{"unicode", "Hello, 世界 👋"},
		{"multiline", "first\nsecond\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "echo", Arguments: map[string]any{"message": tc.message},
			})
			if err != nil {
				t.Fatalf("call echo: %v", err)
			}
			if result.IsError || len(result.Content) != 1 {
				t.Fatalf("expected one successful text result, got %+v", result)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok || text.Text != tc.message {
				t.Fatalf("expected %q, got %+v", tc.message, result.Content[0])
			}
		})
	}

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing", map[string]any{}},
		{"number", map[string]any{"message": 123}},
		{"null", map[string]any{"message": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: tc.args})
			if err == nil && (result == nil || !result.IsError) {
				t.Fatalf("expected invalid arguments to be rejected, got %+v", result)
			}
		})
	}
	t.Run("unknown_tool", func(t *testing.T) {
		if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "unknown", Arguments: map[string]any{}}); err == nil {
			t.Fatal("expected a protocol error for an unknown tool")
		}
	})
}

func TestAddStdio(t *testing.T) {
	ctx, session := startUtility(t)
	for _, tc := range []struct {
		name      string
		args      map[string]any
		text      string
		sum       float64
		wantError bool
	}{
		{"integers", map[string]any{"a": 4, "b": 7}, "11", 11, false},
		{"negative", map[string]any{"a": -4, "b": 7}, "3", 3, false},
		{"fraction", map[string]any{"a": 1.5, "b": 2.25}, "3.75", 3.75, false},
		{"zero", map[string]any{"a": 0, "b": 0}, "0", 0, false},
		{"missing", map[string]any{"a": 4}, "", 0, true},
		{"wrong_type", map[string]any{"a": "4", "b": 7}, "", 0, true},
		{"null", map[string]any{"a": nil, "b": 7}, "", 0, true},
		{"overflow", map[string]any{"a": 1e308, "b": 1e308}, "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "add", Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError != tc.wantError {
				t.Fatalf("unexpected result: %+v", result)
			}
			if tc.wantError {
				return
			}
			if len(result.Content) != 1 {
				t.Fatalf("unexpected content: %+v", result.Content)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok || text.Text != tc.text {
				t.Fatalf("expected %q, got %+v", tc.text, result.Content)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var output addOutput
			if err := json.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			if output.Sum != tc.sum {
				t.Fatalf("expected sum %v, got %v", tc.sum, output.Sum)
			}
		})
	}
}
