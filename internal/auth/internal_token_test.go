package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInternalTokenRoundTripsUntilExpiry(t *testing.T) {
	issuer, err := NewTokenIssuer()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	issuer.now = func() time.Time { return now }
	token := issuer.Mint(42, 5*time.Minute)

	if id, err := issuer.Verify(token); err != nil || id != 42 {
		t.Fatalf("Verify = %d, %v; want 42", id, err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := issuer.Verify(token); !errors.Is(err, ErrInvalidInternalToken) {
		t.Fatalf("at expiry: err = %v, want ErrInvalidInternalToken", err)
	}
}

func TestInternalTokenRejectsForgeriesAndOtherProcesses(t *testing.T) {
	issuer, _ := NewTokenIssuer()
	other, _ := NewTokenIssuer()
	token := issuer.Mint(42, time.Minute)
	payload, mac, _ := strings.Cut(strings.TrimPrefix(token, "gwi_"), ".")
	forgedPayload := issuer.Mint(1, time.Minute) // a valid token for user 1, MAC kept from user 42
	forgedPayload = strings.TrimPrefix(forgedPayload, "gwi_")
	forgedPayload, _, _ = strings.Cut(forgedPayload, ".")

	for name, tok := range map[string]string{
		"other process's token": other.Mint(42, time.Minute),
		"payload swapped":       "gwi_" + forgedPayload + "." + mac,
		"MAC truncated":         "gwi_" + payload + "." + mac[:len(mac)-2],
		"no MAC":                "gwi_" + payload,
		"API key form":          "gw_" + payload,
		"empty":                 "",
	} {
		if _, err := issuer.Verify(tok); !errors.Is(err, ErrInvalidInternalToken) {
			t.Errorf("%s: err = %v, want ErrInvalidInternalToken", name, err)
		}
	}
}
