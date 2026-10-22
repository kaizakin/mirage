// Package oauth is the gateway's OAuth 2.1 authorization server, for MCP clients that sign
// in through a browser (the MCP authorization specification). It serves:
//
//   - Protected Resource Metadata (RFC 9728), which the 401 from /mcp points clients to;
//   - Authorization Server Metadata (RFC 8414);
//   - dynamic client registration (RFC 7591), since MCP clients register themselves;
//   - the token endpoint, for the authorization_code (with PKCE S256) and refresh_token grants.
//
// The authorization endpoint itself is a dashboard page, so it reuses the dashboard's sign-in
// and session; it calls Check, Approve, and Deny through the dashboard API. All clients are
// public, so PKCE is what binds a code to the client that asked for it.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/store"
)

// Store is the persistence the authorization server needs.
type Store interface {
	CreateOAuthClient(ctx context.Context, c store.OAuthClient) error
	OAuthClientByID(ctx context.Context, id string) (store.OAuthClient, error)
	CreateOAuthCode(ctx context.Context, c store.OAuthCode) error
	RedeemOAuthCode(ctx context.Context, hash []byte) (store.OAuthGrant, string, string, error)
	RedeemOAuthRefresh(ctx context.Context, hash []byte) (store.OAuthGrant, error)
	StoreOAuthTokens(ctx context.Context, grantID int64, tokens ...store.IssuedToken) error
}

// Lifetimes of what the server issues.
const (
	codeTTL    = time.Minute
	accessTTL  = time.Hour
	refreshTTL = 30 * 24 * time.Hour
)

// scope is the one scope there is: using the gateway's MCP endpoint as the signed-in user.
const scope = "mcp"

// Config describes where things are.
type Config struct {
	// Issuer is the gateway's public base URL, such as https://gateway.example.com.
	Issuer string
	// AuthorizeURL is the dashboard page that asks the user to approve a client.
	AuthorizeURL string
}

// Server is the authorization server.
type Server struct {
	store Store
	cfg   Config
}

// New returns a Server.
func New(s Store, cfg Config) *Server {
	cfg.Issuer = strings.TrimSuffix(cfg.Issuer, "/")
	return &Server{store: s, cfg: cfg}
}

// Resource is the MCP endpoint's URL, which tokens are issued for.
func (s *Server) Resource() string { return s.cfg.Issuer + "/mcp" }

// ResourceMetadataURL is where the 401 from /mcp sends clients.
func (s *Server) ResourceMetadataURL() string {
	return s.cfg.Issuer + "/.well-known/oauth-protected-resource"
}

// Register adds the public endpoints to mux.
func (s *Server) Register(mux *http.ServeMux) {
	prm := sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               s.Resource(),
		AuthorizationServers:   []string{s.cfg.Issuer},
		ScopesSupported:        []string{scope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Mirage MCP gateway",
	})
	// RFC 9728 inserts the resource's path after the well-known prefix; some clients ask
	// there, others at the bare prefix.
	mux.Handle("GET /.well-known/oauth-protected-resource", prm)
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.metadata)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("OPTIONS /oauth/", preflight)
}

func (s *Server) metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, oauthex.AuthServerMeta{
		Issuer:                            s.cfg.Issuer,
		AuthorizationEndpoint:             s.cfg.AuthorizeURL,
		TokenEndpoint:                     s.cfg.Issuer + "/oauth/token",
		RegistrationEndpoint:              s.cfg.Issuer + "/oauth/register",
		ScopesSupported:                   []string{scope},
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		// Redirects name the issuer (RFC 9207), so a client can tell this server's
		// responses from another's.
		AuthorizationResponseIssParameterSupported: true,
	})
}

