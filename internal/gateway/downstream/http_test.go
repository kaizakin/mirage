package downstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/auth"
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

// Over real HTTP the user travels from the API key through the bearer middleware and the
// session to the audit row; in-process transports carry no TokenInfo, so only this test
// would notice it getting lost.
func TestAuditRowCarriesTheAuthenticatedUserOverHTTP(t *testing.T) {
	h := setup(t, Options{})
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{ID: 42, Email: "ada@example.com", Role: "admin"}
	store := oneKey{id: key.ID, rec: auth.KeyRecord{User: user, SecretHash: key.SecretHash}}
	srv := httptest.NewServer(sdkauth.RequireBearerToken(auth.Verifier(store, nil), nil)(h.gw.Handler()))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "claude"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: bearer{key.Plaintext}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if _, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "woo__list_orders", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}

	entries := h.audit.all()
	if len(entries) != 1 || entries[0].UserID != user.ID {
		t.Fatalf("audit entries = %+v, want one with UserID %d", entries, user.ID)
	}
}
