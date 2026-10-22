package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/store"
)

// memStore keeps clients, codes, and tokens in memory with the real store's rules: codes
// and refresh tokens are used once, and expired ones are invisible.
type memStore struct {
	mu      sync.Mutex
	clients map[string]store.OAuthClient
	grants  map[int64]store.OAuthGrant
	codes   map[string]store.OAuthCode
	codeTo  map[string]int64
	tokens  map[string]store.IssuedToken
	tokenTo map[string]int64
	users   map[int64]auth.User
}

func newMemStore() *memStore {
	return &memStore{clients: map[string]store.OAuthClient{}, grants: map[int64]store.OAuthGrant{},
		codes: map[string]store.OAuthCode{}, codeTo: map[string]int64{}, tokens: map[string]store.IssuedToken{},
		tokenTo: map[string]int64{}, users: map[int64]auth.User{7: {ID: 7, Email: "ada@example.com", Role: "member"}}}
}

func (m *memStore) CreateOAuthClient(_ context.Context, c store.OAuthClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[c.ID] = c
	return nil
}

func (m *memStore) OAuthClientByID(_ context.Context, id string) (store.OAuthClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[id]
	if !ok {
		return c, store.ErrNotFound
	}
	return c, nil
}

func (m *memStore) CreateOAuthCode(_ context.Context, c store.OAuthCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := int64(len(m.grants) + 1)
	m.grants[id] = store.OAuthGrant{ID: id, UserID: c.UserID, ClientID: c.ClientID, Resource: c.Resource}
	m.codes[string(c.Hash)], m.codeTo[string(c.Hash)] = c, id
	return nil
}

func (m *memStore) RedeemOAuthCode(_ context.Context, hash []byte) (store.OAuthGrant, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[string(hash)]
	delete(m.codes, string(hash))
	if !ok || time.Now().After(c.ExpiresAt) {
		return store.OAuthGrant{}, "", "", store.ErrNotFound
	}
	return m.grants[m.codeTo[string(hash)]], c.RedirectURI, c.CodeChallenge, nil
}

func (m *memStore) RedeemOAuthRefresh(_ context.Context, hash []byte) (store.OAuthGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[string(hash)]
	if !ok || t.Kind != "refresh" || time.Now().After(t.ExpiresAt) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	delete(m.tokens, string(hash))
	return m.grants[m.tokenTo[string(hash)]], nil
}

func (m *memStore) StoreOAuthTokens(_ context.Context, grantID int64, tokens ...store.IssuedToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range tokens {
		m.tokens[string(t.Hash)], m.tokenTo[string(t.Hash)] = t, grantID
	}
	return nil
}

func (m *memStore) OAuthAccess(_ context.Context, hash []byte) (auth.OAuthAccess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[string(hash)]
	if !ok || t.Kind != "access" || time.Now().After(t.ExpiresAt) {
		return auth.OAuthAccess{}, auth.ErrInvalidOAuthToken
	}
	g := m.grants[m.tokenTo[string(hash)]]
	return auth.OAuthAccess{User: m.users[g.UserID], Resource: g.Resource, ExpiresAt: t.ExpiresAt}, nil
}

// gateway serves the authorization server and an MCP endpoint behind the gateway's real
// bearer middleware, with one tool that reports who called it.
func gateway(t *testing.T) (*Server, *memStore, *httptest.Server) {
	t.Helper()
	mem := newMemStore()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s := New(mem, Config{Issuer: srv.URL, AuthorizeURL: srv.URL + "/authorize"})
	s.Register(mux)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "whoami"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		u, _ := auth.UserFromTokenInfo(req.Extra.TokenInfo)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: u.Email}}}, struct{}{}, nil
	})
	verify := auth.Verifier(nil, auth.VerifierOptions{OAuth: &auth.OAuth{Tokens: mem, Resource: s.Resource()}})
	mux.Handle("/mcp", sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{ResourceMetadataURL: s.ResourceMetadataURL()})(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)))
	return s, mem, srv
}

// approveAs plays the user at the consent page: it checks the request and approves it, the
// way the dashboard's authorize page does, and returns what the browser would be sent back.
func approveAs(s *Server, userID int64) sdkauth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *sdkauth.AuthorizationArgs) (*sdkauth.AuthorizationResult, error) {
		u, err := url.Parse(args.URL)
		if err != nil {
			return nil, err
		}
		req := ParseAuthorizeRequest(u.Query())
		if _, err := s.Check(ctx, req); err != nil {
			return nil, err
		}
		redirect, err := s.Approve(ctx, userID, req)
		if err != nil {
			return nil, err
		}
		back, _ := url.Parse(redirect)
		q := back.Query()
		return &sdkauth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
	}
}

func TestSDKClientSignsInThroughDiscoveryRegistrationAndPKCE(t *testing.T) {
	s, _, srv := gateway(t)
	handler, err := sdkauth.NewAuthorizationCodeHandler(&sdkauth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &sdkauth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "Test client", RedirectURIs: []string{"http://127.0.0.1:43210/callback"},
		}},
		RedirectURL:              "http://127.0.0.1:43210/callback",
		AuthorizationCodeFetcher: approveAs(s, 7),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", OAuthHandler: handler}, nil)
	if err != nil {
		t.Fatalf("connect through the OAuth flow: %v", err)
	}
	defer session.Close()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Content[0].(*mcp.TextContent).Text; got != "ada@example.com" {
		t.Fatalf("whoami = %q; the call must run as the user who approved", got)
	}
}

