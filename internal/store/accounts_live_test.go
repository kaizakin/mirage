package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/audit"
	"github.com/kaizakin/mirage/internal/secretbox"
)

func migrated(t *testing.T) *Store {
	t.Helper()
	db := freshDB(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestLiveSessionsExpireAndEnd(t *testing.T) {
	s := migrated(t)
	u, err := s.CreateUser(t.Context(), "ada@example.com", "h", "admin")
	if err != nil {
		t.Fatal(err)
	}
	live, idleOut := []byte("live-token-hash"), []byte("idle-token-hash")
	for _, h := range [][]byte{live, idleOut} {
		if err := s.CreateSession(t.Context(), h, u.ID, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.SessionUser(t.Context(), live, time.Hour); err != nil || got != u {
		t.Fatalf("live session = %+v, %v", got, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := s.SessionUser(t.Context(), idleOut, time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session idle past the limit: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteSession(t.Context(), live); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(t.Context(), live, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after logout: err = %v, want ErrNotFound", err)
	}
}

func TestLiveSetPasswordEndsSessions(t *testing.T) {
	s := migrated(t)
	u, err := s.CreateUser(t.Context(), "ada@example.com", "old", "admin")
	if err != nil {
		t.Fatal(err)
	}
	token := []byte("session-hash")
	if err := s.CreateSession(t.Context(), token, u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(t.Context(), u.ID, "new"); err != nil {
		t.Fatal(err)
	}
	if _, hash, err := s.UserByEmail(t.Context(), u.Email); err != nil || hash != "new" {
		t.Fatalf("hash = %q, %v; want new", hash, err)
	}
	if _, err := s.SessionUser(t.Context(), token, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session after password change: err = %v, want ErrNotFound", err)
	}
	if err := s.SetPassword(t.Context(), u.ID+1, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: err = %v, want ErrNotFound", err)
	}
}

func TestLiveInviteIsAcceptedOnce(t *testing.T) {
	s := migrated(t)
	token := []byte("invite-hash")
	if err := s.CreateInvite(t.Context(), token, "bob@example.com", "member", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingInvites(t.Context()); err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	u, err := s.AcceptInvite(t.Context(), token, "pw-hash")
	if err != nil || u.Email != "bob@example.com" || u.Role != "member" || u.ID == 0 {
		t.Fatalf("accept = %+v, %v", u, err)
	}
	if _, err := s.AcceptInvite(t.Context(), token, "pw-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second accept: err = %v, want ErrNotFound", err)
	}
	if pending, _ := s.PendingInvites(t.Context()); len(pending) != 0 {
		t.Fatalf("used invite still pending: %+v", pending)
	}
}

func TestLiveServerPatchKeepsAuthCoherent(t *testing.T) {
	s := migrated(t)
	box, _ := secretbox.New(make([]byte, secretbox.KeySize))
	if err := s.CreateServer(t.Context(), box, ServerSeed{Slug: "cur", URL: "http://c/mcp", AuthType: "header", AuthHeader: "X-Api-Key", Credential: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateServer(t.Context(), box, ServerSeed{Slug: "cur", URL: "http://d/mcp", AuthType: "none"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug: err = %v, want ErrConflict", err)
	}
	none := "none"
	if err := s.UpdateServer(t.Context(), box, "cur", ServerPatch{AuthType: &none}); err != nil {
		t.Fatalf("switch to none: %v", err)
	}
	rows, _ := s.Servers(t.Context())
	if rows[0].HasCredential || rows[0].AuthHeader != "" {
		t.Fatalf("after switching to none: %+v, want credential and header cleared", rows[0])
	}
	bearer := "bearer"
	if err := s.UpdateServer(t.Context(), box, "cur", ServerPatch{AuthType: &bearer}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bearer without a credential: err = %v, want ErrInvalid", err)
	}
	if err := s.UpdateServer(t.Context(), box, "missing", ServerPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown slug: err = %v, want ErrNotFound", err)
	}
}

func TestLiveKeysAndAuditRespectOwnership(t *testing.T) {
	s := migrated(t)
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "admin")
	bob, _ := s.CreateUser(t.Context(), "bob@example.com", "h", "member")
	key, _ := auth.GenerateKey()
	if err := s.CreateAPIKey(t.Context(), ada.ID, key.ID, key.SecretHash); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAPIKeyOf(t.Context(), key.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob revoking ada's key: err = %v, want ErrNotFound", err)
	}
	if keys, _ := s.APIKeys(t.Context(), bob.ID); len(keys) != 0 {
		t.Fatalf("bob sees keys %+v", keys)
	}
	if err := s.RevokeAPIKeyOf(t.Context(), key.ID, ada.ID); err != nil {
		t.Fatal(err)
	}

	for _, e := range []audit.Entry{
		{UserID: ada.ID, ServerSlug: "woo", Tool: "list_orders", StartedAt: time.Now(), Outcome: audit.OK},
		{UserID: bob.ID, ServerSlug: "currency", Tool: "convert", StartedAt: time.Now(), Outcome: audit.Timeout, Error: "slow"},
		{UserID: bob.ID, ServerSlug: "woo", Tool: "get_order", StartedAt: time.Now(), Outcome: audit.OK},
	} {
		if err := s.RecordCall(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	bobs, _ := s.Audit(t.Context(), AuditFilter{UserID: bob.ID, Limit: 10})
	if len(bobs) != 2 || bobs[0].Tool != "get_order" || bobs[0].UserEmail != "bob@example.com" {
		t.Fatalf("bob's audit = %+v, want his 2 rows newest first", bobs)
	}
	page, _ := s.Audit(t.Context(), AuditFilter{Limit: 1, Before: bobs[0].ID})
	if len(page) != 1 || page[0].Tool != "convert" {
		t.Fatalf("next page = %+v, want the convert row", page)
	}
	if timeouts, _ := s.Audit(t.Context(), AuditFilter{Outcome: "timeout", Limit: 10}); len(timeouts) != 1 {
		t.Fatalf("timeouts = %+v", timeouts)
	}
}

func TestLiveAuditOptionsGroupToolsAndRespectScope(t *testing.T) {
	s := migrated(t)
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "admin")
	bob, _ := s.CreateUser(t.Context(), "Bob@example.com", "h", "member")
	_, _ = s.CreateUser(t.Context(), "idle@example.com", "h", "member")
	for _, e := range []audit.Entry{
		{UserID: ada.ID, ServerSlug: "woo", Tool: "list_orders", StartedAt: time.Now(), Outcome: audit.OK},
		{UserID: ada.ID, ServerSlug: "woo", Tool: "list_orders", StartedAt: time.Now(), Outcome: audit.OK},
		{UserID: bob.ID, ServerSlug: "woo", Tool: "get_order", StartedAt: time.Now(), Outcome: audit.OK},
		{UserID: bob.ID, ServerSlug: "currency", Tool: "convert", StartedAt: time.Now(), Outcome: audit.OK},
	} {
		if err := s.RecordCall(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.AuditOptions(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	wantServers := []AuditServer{{Slug: "currency", Tools: []string{"convert"}}, {Slug: "woo", Tools: []string{"get_order", "list_orders"}}}
	wantUsers := []AuditUser{{ID: ada.ID, Email: "ada@example.com"}, {ID: bob.ID, Email: "Bob@example.com"}}
	if !reflect.DeepEqual(all.Servers, wantServers) || !reflect.DeepEqual(all.Users, wantUsers) {
		t.Fatalf("everyone's options = %+v", all)
	}

	bobs, err := s.AuditOptions(t.Context(), bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantServers = []AuditServer{{Slug: "currency", Tools: []string{"convert"}}, {Slug: "woo", Tools: []string{"get_order"}}}
	if !reflect.DeepEqual(bobs.Servers, wantServers) || !reflect.DeepEqual(bobs.Users, []AuditUser{{ID: bob.ID, Email: "Bob@example.com"}}) {
		t.Fatalf("bob's options = %+v; they must not show ada's calls", bobs)
	}
}