// register implements dynamic client registration for public clients.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var meta oauthex.ClientRegistrationMetadata
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&meta); err != nil {
		registrationError(w, "invalid_client_metadata", "the body is not client metadata JSON")
		return
	}
	switch {
	case len(meta.RedirectURIs) == 0 || len(meta.RedirectURIs) > 5:
		registrationError(w, "invalid_redirect_uri", "register 1 to 5 redirect URIs")
		return
	case meta.TokenEndpointAuthMethod != "" && meta.TokenEndpointAuthMethod != "none":
		registrationError(w, "invalid_client_metadata", "only public clients (token_endpoint_auth_method none) are supported")
		return
	}
	for _, g := range meta.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			registrationError(w, "invalid_client_metadata", "unsupported grant type "+g)
			return
		}
	}
	for _, u := range meta.RedirectURIs {
		if err := checkRedirectURI(u); err != nil {
			registrationError(w, "invalid_redirect_uri", err.Error())
			return
		}
	}
	name := strings.TrimSpace(meta.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	client := store.OAuthClient{ID: "mcp_" + randomString(16), Name: name, RedirectURIs: meta.RedirectURIs}
	if err := s.store.CreateOAuthClient(r.Context(), client); err != nil {
		serverError(w, r, err)
		return
	}
	slog.Info("registered OAuth client", "client_id", client.ID, "name", name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ID,
		"client_id_issued_at":        time.Now().Unix(),
		"client_name":                name,
		"redirect_uris":              client.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// checkRedirectURI accepts HTTPS URLs and plain-HTTP loopback URLs (RFC 8252), the two kinds
// OAuth 2.1 allows, without fragments.
func checkRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "":
		return fmt.Errorf("redirect URI %q is not an absolute URL", raw)
	case u.Fragment != "":
		return fmt.Errorf("redirect URI %q has a fragment", raw)
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && isLoopback(u.Hostname()):
		return nil
	}
	return fmt.Errorf("redirect URI %q must be https, or http on localhost", raw)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redirectAllowed reports whether uri is one of the client's registered redirect URIs. A
// loopback URI matches whatever its port, since native apps listen on a free one (RFC 8252
// section 7.3).
func redirectAllowed(registered []string, uri string) bool {
	if slices.Contains(registered, uri) {
		return true
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || !isLoopback(u.Hostname()) {
		return false
	}
	for _, r := range registered {
		ru, err := url.Parse(r)
		if err == nil && ru.Scheme == "http" && ru.Hostname() == u.Hostname() && ru.Path == u.Path && ru.RawQuery == u.RawQuery {
			return true
		}
	}
	return false
}

// AuthorizeRequest is the authorization request's query, as the dashboard page received it.
type AuthorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	Scope               string
}

// ParseAuthorizeRequest reads an authorization request from its query parameters.
func ParseAuthorizeRequest(q url.Values) AuthorizeRequest {
	return AuthorizeRequest{
		ResponseType: q.Get("response_type"), ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"),
		State: q.Get("state"), CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		Resource: q.Get("resource"), Scope: q.Get("scope"),
	}
}

// ErrUnsafeRequest means the request names an unknown client or a redirect URI the client
// did not register, so it must be shown as an error rather than redirected anywhere.
var ErrUnsafeRequest = errors.New("this sign-in link is not valid")

// Check validates an authorization request and returns the client's name for the consent
// page. An ErrUnsafeRequest error must be shown to the user; any other error is to be sent
// back to the client with Deny's redirect.
func (s *Server) Check(ctx context.Context, req AuthorizeRequest) (string, error) {
	client, err := s.store.OAuthClientByID(ctx, req.ClientID)
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("%w: unknown client", ErrUnsafeRequest)
	}
	if err != nil {
		return "", err
	}
	if !redirectAllowed(client.RedirectURIs, req.RedirectURI) {
		return "", fmt.Errorf("%w: the redirect address is not registered for %s", ErrUnsafeRequest, client.Name)
	}
	switch {
	case req.ResponseType != "code":
		return client.Name, errors.New("unsupported_response_type")
	case req.CodeChallenge == "" || req.CodeChallengeMethod != "S256":
		return client.Name, errors.New("invalid_request: PKCE with S256 is required")
	case req.Resource != "" && req.Resource != s.Resource():
		return client.Name, errors.New("invalid_target: unknown resource " + req.Resource)
	case req.Scope != "" && req.Scope != scope:
		return client.Name, errors.New("invalid_scope: the only scope is " + scope)
	}
	return client.Name, nil
}

