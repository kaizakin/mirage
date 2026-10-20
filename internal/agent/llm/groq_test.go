package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGroqRoundTripsToolCalls(t *testing.T) {
	var bodies []map[string]any
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"woo__list_orders","arguments":"{\"after\":\"today\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"One order today."},"finish_reason":"stop"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer KEY" || r.URL.Path != "/chat/completions" {
			t.Errorf("request %s with Authorization %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		_, _ = io.WriteString(w, replies[len(bodies)-1])
	}))
	t.Cleanup(srv.Close)
	g := &Groq{APIKey: "KEY", Model: "openai/gpt-oss-120b", BaseURL: srv.URL}
	tools := []Tool{
		{Name: "woo__list_orders", InputSchema: map[string]any{"$schema": "x", "type": "object", "properties": map[string]any{"after": map[string]any{"type": "string"}}}},
		{Name: "deepwiki__noargs", InputSchema: map[string]any{"type": "object"}},
	}
	history := []Message{{Role: User, Text: "what orders came in today?"}}

	call, err := g.Generate(t.Context(), Request{System: "be brief", Messages: history, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ToolCall{{ID: "call_1", Name: "woo__list_orders", Args: map[string]any{"after": "today"}}}; !reflect.DeepEqual(call.ToolCalls, want) {
		t.Fatalf("tool calls = %+v", call.ToolCalls)
	}
	history = append(history, call, Message{Role: User, ToolResults: []ToolResult{
		{CallID: "call_1", Name: "woo__list_orders", Content: "boom", IsError: true},
	}})
	answer, err := g.Generate(t.Context(), Request{System: "be brief", Messages: history, Tools: tools})
	if err != nil || answer.Text != "One order today." {
		t.Fatalf("answer = %q, %v", answer.Text, err)
	}

	first, _ := json.Marshal(bodies[0]["tools"])
	if strings.Contains(string(first), "$schema") || !strings.Contains(string(first), `"name":"deepwiki__noargs","parameters":{"properties":{},"type":"object"}`) {
		t.Errorf("tools sent = %s", first)
	}
	sent, _ := json.Marshal(bodies[1]["messages"])
	want := `[{"role":"system","content":"be brief"},` +
		`{"role":"user","content":"what orders came in today?"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"woo__list_orders","arguments":"{\"after\":\"today\"}"}}]},` +
		`{"role":"tool","content":"error: boom","tool_call_id":"call_1"}]`
	var wantMessages any
	if err := json.Unmarshal([]byte(want), &wantMessages); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bodies[1]["messages"], wantMessages) {
		t.Fatalf("second request messages:\n got %s\nwant %s", sent, want)
	}
}

func TestGroqRejectsMalformedToolArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"woo__list_orders","arguments":"{not json"}}]}}]}`)
	}))
	t.Cleanup(srv.Close)
	_, err := (&Groq{APIKey: "KEY", Model: "m", BaseURL: srv.URL}).Generate(t.Context(), Request{Messages: []Message{{Role: User, Text: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "woo__list_orders arguments are not a JSON object") {
		t.Fatalf("err = %v", err)
	}
}
