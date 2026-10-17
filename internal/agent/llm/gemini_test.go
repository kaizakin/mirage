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

func TestGeminiParametersConvertsMCPSchemas(t *testing.T) {
	// The shape the go-sdk infers for woo__list_orders, plus keywords Gemini rejects.
	in := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"after":  map[string]any{"type": "string", "description": "today or a date", "title": "After"},
			"status": map[string]any{"type": "string", "enum": []any{"pending", "completed"}},
			"ids":    map[string]any{"type": []any{"null", "array"}, "items": map[string]any{"type": "integer", "format": "int64"}},
			"amount": map[string]any{"type": "number", "minimum": 0.0, "format": "decimal"},
		},
		"required": []any{"after", "missing"},
	}
	want := map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"after":  map[string]any{"type": "STRING", "description": "today or a date"},
			"status": map[string]any{"type": "STRING", "format": "enum", "enum": []any{"pending", "completed"}},
			"ids":    map[string]any{"type": "ARRAY", "nullable": true, "items": map[string]any{"type": "INTEGER", "format": "int64"}},
			"amount": map[string]any{"type": "NUMBER", "minimum": 0.0},
		},
		"required": []any{"after"},
	}
	if got := GeminiParameters(in); !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("converted schema:\n%s", gj)
	}
}

func TestGeminiParametersOmitsArgumentlessTools(t *testing.T) {
	if got := GeminiParameters(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}); got != nil {
		t.Fatalf("no-argument tool got parameters %v; Gemini rejects an OBJECT without properties", got)
	}
}

func TestGeminiRoundTripsToolCalls(t *testing.T) {
	var bodies []map[string]any
	replies := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"woo__list_orders","args":{"after":"today"}},"thoughtSignature":"SIG"}]},"finishReason":"STOP"}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking...","thought":true},{"text":"One order today."}]},"finishReason":"STOP"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "KEY" || strings.Contains(r.URL.String(), "KEY") {
			t.Errorf("API key must travel only in the header; url %s", r.URL)
		}
		if r.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Errorf("path = %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		bodies = append(bodies, b)
		_, _ = io.WriteString(w, replies[len(bodies)-1])
	}))
	t.Cleanup(srv.Close)
	g := &Gemini{APIKey: "KEY", Model: "gemini-test", BaseURL: srv.URL}
	tools := []Tool{{Name: "woo__list_orders", Description: "List orders", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"after": map[string]any{"type": "string"}}}}}
	history := []Message{{Role: User, Text: "what orders came in today?"}}

	first, err := g.Generate(t.Context(), Request{System: "be brief", Messages: history, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "c1" || first.ToolCalls[0].Args["after"] != "today" {
		t.Fatalf("tool calls = %+v", first.ToolCalls)
	}
	history = append(history, first, Message{Role: User, ToolResults: []ToolResult{{CallID: "c1", Name: "woo__list_orders", Content: "order #17"}}})
	second, err := g.Generate(t.Context(), Request{Messages: history, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if second.Text != "One order today." {
		t.Fatalf("text = %q; thought parts must be excluded", second.Text)
	}

	contents, _ := json.Marshal(bodies[1]["contents"])
	for _, want := range []string{`"thoughtSignature":"SIG"`, `"functionResponse":{"id":"c1","name":"woo__list_orders","response":{"content":"order #17","is_error":false}}`} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("second request lacks %s:\n%s", want, contents)
		}
	}
	var sent struct {
		Tools []struct {
			FunctionDeclarations []geminiDeclaration `json:"functionDeclarations"`
		} `json:"tools"`
	}
	b, _ := json.Marshal(bodies[0])
	_ = json.Unmarshal(b, &sent)
	if len(sent.Tools) != 1 || len(sent.Tools[0].FunctionDeclarations) != 1 ||
		sent.Tools[0].FunctionDeclarations[0].Name != "woo__list_orders" || sent.Tools[0].FunctionDeclarations[0].Parameters["type"] != "OBJECT" {
		t.Errorf("tools = %+v", sent.Tools)
	}
}

func TestGeminiReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":429,"message":"Resource has been exhausted"}}`)
	}))
	t.Cleanup(srv.Close)
	_, err := (&Gemini{APIKey: "KEY", Model: "m", BaseURL: srv.URL}).Generate(t.Context(), Request{Messages: []Message{{Role: User, Text: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 429: Resource has been exhausted") {
		t.Fatalf("err = %v", err)
	}
}
