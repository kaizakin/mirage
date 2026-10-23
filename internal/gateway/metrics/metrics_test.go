package metrics

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/gateway/audit"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
)

type fixedStates []upstream.State

func (f fixedStates) Snapshot() []upstream.State { return f }

type discard struct{ n int }

func (d *discard) RecordCall(context.Context, audit.Entry) error {
	d.n++
	return nil
}

func TestRecordedCallsAndUpstreamHealthAreExposed(t *testing.T) {
	m := New(fixedStates{
		{Slug: "currency", Status: upstream.Connected, Tools: []*mcp.Tool{{Name: "convert"}, {Name: "slow_echo"}}},
		{Slug: "store-tea", Status: upstream.Unreachable, Reason: "connection refused"},
	})
	next := &discard{}
	rec := m.Recorder(next)
	_ = rec.RecordCall(t.Context(), audit.Entry{ServerSlug: "currency", Tool: "convert", Outcome: audit.OK, Duration: 30 * time.Millisecond})
	_ = rec.RecordCall(t.Context(), audit.Entry{ServerSlug: "store-tea", Tool: "list_orders", Outcome: audit.Unreachable})
	if next.n != 2 {
		t.Fatalf("the audit recorder got %d calls, want both passed on", next.n)
	}

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rr.Body)
	for _, want := range []string{
		`mirage_tool_calls_total{outcome="ok",server="currency",tool="convert"} 1`,
		`mirage_tool_calls_total{outcome="unreachable",server="store-tea",tool="list_orders"} 1`,
		`mirage_tool_call_duration_seconds_bucket{outcome="ok",server="currency",le="0.05"} 1`,
		`mirage_upstream_up{server="currency"} 1`,
		`mirage_upstream_up{server="store-tea"} 0`,
		`mirage_upstream_status{server="store-tea",status="unreachable"} 1`,
		`mirage_upstream_tools{server="currency"} 2`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}
