package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/chat"

	"github.com/google/uuid"
)

// TestMessageDTOProjectsTextOnlyAndRecomputesHash covers KD-13 / PR-5:
// Console session history must never return raw aap.message-content.v1 envelope
// JSON; contentSha256/contentLength describe the projected body.
func TestMessageDTOProjectsTextOnlyAndRecomputesHash(t *testing.T) {
	t.Parallel()

	routes := &ChatExecutionRoutes{}
	// Durable blob is the full multi-part envelope (hash of envelope stored server-side).
	durable := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"Natural language only."},` +
		`{"type":"a2ui","version":"a2ui-surface.v0","surface":{"root":"form","password":{"label":"Secret field"}}}` +
		`]}`
	durableHash := sha256Hex(durable)
	if durableHash == sha256Hex("Natural language only.") {
		t.Fatal("test setup: durable hash must differ from projected text hash")
	}

	message := chat.Message{
		ID: uuid.NewString(), WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
		Role: "ASSISTANT", Content: durable, ContentSHA256: durableHash,
		ContentLength: int64(len([]byte(durable))), Status: "EXECUTED",
		RunID: uuid.NewString(), CreatedAt: time.Now().UTC(),
	}
	dto, err := routes.messageDTO(context.Background(), message, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	// Text-first: natural language only; no envelope / surface JSON.
	if dto.Content != "Natural language only." {
		t.Fatalf("content=%q", dto.Content)
	}
	if strings.Contains(dto.Content, "schemaVersion") || strings.Contains(dto.Content, "a2ui") ||
		strings.Contains(dto.Content, "surface") || strings.Contains(dto.Content, "password") {
		t.Fatalf("raw envelope leaked to Console: %q", dto.Content)
	}

	// Hash/length self-consistent with projected content (not durable blob).
	wantHash := sha256Hex(dto.Content)
	if dto.ContentSHA256 != wantHash {
		t.Fatalf("contentSha256=%q want projected %q (durable was %q)",
			dto.ContentSHA256, wantHash, durableHash)
	}
	if dto.ContentLength != int64(len([]byte(dto.Content))) {
		t.Fatalf("contentLength=%d want %d", dto.ContentLength, len(dto.Content))
	}
	if dto.ContentSHA256 == durableHash {
		t.Fatal("contentSha256 must not be durable envelope hash when body is projected")
	}
	if dto.ContentLength == int64(len([]byte(durable))) {
		t.Fatal("contentLength must not be durable envelope length when body is projected")
	}
}

func TestMessageDTOPlainTextUnchanged(t *testing.T) {
	t.Parallel()

	routes := &ChatExecutionRoutes{}
	plain := "Keep this exact order request."
	message := chat.Message{
		ID: uuid.NewString(), WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
		Role: "USER", Content: plain, ContentSHA256: sha256Hex(plain),
		ContentLength: int64(len([]byte(plain))), Status: "PROCESSING",
		CreatedAt: time.Now().UTC(),
	}
	dto, err := routes.messageDTO(context.Background(), message, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if dto.Content != plain {
		t.Fatalf("plain content changed: %q", dto.Content)
	}
	if dto.ContentSHA256 != sha256Hex(plain) || dto.ContentLength != int64(len([]byte(plain))) {
		t.Fatalf("plain hash/length mismatch: sha=%s len=%d", dto.ContentSHA256, dto.ContentLength)
	}
}

func TestMessageDTOLoadsPermanentAndProjects(t *testing.T) {
	t.Parallel()

	durable := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"From object store."},` +
		`{"type":"a2ui","surface":{"root":"card"}}` +
		`]}`
	objectID := uuid.NewString()
	workspaceID := uuid.NewString()
	reader := &stubChatContentReader{byObject: map[string]string{objectID: durable}}
	routes := &ChatExecutionRoutes{content: reader}

	message := chat.Message{
		ID: objectID, WorkspaceID: workspaceID, SessionID: uuid.NewString(),
		Role: "ASSISTANT", Content: "", ContentObjectID: objectID,
		// Durable row hash still describes envelope; DTO must recompute from projection.
		ContentSHA256: sha256Hex(durable),
		ContentLength: int64(len([]byte(durable))), Status: "EXECUTED",
		CreatedAt: time.Now().UTC(),
	}
	dto, err := routes.messageDTO(context.Background(), message, "actor-1")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Content != "From object store." {
		t.Fatalf("content=%q", dto.Content)
	}
	if dto.ContentSHA256 != sha256Hex("From object store.") {
		t.Fatalf("hash=%s", dto.ContentSHA256)
	}
	if reader.calls != 1 || reader.lastObject != objectID || reader.lastActor != "actor-1" {
		t.Fatalf("reader calls=%+v", reader)
	}
}

type stubChatContentReader struct {
	byObject   map[string]string
	calls      int
	lastObject string
	lastActor  string
}

func (s *stubChatContentReader) ReadPermanentChat(
	_ context.Context, _, objectID, actorID string,
) (string, error) {
	s.calls++
	s.lastObject, s.lastActor = objectID, actorID
	content, ok := s.byObject[objectID]
	if !ok {
		return "", chat.ErrNotFound
	}
	return content, nil
}

func sha256Hex(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
