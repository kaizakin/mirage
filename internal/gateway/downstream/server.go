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

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/audit"
	"github.com/kaizakin/mirage/internal/gateway/naming"
	"github.com/kaizakin/mirage/internal/gateway/policy"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
)

// Upstreams is the view of the upstream manager the endpoint needs.
type Upstreams interface {
	Snapshot() []upstream.State
	Session(slug string) (*mcp.ClientSession, error)
}

// Options tune a Server. Zero fields take the defaults.
type Options struct {
	// Audit records every tool call. Required.
	Audit audit.Recorder
	// CallTimeout bounds each upstream tools/call. Default 20s.
	CallTimeout time.Duration
	Version     string
	// SessionID makes new session IDs; with several replicas, IDs name the replica that owns
	// the session (see package affinity). Default: random IDs.
	SessionID func() string
	// Limiter caps each user's tool calls; nil means no limit.
	Limiter Limiter
	// Policy holds the rules for which tools each member may use; nil allows every tool.
	Policy policy.Source
}

// Limiter decides whether a user may make another call now. A Limiter error lets the call
// through: the limit protects capacity, and a store outage should not stop every tool.
type Limiter interface {
	Allow(ctx context.Context, userID int64) (bool, time.Duration, error)
	Limit() int
}

// Server is the gateway's downstream MCP endpoint.
type Server struct {
	upstreams   Upstreams
	audit       audit.Recorder
	callTimeout time.Duration
	limiter     Limiter
	policy      policy.Source
	server      *mcp.Server

	mu         sync.Mutex // serializes Sync
	registered map[string]*mcp.Tool
}

// New returns a Server over upstreams. Call Sync whenever the upstreams change.
func New(upstreams Upstreams, opts Options) *Server {
	s := &Server{
		upstreams:   upstreams,
		audit:       opts.Audit,
		callTimeout: opts.CallTimeout,
		limiter:     opts.Limiter,
		policy:      opts.Policy,
		registered:  map[string]*mcp.Tool{},
	}
	if s.callTimeout <= 0 {
		s.callTimeout = 20 * time.Second
	}
	// Declared up front: the tools capability must be advertised even before any upstream
	// has connected, and clients must know to expect tools/list_changed.
	s.server = mcp.NewServer(&mcp.Implementation{Name: "mirage-gateway", Version: opts.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
		GetSessionID: opts.SessionID,
	})
	s.addMetaTools()
	s.server.AddReceivingMiddleware(s.listFor)
	return s
}

// sessionTimeout closes sessions left idle, such as those of clients that crashed without
// ending them; each would otherwise live forever and receive every tools/list_changed.
const sessionTimeout = 30 * time.Minute

// Handler serves the endpoint over streamable HTTP.
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.server },
		&mcp.StreamableHTTPOptions{SessionTimeout: sessionTimeout})
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

// callTool routes a call to the upstream named by the tool's slug prefix and records it.
// The record is written in a defer, so every way a call can end is audited.
func (s *Server) callTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slug, tool, _ := naming.Decode(req.Params.Name)
	entry := audit.Entry{ServerSlug: slug, Tool: tool, StartedAt: time.Now()}
	user, _ := caller(req.Extra)
	entry.UserID = user.ID
	defer func() {
		entry.Duration = time.Since(entry.StartedAt)
		s.record(ctx, entry)
	}()

	p, err := s.policyFor(ctx, req.Extra)
	if err != nil {
		msg := "could not check your tool permissions; try again"
		entry.Outcome, entry.Error = audit.Denied, msg
		return errorResult(msg), nil
	}
	if !p.Allows(slug, tool) {
		msg := fmt.Sprintf("%s is not permitted for your account; ask an admin for access", req.Params.Name)
		entry.Outcome, entry.Error = audit.Denied, msg
		return errorResult(msg), nil
	}
	if msg, limited := s.overLimit(ctx, entry.UserID); limited {
		entry.Outcome, entry.Error = audit.RateLimited, msg
		return errorResult(msg), nil
	}
	res, outcome, msg := s.route(ctx, slug, tool, req.Params.Arguments)
	entry.Outcome, entry.Error = outcome, msg
	if outcome == audit.Canceled {
		// The client went away; there is nobody to answer.
		return nil, ctx.Err()
	}
	return res, nil
}

