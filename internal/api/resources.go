package api

import (
	"cmp"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/naming"
	"github.com/kaizakin/mirage/internal/gateway/ssrf"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
	"github.com/kaizakin/mirage/internal/store"
)

const inviteTTL = 7 * 24 * time.Hour

type toolJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// serverJSON never carries the credential: it is write-only, reported as has_credential.
type serverJSON struct {
	Slug          string     `json:"slug"`
	URL           string     `json:"url"`
	Enabled       bool       `json:"enabled"`
	AuthType      string     `json:"auth_type"`
	AuthHeader    string     `json:"auth_header,omitempty"`
	HasCredential bool       `json:"has_credential"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason,omitempty"`
	Tools         []toolJSON `json:"tools"`
	CreatedAt     time.Time  `json:"created_at"`
	// Internal servers were created by the platform and may reach cluster-internal addresses.
	Internal bool `json:"internal"`
	// Store is the slug of the provisioned store that owns this server, if any.
	Store  string      `json:"store,omitempty"`
	Hosted *hostedJSON `json:"hosted,omitempty"`
}

// hostedJSON is how the platform runs a hosted server. Its environment is write-only.
type hostedJSON struct {
	Image  string `json:"image"`
	Port   int    `json:"port"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Store.Servers(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	live := map[string]upstream.State{}
	for _, s := range a.Upstreams.Snapshot() {
		live[s.Slug] = s
	}
	out := make([]serverJSON, 0, len(rows))
	for _, row := range rows {
		s := serverJSON{Slug: row.Slug, URL: row.URL, Enabled: row.Enabled, AuthType: row.AuthType,
			AuthHeader: row.AuthHeader, HasCredential: row.HasCredential, CreatedAt: row.CreatedAt, Tools: []toolJSON{},
			Internal: row.Internal, Store: row.Shop}
		if h := row.Hosted; h != nil {
			s.Hosted = &hostedJSON{Image: h.Image, Port: h.Port, Path: h.Path, Status: string(h.Status), Reason: h.Reason}
		}
		st, running := live[row.Slug]
		switch {
		case !row.Enabled:
			s.Status = "disabled"
		case !running:
			s.Status = string(upstream.Connecting)
		default:
			s.Status, s.Reason = string(st.Status), st.Reason
			for _, t := range st.Tools {
				s.Tools = append(s.Tools, toolJSON{Name: t.Name, Description: t.Description})
			}
		}
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, out)
}

type serverInput struct {
	Slug       string       `json:"slug"`
	URL        *string      `json:"url"`
	Enabled    *bool        `json:"enabled"`
	AuthType   *string      `json:"auth_type"`
	AuthHeader *string      `json:"auth_header"`
	Credential *string      `json:"credential"`
	Hosted     *hostedInput `json:"hosted"`
}

// hostedInput asks the platform to run the server from an image instead of reaching it at a
// URL. Port and path are fixed once it exists; image and env may change.
type hostedInput struct {
	Image *string           `json:"image"`
	Port  int               `json:"port"`
	Path  string            `json:"path"`
	Env   map[string]string `json:"env"`
}

var (
	// An image reference: [registry/]repository[:tag][@digest], without whitespace or quotes.
	imagePattern   = regexp.MustCompile(`^[a-z0-9]+([._/:@-][a-zA-Z0-9_.-]+)*$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// validate checks a hosted server's fields; creating requires an image.
func (in hostedInput) validate(creating bool) string {
	if creating && in.Image == nil {
		return "hosted.image is required"
	}
	if in.Image != nil && (len(*in.Image) > 255 || !imagePattern.MatchString(*in.Image)) {
		return "hosted.image must be an image reference such as ghcr.io/org/server:1.2"
	}
	if !creating && (in.Port != 0 || in.Path != "") {
		return "a hosted server's port and path are fixed; delete it and host it again to change them"
	}
	if in.Port < 0 || in.Port > 65535 {
		return "hosted.port must be between 1 and 65535"
	}
	if in.Path != "" && !strings.HasPrefix(in.Path, "/") {
		return `hosted.path must start with "/"`
	}
	for name := range in.Env {
		if !envNamePattern.MatchString(name) {
			return fmt.Sprintf("environment variable name %q must be letters, digits, and '_', not starting with a digit", name)
		}
	}
	return ""
}

// validate checks the fields present; creating also requires slug, url, and auth_type.
func (in serverInput) validate(creating bool) string {
	if creating {
		if err := naming.ValidateSlug(in.Slug); err != nil {
			return err.Error()
		}
		if in.AuthType == nil || (in.URL == nil) == (in.Hosted == nil) {
			return "slug, auth_type, and either url or hosted are required"
		}
	}
	if in.Hosted != nil {
		if in.URL != nil {
			return "the platform chooses a hosted server's url; send url or hosted, not both"
		}
		if msg := in.Hosted.validate(creating); msg != "" {
			return msg
		}
	}
	if in.URL != nil {
		if err := ssrf.ValidateURL(*in.URL); err != nil {
			return err.Error()
		}
	}
	if in.AuthType != nil {
		switch upstream.AuthType(*in.AuthType) {
		case upstream.AuthNone, upstream.AuthBearer, upstream.AuthHeader:
		default:
			return `auth_type must be "none", "bearer", or "header"`
		}
	}
	return ""
}

func (a *API) createServer(w http.ResponseWriter, r *http.Request) {
	var in serverInput
	if !decode(w, r, &in) {
		return
	}
	if msg := in.validate(true); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	seed := store.ServerSeed{Slug: in.Slug, AuthType: *in.AuthType}
	if in.AuthHeader != nil {
		seed.AuthHeader = *in.AuthHeader
	}
	if in.Credential != nil {
		seed.Credential = *in.Credential
	}
	var err error
	if h := in.Hosted; h != nil {
		spec := store.HostedSpec{Image: *h.Image, Port: cmp.Or(h.Port, 8080), Path: cmp.Or(h.Path, "/mcp"), Env: h.Env}
		err = a.Store.CreateHostedServer(r.Context(), a.Box, seed, spec)
	} else {
		seed.URL = *in.URL
		err = a.Store.CreateServer(r.Context(), a.Box, seed)
	}
	if err != nil {
		storeError(w, r, err)
		return
	}
	a.Reload()
	w.WriteHeader(http.StatusCreated)
}

func (a *API) updateServer(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	hosted, stop := a.managedServer(w, r, slug)
	if stop {
		return
	}
	var in serverInput
	if !decode(w, r, &in) {
		return
	}
	if msg := in.validate(false); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	switch {
	case hosted && in.URL != nil:
		writeError(w, http.StatusConflict, "the platform chooses a hosted server's url; change its image instead")
		return
	case !hosted && in.Hosted != nil:
		writeError(w, http.StatusConflict, "server "+slug+" is not hosted by the platform")
		return
	}
	patch := store.ServerPatch{URL: in.URL, Enabled: in.Enabled, AuthType: in.AuthType, AuthHeader: in.AuthHeader, Credential: in.Credential}
	if err := a.Store.UpdateServer(r.Context(), a.Box, slug, patch); err != nil {
		storeError(w, r, err)
		return
	}
	if h := in.Hosted; h != nil {
		if err := a.Store.UpdateHostedServer(r.Context(), a.Box, slug, store.HostedPatch{Image: h.Image, Env: h.Env}); err != nil {
			storeError(w, r, err)
			return
		}
	}
	a.Reload()
	w.WriteHeader(http.StatusNoContent)
}

// deleteServer removes a server. A hosted server's namespace goes with it: the provisioner
// deletes namespaces whose server is gone.
func (a *API) deleteServer(w http.ResponseWriter, r *http.Request) {
	if _, stop := a.managedServer(w, r, r.PathValue("slug")); stop {
		return
	}
	if err := a.Store.DeleteServer(r.Context(), r.PathValue("slug")); err != nil {
		storeError(w, r, err)
		return
	}
	a.Reload()
	w.WriteHeader(http.StatusNoContent)
}

type memberJSON struct {
	userJSON
	CreatedAt time.Time `json:"created_at"`
}

func (a *API) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := a.Store.Members(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]memberJSON, 0, len(members))
	for _, m := range members {
		out = append(out, memberJSON{userJSON: toUserJSON(m.User), CreatedAt: m.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// memberID parses the {id} path value; an admin may not change or remove themselves, so the
// dashboard always keeps at least the admin making the change.
func memberID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "member id must be a number")
		return 0, false
	}
	if me, _ := UserFromContext(r.Context()); me.ID == id {
		writeError(w, http.StatusBadRequest, "you cannot change or remove your own account")
		return 0, false
	}
	return id, true
}

