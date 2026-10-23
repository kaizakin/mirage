package store

import (
	"context"
	"time"
)

// CountCall adds one call to the user's count for the window starting at windowStart and
// returns the new count. It is one statement, so concurrent replicas never lose a count.
func (s *Store) CountCall(ctx context.Context, userID int64, windowStart time.Time) (int, error) {
	var calls int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO rate_limit_windows (user_id, window_start, calls) VALUES ($1, $2, 1)
		ON CONFLICT (user_id, window_start) DO UPDATE SET calls = rate_limit_windows.calls + 1
		RETURNING calls`, userID, windowStart).Scan(&calls)
	return calls, err
}

// PruneRateLimits deletes windows that ended over an hour ago.
func (s *Store) PruneRateLimits(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rate_limit_windows WHERE window_start < now() - interval '1 hour'`)
	return err
}
