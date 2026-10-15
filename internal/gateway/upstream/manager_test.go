package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/secretbox"
)

var fastOptions = Options{
	Interval:   100 * time.Millisecond,
	MinBackoff: 50 * time.Millisecond,
	MaxBackoff: 200 * time.Millisecond,
	Timeout:    2 * time.Second,
}

// fakeUpstream is an MCP server on a fixed loopback address that can be stopped and
// restarted there, like a deployment scaled to zero and back.
type fakeUpstream struct {
	t      *testing.T
	addr   string
	server *mcp.Server
	http   *http.Server
}

func startUpstream(t *testing.T, apiKey string) *fakeUpstream {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fake"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeUpstream{t: t, addr: ln.Addr().String(), server: server}
	f.serve(ln, apiKey)
	t.Cleanup(f.stop)
	return f
}

func (f *fakeUpstream) serve(ln net.Listener, apiKey string) {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return f.server }, nil)
	f.http = &http.Server{Handler: auth.RequireHeader("X-Api-Key", apiKey)(handler), ReadHeaderTimeout: time.Second}
	go func() { _ = f.http.Serve(ln) }()
}

func (f *fakeUpstream) stop() { _ = f.http.Close() }

func (f *fakeUpstream) restart(apiKey string) {
	ln, err := net.Listen("tcp", f.addr)
	if err != nil {
		f.t.Fatal(err)
	}
	f.serve(ln, apiKey)
}

func newBox(t *testing.T) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(bytes.Repeat([]byte{1}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func headerConfig(t *testing.T, box *secretbox.Box, slug, addr, credential string) Config {
	t.Helper()
	sealed, err := box.Encrypt([]byte(credential), []byte(slug))
	if err != nil {
		t.Fatal(err)
	}
	return Config{Slug: slug, URL: "http://" + addr + "/mcp", AuthType: AuthHeader, AuthHeader: "X-Api-Key", CredentialEnc: sealed}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func state(m *Manager, slug string) State {
	for _, s := range m.Snapshot() {
		if s.Slug == slug {
			return s
		}
	}
	return State{}
}

func TestConnectsWithInjectedCredentialAndCachesTools(t *testing.T) {
	box := newBox(t)
	up := startUpstream(t, "s3cret")
	m := NewManager(box, fastOptions)
	t.Cleanup(m.Close)
	m.Reconcile(t.Context(), []Config{headerConfig(t, box, "cur", up.addr, "s3cret")})

	waitFor(t, "connected", func() bool { return state(m, "cur").Status == Connected })
	if tools := state(m, "cur").Tools; len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("cached tools = %v, want [echo]", tools)
	}
	session, err := m.Session("cur")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{}}); err != nil {
		t.Fatalf("call through the session: %v", err)
	}
}

func TestWrongCredentialIsUnreachableWithReason(t *testing.T) {
	box := newBox(t)
	up := startUpstream(t, "s3cret")
	m := NewManager(box, fastOptions)
	t.Cleanup(m.Close)
	m.Reconcile(t.Context(), []Config{headerConfig(t, box, "cur", up.addr, "wrong")})

	waitFor(t, "unreachable", func() bool { return state(m, "cur").Status == Unreachable })
	if reason := state(m, "cur").Reason; !strings.HasSuffix(reason, "Unauthorized") {
		t.Fatalf("reason = %q, want it to end in the HTTP status, Unauthorized", reason)
	}
}

func TestRecoversAfterUpstreamRestarts(t *testing.T) {
	box := newBox(t)
	up := startUpstream(t, "s3cret")
	m := NewManager(box, fastOptions)
	t.Cleanup(m.Close)
	m.Reconcile(t.Context(), []Config{headerConfig(t, box, "cur", up.addr, "s3cret")})
	waitFor(t, "connected", func() bool { return state(m, "cur").Status == Connected })

	up.stop()
	waitFor(t, "unreachable after stop", func() bool { return state(m, "cur").Status == Unreachable })
	s := state(m, "cur")
	if s.Reason == "" || len(s.Tools) != 0 {
		t.Fatalf("unreachable state = %+v, want a reason and no tools", s)
	}
	if _, err := m.Session("cur"); err == nil || !strings.Contains(err.Error(), "upstream cur is unreachable: ") {
		t.Fatalf("Session error = %v, want it to name the upstream and reason", err)
	}

	up.restart("s3cret")
	waitFor(t, "reconnected", func() bool { return state(m, "cur").Status == Connected })
}

func TestToolListChangedRefreshesBeforeNextCheck(t *testing.T) {
	box := newBox(t)
	up := startUpstream(t, "s3cret")
	slow := fastOptions
	slow.Interval = time.Hour // only the notification can trigger a refresh
	m := NewManager(box, slow)
	t.Cleanup(m.Close)
	m.Reconcile(t.Context(), []Config{headerConfig(t, box, "cur", up.addr, "s3cret")})
	waitFor(t, "connected", func() bool { return state(m, "cur").Status == Connected })

	mcp.AddTool(up.server, &mcp.Tool{Name: "added", Description: "added"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})
	waitFor(t, "new tool in cache", func() bool { return len(state(m, "cur").Tools) == 2 })
}

func TestReconcileStopsRemovedUpstreamsAndReportsChanges(t *testing.T) {
	box := newBox(t)
	up := startUpstream(t, "s3cret")
	var changes atomic.Int32
	opts := fastOptions
	opts.OnChange = func() { changes.Add(1) }
	m := NewManager(box, opts)
	t.Cleanup(m.Close)
	cfg := headerConfig(t, box, "cur", up.addr, "s3cret")
	m.Reconcile(t.Context(), []Config{cfg})
	waitFor(t, "connected", func() bool { return state(m, "cur").Status == Connected })

	m.Reconcile(t.Context(), []Config{cfg}) // unchanged: must not restart
	before := changes.Load()
	m.Reconcile(t.Context(), nil)
	if len(m.Snapshot()) != 0 || changes.Load() == before {
		t.Fatalf("after removal: snapshot %v, changes %d -> %d", m.Snapshot(), before, changes.Load())
	}
	if _, err := m.Session("cur"); err == nil {
		t.Fatal("Session still returns a removed upstream")
	}
}

func TestUndecryptableCredentialIsReportedNotRetried(t *testing.T) {
	m := NewManager(newBox(t), fastOptions)
	t.Cleanup(m.Close)
	m.Reconcile(t.Context(), []Config{{Slug: "bad", URL: "http://127.0.0.1:1/mcp", AuthType: AuthBearer, CredentialEnc: []byte("junk")}})
	waitFor(t, "unreachable", func() bool { return state(m, "bad").Status == Unreachable })
	if reason := state(m, "bad").Reason; !strings.Contains(reason, "decrypt credential") {
		t.Fatalf("reason = %q, want a decrypt failure", reason)
	}
}

func TestReason(t *testing.T) {
	_, refused := net.Dial("tcp", "127.0.0.1:1")
	cases := map[string]error{
		"timed out":          context.DeadlineExceeded,
		"connection refused": refused,
		"boom":               errors.New("boom"),
		"Unauthorized":       fmt.Errorf(`calling "initialize": %w`, fmt.Errorf(`sending "initialize": %w`, errors.New("Unauthorized"))),
	}
	for want, err := range cases {
		if got := Reason(err); got != want {
			t.Errorf("Reason(%v) = %q, want %q", err, got, want)
		}
	}
}
