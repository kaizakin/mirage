// Package upstream keeps one MCP client session per enabled upstream server. Each server
// gets a goroutine that connects, caches its tools, re-lists them on an interval as a
// health check, and reconnects with exponential backoff; callers read a consistent
// snapshot at any time.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/secretbox"
)

// AuthType is how the gateway authenticates to an upstream.
type AuthType string

// Supported upstream authentication schemes.
const (
	AuthNone   AuthType = "none"
	AuthBearer AuthType = "bearer"
	AuthHeader AuthType = "header"
)

// Config is one upstream server as stored, with its credential still sealed.
type Config struct {
	Slug          string
	URL           string
	AuthType      AuthType
	AuthHeader    string
	CredentialEnc []byte
}

// Status is an upstream's connection state.
type Status string

// Upstream connection states.
const (
	Connecting  Status = "connecting"
	Connected   Status = "connected"
	Unreachable Status = "unreachable"
)

// State is a point-in-time view of one upstream. Callers must not modify Tools.
type State struct {
	Slug   string
	Status Status
	// Reason says why the upstream is unreachable; empty otherwise.
	Reason string
	Tools  []*mcp.Tool
	Since  time.Time
}

// Options tune a Manager. Zero fields take the defaults.
type Options struct {
	// Interval between health checks of a connected upstream. Default 15s.
	Interval time.Duration
	// MinBackoff and MaxBackoff bound the wait between reconnect attempts. Default 1s and 30s.
	MinBackoff, MaxBackoff time.Duration
	// Timeout bounds connecting and each health check. Default 10s.
	Timeout time.Duration
	// OnChange, if set, is called after any upstream's status or tools change. It runs on
	// the upstream's goroutine and must not block for long.
	OnChange func()
}

// Manager owns the sessions to every configured upstream.
type Manager struct {
	box  *secretbox.Box
	opts Options

	mu        sync.RWMutex
	upstreams map[string]*upstream
}

type upstream struct {
	cfg  Config
	stop context.CancelFunc
	done chan struct{}

	// Guarded by Manager.mu.
	state   State
	session *mcp.ClientSession
}

// NewManager returns a Manager that decrypts upstream credentials with box.
func NewManager(box *secretbox.Box, opts Options) *Manager {
	opts.Interval = cmpOr(opts.Interval, 15*time.Second)
	opts.MinBackoff = cmpOr(opts.MinBackoff, time.Second)
	opts.MaxBackoff = cmpOr(opts.MaxBackoff, 30*time.Second)
	opts.Timeout = cmpOr(opts.Timeout, 10*time.Second)
	return &Manager{box: box, opts: opts, upstreams: map[string]*upstream{}}
}

func cmpOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Reconcile makes the running upstreams match configs: it starts new ones, stops removed
// ones, and restarts any whose configuration changed. Calling it again with the same
// configs does nothing. The goroutines it starts live until ctx is done or Close is called.
func (m *Manager) Reconcile(ctx context.Context, configs []Config) {
	want := make(map[string]Config, len(configs))
	for _, c := range configs {
		want[c.Slug] = c
	}

	m.mu.Lock()
	var stale []*upstream
	for slug, u := range m.upstreams {
		if c, ok := want[slug]; !ok || !reflect.DeepEqual(c, u.cfg) {
			stale = append(stale, u)
			delete(m.upstreams, slug)
		}
	}
	for slug, c := range want {
		if _, running := m.upstreams[slug]; running {
			continue
		}
		uctx, stop := context.WithCancel(ctx)
		u := &upstream{cfg: c, stop: stop, done: make(chan struct{}),
			state: State{Slug: slug, Status: Connecting, Since: time.Now()}}
		m.upstreams[slug] = u
		go m.run(uctx, u)
	}
	m.mu.Unlock()

	// Waited outside the lock: a stopping goroutine may still need it to record its state.
	for _, u := range stale {
		u.stop()
		<-u.done
	}
	if len(stale) > 0 {
		m.changed()
	}
}

// Close stops every upstream and waits for their goroutines to exit.
func (m *Manager) Close() {
	m.Reconcile(context.Background(), nil)
}

// Snapshot returns the state of every upstream, sorted by slug.
func (m *Manager) Snapshot() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make([]State, 0, len(m.upstreams))
	for _, u := range m.upstreams {
		states = append(states, u.state)
	}
	slices.SortFunc(states, func(a, b State) int { return strings.Compare(a.Slug, b.Slug) })
	return states
}

