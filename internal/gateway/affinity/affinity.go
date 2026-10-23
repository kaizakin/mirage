// Package affinity lets several gateway replicas serve one MCP endpoint. An MCP session lives
// in the memory of the replica that created it, and a load balancer may send the session's
// next request to any replica. So each session ID names its owner, and a replica that gets
// a request for another replica's session forwards it there.
//
// A session ID is <random>.<owner address>.<MAC>. The MAC, under a key all replicas share,
// stops a client from naming an address of its own choosing and having the gateway proxy
// requests to it.
package affinity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// sessionHeader is the MCP streamable HTTP session header.
const sessionHeader = "Mcp-Session-Id"

// replicaHeader names the replica that served a response, which shows sessions staying on
// their replica while a load balancer spreads requests.
const replicaHeader = "X-Mirage-Replica"

// forwardedHeader marks a request one replica sent to another, so it is never forwarded twice.
const forwardedHeader = "X-Mirage-Forwarded"

// Router makes session IDs that name this replica and forwards other replicas' sessions.
type Router struct {
	self  string // this replica's address, host:port, as other replicas reach it
	name  string // this replica's name, for replicaHeader
	key   []byte
	proxy *httputil.ReverseProxy
}

// New returns a Router for the replica reachable at self (host:port), with the key all
// replicas share.
func New(self string, key []byte) (*Router, error) {
	if _, _, err := net.SplitHostPort(self); err != nil {
		return nil, err
	}
	if len(key) < 16 {
		return nil, errors.New("affinity: the session key must be at least 16 bytes")
	}
	name, _ := os.Hostname() // the pod's name in Kubernetes
	r := &Router{self: self, name: name, key: key}
	r.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			owner, _ := pr.In.Context().Value(ownerKey{}).(string)
			pr.SetURL(&url.URL{Scheme: "http", Host: owner})
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set(forwardedHeader, "1")
		},
		// Responses may be server-sent event streams; pass each event on as it comes.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			// The owner is gone, so its sessions are too. 404 tells an MCP client to start
			// a new session.
			slog.Warn("forwarding to a session's replica failed", "owner", req.Context().Value(ownerKey{}), "err", err)
			http.Error(w, "session not found", http.StatusNotFound)
		},
	}
	return r, nil
}

type ownerKey struct{}

// NewSessionID returns a session ID owned by this replica. It suits mcp.ServerOptions.GetSessionID.
func (r *Router) NewSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	nonce := base64.RawURLEncoding.EncodeToString(b)
	owner := base64.RawURLEncoding.EncodeToString([]byte(r.self))
	return nonce + "." + owner + "." + r.mac(nonce, owner)
}

func (r *Router) mac(nonce, owner string) string {
	m := hmac.New(sha256.New, r.key)
	m.Write([]byte(nonce + "." + owner))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// owner returns the address of the replica that issued id, if id is genuine.
func (r *Router) owner(id string) (string, bool) {
	parts := strings.Split(id, ".")
	if len(parts) != 3 || !hmac.Equal([]byte(parts[2]), []byte(r.mac(parts[0], parts[1]))) {
		return "", false
	}
	addr, err := base64.RawURLEncoding.DecodeString(parts[1])
	return string(addr), err == nil
}

// Handler forwards requests for sessions this replica does not own to the replica that does,
// and serves the rest with next. A session ID that is not genuine gets 404.
func (r *Router) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := req.Header.Get(sessionHeader)
		if id == "" {
			r.serve(w, req, next)
			return
		}
		owner, ok := r.owner(id)
		switch {
		case !ok:
			http.Error(w, "session not found", http.StatusNotFound)
		case owner == r.self:
			r.serve(w, req, next)
		case req.Header.Get(forwardedHeader) != "":
			// Forwarded here, yet not ours: the owner's address now belongs to another pod.
			http.Error(w, "session not found", http.StatusNotFound)
		default:
			r.proxy.ServeHTTP(w, req.WithContext(contextWithOwner(req, owner)))
		}
	})
}

func contextWithOwner(req *http.Request, owner string) context.Context {
	return context.WithValue(req.Context(), ownerKey{}, owner)
}

func (r *Router) serve(w http.ResponseWriter, req *http.Request, next http.Handler) {
	w.Header().Set(replicaHeader, r.name)
	next.ServeHTTP(w, req)
}
