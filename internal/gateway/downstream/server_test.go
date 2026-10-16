package downstream

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/gateway/audit"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
)

type echoArgs struct {
	Text string `json:"text,omitempty"`
	Wait int    `json:"wait_ms,omitempty"`
}

// newUpstream starts an in-memory MCP server whose tools echo their name and arguments,
// waiting wait_ms first, and returns a client session to it.
func newUpstream(t *testing.T, name string, tools ...string) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	for _, tool := range tools {
		mcp.AddTool(server, &mcp.Tool{Name: tool, Description: name + " " + tool},
			func(ctx context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
				select {
				case <-time.After(time.Duration(in.Wait) * time.Millisecond):
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
				if in.Text == "fail" {
					return nil, nil, fmt.Errorf("%s refused", tool)
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name + "/" + tool + ":" + in.Text}}}, nil, nil
			})
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "gateway"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// fakeUpstreams stands in for the upstream manager.
type fakeUpstreams struct {
	mu       sync.Mutex
	states   map[string]upstream.State
	sessions map[string]*mcp.ClientSession
}

func (f *fakeUpstreams) connect(t *testing.T, slug string, cs *mcp.ClientSession) {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[slug] = upstream.State{Slug: slug, Status: upstream.Connected, Tools: res.Tools}
	f.sessions[slug] = cs
}

func (f *fakeUpstreams) drop(slug, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[slug] = upstream.State{Slug: slug, Status: upstream.Unreachable, Reason: reason}
	delete(f.sessions, slug)
}

func (f *fakeUpstreams) Snapshot() []upstream.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []upstream.State
	for _, s := range f.states {
		out = append(out, s)
	}
	return out
}

func (f *fakeUpstreams) Session(slug string) (*mcp.ClientSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cs, ok := f.sessions[slug]; ok {
		return cs, nil
	}
	return nil, fmt.Errorf("upstream %s is unreachable: %s", slug, f.states[slug].Reason)
}

type recorder struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recorder) RecordCall(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *recorder) all() []audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.entries)
}

type harness struct {
	audit   *recorder
	ups     *fakeUpstreams
	gw      *Server
	client  *mcp.ClientSession
	changed chan struct{}
}

func setup(t *testing.T, opts Options) *harness {
	t.Helper()
	h := &harness{
		audit:   &recorder{},
		ups:     &fakeUpstreams{states: map[string]upstream.State{}, sessions: map[string]*mcp.ClientSession{}},
		changed: make(chan struct{}, 10),
	}
	h.ups.connect(t, "woo", newUpstream(t, "woo", "list_orders", "get_order"))
	h.ups.connect(t, "currency", newUpstream(t, "currency", "convert", "slow_echo"))
	opts.Audit = h.audit
	h.gw = New(h.ups, opts)
	h.gw.Sync()

	st, ct := mcp.NewInMemoryTransports()
	if _, err := h.gw.MCPServer().Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "claude"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { h.changed <- struct{}{} },
	})
	cs, err := client.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	h.client = cs
	return h
}