// Session returns the live session for slug, or an error saying why there is none, such
// as "upstream currency is unreachable: connection refused".
func (m *Manager) Session(slug string) (*mcp.ClientSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.upstreams[slug]
	switch {
	case !ok:
		return nil, fmt.Errorf("upstream %s is not configured or is disabled", slug)
	case u.state.Status == Unreachable:
		return nil, fmt.Errorf("upstream %s is unreachable: %s", slug, u.state.Reason)
	case u.session == nil:
		return nil, fmt.Errorf("upstream %s is still connecting", slug)
	}
	return u.session, nil
}

func (m *Manager) run(ctx context.Context, u *upstream) {
	defer close(u.done)
	log := slog.With("upstream", u.cfg.Slug)
	hc, err := httpClient(u.cfg, m.box)
	if hc != nil {
		defer hc.CloseIdleConnections()
	}
	if err != nil {
		// A credential that will not decrypt will not start decrypting on retry.
		m.set(u, Unreachable, err.Error(), nil, nil)
		log.Error("upstream misconfigured", "reason", err)
		<-ctx.Done()
		return
	}

	backoff := m.opts.MinBackoff
	for {
		session, tools, err := m.connect(ctx, u.cfg, hc)
		if err == nil {
			backoff = m.opts.MinBackoff
			m.set(u, Connected, "", tools, session)
			log.Info("upstream connected", "tools", len(tools))
			err = m.watch(ctx, u, session)
			_ = session.Close()
		}
		if ctx.Err() != nil {
			return
		}
		reason := Reason(err)
		m.set(u, Unreachable, reason, nil, nil)
		log.Warn("upstream unreachable", "reason", reason, "retry_in", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
		}
		backoff = min(backoff*2, m.opts.MaxBackoff)
	}
}

// connect opens a session and lists its tools. The SDK detaches the session's lifetime
// from the context passed to Connect, so a timeout here bounds only the handshake.
//
// The standalone server-sent event stream stays off: some upstreams (DeepWiki, which is
// stateless) hold that GET open without answering, stalling Connect for over 15s. The
// gateway needs no server-initiated messages from upstreams; the interval check picks up
// tool changes.
func (m *Manager) connect(ctx context.Context, cfg Config, hc *http.Client) (*mcp.ClientSession, []*mcp.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, m.opts.Timeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "mirage-gateway"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: hc, DisableStandaloneSSE: true}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, err
	}
	tools, err := listTools(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	return session, tools, nil
}

// watch health-checks a connected session until a check fails or ctx is done, refreshing
// the cached tools on every tick.
func (m *Manager) watch(ctx context.Context, u *upstream, session *mcp.ClientSession) error {
	ticker := time.NewTicker(m.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		checkCtx, cancel := context.WithTimeout(ctx, m.opts.Timeout)
		tools, err := listTools(checkCtx, session)
		cancel()
		if err != nil {
			return err
		}
		m.set(u, Connected, "", tools, session)
	}
}

// maxTools bounds what one upstream can make the gateway hold and serve to every client.
const maxTools = 1000

func listTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if len(tools) == maxTools {
			return nil, fmt.Errorf("lists more than %d tools", maxTools)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// set records a new state and reports a change only if status, reason, or tools differ.
func (m *Manager) set(u *upstream, status Status, reason string, tools []*mcp.Tool, session *mcp.ClientSession) {
	m.mu.Lock()
	old := u.state
	changed := old.Status != status || old.Reason != reason || !reflect.DeepEqual(old.Tools, tools)
	if changed {
		u.state = State{Slug: u.cfg.Slug, Status: status, Reason: reason, Tools: tools, Since: time.Now()}
	}
	u.session = session
	m.mu.Unlock()
	if changed {
		m.changed()
	}
}

func (m *Manager) changed() {
	if m.opts.OnChange != nil {
		m.opts.OnChange()
	}
}

// jitter spreads reconnects by up to ±20% so upstreams that failed together do not retry in lockstep.
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// Reason condenses a connection or call error into the short phrase operators and models
// see, such as "connection refused" or "timed out".
func Reason(err error) string {
	var dnsErr *net.DNSError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	case errors.As(err, &dnsErr):
		return "host not found: " + dnsErr.Name
	}
	// The innermost error is the cause; the layers above it name SDK internals.
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return Truncate(err.Error())
}

// maxReasonLen bounds upstream-controlled text the gateway passes on to models and logs.
const maxReasonLen = 512

// Truncate shortens s to at most 512 bytes, without splitting a UTF-8 character.
func Truncate(s string) string {
	if len(s) <= maxReasonLen {
		return s
	}
	cut := maxReasonLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
