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

func TestLookupRecordStdio(t *testing.T) {
	buildCtx, cancelBuild := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancelBuild()
	binary := filepath.Join(t.TempDir(), "data-server")
	if output, err := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "data-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "lookup_record" {
		t.Fatalf("unexpected tools: %+v", listed.Tools)
	}
	for _, tc := range []struct {
		name      string
		args      map[string]any
		want      record
		wantError bool
	}{
		{"Alice", map[string]any{"id": 1}, record{ID: 1, Name: "Alice"}, false},
		{"Bob", map[string]any{"id": 2}, record{ID: 2, Name: "Bob"}, false},
		{"Charlie", map[string]any{"id": 3}, record{ID: 3, Name: "Charlie"}, false},
		{"not_found", map[string]any{"id": 99}, record{}, true},
		{"missing", map[string]any{}, record{}, true},
		{"string", map[string]any{"id": "2"}, record{}, true},
		{"fraction", map[string]any{"id": 2.5}, record{}, true},
		{"null", map[string]any{"id": nil}, record{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "lookup_record", Arguments: tc.args})
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
			if !ok || text.Text != tc.want.Name {
				t.Fatalf("expected %s, got %+v", tc.want.Name, result.Content)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var got record
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}
