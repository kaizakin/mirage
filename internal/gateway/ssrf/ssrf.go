// Package ssrf keeps admin-supplied upstream URLs from reaching the gateway's own network:
// loopback, the cluster, and cloud metadata endpoints. ValidateURL rejects the obvious cases
// early with a readable message; the guarded dialer is the real defense, because it checks
// the address actually being connected to, after DNS, so a name that resolves (or later
// rebinds) to a private address is still refused.
package ssrf

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// BlockedError reports a refused connection to a non-public address.
type BlockedError struct {
	Addr   netip.Addr
	Reason string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("ssrf: refusing to connect to %s (%s address)", e.Addr, e.Reason)
}

var (
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	cgnat       = netip.MustParsePrefix("100.64.0.0/10")
	reserved    = netip.MustParsePrefix("240.0.0.0/4") // includes broadcast 255.255.255.255
	nat64       = netip.MustParsePrefix("64:ff9b::/96")
)

// blocked returns why addr is not a public unicast address, or "".
func blocked(addr netip.Addr) string {
	addr = addr.Unmap()
	if nat64.Contains(addr) {
		// NAT64 forwards to the IPv4 address in the low 32 bits.
		b := addr.As16()
		addr = netip.AddrFrom4([4]byte(b[12:]))
	}
	switch {
	case addr.IsLoopback():
		return "loopback"
	case addr.IsUnspecified(), thisNetwork.Contains(addr):
		return "unspecified"
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return "link-local"
	case addr.IsPrivate():
		return "private"
	case cgnat.Contains(addr):
		return "shared (CGNAT)"
	case addr.IsMulticast(), addr.IsInterfaceLocalMulticast():
		return "multicast"
	case reserved.Contains(addr):
		return "reserved"
	}
	return ""
}

// internalSuffixes are names that only resolve inside a cluster or cloud network.
var internalSuffixes = []string{".localhost", ".local", ".svc", ".internal"}

// ValidateURL checks that raw is an absolute http(s) URL naming a public host. It cannot
// prove the host resolves to a public address; the guarded dialer checks that on connect.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return errors.New("url must be an absolute http or https URL")
	}
	if u.User != nil {
		return errors.New("url must not contain a username or password; use the credential field")
	}
	if strings.Contains(raw, "#") {
		return errors.New("url must not contain a fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return errors.New("url must name a host")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if why := blocked(addr); why != "" {
			return fmt.Errorf("url points at a %s address; only public hosts are allowed", why)
		}
		return nil
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return fmt.Errorf("url host %q is not a public domain name", host)
	}
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(host, suffix) {
			return fmt.Errorf("url host %q is an internal name; only public hosts are allowed", host)
		}
	}
	return nil
}

// Control is a net.Dialer Control func that refuses non-public addresses. It runs after
// name resolution, once per address tried, with the exact IP the socket connects to.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("ssrf: unexpected dial address %q", address)
	}
	if why := blocked(ap.Addr()); why != "" {
		return &BlockedError{Addr: ap.Addr().Unmap(), Reason: why}
	}
	return nil
}

// Dialer returns a dialer that only connects to public addresses, with the same timeouts
// as http.DefaultTransport's.
func Dialer() *net.Dialer {
	return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: Control}
}