// Approve records the user's consent and returns where to send the browser: the client's
// redirect URI with a one-time code.
func (s *Server) Approve(ctx context.Context, userID int64, req AuthorizeRequest) (string, error) {
	if _, err := s.Check(ctx, req); err != nil {
		return "", err
	}
	code := randomString(32)
	if err := s.store.CreateOAuthCode(ctx, store.OAuthCode{
		Hash: auth.HashToken(code), UserID: userID, ClientID: req.ClientID, Resource: s.Resource(),
		RedirectURI: req.RedirectURI, CodeChallenge: req.CodeChallenge, ExpiresAt: time.Now().Add(codeTTL),
	}); err != nil {
		return "", err
	}
	return s.callback(req, url.Values{"code": {code}}), nil
}

// Deny returns where to send the browser when the user refuses, or when Check found an error
// that the client should hear about.
func (s *Server) Deny(req AuthorizeRequest, oauthErr string) string {
	code, desc, _ := strings.Cut(oauthErr, ": ")
	v := url.Values{"error": {code}}
	if desc != "" {
		v.Set("error_description", desc)
	}
	return s.callback(req, v)
}

func (s *Server) callback(req AuthorizeRequest, v url.Values) string {
	if req.State != "" {
		v.Set("state", req.State)
	}
	v.Set("iss", s.cfg.Issuer)
	sep := "?"
	if strings.Contains(req.RedirectURI, "?") {
		sep = "&"
	}
	return req.RedirectURI + sep + v.Encode()
}

// token implements the token endpoint.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "the body is not a form")
		return
	}
	clientID := r.PostForm.Get("client_id")
	if clientID == "" {
		tokenError(w, http.StatusUnauthorized, "invalid_client", "client_id is required")
		return
	}
	var grant store.OAuthGrant
	var err error
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		grant, err = s.redeemCode(r.Context(), r.PostForm)
	case "refresh_token":
		grant, err = s.store.RedeemOAuthRefresh(r.Context(), auth.HashToken(r.PostForm.Get("refresh_token")))
		if errors.Is(err, store.ErrNotFound) {
			err = errGrant("the refresh token is invalid, expired, or already used")
		}
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
		return
	}
	if err == nil && grant.ClientID != clientID {
		err = errGrant("the grant belongs to another client")
	}
	if resource := r.PostForm.Get("resource"); err == nil && resource != "" && resource != grant.Resource {
		err = errGrant("the grant is for " + grant.Resource)
	}
	var ge grantError
	if errors.As(err, &ge) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", string(ge))
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}

	access, refresh := auth.OAuthAccessPrefix+randomString(32), auth.OAuthRefreshPrefix+randomString(32)
	now := time.Now()
	if err := s.store.StoreOAuthTokens(r.Context(), grant.ID,
		store.IssuedToken{Hash: auth.HashToken(access), Kind: "access", ExpiresAt: now.Add(accessTTL)},
		store.IssuedToken{Hash: auth.HashToken(refresh), Kind: "refresh", ExpiresAt: now.Add(refreshTTL)},
	); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

type grantError string

func (e grantError) Error() string { return string(e) }

func errGrant(msg string) error { return grantError(msg) }

func (s *Server) redeemCode(ctx context.Context, form map[string][]string) (store.OAuthGrant, error) {
	get := func(k string) string {
		if v := form[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	grant, redirectURI, challenge, err := s.store.RedeemOAuthCode(ctx, auth.HashToken(get("code")))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return grant, errGrant("the code is invalid, expired, or already used")
	case err != nil:
		return grant, err
	case get("redirect_uri") != redirectURI:
		return grant, errGrant("redirect_uri does not match the authorization request")
	case pkceChallenge(get("code_verifier")) != challenge:
		return grant, errGrant("code_verifier does not match the code challenge")
	}
	return grant, nil
}

// pkceChallenge is the S256 transform of a PKCE code verifier (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return base64.RawURLEncoding.EncodeToString(b)
}

// cors lets browser-based MCP clients call the public endpoints. They carry no cookies, so
// allowing any origin exposes nothing.
func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	cors(w)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	cors(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func tokenError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func registrationError(w http.ResponseWriter, code, desc string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": desc})
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "oauth", "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
}
