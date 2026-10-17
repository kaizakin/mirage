// Package agent runs the dashboard's chat: a model-driven loop whose tool calls go through
// the gateway's own /mcp endpoint as the logged-in user, streamed to the browser over
// server-sent events.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/agent/llm"
	"github.com/kaizakin/mirage/internal/api"
	"github.com/kaizakin/mirage/internal/auth"
)

const (
	maxSteps = 8
	// tokenTTL bounds the internal token; runTimeout keeps every call of a run inside it.
	tokenTTL   = 5 * time.Minute
	runTimeout = 4 * time.Minute

	maxMessages      = 50
	maxHistoryBytes  = 64 << 10
	maxResultForLLM  = 16 << 10
	maxResultForUser = 8 << 10
)

const systemPrompt = `You are the assistant for the operators of a WooCommerce shop, working through tools:
woo__ tools act on the store, currency__ tools convert currencies at fixed reference rates, and deepwiki__ tools answer questions about open-source repositories.
Use tools for facts; never invent orders, products, customers, or prices.
For anything about today, call woo__list_orders with after "today", which means the store's own day.
Store amounts are in the store's currency; use currency__convert when asked for another currency.
Keep answers short, and use a table for several items.`

// Agent serves POST /api/chat.
type Agent struct {
	provider llm.Provider
	issuer   *auth.TokenIssuer
	mcpURL   string
}

// New returns an Agent that calls tools through the MCP endpoint at mcpURL, authenticating
// each run with an internal token from issuer.
func New(provider llm.Provider, issuer *auth.TokenIssuer, mcpURL string) *Agent {
	return &Agent{provider: provider, issuer: issuer, mcpURL: mcpURL}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ServeHTTP reads {messages: [{role: "user"|"assistant", content}]} and streams the run as
// server-sent events: tool_call, tool_result, then answer or error, then done.
func (a *Agent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := api.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "log in first", http.StatusUnauthorized)
		return
	}
	var in struct {
		Messages []chatMessage `json:"messages"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHistoryBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	history, err := toHistory(in.Messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	ctx, cancel := context.WithTimeout(r.Context(), runTimeout)
	defer cancel()
	if err := a.run(ctx, user, history, emit); err != nil {
		slog.WarnContext(ctx, "chat run failed", "user", user.Email, "err", err)
		emit("error", map[string]string{"message": err.Error()})
	}
	emit("done", struct{}{})
}

func toHistory(msgs []chatMessage) ([]llm.Message, error) {
	if len(msgs) == 0 || len(msgs) > maxMessages {
		return nil, fmt.Errorf("send between 1 and %d messages", maxMessages)
	}
	history := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			history = append(history, llm.Message{Role: llm.User, Text: m.Content})
		case "assistant":
			history = append(history, llm.Message{Role: llm.Model, Text: m.Content})
		default:
			return nil, fmt.Errorf(`message role must be "user" or "assistant", not %q`, m.Role)
		}
	}
	if history[len(history)-1].Role != llm.User || strings.TrimSpace(msgs[len(msgs)-1].Content) == "" {
		return nil, errors.New("the last message must be a non-empty user message")
	}
	return history, nil
}

// run is the tool loop: call the model, run its tool calls through the gateway as user,
// append the results, and repeat until it answers in text or maxSteps runs out.
func (a *Agent) run(ctx context.Context, user auth.User, history []llm.Message, emit func(string, any)) error {
	session, err := a.connect(ctx, user)
	if err != nil {
		return fmt.Errorf("could not reach the gateway's tools: %w", err)
	}
	defer session.Close()

	var tools []llm.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("listing tools: %w", err)
		}
		var schema map[string]any
		raw, _ := json.Marshal(tool.InputSchema)
		_ = json.Unmarshal(raw, &schema)
		tools = append(tools, llm.Tool{Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}

	messages := history
	for range maxSteps {
		reply, err := a.provider.Generate(ctx, llm.Request{System: systemPrompt, Messages: messages, Tools: tools})
		if err != nil {
			return err
		}
		messages = append(messages, reply)
		if len(reply.ToolCalls) == 0 {
			emit("answer", map[string]string{"text": reply.Text})
			return nil
		}
		results := make([]llm.ToolResult, 0, len(reply.ToolCalls))
		for _, call := range reply.ToolCalls {
			emit("tool_call", map[string]any{"id": call.ID, "name": call.Name, "arguments": orEmpty(call.Args)})
			content, isError := callTool(ctx, session, call)
			emit("tool_result", map[string]any{"id": call.ID, "name": call.Name, "is_error": isError, "content": truncate(content, maxResultForUser)})
			results = append(results, llm.ToolResult{CallID: call.ID, Name: call.Name, Content: truncate(content, maxResultForLLM), IsError: isError})
		}
		messages = append(messages, llm.Message{Role: llm.User, ToolResults: results})
	}
	return fmt.Errorf("stopped after %d steps without a final answer", maxSteps)
}

// connect opens an MCP session to the gateway as user, with a token that expires shortly
// after the run's timeout.
func (a *Agent) connect(ctx context.Context, user auth.User) (*mcp.ClientSession, error) {
	token := a.issuer.Mint(user.ID, tokenTTL)
	client := mcp.NewClient(&mcp.Implementation{Name: "mirage-chat"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             a.mcpURL,
		HTTPClient:           &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}, nil)
}

func callTool(ctx context.Context, session *mcp.ClientSession, call llm.ToolCall) (string, bool) {
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: call.Name, Arguments: orEmpty(call.Args)})
	if err != nil {
		return err.Error(), true
	}
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n"), res.IsError
}

// orEmpty keeps an argument-less call's arguments an object, as MCP requires.
func orEmpty(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

// truncate shortens s to about n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n…(truncated)"
}

type bearer struct {
	token string
	base  http.RoundTripper
}

// RoundTrip implements http.RoundTripper, setting the header on a clone as the contract requires.
func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}