// listFor shapes tools/list for the caller: in search mode only the gateway's own tools,
// otherwise every upstream tool the caller may use. Calls are checked again in callTool, so a
// client that guesses a hidden name is still refused.
func (s *Server) listFor(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		list, ok := res.(*mcp.ListToolsResult)
		if err != nil || !ok {
			return res, err
		}
		p, err := s.policyFor(ctx, req.GetExtra())
		if err != nil {
			return nil, fmt.Errorf("checking tool permissions: %w", err)
		}
		search := searchModeRequested(req.GetExtra())
		visible := make([]*mcp.Tool, 0, len(list.Tools))
		for _, t := range list.Tools {
			slug, tool, upstream := naming.Decode(t.Name)
			if upstream && !search && p.Allows(slug, tool) || !upstream && search {
				visible = append(visible, t)
			}
		}
		filtered := *list
		filtered.Tools = visible
		return &filtered, nil
	}
}

// policyFor returns the caller's tool policy. A request with no user comes from an
// in-process transport, never from a client, since HTTP requires a token.
func (s *Server) policyFor(ctx context.Context, extra *mcp.RequestExtra) (policy.Policy, error) {
	u, ok := caller(extra)
	if s.policy == nil || !ok {
		return policy.AllowAll, nil
	}
	p, err := policy.For(ctx, s.policy, u)
	if err != nil {
		slog.ErrorContext(ctx, "reading tool rules", "user", u.ID, "err", err)
	}
	return p, err
}

// caller is the authenticated user of a request. Extra is set only by HTTP transports.
func caller(extra *mcp.RequestExtra) (auth.User, bool) {
	if extra == nil {
		return auth.User{}, false
	}
	return auth.UserFromTokenInfo(extra.TokenInfo)
}

// route makes the upstream call and classifies how it ended. Every failure becomes an
// isError result a model can read, never a protocol error.
func (s *Server) route(ctx context.Context, slug, tool string, args any) (*mcp.CallToolResult, audit.Outcome, string) {
	if slug == "" {
		msg := "unknown tool: gateway tool names have the form <server>__<tool>"
		return errorResult(msg), audit.Unreachable, msg
	}
	session, err := s.upstreams.Session(slug)
	if err != nil {
		return errorResult(err.Error()), audit.Unreachable, err.Error()
	}

	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	res, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: tool, Arguments: args})
	switch {
	case err == nil && res.IsError:
		return res, audit.ToolError, upstream.Truncate(firstText(res))
	case err == nil:
		return res, audit.OK, ""
	case ctx.Err() != nil:
		return nil, audit.Canceled, "client canceled the call"
	case errors.Is(callCtx.Err(), context.DeadlineExceeded):
		msg := fmt.Sprintf("upstream %s did not answer %s within %s", slug, tool, s.callTimeout)
		return errorResult(msg), audit.Timeout, msg
	default:
		msg := fmt.Sprintf("upstream %s is unreachable: %s", slug, upstream.Reason(err))
		return errorResult(msg), audit.Unreachable, msg
	}
}

// record writes an audit entry. It outlives the request's context, so a call whose client
// disconnected is still recorded; a failed write is logged, not surfaced to the caller.
func (s *Server) record(ctx context.Context, e audit.Entry) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.audit.RecordCall(ctx, e); err != nil {
		slog.Error("writing audit row", "server", e.ServerSlug, "tool", e.Tool, "outcome", e.Outcome, "err", err)
	}
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// errorResult is a tool error the model can read and act on, rather than a protocol error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// overLimit reports whether the user is over their call limit, with a message saying when
// they may call again.
func (s *Server) overLimit(ctx context.Context, userID int64) (string, bool) {
	if s.limiter == nil || userID == 0 {
		return "", false
	}
	ok, retry, err := s.limiter.Allow(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "checking the rate limit; allowing the call", "err", err)
		return "", false
	}
	if ok {
		return "", false
	}
	return fmt.Sprintf("rate limit reached: at most %d tool calls a minute; try again in %ds",
		s.limiter.Limit(), int(retry.Round(time.Second).Seconds())), true
}
