// Package llm is the chat agent's view of a model provider: a conversation of messages,
// tools described by JSON Schema, and replies that are text or tool calls.
package llm

import (
	"context"
	"encoding/json"
)

// Role says who produced a message.
type Role string

// Message roles.
const (
	User  Role = "user"
	Model Role = "model"
)

// Message is one turn. A user turn carries Text or ToolResults; a model turn carries Text
// and/or ToolCalls.
type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	// Raw is the provider's own encoding of a model turn, echoed back verbatim on the next
	// request; Gemini, for one, attaches thought signatures it expects to see again.
	Raw json.RawMessage
}

// ToolCall is the model asking to run a tool.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// Tool is a function the model may call. InputSchema is the MCP tool's JSON Schema; each
// provider converts it to its own format.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Request is one model call.
type Request struct {
	System   string
	Messages []Message
	Tools    []Tool
}

// Provider generates the model's next turn.
type Provider interface {
	Generate(ctx context.Context, req Request) (Message, error)
}
