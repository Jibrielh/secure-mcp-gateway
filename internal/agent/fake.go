package agent

import (
	"context"
	"strings"
)

// Fake is a deterministic provider for tests and the local demo when no model is installed.
// It is not a substitute for a live Ollama response.
type Fake struct{}

func (Fake) Stream(_ context.Context, _ string, messages []Message, tools []ToolSchema, emit func(Chunk)) error {
	lastUser := ""
	toolResult := ""
	for _, message := range messages {
		if message.Role == "user" {
			lastUser = message.Content
		}
		if message.Role == "tool" {
			toolResult = message.Content
		}
	}
	allowed := map[string]bool{}
	for _, tool := range tools {
		allowed[tool.Name] = true
	}
	lower := strings.ToLower(lastUser)
	if strings.Contains(lower, "loop") && allowed["utility__echo"] {
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-loop", Name: "utility__echo", Arguments: `{"message":"loop"}`}}})
		return nil
	}
	if toolResult != "" {
		emit(Chunk{Text: "Using the gateway result: " + toolResult})
		return nil
	}
	switch {
	case strings.Contains(lower, "malformed"):
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-bad", Name: "utility__add", Arguments: "{"}}})
	case strings.Contains(lower, "destroy") || strings.Contains(lower, "maintenance"):
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-risk", Name: "admin__mock_destructive_action", Arguments: `{"target":"demo"}`}}})
	case strings.Contains(lower, "22") && strings.Contains(lower, "35") && allowed["utility__add"]:
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-add", Name: "utility__add", Arguments: `{"a":22,"b":35}`}}})
	case (strings.Contains(lower, "workspace") || strings.Contains(lower, "readme")) && allowed["files__list"]:
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-files", Name: "files__list", Arguments: `{"path":"."}`}}})
	case strings.Contains(lower, "revenue") && allowed["sample__revenue_by_region"]:
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-sample", Name: "sample__revenue_by_region", Arguments: `{}`}}})
	case strings.Contains(lower, "issue") && allowed["github__list_issues"]:
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-gh", Name: "github__list_issues", Arguments: `{"owner":"golang","repo":"go","limit":1}`}}})
	case strings.Contains(lower, "ignore previous") || strings.Contains(lower, "bypass"):
		emit(Chunk{ToolCalls: []ToolCall{{ID: "call-inject", Name: "admin__mock_destructive_action", Arguments: `{"target":"injected"}`}}})
	default:
		emit(Chunk{Text: "I can help with the local tools that this agent is allowed to use."})
	}
	return nil
}
