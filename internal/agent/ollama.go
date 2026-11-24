package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Ollama struct {
	BaseURL string
	Client  *http.Client
}

func NewOllama(base string) (*Ollama, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ollama URL is not allowlisted")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if parsed.Scheme != "http" || ip == nil || !ip.IsLoopback() {
		return nil, errors.New("ollama URL must be an http loopback address")
	}
	return &Ollama{BaseURL: strings.TrimRight(base, "/"), Client: &http.Client{Timeout: 2 * time.Minute}}, nil
}

type ollamaRequest struct {
	Model    string       `json:"model"`
	Messages []ollamaMsg  `json:"messages"`
	Tools    []ollamaTool `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
}

type ollamaMsg struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaCallWrap `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
}
type ollamaFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type ollamaCallWrap struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func (o *Ollama) Stream(ctx context.Context, model string, messages []Message, tools []ToolSchema, emit func(Chunk)) error {
	body := ollamaRequest{Model: model, Stream: true}
	for _, message := range messages {
		item := ollamaMsg{Role: message.Role, Content: message.Content, ToolName: message.ToolName}
		for _, call := range message.ToolCalls {
			args := json.RawMessage(call.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			item.ToolCalls = append(item.ToolCalls, ollamaCallWrap{Function: struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}{Name: call.Name, Arguments: args}})
		}
		body.Messages = append(body.Messages, item)
	}
	for _, tool := range tools {
		body.Tools = append(body.Tools, ollamaTool{Type: "function", Function: ollamaFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}})
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/chat", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("ollama request failed")
	}
	scanner := bufio.NewScanner(io.LimitReader(res.Body, 2<<20))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var calls []ToolCall
	for scanner.Scan() {
		var line struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []ollamaCallWrap `json:"tool_calls"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return errors.New("ollama returned malformed output")
		}
		if line.Message.Content != "" {
			emit(Chunk{Text: line.Message.Content})
		}
		for i, call := range line.Message.ToolCalls {
			args := call.Function.Arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			if args[0] != '{' {
				raw, err := json.Marshal(json.RawMessage(args))
				if err != nil {
					return errors.New("ollama returned malformed tool arguments")
				}
				var text string
				if err := json.Unmarshal(raw, &text); err != nil || !json.Valid([]byte(text)) {
					return errors.New("ollama returned malformed tool arguments")
				}
				args = json.RawMessage(text)
			}
			calls = append(calls, ToolCall{ID: "ollama-" + strconv.Itoa(len(calls)+1) + "-" + strconv.Itoa(i), Name: call.Function.Name, Arguments: string(args)})
		}
		if line.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(calls) > 0 {
		emit(Chunk{ToolCalls: calls})
	}
	return nil
}

type ModelInfo struct {
	Name string `json:"name"`
}

func (o *Ollama) Models(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	res, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("ollama request failed")
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	result := make([]ModelInfo, 0, len(payload.Models))
	for _, model := range payload.Models {
		result = append(result, ModelInfo{Name: model.Name})
	}
	return result, nil
}

func (o *Ollama) Summarize(ctx context.Context, model, prompt string) (string, error) {
	var text strings.Builder
	err := o.Stream(ctx, model, []Message{{Role: "user", Content: prompt}}, nil, func(chunk Chunk) {
		text.WriteString(chunk.Text)
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("empty summary")
	}
	return text.String(), nil
}
