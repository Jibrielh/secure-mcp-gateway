package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Chunk struct {
	Text      string
	ToolCalls []ToolCall
}

type Provider interface {
	Stream(ctx context.Context, model string, messages []Message, tools []ToolSchema, emit func(Chunk)) error
}

type Result struct {
	Text       string
	IsError    bool
	Code       string
	ApprovalID string
	RequestID  string
}

type Caller interface {
	Call(ctx context.Context, tool string, args json.RawMessage, approvalID string) (Result, error)
}

type Event struct {
	Type    string
	Payload map[string]any
}

type Run struct {
	Messages   []Message `json:"messages"`
	Steps      int       `json:"steps"`
	Status     string    `json:"status"`
	Pending    *ToolCall `json:"pending,omitempty"`
	ApprovalID string    `json:"approval_id,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type Engine struct {
	Provider Provider
	Caller   Caller
	MaxSteps int
	Now      func() time.Time
}

func (e *Engine) maxSteps() int {
	if e.MaxSteps < 1 {
		return 4
	}
	if e.MaxSteps > 8 {
		return 8
	}
	return e.MaxSteps
}

func (e *Engine) Execute(ctx context.Context, model, system string, run Run, tools []ToolSchema, emit func(Event)) Run {
	if run.Status == "" {
		run.Status = "running"
		emit(Event{Type: "run.started", Payload: map[string]any{"model": model}})
	}
	allowed := map[string]ToolSchema{}
	for _, tool := range tools {
		allowed[tool.Name] = tool
	}
	messages := run.Messages
	if system != "" && (len(messages) == 0 || messages[0].Role != "system") {
		messages = append([]Message{{Role: "system", Content: system}}, messages...)
	}
	if run.Pending != nil && run.ApprovalID != "" {
		var status, approval string
		var pending *ToolCall
		messages, status, pending, approval = e.executeTool(ctx, messages, *run.Pending, run.ApprovalID, allowed, emit)
		run.Pending = pending
		run.ApprovalID = approval
		run.Status = status
		run.Messages = messages
		if status != "running" {
			return run
		}
	}
	for run.Steps < e.maxSteps() {
		if ctx.Err() != nil {
			run.Status = "cancelled"
			run.Messages = messages
			emit(Event{Type: "run.cancelled", Payload: map[string]any{}})
			return run
		}
		var text strings.Builder
		var calls []ToolCall
		err := e.Provider.Stream(ctx, model, messages, tools, func(chunk Chunk) {
			if chunk.Text != "" {
				text.WriteString(chunk.Text)
				emit(Event{Type: "assistant.delta", Payload: map[string]any{"text": chunk.Text}})
			}
			calls = append(calls, chunk.ToolCalls...)
		})
		if err != nil {
			run.Status = "failed"
			run.Error = "model_unavailable"
			run.Messages = messages
			emit(Event{Type: "run.failed", Payload: map[string]any{"error": "model_unavailable"}})
			return run
		}
		if len(calls) == 0 {
			messages = append(messages, Message{Role: "assistant", Content: text.String()})
			run.Messages = messages
			run.Status = "completed"
			emit(Event{Type: "run.completed", Payload: map[string]any{"text": text.String()}})
			return run
		}
		messages = append(messages, Message{Role: "assistant", Content: text.String(), ToolCalls: calls})
		for _, call := range calls {
			run.Steps++
			if run.Steps > e.maxSteps() {
				run.Status = "failed"
				run.Error = "step_limit"
				run.Messages = messages
				emit(Event{Type: "run.failed", Payload: map[string]any{"error": "step_limit"}})
				return run
			}
			var status, approval string
			var pending *ToolCall
			messages, status, pending, approval = e.executeTool(ctx, messages, call, "", allowed, emit)
			run.Messages = messages
			run.Status = status
			run.Pending = pending
			run.ApprovalID = approval
			if status != "running" {
				return run
			}
		}
	}
	run.Status = "failed"
	run.Error = "step_limit"
	run.Messages = messages
	emit(Event{Type: "run.failed", Payload: map[string]any{"error": "step_limit"}})
	return run
}

func (e *Engine) executeTool(ctx context.Context, messages []Message, call ToolCall, approvalID string, allowed map[string]ToolSchema, emit func(Event)) ([]Message, string, *ToolCall, string) {
	tool, ok := policy.ByProvider(call.Name)
	if !ok {
		emit(Event{Type: "tool.blocked", Payload: map[string]any{"name": call.Name, "code": "unknown_tool"}})
		messages = append(messages, Message{Role: "tool", ToolName: call.Name, ToolCallID: call.ID, Content: "The gateway rejected this tool."})
		return messages, "running", nil, ""
	}
	if _, listed := allowed[tool.ProviderName]; !listed {
		emit(Event{Type: "tool.blocked", Payload: map[string]any{"name": tool.Name, "code": "agent_scope_deny"}})
		messages = append(messages, Message{Role: "tool", ToolName: tool.Name, ToolCallID: call.ID, Content: "This agent is not allowed to use that tool."})
		return messages, "running", nil, ""
	}
	args := json.RawMessage(call.Arguments)
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if !json.Valid(args) {
		emit(Event{Type: "tool.blocked", Payload: map[string]any{"name": tool.Name, "code": "malformed_arguments"}})
		messages = append(messages, Message{Role: "tool", ToolName: tool.Name, ToolCallID: call.ID, Content: "The model produced malformed tool arguments."})
		return messages, "running", nil, ""
	}
	emit(Event{Type: "tool.proposed", Payload: map[string]any{"name": tool.Name, "arguments": string(args)}})
	result, err := e.Caller.Call(ctx, tool.Name, args, approvalID)
	if err != nil {
		emit(Event{Type: "tool.failed", Payload: map[string]any{"name": tool.Name, "code": "gateway_unavailable"}})
		messages = append(messages, Message{Role: "tool", ToolName: tool.Name, ToolCallID: call.ID, Content: "The gateway could not be reached."})
		return messages, "running", nil, ""
	}
	if result.Code == "approval_required" {
		emit(Event{Type: "approval.required", Payload: map[string]any{"name": tool.Name, "arguments": string(args), "approval_id": result.ApprovalID}})
		pending := call
		return messages, "waiting_for_approval", &pending, result.ApprovalID
	}
	if result.IsError {
		emit(Event{Type: "tool.blocked", Payload: map[string]any{"name": tool.Name, "code": result.Code, "request_id": result.RequestID}})
		messages = append(messages, Message{Role: "tool", ToolName: tool.Name, ToolCallID: call.ID, Content: "Gateway decision: " + result.Code})
		return messages, "running", nil, ""
	}
	text := result.Text
	if len(text) > 8000 {
		text = text[:8000] + "\n[truncated]"
	}
	emit(Event{Type: "tool.completed", Payload: map[string]any{"name": tool.Name, "request_id": result.RequestID, "text": text}})
	messages = append(messages, Message{Role: "tool", ToolName: tool.Name, ToolCallID: call.ID, Content: text})
	return messages, "running", nil, ""
}
