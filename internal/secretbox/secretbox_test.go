package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{7}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := newBox(t)
	for _, plaintext := range [][]byte{[]byte("ck_live_bearer_token"), {}} {
		sealed, err := b.Encrypt(plaintext, []byte("woo"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.Decrypt(sealed, []byte("woo"))
		if err != nil {
			t.Fatalf("Decrypt(Encrypt(%q)): %v", plaintext, err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("round trip = %q, want %q", got, plaintext)
		}
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	b := newBox(t)
	first, _ := b.Encrypt([]byte("same"), nil)
	second, _ := b.Encrypt([]byte("same"), nil)
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same plaintext are identical; the nonce is being reused")
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	b := newBox(t)
	sealed, err := b.Encrypt([]byte("ck_live_bearer_token"), []byte("woo"))
	if err != nil {
		t.Fatal(err)
	}
	// Flip one bit in each region: the nonce, the encrypted body, and the tag.
	for _, i := range []int{0, 12, len(sealed) - 1} {
		tampered := bytes.Clone(sealed)
		tampered[i] ^= 0x01
		if _, err := b.Decrypt(tampered, []byte("woo")); !errors.Is(err, ErrOpen) {
			t.Errorf("byte %d flipped: err = %v, want ErrOpen", i, err)
		}
	}
	if _, err := b.Decrypt(sealed[:len(sealed)-1], []byte("woo")); !errors.Is(err, ErrOpen) {
		t.Errorf("truncated: err = %v, want ErrOpen", err)
	}
	if _, err := b.Decrypt(sealed[:5], []byte("woo")); !errors.Is(err, ErrOpen) {
		t.Errorf("shorter than a nonce: err = %v, want ErrOpen", err)
	}
}

func TestDecryptBindsToAdditionalDataAndKey(t *testing.T) {
	b := newBox(t)
	sealed, err := b.Encrypt([]byte("ck_live_bearer_token"), []byte("woo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Decrypt(sealed, []byte("currency")); !errors.Is(err, ErrOpen) {
		t.Errorf("other server's associated data: err = %v, want ErrOpen", err)
	}

	otherKey := make([]byte, KeySize)
	_, _ = rand.Read(otherKey)
	other, err := New(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decrypt(sealed, []byte("woo")); !errors.Is(err, ErrOpen) {
		t.Errorf("other key: err = %v, want ErrOpen", err)
	}
}

func TestKeyValidation(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Error("New accepted a 16-byte key")
	}
	if _, err := NewFromBase64("not base64!"); err == nil {
		t.Error("NewFromBase64 accepted invalid base64")
	}
	if _, err := NewFromBase64(base64.StdEncoding.EncodeToString(make([]byte, KeySize))); err != nil {
		t.Errorf("NewFromBase64 rejected a valid key: %v", err)
	}
}
