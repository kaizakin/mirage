// Package ratelimit caps tool calls per user per minute. Counts live in a shared store, so
// the cap holds across gateway replicas; it covers all of a user's credentials (API keys,
// OAuth tokens, and chat) together.
package ratelimit

import (
	"context"
	"time"
)

// Counter counts a user's calls in a window and returns the count including this one.
type Counter interface {
	CountCall(ctx context.Context, userID int64, windowStart time.Time) (int, error)
}

// Limiter allows at most PerMinute tool calls per user in each clock minute.
type Limiter struct {
	counter   Counter
	perMinute int
	now       func() time.Time
}

// New returns a Limiter allowing perMinute calls per user per minute.
func New(c Counter, perMinute int) *Limiter {
	return &Limiter{counter: c, perMinute: perMinute, now: time.Now}
}

// Limit is the number of calls allowed per minute.
func (l *Limiter) Limit() int { return l.perMinute }

// Allow counts a call by the user and reports whether it is within the limit, and if not,
// how long until the next window opens.
func (l *Limiter) Allow(ctx context.Context, userID int64) (bool, time.Duration, error) {
	now := l.now()
	window := now.Truncate(time.Minute)
	calls, err := l.counter.CountCall(ctx, userID, window)
	if err != nil {
		return false, 0, err
	}
	if calls > l.perMinute {
		return false, window.Add(time.Minute).Sub(now), nil
	}
	return true, 0, nil
}
