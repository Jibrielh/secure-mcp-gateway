package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOllamaToolCallLive(t *testing.T) {
	if os.Getenv("AGENTGATE_OLLAMA_LIVE") == "" {
		t.Skip("set AGENTGATE_OLLAMA_LIVE=1 to call the local model")
	}
	client, err := NewOllama("http://127.0.0.1:11434")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var calls []ToolCall
	err = client.Stream(t.Context(), "qwen2.5:3b", []Message{{Role: "user", Content: "Use the utility__add tool to add 2 and 3."}}, []ToolSchema{{
		Name: "utility__add", Description: "Add two numbers",
		Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
	}}, func(chunk Chunk) {
		text.WriteString(chunk.Text)
		calls = append(calls, chunk.ToolCalls...)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "utility__add" {
		t.Fatalf("calls=%v text=%s", calls, text.String())
	}
}
