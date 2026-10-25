package downstream

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/gateway/naming"
	"github.com/kaizakin/mirage/internal/gateway/policy"
)

// A client with many servers behind the gateway can ask for search mode, sending
// toolModeHeader: search on its requests. Its tools/list then holds only two tools: one finds
// tools by keyword, the other calls a tool by name. The model reads a short list and fetches
// the schemas it needs, instead of every schema of every server on each turn.
const (
	toolModeHeader = "X-Mirage-Tools"
	searchMode     = "search"
)

var (
	searchToolName = naming.Gateway + naming.Separator + "search_tools"
	callToolName   = naming.Gateway + naming.Separator + "call_tool"
)

const (
	defaultSearchResults = 8
	maxSearchResults     = 25
)

type searchInput struct {
	Query string `json:"query" jsonschema:"words describing the task, such as 'list recent orders' or 'convert currency'"`
	Limit int    `json:"limit,omitempty" jsonschema:"most tools to return; default 8, at most 25"`
}

type foundTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type searchOutput struct {
	Tools []foundTool `json:"tools"`
}

type callInput struct {
	Name      string         `json:"name" jsonschema:"a tool name from search_tools, such as store-tea__list_orders"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"the tool's arguments, matching its input_schema"`
}

// addMetaTools registers the search-mode tools. They are on the shared server for every
// session, so a call works in either mode, but only search-mode lists show them.
func (s *Server) addMetaTools() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name: searchToolName,
		Description: "Find the gateway's tools for a task. Returns the best matches with their input schemas; " +
			"call one with " + callToolName + ".",
	}, s.searchTools)
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        callToolName,
		Description: "Call a tool found with " + searchToolName + ", by its full name, with arguments matching its input_schema.",
	}, s.callByName)
}

func (s *Server) searchTools(ctx context.Context, req *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
	p, err := s.policyFor(ctx, req.Extra)
	if err != nil {
		return errorResult("could not check your tool permissions; try again"), searchOutput{}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchResults
	}
	limit = min(limit, maxSearchResults)
	return nil, searchOutput{Tools: rank(s.visible(p), in.Query, limit)}, nil
}

// callByName runs the named tool exactly as a direct call would, so the caller's
// permissions, rate limit, and audit row are those of the tool itself.
func (s *Server) callByName(ctx context.Context, req *mcp.CallToolRequest, in callInput) (*mcp.CallToolResult, any, error) {
	if !s.isUpstreamTool(in.Name) {
		return errorResult("unknown tool " + in.Name + "; find tools with " + searchToolName), nil, nil
	}
	if in.Arguments == nil {
		in.Arguments = map[string]any{}
	}
	args, err := json.Marshal(in.Arguments)
	if err != nil {
		return nil, nil, err
	}
	inner := *req
	inner.Params = &mcp.CallToolParamsRaw{Name: in.Name, Arguments: args}
	res, err := s.callTool(ctx, &inner)
	return res, nil, err
}

func (s *Server) isUpstreamTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.registered[name]
	return ok
}

// visible returns the upstream tools p allows.
func (s *Server) visible(p policy.Policy) []*mcp.Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*mcp.Tool, 0, len(s.registered))
	for name, t := range s.registered {
		if slug, tool, _ := naming.Decode(name); p.Allows(slug, tool) {
			out = append(out, t)
		}
	}
	return out
}

// rank scores tools by the query's words: a word that is a whole part of the tool's name
// counts most, one inside the name less, one in the description least. Tools no word
// matches are left out.
func rank(tools []*mcp.Tool, query string, limit int) []foundTool {
	words := split(query)
	type scored struct {
		t     *mcp.Tool
		score int
	}
	var hits []scored
	for _, t := range tools {
		name, parts, desc := strings.ToLower(t.Name), split(t.Name), strings.ToLower(t.Description)
		score := 0
		for _, w := range words {
			switch {
			case slices.Contains(parts, w):
				score += 3
			case strings.Contains(name, w):
				score += 2
			case strings.Contains(desc, w):
				score++
			}
		}
		if score > 0 {
			hits = append(hits, scored{t, score})
		}
	}
	slices.SortFunc(hits, func(a, b scored) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.t.Name, b.t.Name))
	})
	out := make([]foundTool, 0, min(limit, len(hits)))
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, foundTool{Name: h.t.Name, Description: h.t.Description, InputSchema: h.t.InputSchema})
	}
	return out
}

// split lowercases s and cuts it into words at anything but letters and digits, dropping
// short words such as "a" and "of" that would match nearly everything.
func split(s string) []string {
	return slices.DeleteFunc(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), func(w string) bool { return len(w) < 3 })
}

func searchModeRequested(extra *mcp.RequestExtra) bool {
	return extra != nil && strings.EqualFold(extra.Header.Get(toolModeHeader), searchMode)
}
