// Command mirage serves an echo MCP tool over streamable HTTP at http://127.0.0.1:8080/mcp.
package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

func echo(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: args.Text}},
	}, nil, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "echo",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Echo the given text back to the caller",
	}, echo)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	},
		&mcp.StreamableHTTPOptions{
			JSONResponse: false,
		},
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", logHTTP(mcpHandler))

	addr := "127.0.0.1:8080"
	log.Printf("echo MCP server listening on http://%s/mcp", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func logHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))

		log.Printf("-> %s %s mcp-session-id=%q\n%s", r.Method, r.URL.Path, r.Header.Get("Mcp-Session-Id"), body)

		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		log.Printf("<- %d content-type=%q mcp-session-id=%q", sw.code, w.Header().Get("Content-Type"), w.Header().Get("Mcp-Session-Id"))
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
