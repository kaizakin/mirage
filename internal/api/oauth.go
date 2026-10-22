package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kaizakin/mirage/internal/oauth"
)

// authorizeInfo validates an OAuth authorization request for the dashboard's consent page.
// A request that names an unknown client or an unregistered redirect address is an error
// page; any other problem goes straight back to the client.
func (a *API) authorizeInfo(w http.ResponseWriter, r *http.Request) {
	req := oauth.ParseAuthorizeRequest(r.URL.Query())
	name, err := a.OAuth.Check(r.Context(), req)
	switch {
	case errors.Is(err, oauth.ErrUnsafeRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil && name == "":
		internalError(w, r, err)
	case err != nil:
		writeJSON(w, http.StatusOK, map[string]string{"redirect": a.OAuth.Deny(req, err.Error())})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"client_name": name})
	}
}

// authorizeDecision records the signed-in user's answer and returns where to send the
// browser: back to the client with a code, or with access_denied.
func (a *API) authorizeDecision(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query   string `json:"query"`
		Approve bool   `json:"approve"`
	}
	if !decode(w, r, &in) {
		return
	}
	q, err := url.ParseQuery(in.Query)
	if err != nil {
		writeError(w, http.StatusBadRequest, "query is not a URL query")
		return
	}
	req := oauth.ParseAuthorizeRequest(q)
	if !in.Approve {
		if _, err := a.OAuth.Check(r.Context(), req); errors.Is(err, oauth.ErrUnsafeRequest) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"redirect": a.OAuth.Deny(req, "access_denied: the user declined")})
		return
	}
	u, _ := UserFromContext(r.Context())
	redirect, err := a.OAuth.Approve(r.Context(), u.ID, req)
	switch {
	case errors.Is(err, oauth.ErrUnsafeRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect})
	}
}

type connectedAppJSON struct {
	ID         int64      `json:"id"`
	ClientName string     `json:"client_name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// listConnectedApps lists the clients the caller has signed in to the gateway with.
func (a *API) listConnectedApps(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	apps, err := a.Store.ConnectedApps(r.Context(), u.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]connectedAppJSON, 0, len(apps))
	for _, app := range apps {
		out = append(out, connectedAppJSON{ID: app.GrantID, ClientName: app.ClientName, CreatedAt: app.CreatedAt, LastUsedAt: app.LastUsedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// revokeConnectedApp signs a client out: its access and refresh tokens stop working at once.
func (a *API) revokeConnectedApp(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	u, _ := UserFromContext(r.Context())
	if err := a.Store.RevokeGrant(r.Context(), u.ID, id); err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
