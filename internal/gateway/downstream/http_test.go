package downstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/audit"
)

type oneKey struct {
	id  string
	rec auth.KeyRecord
}

func (k oneKey) APIKeyByID(_ context.Context, id string) (auth.KeyRecord, error) {
	if id != k.id {
		return auth.KeyRecord{}, auth.ErrKeyNotFound
	}
	return k.rec, nil
}

type bearer struct{ key string }

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(req)
}

// connectAs serves the gateway behind the real bearer middleware and returns an MCP client
// session authenticated as user 42.
func connectAs(t *testing.T, h *harness) *mcp.ClientSession {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{ID: 42, Email: "ada@example.com", Role: "admin"}
	store := oneKey{id: key.ID, rec: auth.KeyRecord{User: user, SecretHash: key.SecretHash}}
	srv := httptest.NewServer(sdkauth.RequireBearerToken(auth.Verifier(store, auth.VerifierOptions{}), nil)(h.gw.Handler()))
	t.Cleanup(srv.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: bearer{key.Plaintext}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// Over real HTTP the user travels from the API key through the bearer middleware and the
// session to the audit row; in-process transports carry no TokenInfo, so only this test
// would notice it getting lost.
func TestAuditRowCarriesTheAuthenticatedUserOverHTTP(t *testing.T) {
	h := setup(t, Options{})
	cs := connectAs(t, h)
	if _, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "woo__list_orders", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	entries := h.audit.all()
	if len(entries) != 1 || entries[0].UserID != 42 {
		t.Fatalf("audit entries = %+v, want one with UserID 42", entries)
	}
}

// allowOnce lets each user make one call, then refuses with 30s to wait.
type allowOnce struct{ seen map[int64]bool }

func (a allowOnce) Allow(_ context.Context, userID int64) (bool, time.Duration, error) {
	if a.seen[userID] {
		return false, 30 * time.Second, nil
	}
	a.seen[userID] = true
	return true, 0, nil
}

func (allowOnce) Limit() int { return 1 }

func TestCallsOverTheRateLimitAreRefusedAndAudited(t *testing.T) {
	h := setup(t, Options{Limiter: allowOnce{seen: map[int64]bool{}}})
	cs := connectAs(t, h)
	call := func() *mcp.CallToolResult {
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "woo__list_orders", Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(); res.IsError {
		t.Fatalf("the first call was refused: %s", text(res))
	}
	res := call()
	want := "rate limit reached: at most 1 tool calls a minute; try again in 30s"
	if !res.IsError || text(res) != want {
		t.Fatalf("over the limit: isError %v, %q; want %q", res.IsError, text(res), want)
	}
	entries := h.audit.all()
	if len(entries) != 2 || entries[1].Outcome != audit.RateLimited || entries[1].UserID != 42 {
		t.Fatalf("audit = %+v; the refused call must be recorded as rate_limited for its user", entries)
	}
}
