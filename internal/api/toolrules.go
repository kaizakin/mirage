package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/kaizakin/mirage/internal/gateway/policy"
	"github.com/kaizakin/mirage/internal/store"
)

// toolRuleJSON is a rule: user_id 0 covers every member, and an empty server or tool covers
// every server or tool.
type toolRuleJSON struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	UserEmail string    `json:"user_email,omitempty"`
	Server    string    `json:"server"`
	Tool      string    `json:"tool"`
	Allow     bool      `json:"allow"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *API) listToolRules(w http.ResponseWriter, r *http.Request) {
	rules, err := a.Store.ToolRules(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]toolRuleJSON, 0, len(rules))
	for _, rule := range rules {
		out = append(out, toolRuleJSON{ID: rule.ID, UserID: rule.UserID, UserEmail: rule.UserEmail, Server: rule.Server,
			Tool: rule.Tool, Allow: rule.Allow, CreatedAt: rule.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createToolRule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID int64  `json:"user_id"`
		Server string `json:"server"`
		Tool   string `json:"tool"`
		Allow  bool   `json:"allow"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := a.Store.CreateToolRule(r.Context(), policy.Rule{UserID: in.UserID, Server: in.Server, Tool: in.Tool, Allow: in.Allow})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusBadRequest, "no server is called "+in.Server)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "a rule for that member, server, and tool already exists; delete it first")
	case err != nil:
		storeError(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
	}
}

func (a *API) deleteToolRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "rule id must be a number")
		return
	}
	if err := a.Store.DeleteToolRule(r.Context(), id); err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