// tokenRequest posts a form to the token endpoint and returns the status and error code.
func tokenRequest(t *testing.T, srv *httptest.Server, form url.Values) (int, string) {
	t.Helper()
	resp, err := http.PostForm(srv.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	_ = jsonDecode(resp, &body)
	return resp.StatusCode, body.Error
}

func TestTokenEndpointRefusesReplayedCodesWrongVerifiersAndOtherClients(t *testing.T) {
	s, mem, srv := gateway(t)
	_ = mem.CreateOAuthClient(t.Context(), store.OAuthClient{ID: "c1", Name: "One", RedirectURIs: []string{"http://localhost/cb"}})
	_ = mem.CreateOAuthClient(t.Context(), store.OAuthClient{ID: "c2", Name: "Two", RedirectURIs: []string{"http://localhost/cb"}})
	verifier := "a-sufficiently-long-pkce-code-verifier-0123456789"
	req := AuthorizeRequest{ResponseType: "code", ClientID: "c1", RedirectURI: "http://localhost:5555/cb",
		CodeChallenge: pkceChallenge(verifier), CodeChallengeMethod: "S256", State: "xyz"}
	code := func() string {
		redirect, err := s.Approve(t.Context(), 7, req)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(redirect)
		if u.Query().Get("state") != "xyz" || u.Query().Get("iss") != srv.URL {
			t.Fatalf("redirect %s lacks state or iss", redirect)
		}
		return u.Query().Get("code")
	}
	exchange := func(code, client, verifier string) (int, string) {
		return tokenRequest(t, srv, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client},
			"redirect_uri": {req.RedirectURI}, "code_verifier": {verifier}})
	}

	if status, e := exchange(code(), "c1", "the-wrong-verifier-0123456789-0123456789-0123"); status != 400 || e != "invalid_grant" {
		t.Errorf("wrong verifier: %d %s, want 400 invalid_grant", status, e)
	}
	if status, e := exchange(code(), "c2", verifier); status != 400 || e != "invalid_grant" {
		t.Errorf("another client's code: %d %s, want 400 invalid_grant", status, e)
	}
	good := code()
	if status, _ := exchange(good, "c1", verifier); status != 200 {
		t.Fatalf("a valid exchange answered %d", status)
	}
	if status, e := exchange(good, "c1", verifier); status != 400 || e != "invalid_grant" {
		t.Errorf("replayed code: %d %s, want 400 invalid_grant", status, e)
	}
}

func TestRefreshTokensRotate(t *testing.T) {
	s, mem, srv := gateway(t)
	_ = mem.CreateOAuthClient(t.Context(), store.OAuthClient{ID: "c1", Name: "One", RedirectURIs: []string{"http://localhost/cb"}})
	verifier := "a-sufficiently-long-pkce-code-verifier-0123456789"
	redirect, err := s.Approve(t.Context(), 7, AuthorizeRequest{ResponseType: "code", ClientID: "c1", RedirectURI: "http://localhost/cb",
		CodeChallenge: pkceChallenge(verifier), CodeChallengeMethod: "S256"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	resp, err := http.PostForm(srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")},
		"client_id": {"c1"}, "redirect_uri": {"http://localhost/cb"}, "code_verifier": {verifier}})
	if err != nil {
		t.Fatal(err)
	}
	var tokens struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := jsonDecode(resp, &tokens); err != nil || !strings.HasPrefix(tokens.RefreshToken, auth.OAuthRefreshPrefix) {
		t.Fatalf("token response: %+v, %v", tokens, err)
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {"c1"}}
	if status, _ := tokenRequest(t, srv, refresh); status != 200 {
		t.Fatalf("first refresh answered %d", status)
	}
	if status, e := tokenRequest(t, srv, refresh); status != 400 || e != "invalid_grant" {
		t.Fatalf("reused refresh token: %d %s; it must work once", status, e)
	}
}

func TestUnsafeAuthorizeRequestsAreNeverRedirected(t *testing.T) {
	s, mem, _ := gateway(t)
	_ = mem.CreateOAuthClient(t.Context(), store.OAuthClient{ID: "c1", Name: "One", RedirectURIs: []string{"https://app.example/cb", "http://127.0.0.1/cb"}})
	ok := AuthorizeRequest{ResponseType: "code", ClientID: "c1", CodeChallenge: "x", CodeChallengeMethod: "S256"}
	for name, tc := range map[string]struct {
		redirect, client string
		unsafe           bool
	}{
		"registered https":         {"https://app.example/cb", "c1", false},
		"loopback on another port": {"http://127.0.0.1:61234/cb", "c1", false},
		"unregistered host":        {"https://evil.example/cb", "c1", true},
		"loopback, other path":     {"http://127.0.0.1:61234/steal", "c1", true},
		"unknown client":           {"https://app.example/cb", "nope", true},
	} {
		req := ok
		req.RedirectURI, req.ClientID = tc.redirect, tc.client
		_, err := s.Check(t.Context(), req)
		if got := errors.Is(err, ErrUnsafeRequest); got != tc.unsafe {
			t.Errorf("%s: unsafe = %v (err %v), want %v", name, got, err, tc.unsafe)
		}
	}
	plain := ok
	plain.RedirectURI, plain.CodeChallengeMethod = "https://app.example/cb", "plain"
	if _, err := s.Check(t.Context(), plain); err == nil || errors.Is(err, ErrUnsafeRequest) {
		t.Fatalf("PKCE plain: err = %v; want an error sent back to the client", err)
	}
}

func jsonDecode(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}
