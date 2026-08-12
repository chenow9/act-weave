package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/sessioncontext"
)

// countingSessions tracks ListMessages vs reverse-page usage for D-01.
type countingSessions struct {
	messages        []chat.Message
	listAllCalls    atomic.Int64
	reverseCalls    atomic.Int64
	getMessageCalls atomic.Int64
}

func (s *countingSessions) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}

func (s *countingSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	s.listAllCalls.Add(1)
	return append([]chat.Message(nil), s.messages...), nil
}

func (s *countingSessions) ListMessagesReversePage(
	_ context.Context, _, _ string, limit int, cursor *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	s.reverseCalls.Add(1)
	msgs := append([]chat.Message(nil), s.messages...)
	// newest first
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	start := 0
	if cursor != nil {
		for i, m := range msgs {
			if m.ID == cursor.ID {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	hasMore := end < len(msgs)
	if end > len(msgs) {
		end = len(msgs)
	}
	if start > len(msgs) {
		start = len(msgs)
	}
	page := chat.MessagePage{Messages: msgs[start:end], HasMore: hasMore}
	if hasMore && len(page.Messages) > 0 {
		last := page.Messages[len(page.Messages)-1]
		page.NextCursor = &chat.MessagePageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (s *countingSessions) GetMessage(_ context.Context, _, messageID string) (chat.Message, error) {
	s.getMessageCalls.Add(1)
	for _, m := range s.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return chat.Message{}, chat.ErrNotFound
}

type countingContent struct {
	reads atomic.Int64
	// body by object id
	bodies map[string]string
}

func (c *countingContent) ReadPermanentChat(_ context.Context, _, objectID, _ string) (string, error) {
	c.reads.Add(1)
	if body, ok := c.bodies[objectID]; ok {
		return body, nil
	}
	return "decrypted-" + objectID, nil
}

// TestLoadBoundedHistoryStopsDecryptAfterBudget proves D-01:
// token_window assembly must not ListMessages(full) and must not decrypt every
// permanent body in a long session once MaxRecentTurns is satisfied.
func TestLoadBoundedHistoryStopsDecryptAfterBudget(t *testing.T) {
	const (
		ws      = "ws-1"
		session = "sess-1"
		// Small page size so reverse pagination is exercised across many pages.
		// historyPageSize is package const 50; with 40 messages and maxRecentTurns=2
		// we stop well before the second page would be needed if page were 50 —
		// force smaller effective paging via many messages and assert reverse
		// is used and full list is not.
	)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 20 prior turns (USER+ASSISTANT) + current USER = 41 messages, all object-backed.
	const priorTurns = 20
	messages := make([]chat.Message, 0, priorTurns*2+1)
	bodies := map[string]string{}
	for i := 0; i < priorTurns; i++ {
		uID := fmt.Sprintf("u-%02d", i)
		aID := fmt.Sprintf("a-%02d", i)
		uObj := "obj-" + uID
		aObj := "obj-" + aID
		bodies[uObj] = fmt.Sprintf("user turn %d content enough for tokens", i)
		bodies[aObj] = fmt.Sprintf("assistant turn %d content enough for tokens", i)
		messages = append(messages,
			chat.Message{
				ID: uID, WorkspaceID: ws, SessionID: session, Role: "USER",
				ContentObjectID: uObj, ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Status: "RECEIVED", CreatedAt: base.Add(time.Duration(i*2) * time.Minute),
			},
			chat.Message{
				ID: aID, WorkspaceID: ws, SessionID: session, Role: "ASSISTANT",
				ContentObjectID: aObj, ContentSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Status: "EXECUTED", CreatedAt: base.Add(time.Duration(i*2+1) * time.Minute),
			},
		)
	}
	currentID := "u-current"
	curObj := "obj-current"
	bodies[curObj] = "current user message for this run"
	messages = append(messages, chat.Message{
		ID: currentID, WorkspaceID: ws, SessionID: session, Role: "USER",
		ContentObjectID: curObj, ContentSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Status: "RECEIVED", CreatedAt: base.Add(time.Duration(priorTurns*2+1) * time.Minute),
	})

	sessions := &countingSessions{messages: messages}
	content := &countingContent{bodies: bodies}
	b := &Bridge{sessions: sessions, content: content}

	policy := sessioncontext.ResolvedSnapshot{
		SchemaVersion:            sessioncontext.SnapshotSchemaV1,
		Mode:                     sessioncontext.ModeTokenWindow,
		ModelContextWindowTokens: 128000,
		EffectiveMaxInputTokens:  100000,
		OutputReserveTokens:      4096,
		SafetyMarginTokens:       512,
		MaxRecentTurns:           2, // stop after 2 prior turns
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
	}
	job := agentrun.Job{
		WorkspaceID:   ws,
		SessionID:     session,
		UserMessageID: currentID,
		ActorID:       "actor-1",
		RunID:         "run-1",
	}

	// Override page size effect: with historyPageSize=50, all 41 fit one page.
	// Decrypt-stop is still proven: only recent messages decrypt, not all 41.
	current, turns, err := b.loadBoundedHistoryForAssembly(context.Background(), job, "system instruction", policy, nil)
	if err != nil {
		t.Fatalf("loadBoundedHistoryForAssembly: %v", err)
	}
	if current.ID != currentID {
		t.Fatalf("current id: %s", current.ID)
	}
	if len(turns) < 2 {
		t.Fatalf("expected at least 2 prior turns loaded for selection, got %d", len(turns))
	}
	// Full ListMessages must never be used on the token_window path.
	if n := sessions.listAllCalls.Load(); n != 0 {
		t.Fatalf("ListMessages called %d times; expected 0 on token_window path", n)
	}
	if n := sessions.reverseCalls.Load(); n < 1 {
		t.Fatalf("ListMessagesReversePage not called")
	}
	if n := sessions.getMessageCalls.Load(); n < 1 {
		t.Fatalf("GetMessage not called for current user")
	}
	// Decrypt count: current + messages for ~2 turns (user+assistant each) + at most
	// a small overshoot while detecting the turn cap. Must be far below full history.
	const maxAllowedDecrypts = 12 // current + ~2 turns + margin
	decrypts := content.reads.Load()
	if decrypts > maxAllowedDecrypts {
		t.Fatalf("ReadPermanentChat called %d times; want <= %d (not full history of %d object-backed msgs)",
			decrypts, maxAllowedDecrypts, len(messages))
	}
	if decrypts < 3 {
		t.Fatalf("expected some decrypts for current+recent turns, got %d", decrypts)
	}
	t.Logf("D-01 evidence: reversePages=%d listAll=%d decrypts=%d priorTurnsLoaded=%d",
		sessions.reverseCalls.Load(), sessions.listAllCalls.Load(), decrypts, len(turns))
}

// TestBuildMessagesTokenWindowUsesBoundedHistory wires the public assembly entry
// and asserts the same no-full-list property end-to-end.
func TestBuildMessagesTokenWindowUsesBoundedHistory(t *testing.T) {
	const ws, session = "ws-1", "sess-1"
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	var messages []chat.Message
	// 15 short prior user-only turns + current (turns form with user-only).
	for i := 0; i < 15; i++ {
		messages = append(messages, chat.Message{
			ID: fmt.Sprintf("hist-%02d", i), WorkspaceID: ws, SessionID: session,
			Role: "USER", Content: fmt.Sprintf("history %d", i),
			ContentSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			Status:        "RECEIVED", CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	currentID := "hist-current"
	messages = append(messages, chat.Message{
		ID: currentID, WorkspaceID: ws, SessionID: session, Role: "USER",
		Content: "current", ContentSHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Status: "RECEIVED", CreatedAt: base.Add(20 * time.Minute),
	})

	sessions := &countingSessions{messages: messages}
	b := &Bridge{sessions: sessions}

	snap, _ := json.Marshal(sessioncontext.ResolvedSnapshot{
		SchemaVersion:            sessioncontext.SnapshotSchemaV1,
		Mode:                     sessioncontext.ModeTokenWindow,
		ModelContextWindowTokens: 8000,
		EffectiveMaxInputTokens:  6000,
		OutputReserveTokens:      500,
		SafetyMarginTokens:       100,
		MaxRecentTurns:           3,
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
	})
	run := execution.AgentRun{
		ID:                    "run-x",
		WorkspaceID:           ws,
		SessionID:             session,
		SnapshotSchemaVersion: execution.RunSnapshotSchemaV2,
		ContextPolicySnapshot: snap,
		ModelSnapshot:         json.RawMessage(`{}`),
		CapabilitySnapshot:    json.RawMessage(`{}`),
		AgentSnapshot:         json.RawMessage(`{}`),
	}
	policy, err := sessioncontext.ParseResolvedSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	job := agentrun.Job{WorkspaceID: ws, SessionID: session, UserMessageID: currentID, RunID: run.ID}

	msgs, err := b.buildMessagesTokenWindow(context.Background(), job, run, "sys", policy, nil)
	if err != nil {
		t.Fatalf("buildMessagesTokenWindow: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected system+user at least, got %d", len(msgs))
	}
	if n := sessions.listAllCalls.Load(); n != 0 {
		t.Fatalf("ListMessages used on token_window path: %d", n)
	}
	if n := sessions.reverseCalls.Load(); n < 1 {
		t.Fatalf("expected reverse page calls")
	}
}
