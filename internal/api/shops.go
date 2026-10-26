package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kaizakin/mirage/internal/store"
)

// ShopOptions configures store provisioning requests.
type ShopOptions struct {
	// Domain is appended to a store's slug to make its host: <slug>.<Domain>.
	Domain string
	// MaxPerUser caps the stores one user may have at once.
	MaxPerUser int
	// Scheme of the stores' public URLs, http or https.
	Scheme string
}

// shopSlug matches the database's shops_slug_format check; a store's namespace, host label,
// and server slug are all built from it.
var shopSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxShopSlug = 20

type shopJSON struct {
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	URL        string     `json:"url"`
	Server     string     `json:"server"`
	OwnerEmail string     `json:"owner_email"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadyAt    *time.Time `json:"ready_at,omitempty"`
}

func (a *API) toShopJSON(s store.Shop) shopJSON {
	return shopJSON{Slug: s.Slug, Name: s.Name, Status: string(s.Status), Reason: s.Reason, URL: a.Shops.Scheme + "://" + s.Host,
		Server: s.ServerSlug(), OwnerEmail: s.OwnerEmail, CreatedAt: s.CreatedAt, ReadyAt: s.ReadyAt}
}

// listShops returns the caller's stores; an admin sees everyone's.
func (a *API) listShops(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	owner := u.ID
	if u.Role == roleAdmin {
		owner = 0
	}
	shops, err := a.Store.Shops(r.Context(), owner)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]shopJSON, 0, len(shops))
	for _, s := range shops {
		out = append(out, a.toShopJSON(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// createShop asks for a new store. Asking again for the caller's own slug is safe: it
// returns the existing store, and restarts it if it had failed.
func (a *API) createShop(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "" || len(in.Name) > 60:
		writeError(w, http.StatusBadRequest, "name must be 1 to 60 characters")
		return
	case len(in.Slug) > maxShopSlug || !shopSlug.MatchString(in.Slug):
		writeError(w, http.StatusBadRequest, "address must be 1 to 20 lowercase letters, digits, or single hyphens")
		return
	}
	u, _ := UserFromContext(r.Context())
	shop, created, err := a.Store.CreateShop(r.Context(), store.NewShop{
		Slug: in.Slug, Name: in.Name, Host: in.Slug + "." + a.Shops.Domain, OwnerID: u.ID, MaxPerOwner: a.Shops.MaxPerUser,
	})
	switch {
	case errors.Is(err, store.ErrShopLimit):
		writeError(w, http.StatusConflict, err.Error()+"; the limit is "+strconv.Itoa(a.Shops.MaxPerUser))
		return
	case errors.Is(err, store.ErrConflict):
		// These messages name only the slug the caller asked for.
		writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": "))
		return
	case err != nil:
		storeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, a.toShopJSON(shop))
}

// deleteShop asks for a store's teardown. Only its owner or an admin may; anyone else is
// told it does not exist.
func (a *API) deleteShop(w http.ResponseWriter, r *http.Request) {
	shop, err := a.Store.ShopBySlug(r.Context(), r.PathValue("slug"))
	u, _ := UserFromContext(r.Context())
	if err == nil && shop.OwnerID != u.ID && u.Role != roleAdmin {
		err = store.ErrNotFound
	}
	if err == nil {
		err = a.Store.MarkShopDeleting(r.Context(), shop.ID)
	}
	if err != nil {
		storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// managedServer answers 409 and reports stop when the server belongs to a store, whose
// lifecycle manages it. hosted reports whether the platform runs the server from an image.
func (a *API) managedServer(w http.ResponseWriter, r *http.Request, slug string) (hosted, stop bool) {
	shop, hosted, err := a.Store.ServerOwner(r.Context(), slug)
	if err != nil {
		storeError(w, r, err)
		return false, true
	}
	if shop != "" {
		writeError(w, http.StatusConflict, "server "+slug+" belongs to store "+shop+"; manage it from the Stores page")
		return false, true
	}
	return hosted, false
}
