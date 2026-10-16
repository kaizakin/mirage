package upstream

import (
	"fmt"
	"net/http"

	"github.com/kaizakin/mirage/internal/secretbox"
)

// credentialTransport adds one header to every request. It holds the only decrypted copy
// of an upstream's credential, so the credential never leaves this package.
type credentialTransport struct {
	base   http.RoundTripper
	header string
	value  string
}

// RoundTrip implements http.RoundTripper. A RoundTripper must not modify its request, so
// it sets the header on a clone.
func (t *credentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(t.header, t.value)
	return t.base.RoundTrip(req)
}

// httpClient returns the client for one upstream, decrypting its credential with the slug
// as associated data. It has no overall timeout; callers bound each call with a context.
//
// It never follows redirects. Go strips Authorization on a cross-host redirect, but
// credentialTransport would add the header back on the next hop and hand the credential,
// and the replayed request body, to wherever the upstream points.
func httpClient(cfg Config, box *secretbox.Box) (*http.Client, error) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	noRedirects := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if cfg.AuthType == AuthNone {
		return &http.Client{Transport: base, CheckRedirect: noRedirects}, nil
	}
	credential, err := box.Decrypt(cfg.CredentialEnc, []byte(cfg.Slug))
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	t := &credentialTransport{base: base, header: cfg.AuthHeader, value: string(credential)}
	if cfg.AuthType == AuthBearer {
		t.header, t.value = "Authorization", "Bearer "+string(credential)
	}
	return &http.Client{Transport: t, CheckRedirect: noRedirects}, nil
}