func (a *API) updateMember(w http.ResponseWriter, r *http.Request) {
	id, ok := memberID(w, r)
	if !ok {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !isRole(in.Role) {
		writeError(w, http.StatusBadRequest, `role must be "admin" or "member"`)
		return
	}
	if err := a.Store.SetRole(r.Context(), id, in.Role); err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteMember(w http.ResponseWriter, r *http.Request) {
	id, ok := memberID(w, r)
	if !ok {
		return
	}
	if err := a.Store.DeleteUser(r.Context(), id); err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type inviteJSON struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	// Token is returned once, when the invite is created; only its hash is stored.
	Token string `json:"token,omitempty"`
}

func (a *API) listInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := a.Store.PendingInvites(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]inviteJSON, 0, len(invites))
	for _, inv := range invites {
		out = append(out, inviteJSON{ID: hex.EncodeToString(inv.TokenHash), Email: inv.Email, Role: inv.Role, ExpiresAt: inv.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !isRole(in.Role) {
		writeError(w, http.StatusBadRequest, `role must be "admin" or "member"`)
		return
	}
	email, err := mailAddress(in.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "email is not a valid address")
		return
	}
	token, err := randomToken()
	if err != nil {
		internalError(w, r, err)
		return
	}
	hash, expires := hashToken(token), time.Now().Add(inviteTTL)
	if err := a.Store.CreateInvite(r.Context(), hash, email, in.Role, expires); err != nil {
		storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, inviteJSON{ID: hex.EncodeToString(hash), Email: email, Role: in.Role, ExpiresAt: expires, Token: token})
}

func (a *API) deleteInvite(w http.ResponseWriter, r *http.Request) {
	hash, err := hex.DecodeString(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invite id is not valid")
		return
	}
	if err := a.Store.DeleteInvite(r.Context(), hash); err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type keyJSON struct {
	ID         string     `json:"id"`
	UserEmail  string     `json:"user_email"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// Key is the full plaintext, returned once when the key is created and never again.
	Key string `json:"key,omitempty"`
}

// keyScope is whose keys a request may see and revoke: everyone's for an admin asking with
// ?all=1, otherwise only the caller's (0 means everyone).
func keyScope(r *http.Request) int64 {
	u, _ := UserFromContext(r.Context())
	if u.Role == roleAdmin && r.URL.Query().Get("all") == "1" {
		return 0
	}
	return u.ID
}

func (a *API) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.Store.APIKeys(r.Context(), keyScope(r))
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]keyJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyJSON{ID: k.ID, UserEmail: k.UserEmail, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt, LastUsedAt: k.LastUsedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	key, err := auth.GenerateKey()
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := a.Store.CreateAPIKey(r.Context(), u.ID, key.ID, key.SecretHash); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, keyJSON{ID: key.ID, UserEmail: u.Email, CreatedAt: time.Now(), Key: key.Plaintext})
}

func (a *API) revokeKey(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	owner := u.ID
	if u.Role == roleAdmin {
		owner = 0
	}
	if err := a.Store.RevokeAPIKeyOf(r.Context(), r.PathValue("id"), owner); err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type auditJSON struct {
	ID         int64     `json:"id"`
	UserEmail  string    `json:"user_email"`
	Server     string    `json:"server"`
	Tool       string    `json:"tool"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int       `json:"duration_ms"`
	Outcome    string    `json:"outcome"`
	Error      string    `json:"error,omitempty"`
}

// listAudit pages the audit log newest first. Members see only their own calls; admins see
// everyone's and may filter by user_id. next_before is the cursor for the following page.
type auditOptionsJSON struct {
	Servers []auditServerJSON `json:"servers"`
	Users   []auditUserJSON   `json:"users"`
}

type auditServerJSON struct {
	Slug  string   `json:"slug"`
	Tools []string `json:"tools"`
}

type auditUserJSON struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// auditOptions lists the servers, tools, and users the caller's audit view can be filtered
// by. A member's options come from their own calls only.
func (a *API) auditOptions(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	scope := u.ID
	if u.Role == roleAdmin {
		scope = 0
	}
	opts, err := a.Store.AuditOptions(r.Context(), scope)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := auditOptionsJSON{Servers: make([]auditServerJSON, 0, len(opts.Servers)), Users: make([]auditUserJSON, 0, len(opts.Users))}
	for _, s := range opts.Servers {
		out.Servers = append(out.Servers, auditServerJSON{Slug: s.Slug, Tools: s.Tools})
	}
	for _, user := range opts.Users {
		out.Users = append(out.Users, auditUserJSON{ID: user.ID, Email: user.Email})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{Server: q.Get("server"), Tool: q.Get("tool"), Outcome: q.Get("outcome"), Limit: 50}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = min(n, 200)
	}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	u, _ := UserFromContext(r.Context())
	if u.Role == roleAdmin {
		f.UserID, _ = strconv.ParseInt(q.Get("user_id"), 10, 64)
	} else {
		f.UserID = u.ID
	}

	rows, err := a.Store.Audit(r.Context(), f)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := struct {
		Entries    []auditJSON `json:"entries"`
		NextBefore int64       `json:"next_before,omitempty"`
	}{Entries: make([]auditJSON, 0, len(rows))}
	for _, row := range rows {
		out.Entries = append(out.Entries, auditJSON{ID: row.ID, UserEmail: row.UserEmail, Server: row.ServerSlug, Tool: row.Tool,
			StartedAt: row.StartedAt, DurationMS: row.DurationMS, Outcome: row.Outcome, Error: row.Error})
	}
	if len(rows) == f.Limit {
		out.NextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}
