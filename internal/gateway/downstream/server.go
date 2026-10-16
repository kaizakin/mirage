// Package downstream is the MCP endpoint clients connect to. It exposes every connected
// upstream's tools as <slug>__<tool> on one shared server and routes each call to its
// upstream. See docs/tradeoffs.md for why one shared server rather than one per user.
package downstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/gateway/naming"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
)

// Upstreams is the view of the upstream manager the endpoint needs.
type Upstreams interface {
	Snapshot() []upstream.State
	Session(slug string) (*mcp.ClientSession, error)
}

// Options tune a Server. Zero fields take the defaults.
type Options struct {
	// CallTimeout bounds each upstream tools/call. Default 20s.
	CallTimeout time.Duration
	Version     string
}

// Server is the gateway's downstream MCP endpoint.
type Server struct {
	upstreams   Upstreams
	callTimeout time.Duration
	server      *mcp.Server

	mu         sync.Mutex // serializes Sync
	registered map[string]*mcp.Tool
}

// New returns a Server over upstreams. Call Sync whenever the upstreams change.
func New(upstreams Upstreams, opts Options) *Server {
	s := &Server{
		upstreams:   upstreams,
		callTimeout: opts.CallTimeout,
		registered:  map[string]*mcp.Tool{},
	}
	if s.callTimeout <= 0 {
		s.callTimeout = 20 * time.Second
	}
	// Declared up front: the tools capability must be advertised even before any upstream
	// has connected, and clients must know to expect tools/list_changed.
	s.server = mcp.NewServer(&mcp.Implementation{Name: "mirage-gateway", Version: opts.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	return s
}

// Handler serves the endpoint over streamable HTTP.
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.server }, nil)
}

// MCPServer exposes the underlying server, for wiring other transports in tests.
func (s *Server) MCPServer() *mcp.Server { return s.server }

// Sync makes the registered tools match the connected upstreams'. The SDK notifies every
// live session with tools/list_changed when the set changes.
func (s *Server) Sync() {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := map[string]*mcp.Tool{}
	for _, st := range s.upstreams.Snapshot() {
		if st.Status != upstream.Connected {
			continue
		}
		for _, t := range st.Tools {
			name, err := naming.Encode(st.Slug, t.Name)
			if err != nil {
				slog.Warn("skipping upstream tool", "upstream", st.Slug, "tool", t.Name, "reason", err)
				continue
			}
			exposed := *t
			exposed.Name = name
			want[name] = &exposed
		}
	}

	var removed []string
	for name, t := range s.registered {
		if w, ok := want[name]; !ok || !reflect.DeepEqual(w, t) {
			removed = append(removed, name)
			delete(s.registered, name)
		}
	}
	if len(removed) > 0 {
		s.server.RemoveTools(removed...)
	}
	added := 0
	for name, t := range want {
		if _, ok := s.registered[name]; ok {
			continue
		}
		if err := s.register(t); err != nil {
			slog.Warn("skipping upstream tool", "tool", name, "reason", err)
			continue
		}
		s.registered[name] = t
		added++
	}
	if added > 0 || len(removed) > 0 {
		slog.Info("tools changed", "added", added, "removed", len(removed), "total", len(s.registered))
	}
}

// register adds one tool. Upstream schemas are untrusted, and the SDK panics on a schema
// it cannot use, so a bad tool is reported instead of taking the gateway down.
func (s *Server) register(t *mcp.Tool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unusable tool definition: %v", r)
		}
	}()
	s.server.AddTool(t, s.callTool)
	return nil
}

// callTool routes a call to the upstream named by the tool's slug prefix.
func (s *Server) callTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slug, tool, ok := naming.Decode(req.Params.Name)
	if !ok {
		return errorResult(fmt.Sprintf("unknown tool %q", req.Params.Name)), nil
	}
	session, err := s.upstreams.Session(slug)
	if err != nil {
		return errorResult(err.Error()), nil
	}

	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	res, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: tool, Arguments: req.Params.Arguments})
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		// The client went away; there is nobody to answer.
		return nil, ctx.Err()
	case errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return errorResult(fmt.Sprintf("upstream %s did not answer %s within %s", slug, tool, s.callTimeout)), nil
	default:
		return errorResult(fmt.Sprintf("upstream %s is unreachable: %s", slug, upstream.Reason(err))), nil
	}
}

// errorResult is a tool error the model can read and act on, rather than a protocol error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
