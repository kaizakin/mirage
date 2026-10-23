package store

import (
	"context"
	"time"
)

// ServerCalls summarizes one server's tool calls since some time.
type ServerCalls struct {
	Server      string
	Calls       int
	Failed      int // any outcome but ok
	P50, P95    time.Duration
	LastError   string
	LastErrorAt *time.Time
	LastOutcome string // of the latest failed call
}

// CallsSince summarizes each server's calls since since, from the audit log: how many, how
// many failed, latency percentiles, and the latest failure's reason.
func (s *Store) CallsSince(ctx context.Context, since time.Time) ([]ServerCalls, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.server_slug, count(*), count(*) FILTER (WHERE a.outcome <> 'ok'),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY a.duration_ms),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY a.duration_ms),
			last.error, last.started_at, last.outcome
		FROM audit_log a
		LEFT JOIN LATERAL (
			SELECT coalesce(f.error, f.outcome) AS error, f.started_at, f.outcome FROM audit_log f
			WHERE f.server_slug = a.server_slug AND f.outcome <> 'ok' AND f.started_at >= $1
			ORDER BY f.id DESC LIMIT 1
		) last ON true
		WHERE a.started_at >= $1
		GROUP BY a.server_slug, last.error, last.started_at, last.outcome
		ORDER BY a.server_slug`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerCalls
	for rows.Next() {
		var c ServerCalls
		var p50, p95 float64
		var lastErr, lastOutcome *string
		if err := rows.Scan(&c.Server, &c.Calls, &c.Failed, &p50, &p95, &lastErr, &c.LastErrorAt, &lastOutcome); err != nil {
			return nil, err
		}
		c.P50, c.P95 = time.Duration(p50*float64(time.Millisecond)), time.Duration(p95*float64(time.Millisecond))
		if lastErr != nil {
			c.LastError, c.LastOutcome = *lastErr, *lastOutcome
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
