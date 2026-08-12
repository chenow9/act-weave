package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2ui"
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
	// An older surface version has no renderer, so it must not reach Console at all.
	if len(dto.A2UI) != 0 {
		t.Fatalf("a2ui=%v want nothing for %q", dto.A2UI, "a2ui-surface.v0")
	}
}

// The surface travels in its own field so that Content stays the text a human
// reads and its hash keeps describing exactly that.
func TestMessageDTOExposesCurrentSurfaceBesideText(t *testing.T) {
	t.Parallel()

	surface := `{"surfaceId":"srf_1","catalogId":"` + a2ui.CatalogID + `","components":[` +
		`{"id":"root","component":"Chart","chartType":"bar","series":[{"points":[{"label":"Q1","value":1}]}]}` +
		`]}`
	durable := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"季度营收如下。"},` +
		`{"type":"a2ui","version":"` + a2ui.EnvelopeVersionV1 + `","catalogId":"` + a2ui.CatalogID +
		`","surface":` + surface + `}` +
		`]}`

	routes := &ChatExecutionRoutes{}
	message := chat.Message{
		ID: uuid.NewString(), WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
		Role: "ASSISTANT", Content: durable, ContentSHA256: sha256Hex(durable),
		ContentLength: int64(len([]byte(durable))), Status: "EXECUTED",
		RunID: uuid.NewString(), CreatedAt: time.Now().UTC(),
	}
	dto, err := routes.messageDTO(context.Background(), message, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	if dto.Content != "季度营收如下。" {
		t.Fatalf("content=%q", dto.Content)
	}
	if dto.ContentSHA256 != sha256Hex(dto.Content) ||
		dto.ContentLength != int64(len([]byte(dto.Content))) {
		t.Fatalf("hash/length describe something other than content: %s %d",
			dto.ContentSHA256, dto.ContentLength)
	}
	if len(dto.A2UI) != 1 {
		t.Fatalf("a2ui=%v want exactly one surface", dto.A2UI)
	}

	// The channel carries the surface alone: no envelope, no content-part wrapper.
	var got map[string]any
	if err := json.Unmarshal(dto.A2UI[0], &got); err != nil {
		t.Fatalf("surface is not an object: %v", err)
	}
	if got["catalogId"] != a2ui.CatalogID || got["surfaceId"] != "srf_1" {
		t.Fatalf("identity lost: %+v", got)
	}
	if _, nested := got["parts"]; nested {
		t.Fatalf("envelope leaked into the surface: %+v", got)
	}
	for _, key := range []string{"type", "version", "schemaVersion"} {
		if _, present := got[key]; present {
			t.Fatalf("content-part wrapper key %q leaked into the surface", key)
		}
	}

	// The body a client parses as markdown still holds no surface JSON.
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dto.Content, "components") || strings.Contains(dto.Content, "chartType") {
		t.Fatalf("surface leaked into content: %q", dto.Content)
	}
	if !strings.Contains(string(body), `"a2ui":[`) {
		t.Fatalf("a2ui channel missing from the response body: %s", body)
	}
}

// Absent rather than null or empty: a message without a surface must look exactly
// as it did before the channel existed.
func TestMessageDTOOmitsA2UIWithoutASurface(t *testing.T) {
	t.Parallel()

	routes := &ChatExecutionRoutes{}
	for name, content := range map[string]string{
		"plain text": "Just text.",
		"text-only envelope": `{"schemaVersion":"aap.message-content.v1","parts":[` +
			`{"type":"text","text":"Just text."}]}`,
		"older surface version": `{"schemaVersion":"aap.message-content.v1","parts":[` +
			`{"type":"text","text":"Just text."},` +
			`{"type":"a2ui","version":"a2ui-surface.v0","surface":{"root":"form"}}]}`,
		"a2ui part without a version": `{"schemaVersion":"aap.message-content.v1","parts":[` +
			`{"type":"text","text":"Just text."},` +
			`{"type":"a2ui","surface":{"components":[]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			message := chat.Message{
				ID: uuid.NewString(), WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
				Role: "ASSISTANT", Content: content, ContentSHA256: sha256Hex(content),
				ContentLength: int64(len([]byte(content))), Status: "EXECUTED",
				CreatedAt: time.Now().UTC(),
			}
			dto, err := routes.messageDTO(context.Background(), message, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if len(dto.A2UI) != 0 {
				t.Fatalf("a2ui=%v want nothing", dto.A2UI)
			}
			body, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), `"a2ui"`) {
				t.Fatalf("a2ui key present without a surface: %s", body)
			}
		})
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
