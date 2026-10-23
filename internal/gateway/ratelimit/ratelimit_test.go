package ratelimit

import (
	"context"
	"testing"
	"time"
)

type memCounter map[[2]int64]int

func (m memCounter) CountCall(_ context.Context, userID int64, w time.Time) (int, error) {
	k := [2]int64{userID, w.Unix()}
	m[k]++
	return m[k], nil
}

func TestLimitPerUserPerMinute(t *testing.T) {
	l := New(memCounter{}, 3)
	now := time.Date(2026, 10, 26, 12, 0, 40, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := range 3 {
		if ok, _, _ := l.Allow(t.Context(), 1); !ok {
			t.Fatalf("call %d of 3 refused", i+1)
		}
	}
	ok, retry, _ := l.Allow(t.Context(), 1)
	if ok || retry != 20*time.Second {
		t.Fatalf("fourth call: allowed %v, retry in %s; want refused, retry in 20s", ok, retry)
	}
	if ok, _, _ := l.Allow(t.Context(), 2); !ok {
		t.Fatal("another user's call was refused; limits are per user")
	}
	now = now.Add(20 * time.Second)
	if ok, _, _ := l.Allow(t.Context(), 1); !ok {
		t.Fatal("the next minute's first call was refused")
	}
}
