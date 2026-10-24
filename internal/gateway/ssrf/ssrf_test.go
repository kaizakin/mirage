package ssrf

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr string
	}{
		{"https://mcp.deepwiki.com/mcp", ""},
		{"http://example.com:8080/mcp?x=1", ""},
		{"https://93.184.215.14/mcp", ""},
		{"https://[2606:4700::1111]/mcp", ""},
		{"ftp://example.com/mcp", "absolute http or https"},
		{"/mcp", "absolute http or https"},
		{"http:example.com", "absolute http or https"},
		{"https://user:pw@example.com/mcp", "username or password"},
		{"https://example.com/mcp#frag", "fragment"},
		{"https://example.com/mcp#", "fragment"},
		{"http://127.0.0.1:8080/mcp", "loopback"},
		{"http://[::1]/mcp", "loopback"},
		{"http://0.0.0.0/mcp", "unspecified"},
		{"http://169.254.169.254/latest/meta-data", "link-local"},
		{"http://[fd00:ec2::254]/", "private"},
		{"http://10.43.0.10:5432/", "private"},
		{"http://[::ffff:10.0.0.1]/", "private"},
		{"http://100.100.100.200/", "CGNAT"},
		{"http://localhost:8080/mcp", "not a public domain"},
		{"http://LOCALHOST./mcp", "not a public domain"},
		{"http://mirage-postgres:5432/", "not a public domain"},
		{"http://store-woo-mcp.store-acme.svc.cluster.local:8080/mcp", "internal name"},
		{"http://mirage-currency-mcp.mirage.svc:8080/mcp", "internal name"},
		{"http://metadata.google.internal/computeMetadata/v1/", "internal name"},
		{"http://foo.localhost/", "internal name"},
	}
	for _, tt := range tests {
		err := ValidateURL(tt.url)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("ValidateURL(%q) = %v, want nil", tt.url, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("ValidateURL(%q) = %v, want error containing %q", tt.url, err, tt.wantErr)
		}
	}
}

func TestControl(t *testing.T) {
	tests := []struct {
		address string
		reason  string
	}{
		{"93.184.215.14:443", ""},
		{"[2606:4700::1111]:443", ""},
		{"[64:ff9b::5db8:d70e]:443", ""}, // NAT64 of a public address
		{"127.0.0.1:80", "loopback"},
		{"127.9.9.9:80", "loopback"},
		{"[::1]:80", "loopback"},
		{"[::ffff:127.0.0.1]:80", "loopback"},
		{"0.0.0.0:80", "unspecified"},
		{"0.1.2.3:80", "unspecified"},
		{"[::]:80", "unspecified"},
		{"169.254.169.254:80", "link-local"},
		{"[::ffff:169.254.169.254]:80", "link-local"},
		{"[fe80::1]:80", "link-local"},
		{"10.42.0.5:8080", "private"},
		{"172.16.0.1:80", "private"},
		{"172.31.255.255:80", "private"},
		{"192.168.1.1:80", "private"},
		{"[fd00:ec2::254]:80", "private"},
		{"[fc00::1]:80", "private"},
		{"[64:ff9b::a00:1]:80", "private"}, // NAT64 of 10.0.0.1
		{"100.64.0.1:80", "shared (CGNAT)"},
		{"[::ffff:100.64.0.1]:80", "shared (CGNAT)"},
		{"224.0.0.1:80", "link-local"},
		{"239.1.1.1:80", "multicast"},
		{"[ff02::1]:80", "link-local"},
		{"[ff0e::1]:80", "multicast"},
		{"255.255.255.255:80", "reserved"},
		{"240.0.0.1:80", "reserved"},
	}
	for _, tt := range tests {
		err := Control("tcp", tt.address, nil)
		if tt.reason == "" {
			if err != nil {
				t.Errorf("Control(%q) = %v, want nil", tt.address, err)
			}
			continue
		}
		var blockedErr *BlockedError
		if !errors.As(err, &blockedErr) || blockedErr.Reason != tt.reason {
			t.Errorf("Control(%q) = %v, want a %s block", tt.address, err, tt.reason)
		}
	}
}

func TestBlockedErrorMessage(t *testing.T) {
	err := Control("tcp", "10.42.0.5:8080", nil)
	if want := "ssrf: refusing to connect to 10.42.0.5 (private address)"; err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	err = Control("tcp", "[::ffff:169.254.169.254]:80", nil)
	if want := "ssrf: refusing to connect to 169.254.169.254 (link-local address)"; err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// A name that resolves to a private address passes ValidateURL's name checks only if it
// looks public, so the dialer must refuse it after resolution. "localhost" stands in for a
// rebinding name: the dial goes through the resolver and reaches a live listener on loopback.
func TestDialerRefusesResolvedPrivateAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	target := net.JoinHostPort("localhost", strconv.Itoa(port))

	plain, err := (&net.Dialer{}).DialContext(t.Context(), "tcp4", target)
	if err != nil {
		t.Fatalf("unguarded dial should reach the listener: %v", err)
	}
	_ = plain.Close()

	conn, err := Dialer().DialContext(t.Context(), "tcp4", target)
	if err == nil {
		_ = conn.Close()
		t.Fatal("guarded dial connected to loopback")
	}
	var blockedErr *BlockedError
	if !errors.As(err, &blockedErr) || blockedErr.Addr != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("got %v, want a BlockedError for 127.0.0.1", err)
	}
}
