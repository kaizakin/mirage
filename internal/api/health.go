package api

import (
	"net/http"
	"time"

	"github.com/kaizakin/mirage/internal/gateway/upstream"
)

type serverHealthJSON struct {
	Server      string     `json:"server"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Calls       int        `json:"calls"`
	Failed      int        `json:"failed"`
	ErrorRate   float64    `json:"error_rate"`
	P50MS       int64      `json:"p50_ms"`
	P95MS       int64      `json:"p95_ms"`
	LastError   string     `json:"last_error,omitempty"`
	LastOutcome string     `json:"last_outcome,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}

// healthWindow is how far back the health page's call statistics reach.
const healthWindow = time.Hour

// health reports, for each server: whether it is connected right now and if not why and since
// when, and its last hour of calls: how many, the share that failed, latency, and the latest
// failure's reason.
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	servers, err := a.Store.Servers(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	calls, err := a.Store.CallsSince(r.Context(), time.Now().Add(-healthWindow))
	if err != nil {
		internalError(w, r, err)
		return
	}
	live := map[string]upstream.State{}
	for _, st := range a.Upstreams.Snapshot() {
		live[st.Slug] = st
	}
	byServer := map[string]serverHealthJSON{}
	for _, srv := range servers {
		h := serverHealthJSON{Server: srv.Slug, Status: "disabled"}
		if st, ok := live[srv.Slug]; ok && srv.Enabled {
			since := st.Since
			h.Status, h.Reason, h.Since = string(st.Status), st.Reason, &since
		}
		byServer[srv.Slug] = h
	}
	// Servers deleted since still show their calls.
	for _, c := range calls {
		h, ok := byServer[c.Server]
		if !ok {
			h = serverHealthJSON{Server: c.Server, Status: "removed"}
		}
		h.Calls, h.Failed = c.Calls, c.Failed
		h.ErrorRate = float64(c.Failed) / float64(c.Calls)
		h.P50MS, h.P95MS = c.P50.Milliseconds(), c.P95.Milliseconds()
		h.LastError, h.LastOutcome, h.LastErrorAt = c.LastError, c.LastOutcome, c.LastErrorAt
		byServer[c.Server] = h
	}
	out := make([]serverHealthJSON, 0, len(byServer))
	for _, srv := range servers {
		out = append(out, byServer[srv.Slug])
		delete(byServer, srv.Slug)
	}
	for _, h := range byServer {
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, map[string]any{"window_minutes": int(healthWindow.Minutes()), "servers": out})
}
