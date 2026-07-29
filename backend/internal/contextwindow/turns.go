package contextwindow

import (
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	// ErrCurrentUserMissing is returned when the current USER message cannot be located.
	ErrCurrentUserMissing = errors.New("current user message missing")
	// ErrCurrentUserAmbiguous is returned when multiple messages share the current user id.
	ErrCurrentUserAmbiguous = errors.New("current user message ambiguous")
	// ErrCurrentUserSessionMismatch is returned when the current USER is not in the session.
	ErrCurrentUserSessionMismatch = errors.New("current user message session mismatch")
)

// HistoryMessage is a principal-authorized history record used for turn grouping.
// Content may be empty when the caller has not yet decrypted permanent bodies.
type HistoryMessage struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	// ContentHash is the stored content_sha256; required for assembly manifests later.
	ContentHash string
	// RunID associates the message with an agent run when present.
	RunID string
	// RunStatus is optional; when "FAILED", automatic failure assistant text is excluded.
	RunStatus string
	CreatedAt time.Time
}

// Turn is an atomic unit: one USER message and the ASSISTANT messages that follow
// until the next USER (or end of history). TOOL/SYSTEM history is never included.
type Turn struct {
	User       HistoryMessage
	Assistants []HistoryMessage
}

// Messages returns USER + ASSISTANT messages in chronological order.
func (t Turn) Messages() []HistoryMessage {
	out := make([]HistoryMessage, 0, 1+len(t.Assistants))
	out = append(out, t.User)
	out = append(out, t.Assistants...)
	return out
}

// NormalizeTurns groups prior history into complete turns for assembly.
// - Sorts by (created_at, id) ascending.
// - Filters SYSTEM and TOOL roles.
// - Locates current USER via currentUserMessageID; excludes it from prior turns.
// - Drops ASSISTANT messages tied to FAILED runs (failure text must not re-enter the prompt).
// - USER-only turns (no assistants, or only failed assistants) are retained.
func NormalizeTurns(messages []HistoryMessage, currentUserMessageID, sessionID string) ([]Turn, HistoryMessage, error) {
	currentUserMessageID = strings.TrimSpace(currentUserMessageID)
	sessionID = strings.TrimSpace(sessionID)
	if currentUserMessageID == "" {
		return nil, HistoryMessage{}, ErrCurrentUserMissing
	}

	sorted := append([]HistoryMessage(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	var current HistoryMessage
	var currentFound bool
	filtered := make([]HistoryMessage, 0, len(sorted))
	for _, msg := range sorted {
		role := strings.ToUpper(strings.TrimSpace(msg.Role))
		if role == "SYSTEM" || role == "TOOL" {
			continue
		}
		if msg.ID == currentUserMessageID {
			if currentFound {
				return nil, HistoryMessage{}, ErrCurrentUserAmbiguous
			}
			if sessionID != "" && msg.SessionID != "" && msg.SessionID != sessionID {
				return nil, HistoryMessage{}, ErrCurrentUserSessionMismatch
			}
			if role != "USER" {
				return nil, HistoryMessage{}, ErrCurrentUserMissing
			}
			current = msg
			currentFound = true
			continue
		}
		// Exclude automatic failure assistant text associated with FAILED runs.
		if role == "ASSISTANT" && strings.EqualFold(strings.TrimSpace(msg.RunStatus), "FAILED") {
			continue
		}
		if role != "USER" && role != "ASSISTANT" {
			continue
		}
		filtered = append(filtered, msg)
	}
	if !currentFound {
		return nil, HistoryMessage{}, ErrCurrentUserMissing
	}

	// Build turns only from messages strictly before current user (already excluded).
	turns := make([]Turn, 0)
	var open *Turn
	for _, msg := range filtered {
		role := strings.ToUpper(strings.TrimSpace(msg.Role))
		switch role {
		case "USER":
			if open != nil {
				turns = append(turns, *open)
			}
			t := Turn{User: msg}
			open = &t
		case "ASSISTANT":
			if open == nil {
				// Orphan assistant without preceding user: skip (cannot form a unit).
				continue
			}
			open.Assistants = append(open.Assistants, msg)
		}
	}
	if open != nil {
		turns = append(turns, *open)
	}
	return turns, current, nil
}
