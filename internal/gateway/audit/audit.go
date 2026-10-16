// Package audit defines the row the gateway records for every tool call, whatever its
// outcome.
package audit

import (
	"context"
	"time"
)

// Outcome classifies how a call ended.
type Outcome string

// Call outcomes, as stored in audit_log.outcome.
const (
	// OK means the upstream answered without an error.
	OK Outcome = "ok"
	// ToolError means the upstream answered with an isError result.
	ToolError Outcome = "tool_error"
	// Unreachable means the gateway could not reach the upstream.
	Unreachable Outcome = "unreachable"
	// Timeout means the upstream did not answer within the call timeout.
	Timeout Outcome = "timeout"
	// Canceled means the client went away before the call finished.
	Canceled Outcome = "canceled"
)

// Entry is one tool call. UserID is 0 when the caller was not authenticated.
type Entry struct {
	UserID     int64
	ServerSlug string
	Tool       string
	StartedAt  time.Time
	Duration   time.Duration
	Outcome    Outcome
	Error      string
}

// Recorder stores entries.
type Recorder interface {
	RecordCall(ctx context.Context, e Entry) error
}
