package affinity

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var testKey = []byte("a shared session key, 32 bytes!!")

// replica serves an MCP server whose one tool names the replica, behind a Router, on its
// own address, the way each gateway pod does.
func replica(t *testing.T, name string) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	router, err := New(ln.Addr().String(), testKey)
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: name}, &mcp.ServerOptions{GetSessionID: router.NewSessionID})
	mcp.AddTool(server, &mcp.Tool{Name: "which"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name}}}, struct{}{}, nil
	})
	srv := httptest.NewUnstartedServer(router.Handler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// roundRobin is a load balancer with no affinity: each request goes to the next backend.
func roundRobin(t *testing.T, backends ...*httptest.Server) *httptest.Server {
	t.Helper()
	var n atomic.Uint64
	lb := httptest.NewServer(&httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			u, _ := url.Parse(backends[n.Add(1)%uint64(len(backends))].URL)
			pr.SetURL(u)
		},
		FlushInterval: -1,
	})
	t.Cleanup(lb.Close)
	return lb
}

func TestSessionsSurviveALoadBalancerWithoutAffinity(t *testing.T) {
	a, b := replica(t, "a"), replica(t, "b")
	lb := roundRobin(t, a, b)

	session, err := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: lb.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var owner string
	for i := range 8 {
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "which"})
		if err != nil {
			t.Fatalf("call %d through the load balancer: %v", i, err)
		}
		got := res.Content[0].(*mcp.TextContent).Text
		if owner == "" {
			owner = got
		}
		if got != owner {
			t.Fatalf("call %d reached replica %s; the session belongs to %s", i, got, owner)
		}
	}
}

func TestForgedOrOrphanedSessionsAreNotFound(t *testing.T) {
	a := replica(t, "a")
	post := func(id string) int {
		req, _ := http.NewRequest(http.MethodPost, a.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set(sessionHeader, id)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// A client naming an address of its choice, such as an internal service, is refused
	// rather than proxied.
	forged := "nonce." + "MTI3LjAuMC4xOjU0MzI" + ".bm90LWEtcmVhbC1tYWM"
	if got := post(forged); got != http.StatusNotFound {
		t.Fatalf("forged session ID answered %d, want 404", got)
	}

	// A genuine ID whose replica has gone tells the client to start a new session.
	gone, _ := New("127.0.0.1:1", testKey)
	if got := post(gone.NewSessionID()); got != http.StatusNotFound {
		t.Fatalf("session of a vanished replica answered %d, want 404", got)
	}
}
