package llm

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Gemini calls the Gemini API's generateContent endpoint.
//
// A request declares tools and carries the conversation:
//
//	{"systemInstruction": {"parts": [{"text": "..."}]},
//	 "contents": [{"role": "user", "parts": [{"text": "what orders came in today?"}]}],
//	 "tools": [{"functionDeclarations": [
//	   {"name": "woo__list_orders", "description": "...",
//	    "parameters": {"type": "OBJECT", "properties": {"after": {"type": "STRING"}}}}]}]}
//
// A reply that calls tools has functionCall parts, possibly with a thoughtSignature:
//
//	{"candidates": [{"content": {"role": "model", "parts": [
//	   {"functionCall": {"id": "...", "name": "woo__list_orders", "args": {"after": "today"}},
//	    "thoughtSignature": "..."}]},
//	  "finishReason": "STOP"}]}
//
// The next request appends that model content verbatim, then the results as a user turn:
//
//	{"role": "user", "parts": [{"functionResponse":
//	   {"id": "...", "name": "woo__list_orders", "response": {"content": "...", "is_error": false}}}]}
//
// and the loop repeats until a reply has only text parts.
type Gemini struct {
	APIKey  string
	Model   string
	BaseURL string // default https://generativelanguage.googleapis.com
	HTTP    *http.Client
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	Thought          bool                `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFuncResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Generate implements Provider.
func (g *Gemini) Generate(ctx context.Context, req Request) (Message, error) {
	contents := make([]json.RawMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		c, err := encodeGemini(m)
		if err != nil {
			return Message{}, err
		}
		contents = append(contents, c)
	}
	body := map[string]any{"contents": contents}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": req.System}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]geminiDeclaration, len(req.Tools))
		for i, t := range req.Tools {
			decls[i] = geminiDeclaration{Name: t.Name, Description: t.Description, Parameters: GeminiParameters(t.InputSchema)}
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}
	base := cmp.Or(g.BaseURL, "https://generativelanguage.googleapis.com")
	// The key travels in a header, never the URL, so it stays out of proxy and access logs.
	raw, err := postJSON(ctx, g.HTTP, "gemini", base+"/v1beta/models/"+url.PathEscape(g.Model)+":generateContent",
		http.Header{"X-Goog-Api-Key": {g.APIKey}}, payload)
	if err != nil {
		return Message{}, err
	}
	return decodeGemini(raw)
}

func encodeGemini(m Message) (json.RawMessage, error) {
	if m.Raw != nil {
		return m.Raw, nil
	}
	c := geminiContent{Role: string(m.Role)}
	if m.Text != "" {
		c.Parts = append(c.Parts, geminiPart{Text: m.Text})
	}
	for _, r := range m.ToolResults {
		c.Parts = append(c.Parts, geminiPart{FunctionResponse: &geminiFuncResponse{
			ID: r.CallID, Name: r.Name, Response: map[string]any{"content": r.Content, "is_error": r.IsError},
		}})
	}
	for _, call := range m.ToolCalls {
		c.Parts = append(c.Parts, geminiPart{FunctionCall: &geminiFunctionCall{ID: call.ID, Name: call.Name, Args: call.Args}})
	}
	return json.Marshal(c)
}

func decodeGemini(raw []byte) (Message, error) {
	var resp struct {
		Candidates []struct {
			Content      json.RawMessage `json:"content"`
			FinishReason string          `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Message{}, fmt.Errorf("gemini: decoding response: %w", err)
	}
	if len(resp.Candidates) == 0 {
		return Message{}, fmt.Errorf("gemini: no reply (blocked: %s)", firstNonEmpty(resp.PromptFeedback.BlockReason, "unknown reason"))
	}
	cand := resp.Candidates[0]
	var content geminiContent
	if err := json.Unmarshal(cand.Content, &content); err != nil {
		return Message{}, fmt.Errorf("gemini: decoding content: %w", err)
	}
	m := Message{Role: Model, Raw: cand.Content}
	var text []string
	for i, p := range content.Parts {
		switch {
		case p.FunctionCall != nil:
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call-%d", i)
			}
			m.ToolCalls = append(m.ToolCalls, ToolCall{ID: id, Name: p.FunctionCall.Name, Args: p.FunctionCall.Args})
		case p.Text != "" && !p.Thought:
			text = append(text, p.Text)
		}
	}
	m.Text = strings.Join(text, "")
	if m.Text == "" && len(m.ToolCalls) == 0 {
		return Message{}, fmt.Errorf("gemini: empty reply (finish reason %s)", firstNonEmpty(cand.FinishReason, "unknown"))
	}
	return m, nil
}

// geminiKeywords are the JSON Schema keywords Gemini's OpenAPI-subset Schema accepts with
// the same meaning; anything else (additionalProperties, $schema, title, ...) is rejected
// by the API, so it is dropped.
var geminiKeywords = map[string]bool{
	"description": true, "enum": true, "minimum": true, "maximum": true,
	"minItems": true, "maxItems": true, "minLength": true, "maxLength": true, "pattern": true,
}

// geminiFormats are the formats Gemini accepts; others are dropped.
var geminiFormats = map[string]bool{"date-time": true, "int32": true, "int64": true, "float": true, "double": true, "enum": true}

// GeminiParameters converts an MCP tool's input schema to a Gemini function declaration's
// parameters: uppercase OpenAPI types, ["null", T] unions as nullable T, and only the
// keywords Gemini understands. It returns nil for a tool that takes no arguments, because
// Gemini rejects an OBJECT with no properties.
func GeminiParameters(schema map[string]any) map[string]any {
	if props, _ := schema["properties"].(map[string]any); len(props) == 0 {
		return nil
	}
	return convertSchema(schema)
}

func convertSchema(s map[string]any) map[string]any {
	out := map[string]any{}
	switch t := s["type"].(type) {
	case string:
		out["type"] = strings.ToUpper(t)
	case []any:
		for _, v := range t {
			if name, _ := v.(string); name == "null" {
				out["nullable"] = true
			} else if name != "" {
				out["type"] = strings.ToUpper(name)
			}
		}
	}
	for k, v := range s {
		if geminiKeywords[k] {
			out[k] = v
		}
	}
	if f, _ := s["format"].(string); geminiFormats[f] {
		out["format"] = f
	}
	if enum, ok := out["enum"].([]any); ok {
		// Gemini enums are strings, declared on STRING with format "enum".
		strs := make([]any, 0, len(enum))
		for _, e := range enum {
			strs = append(strs, fmt.Sprint(e))
		}
		out["enum"], out["type"], out["format"] = strs, "STRING", "enum"
	}
	if props, ok := s["properties"].(map[string]any); ok {
		converted := map[string]any{}
		for name, p := range props {
			if ps, ok := p.(map[string]any); ok {
				converted[name] = convertSchema(ps)
			}
		}
		out["properties"] = converted
		var required []any
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, _ := r.(string); converted[name] != nil {
					required = append(required, name)
				}
			}
		}
		if len(required) > 0 {
			out["required"] = required
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		out["items"] = convertSchema(items)
	}
	if anyOf, ok := s["anyOf"].([]any); ok {
		var branches []any
		for _, b := range anyOf {
			if bs, ok := b.(map[string]any); ok {
				branches = append(branches, convertSchema(bs))
			}
		}
		out["anyOf"] = branches
	}
	return out
}
