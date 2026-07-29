package contextwindow_test

import (
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/contextwindow"
)

func TestNormalizeTurnsCompleteUnitsAndFilter(t *testing.T) {
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	msgs := []contextwindow.HistoryMessage{
		{ID: "s1", SessionID: "sess", Role: "SYSTEM", Content: "sys", CreatedAt: base},
		{ID: "u1", SessionID: "sess", Role: "USER", Content: "hi", CreatedAt: base.Add(time.Second)},
		{ID: "a1", SessionID: "sess", Role: "ASSISTANT", Content: "hello", CreatedAt: base.Add(2 * time.Second)},
		{ID: "t1", SessionID: "sess", Role: "TOOL", Content: "tool", CreatedAt: base.Add(3 * time.Second)},
		{ID: "u2", SessionID: "sess", Role: "USER", Content: "again", CreatedAt: base.Add(4 * time.Second)},
		{ID: "a2", SessionID: "sess", Role: "ASSISTANT", Content: "fail", RunStatus: "FAILED", CreatedAt: base.Add(5 * time.Second)},
		{ID: "u3", SessionID: "sess", Role: "USER", Content: "current", CreatedAt: base.Add(6 * time.Second)},
	}
	turns, current, err := contextwindow.NormalizeTurns(msgs, "u3", "sess")
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != "u3" {
		t.Fatalf("current: %+v", current)
	}
	if len(turns) != 2 {
		t.Fatalf("turns=%d want 2: %+v", len(turns), turns)
	}
	if turns[0].User.ID != "u1" || len(turns[0].Assistants) != 1 || turns[0].Assistants[0].ID != "a1" {
		t.Fatalf("turn0: %+v", turns[0])
	}
	// u2 has failed assistant dropped → user-only turn retained.
	if turns[1].User.ID != "u2" || len(turns[1].Assistants) != 0 {
		t.Fatalf("turn1: %+v", turns[1])
	}
}

func TestNormalizeTurnsMissingCurrent(t *testing.T) {
	_, _, err := contextwindow.NormalizeTurns(nil, "missing", "sess")
	if !errors.Is(err, contextwindow.ErrCurrentUserMissing) {
		t.Fatalf("got %v", err)
	}
}

func TestNormalizeTurnsDoesNotSplitAcrossUsers(t *testing.T) {
	base := time.Now().UTC()
	msgs := []contextwindow.HistoryMessage{
		{ID: "u1", SessionID: "s", Role: "USER", CreatedAt: base},
		{ID: "a1", SessionID: "s", Role: "ASSISTANT", CreatedAt: base.Add(time.Second)},
		{ID: "a2", SessionID: "s", Role: "ASSISTANT", CreatedAt: base.Add(2 * time.Second)},
		{ID: "u2", SessionID: "s", Role: "USER", CreatedAt: base.Add(3 * time.Second)},
		{ID: "cur", SessionID: "s", Role: "USER", CreatedAt: base.Add(4 * time.Second)},
	}
	turns, _, err := contextwindow.NormalizeTurns(msgs, "cur", "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || len(turns[0].Assistants) != 2 {
		t.Fatalf("expected unsplit multi-assistant turn: %+v", turns)
	}
}
