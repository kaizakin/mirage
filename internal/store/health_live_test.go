package store

import (
	"testing"
	"time"

	"github.com/kaizakin/mirage/internal/gateway/audit"
)

func TestLiveCallsSinceSummarizesEachServer(t *testing.T) {
	s := migrated(t)
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "member")
	now := time.Now()
	for _, e := range []audit.Entry{
		{UserID: ada.ID, ServerSlug: "currency", Tool: "convert", StartedAt: now.Add(-3 * time.Minute), Duration: 10 * time.Millisecond, Outcome: audit.OK},
		{UserID: ada.ID, ServerSlug: "currency", Tool: "convert", StartedAt: now.Add(-2 * time.Minute), Duration: 30 * time.Millisecond, Outcome: audit.OK},
		{UserID: ada.ID, ServerSlug: "currency", Tool: "slow_echo", StartedAt: now.Add(-1 * time.Minute), Duration: 20 * time.Second, Outcome: audit.Timeout, Error: "upstream currency did not answer slow_echo within 20s"},
		{UserID: ada.ID, ServerSlug: "store-tea", Tool: "list_orders", StartedAt: now.Add(-1 * time.Minute), Duration: 5 * time.Millisecond, Outcome: audit.OK},
		{UserID: ada.ID, ServerSlug: "store-tea", Tool: "list_orders", StartedAt: now.Add(-2 * time.Hour), Outcome: audit.Unreachable, Error: "old"},
	} {
		if err := s.RecordCall(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.CallsSince(t.Context(), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("summaries = %+v, want currency and store-tea", got)
	}
	cur, tea := got[0], got[1]
	if cur.Server != "currency" || cur.Calls != 3 || cur.Failed != 1 || cur.P50 != 30*time.Millisecond ||
		cur.LastOutcome != "timeout" || cur.LastError != "upstream currency did not answer slow_echo within 20s" || cur.LastErrorAt == nil {
		t.Fatalf("currency = %+v", cur)
	}
	if tea.Server != "store-tea" || tea.Calls != 1 || tea.Failed != 0 || tea.LastError != "" {
		t.Fatalf("store-tea = %+v; the failure outside the window must not count", tea)
	}
}
