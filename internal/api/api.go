// Package api is the dashboard's REST API under /api: cookie-session login, servers,
// members and invites, API keys, and the audit log.
//
// Sessions follow the OWASP Session Management guidance: a random 256-bit token in an
// HttpOnly, SameSite=Lax cookie, stored only as a hash, with absolute and idle expiry, and
// a fresh token on every login. Cross-site request forgery is blocked in layers (OWASP CSRF
// Prevention): SameSite=Lax keeps the cookie off cross-site POSTs, state-changing requests
// must be JSON, which a cross-site form cannot send, and a present Origin must match the host.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
	"github.com/kaizakin/mirage/internal/oauth"
	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

const (
	cookieName     = "mirage_session"
	sessionTTL     = 12 * time.Hour
	sessionIdle    = 2 * time.Hour
	maxBodyBytes   = 1 << 20
	roleAdmin      = "admin"
	roleMember     = "member"
	minPasswordLen = 12
)

// Upstreams is the view of the upstream manager the API reports server status from.
type Upstreams interface {
	Snapshot() []upstream.State
}

// Options configure the API.
type Options struct {
	Store     *store.Store
	Box       *secretbox.Box
	Upstreams Upstreams
	// Reload asks the gateway to re-read servers from the database now, so an edit takes
	// effect, and clients get tools/list_changed, without waiting for the next poll.
	Reload func()
	// SecureCookie sets the Secure attribute; off only for plain-HTTP local clusters.
	SecureCookie bool
	// Chat serves POST /api/chat for the logged-in user; nil leaves the route out.
	Chat  http.Handler
	Shops ShopOptions
	// OAuth is the gateway's authorization server, whose consent page the dashboard serves;
	// nil leaves the OAuth routes out.
	OAuth *oauth.Server
}

// API serves /api.
type API struct {
	Options
	// dummyHash is compared against when a login names an unknown email, so a miss takes as
	// long as a wrong password and response times do not reveal which accounts exist.
	dummyHash []byte
}

// New returns the API.
func New(opts Options) *API {
	return &API{Options: opts, dummyHash: mustHash("timing equalizer for unknown accounts")}
}

// Handler routes /api.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/invites/accept", a.acceptInvite)

	user := func(h http.HandlerFunc) http.Handler { return a.requireUser(h) }
	admin := func(h http.HandlerFunc) http.Handler { return a.requireUser(requireAdmin(h)) }

	mux.Handle("POST /api/logout", user(a.logout))
	mux.Handle("GET /api/me", user(a.me))
	mux.Handle("GET /api/keys", user(a.listKeys))
	mux.Handle("POST /api/keys", user(a.createKey))
	mux.Handle("DELETE /api/keys/{id}", user(a.revokeKey))
	mux.Handle("GET /api/audit", user(a.listAudit))
	mux.Handle("GET /api/audit/options", user(a.auditOptions))
	if a.OAuth != nil {
		mux.Handle("GET /api/oauth/authorize", user(a.authorizeInfo))
		mux.Handle("POST /api/oauth/authorize", user(a.authorizeDecision))
		mux.Handle("GET /api/oauth/apps", user(a.listConnectedApps))
		mux.Handle("DELETE /api/oauth/apps/{id}", user(a.revokeConnectedApp))
	}
	mux.Handle("GET /api/stores", user(a.listShops))
	mux.Handle("POST /api/stores", user(a.createShop))
	mux.Handle("DELETE /api/stores/{slug}", user(a.deleteShop))
	if a.Chat != nil {
		mux.Handle("POST /api/chat", a.requireUser(a.Chat))
	}

	mux.Handle("GET /api/servers", admin(a.listServers))
	mux.Handle("POST /api/servers", admin(a.createServer))
	mux.Handle("PATCH /api/servers/{slug}", admin(a.updateServer))
	mux.Handle("DELETE /api/servers/{slug}", admin(a.deleteServer))
	mux.Handle("GET /api/members", admin(a.listMembers))
	mux.Handle("PATCH /api/members/{id}", admin(a.updateMember))
	mux.Handle("DELETE /api/members/{id}", admin(a.deleteMember))
	mux.Handle("GET /api/invites", admin(a.listInvites))
	mux.Handle("POST /api/invites", admin(a.createInvite))
	mux.Handle("DELETE /api/invites/{id}", admin(a.deleteInvite))

	return sameOrigin(mux)
}

type userKey struct{}

// UserFromContext returns the logged-in user of a request that passed requireUser.
func UserFromContext(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(userKey{}).(auth.User)
	return u, ok
}

func (a *API) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "log in first")
			return
		}
		u, err := a.Store.SessionUser(r.Context(), hashToken(c.Value), sessionIdle)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "your session has ended; log in again")
			return
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, _ := UserFromContext(r.Context()); u.Role != roleAdmin {
			writeError(w, http.StatusForbidden, "admins only")
			return
		}
		next(w, r)
	}
}

// sameOrigin rejects state-changing requests a cross-site page could forge: their body must
// be JSON, and a browser-sent Origin must name this host.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); r.ContentLength != 0 && mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "send JSON with Content-Type: application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// decode reads a JSON body into v, rejecting unknown fields and oversized bodies.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// internalError logs the cause and tells the client nothing about it.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "api request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "something went wrong; the gateway log has details")
}

// storeError maps the store's sentinel errors to statuses.
func storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "already exists")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		internalError(w, r, err)
	}
}