func (h *harness) toolNames(t *testing.T) []string {
	t.Helper()
	res, err := h.client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func (h *harness) call(t *testing.T, name string, args echoArgs) *mcp.CallToolResult {
	t.Helper()
	res, err := h.client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v; tool failures must be isError results", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	b, _ := json.Marshal(res.Content[0])
	return string(b)
}

func TestListsPrefixedToolsOfConnectedUpstreams(t *testing.T) {
	h := setup(t, Options{})
	want := []string{"currency__convert", "currency__slow_echo", "woo__get_order", "woo__list_orders"}
	if got := h.toolNames(t); !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestDroppedUpstreamLeavesListAndNotifiesClients(t *testing.T) {
	h := setup(t, Options{})
	h.ups.drop("currency", "connection refused")
	h.gw.Sync()

	select {
	case <-h.changed:
	case <-time.After(5 * time.Second):
		t.Fatal("client got no tools/list_changed")
	}
	if got := h.toolNames(t); !slices.Equal(got, []string{"woo__get_order", "woo__list_orders"}) {
		t.Fatalf("tools after currency dropped = %v", got)
	}
}

func TestRepeatedSyncDoesNotChurn(t *testing.T) {
	h := setup(t, Options{})
	for range 3 {
		h.gw.Sync()
	}
	select {
	case <-h.changed:
		t.Fatal("an unchanged sync sent tools/list_changed")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCallRoutesToUpstreamWithOriginalName(t *testing.T) {
	h := setup(t, Options{})
	res := h.call(t, "woo__list_orders", echoArgs{Text: "today"})
	if res.IsError || text(res) != "woo/list_orders:today" {
		t.Fatalf("result = %q (isError %v), want woo/list_orders:today", text(res), res.IsError)
	}
}

func TestCallToUnreachableUpstreamIsToolError(t *testing.T) {
	h := setup(t, Options{})
	h.ups.drop("currency", "connection refused")
	// The client may still hold the old list; the call must fail cleanly anyway.
	res := h.call(t, "currency__convert", echoArgs{})
	if !res.IsError || text(res) != "upstream currency is unreachable: connection refused" {
		t.Fatalf("result = %q (isError %v)", text(res), res.IsError)
	}
}

func TestSlowCallTimesOutWhileOthersProceed(t *testing.T) {
	h := setup(t, Options{CallTimeout: 300 * time.Millisecond})
	var wg sync.WaitGroup
	var slow *mcp.CallToolResult
	start := time.Now()
	wg.Go(func() { slow = h.call(t, "currency__slow_echo", echoArgs{Wait: 10_000}) })
	fast := h.call(t, "woo__list_orders", echoArgs{Text: "x"})
	fastTook := time.Since(start)
	wg.Wait()

	if fast.IsError || fastTook > 200*time.Millisecond {
		t.Fatalf("parallel call: %q after %v, want an answer well before the slow call's timeout", text(fast), fastTook)
	}
	if !slow.IsError || text(slow) != "upstream currency did not answer slow_echo within 300ms" {
		t.Fatalf("slow call = %q (isError %v)", text(slow), slow.IsError)
	}
}

func TestUnusableUpstreamToolIsSkipped(t *testing.T) {
	h := setup(t, Options{})
	h.ups.mu.Lock()
	st := h.ups.states["woo"]
	st.Tools = append(slices.Clone(st.Tools), &mcp.Tool{Name: "broken", InputSchema: map[string]any{"type": "string"}})
	h.ups.states["woo"] = st
	h.ups.mu.Unlock()

	h.gw.Sync() // must not panic
	if got := h.toolNames(t); slices.Contains(got, "woo__broken") || !slices.Contains(got, "woo__list_orders") {
		t.Fatalf("tools = %v, want the broken tool skipped and the rest kept", got)
	}
}

func TestEveryCallIsAuditedWithItsOutcome(t *testing.T) {
	h := setup(t, Options{CallTimeout: 300 * time.Millisecond})
	h.call(t, "woo__list_orders", echoArgs{Text: "today"})
	h.call(t, "woo__get_order", echoArgs{Text: "fail"})
	h.call(t, "currency__slow_echo", echoArgs{Wait: 10_000})
	h.ups.drop("currency", "connection refused")
	h.call(t, "currency__convert", echoArgs{})

	type row struct {
		slug, tool string
		outcome    audit.Outcome
		err        string
	}
	var got []row
	for _, e := range h.audit.all() {
		if e.StartedAt.IsZero() || e.Duration < 0 {
			t.Errorf("entry %+v lacks a start time or duration", e)
		}
		got = append(got, row{e.ServerSlug, e.Tool, e.Outcome, e.Error})
	}
	want := []row{
		{"woo", "list_orders", audit.OK, ""},
		{"woo", "get_order", audit.ToolError, "get_order refused"},
		{"currency", "slow_echo", audit.Timeout, "upstream currency did not answer slow_echo within 300ms"},
		{"currency", "convert", audit.Unreachable, "upstream currency is unreachable: connection refused"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("audit rows:\n got %+v\nwant %+v", got, want)
	}
}
