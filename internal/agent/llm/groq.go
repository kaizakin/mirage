package llm

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
)

// Groq calls Groq's OpenAI-compatible chat completions endpoint.
//
// A request carries the system prompt as the first message and declares tools by their
// JSON Schema:
//
//	{"model": "openai/gpt-oss-120b",
//	 "messages": [{"role": "system", "content": "..."},
//	              {"role": "user", "content": "what orders came in today?"}],
//	 "tools": [{"type": "function", "function":
//	   {"name": "woo__list_orders", "description": "...", "parameters": {"type": "object", ...}}}]}
//
// A reply that calls tools has tool_calls whose arguments are a JSON string:
//
//	{"choices": [{"message": {"role": "assistant", "content": null, "tool_calls": [
//	   {"id": "call_1", "type": "function",
//	    "function": {"name": "woo__list_orders", "arguments": "{\"after\":\"today\"}"}}]},
//	  "finish_reason": "tool_calls"}]}
//
// The next request appends that assistant message, then one tool message per result:
//
//	{"role": "tool", "tool_call_id": "call_1", "content": "..."}
//
// and the loop repeats until a reply has no tool calls.
type Groq struct {
	APIKey  string
	Model   string
	BaseURL string // default https://api.groq.com/openai/v1
	HTTP    *http.Client
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// Generate implements Provider.
func (g *Groq) Generate(ctx context.Context, req Request) (Message, error) {
	var messages []openAIMessage
	if req.System != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: &req.System})
	}
	for _, m := range req.Messages {
		encoded, err := encodeOpenAI(m)
		if err != nil {
			return Message{}, err
		}
		messages = append(messages, encoded...)
	}
	body := map[string]any{"model": g.Model, "messages": messages}
	if len(req.Tools) > 0 {
		tools := make([]openAITool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = openAITool{Type: "function", Function: openAIFunction{
				Name: t.Name, Description: t.Description, Parameters: openAIParameters(t.InputSchema),
			}}
		}
		body["tools"] = tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}
	base := cmp.Or(g.BaseURL, "https://api.groq.com/openai/v1")
	raw, err := postJSON(ctx, g.HTTP, "groq", base+"/chat/completions",
		http.Header{"Authorization": {"Bearer " + g.APIKey}}, payload)
	if err != nil {
		return Message{}, err
	}
	return decodeOpenAI(raw)
}

// encodeOpenAI turns one turn into chat messages: a user turn's tool results each become
// a tool message, and a model turn becomes one assistant message.
func encodeOpenAI(m Message) ([]openAIMessage, error) {
	if m.Role == User {
		if len(m.ToolResults) == 0 {
			return []openAIMessage{{Role: "user", Content: &m.Text}}, nil
		}
		out := make([]openAIMessage, len(m.ToolResults))
		for i, r := range m.ToolResults {
			content := r.Content
			if r.IsError {
				content = "error: " + content
			}
			out[i] = openAIMessage{Role: "tool", ToolCallID: r.CallID, Content: &content}
		}
		return out, nil
	}
	msg := openAIMessage{Role: "assistant"}
	if m.Text != "" {
		msg.Content = &m.Text
	}
	for _, c := range m.ToolCalls {
		argMap := c.Args
		if argMap == nil {
			argMap = map[string]any{}
		}
		args, err := json.Marshal(argMap)
		if err != nil {
			return nil, err
		}
		call := openAIToolCall{ID: c.ID, Type: "function"}
		call.Function.Name, call.Function.Arguments = c.Name, string(args)
		msg.ToolCalls = append(msg.ToolCalls, call)
	}
	return []openAIMessage{msg}, nil
}

func decodeOpenAI(raw []byte) (Message, error) {
	var resp struct {
		Choices []struct {
			Message      openAIMessage `json:"message"`
			FinishReason string        `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Message{}, fmt.Errorf("groq: decoding response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Message{}, fmt.Errorf("groq: reply had no choices")
	}
	choice := resp.Choices[0]
	m := Message{Role: Model}
	if choice.Message.Content != nil {
		m.Text = *choice.Message.Content
	}
	for _, c := range choice.Message.ToolCalls {
		var args map[string]any
		if c.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
				return Message{}, fmt.Errorf("groq: tool %s arguments are not a JSON object: %w", c.Function.Name, err)
			}
		}
		m.ToolCalls = append(m.ToolCalls, ToolCall{ID: c.ID, Name: c.Function.Name, Args: args})
	}
	if m.Text == "" && len(m.ToolCalls) == 0 {
		return Message{}, fmt.Errorf("groq: empty reply (finish reason %s)", firstNonEmpty(choice.FinishReason, "unknown"))
	}
	return m, nil
}

// openAIParameters passes an MCP tool's JSON Schema through, minus $schema, which names a
// draft the API does not need. A tool without arguments still gets an object schema, which
// the API requires.
func openAIParameters(schema map[string]any) map[string]any {
	out := maps.Clone(schema)
	delete(out, "$schema")
	if out == nil {
		out = map[string]any{}
	}
	if out["type"] == nil {
		out["type"] = "object"
	}
	if out["properties"] == nil {
		out["properties"] = map[string]any{}
	}
	return out
}
