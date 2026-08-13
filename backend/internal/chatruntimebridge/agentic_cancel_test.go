package chatruntimebridge_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/chatruntimebridge"
)

// TestAgenticInitial_UserCancelDoesNotForceFailed is the cancel-status race:
// CancelRun sets Cause=ErrRunCancelled, the drive returns ctx.Err(), and a
// WithoutCancel failRun used to persist FAILED before the cancel API's
// CANCELLED CAS. Durable cancel is owned by that API; Execute must not rewrite it.
func TestAgenticInitial_UserCancelDoesNotForceFailed(t *testing.T) {
	f := newAgenticFixture(t, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(chatruntimebridge.ErrRunCancelled)

	err := f.bridge(t).Execute(ctx, f.job())
	if err == nil {
		t.Fatal("Execute must surface the cancelled drive")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, chatruntimebridge.ErrRunCancelled) {
		t.Fatalf("Execute error = %v, want canceled", err)
	}
	if f.results.content != "" {
		t.Fatalf("user cancel persisted assistant content %q (would mark the run FAILED)", f.results.content)
	}
	if f.events.calls.Load() != 0 {
		t.Fatalf("user cancel projected %d protocol records, want 0", f.events.calls.Load())
	}
}

func TestAgenticInitial_DeadlineStillFailsTheRun(t *testing.T) {
	f := newAgenticFixture(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	err := f.bridge(t).Execute(ctx, f.job())
	if err == nil {
		t.Fatal("Execute must fail a deadline")
	}
	if !strings.Contains(f.results.content, "抱歉") {
		t.Fatalf("deadline must persist FAILED assistant text, got %q", f.results.content)
	}
}
