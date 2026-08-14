package chatruntimebridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

const outboundAttachSurface = `{"components":[{"id":"root","component":"Text","text":"Hello"}]}`

func TestAttachOutboundFilesEmptyUnchanged(t *testing.T) {
	got, attached := attachOutboundFiles("hello", nil)
	if got != "hello" || attached != nil {
		t.Fatalf("got=%q attached=%v", got, attached)
	}
}

func TestAttachOutboundFilesEmptyTextCSV(t *testing.T) {
	fileID := "019f0000-0000-7000-8000-00000000f001"
	files := []protocolevent.OutputFileContentPart{{
		Type: protocolevent.ContentPartTypeOutputFile, FileID: fileID,
		MediaType: "text/csv", Filename: "invoice.csv", SizeBytes: 12,
	}}
	got, attached := attachOutboundFiles("", files)
	if strings.TrimSpace(got) == "" {
		t.Fatal("file-only envelope must be non-empty")
	}
	if len(attached) != 1 || attached[0].FileID != fileID {
		t.Fatalf("attached=%+v", attached)
	}
	parts, err := chat.ParseMessageContentParts(got)
	if err != nil {
		t.Fatal(err)
	}
	var hasFile bool
	for _, part := range parts {
		if file, ok := part.(protocolevent.OutputFileContentPart); ok {
			hasFile = true
			if file.FileID != fileID || strings.Contains(got, "url") {
				t.Fatalf("part=%+v body=%s", file, got)
			}
		}
	}
	if !hasFile {
		t.Fatalf("missing output_file in %s", got)
	}
	if err := preflightAssistantItem(uuid.NewString(), got); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	mapped, err := chat.NewProtocolMessageMapper(nil).MapCompleted(context.Background(), chat.Message{
		ID: uuid.NewString(), WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
		RunID: uuid.NewString(), Role: "ASSISTANT", Content: got,
		ContentSHA256: sha256Hex(got), ContentLength: int64(len(got)),
		Status: "EXECUTED", CreatedAt: time.Now().UTC(),
	}, "")
	if err != nil {
		t.Fatalf("MapCompleted: %v", err)
	}
	if err := protocolevent.ValidateProjectionItem(
		mapped, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("projection: %v", err)
	}
}

func TestAttachOutboundFilesMergesA2UI(t *testing.T) {
	durable, err := a2ui.SerializeAssistantDurable("lead", &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV1, CatalogID: a2ui.CatalogID,
		Surface: json.RawMessage(outboundAttachSurface),
	})
	if err != nil {
		t.Fatal(err)
	}
	files := []protocolevent.OutputFileContentPart{{
		Type:      protocolevent.ContentPartTypeOutputFile,
		FileID:    "019f0000-0000-7000-8000-00000000f002",
		MediaType: "text/plain", Filename: "note.txt", SizeBytes: 4,
	}}
	got, attached := attachOutboundFiles(durable, files)
	if len(attached) != 1 {
		t.Fatalf("attached=%v", attached)
	}
	if chat.JoinTextPartsFromDurable(got) != "lead" {
		t.Fatalf("text=%q", chat.JoinTextPartsFromDurable(got))
	}
	if len(chat.A2UISurfacesFromDurable(got, a2ui.EnvelopeVersionV1)) != 1 {
		t.Fatalf("a2ui missing: %s", got)
	}
	if len(chat.OutputFilesFromDurable(got)) != 1 {
		t.Fatalf("files missing: %s", got)
	}
}

func TestAttachOutboundFilesDegradesOnInvalidFile(t *testing.T) {
	files := []protocolevent.OutputFileContentPart{{
		Type: protocolevent.ContentPartTypeOutputFile, FileID: "not-a-uuid",
		MediaType: "text/csv", Filename: "bad.csv", SizeBytes: 1,
	}}
	got, attached := attachOutboundFiles("keep-me", files)
	if got != "keep-me" || attached != nil {
		t.Fatalf("got=%q attached=%v", got, attached)
	}
}
