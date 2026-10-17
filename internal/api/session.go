package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/store"
)

type userJSON struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func toUserJSON(u auth.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Role: u.Role}
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, hash, err := a.Store.UserByEmail(r.Context(), in.Email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, r, err)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(a.dummyHash, []byte(in.Password))
		writeError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserJSON(u))
}

// startSession issues a new session token, so a session identifier fixed before login is
// never the one that ends up authenticated.
func (a *API) startSession(w http.ResponseWriter, r *http.Request, u auth.User) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := a.Store.CreateSession(r.Context(), hashToken(token), u.ID, sessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, a.cookie(token, int(sessionTTL.Seconds())))
	return nil
}

func (a *API) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/api",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.SecureCookie,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if err := a.Store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			internalError(w, r, err)
			return
		}
	}
	http.SetCookie(w, a.cookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	writeJSON(w, http.StatusOK, toUserJSON(u))
}

func (a *API) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "passwords must be at least 12 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(w, r, err)
		return
	}
	u, err := a.Store.AcceptInvite(r.Context(), hashToken(strings.TrimSpace(in.Token)), string(hash))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "this invite is invalid, used, or expired")
		return
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "an account with this email already exists")
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toUserJSON(u))
}

// randomToken returns 256 random bits, URL-safe.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func mustHash(s string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}

func isRole(role string) bool { return role == roleAdmin || role == roleMember }

// mailAddress returns the bare address of a well-formed email, or an error.
func mailAddress(s string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}
	return a.Address, nil
}
