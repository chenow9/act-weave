package chatruntime_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/chatruntime"
)

const (
	mmWorkspace = "d41f1f2e-7b5a-7c3d-8e9f-123456789001"
	mmAgent     = "d41f1f2e-7b5a-7c3d-8e9f-123456789002"
	mmFileID    = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f1"
	mmObjectID  = "d41f1f2e-7b5a-7c3d-8e9f-1234567890b1"
)

// tinyPNG is a minimal valid 1x1 PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

type fakeFileSource struct {
	meta  chatruntime.MultimodalFileMeta
	body  []byte
	getFn func(ctx context.Context, workspaceID, fileID string) (chatruntime.MultimodalFileMeta, error)
}

func (f *fakeFileSource) GetFile(ctx context.Context, workspaceID, fileID string) (chatruntime.MultimodalFileMeta, error) {
	if f.getFn != nil {
		return f.getFn(ctx, workspaceID, fileID)
	}
	if fileID != f.meta.ID {
		return chatruntime.MultimodalFileMeta{}, errors.New("not found")
	}
	return f.meta, nil
}

func (f *fakeFileSource) OpenFileBytes(_ context.Context, _, storedObjectID string) ([]byte, error) {
	if storedObjectID != f.meta.StoredObjectID {
		return nil, errors.New("object not found")
	}
	return append([]byte(nil), f.body...), nil
}

func v1Content(parts ...map[string]string) string {
	raw, _ := json.Marshal(map[string]any{
		"schemaVersion": chatruntime.MessageContentSchemaVersion,
		"parts":         parts,
	})
	return string(raw)
}

func TestAssembleUserMessage_LegacyPlainText(t *testing.T) {
	a := &chatruntime.MultimodalAssembler{}
	msg, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "hello world" || len(msg.UserInputMultiContent) != 0 {
		t.Fatalf("legacy text: %+v", msg)
	}
}

func TestAssembleUserMessage_V1TextOnlyExtractsText(t *testing.T) {
	a := &chatruntime.MultimodalAssembler{RuntimeMultimodal: false}
	body := v1Content(
		map[string]string{"type": "text", "text": "summarize "},
		map[string]string{"type": "text", "text": "this"},
	)
	msg, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, body)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "summarize this" {
		t.Fatalf("want joined text, got %q", msg.Content)
	}
	// Must not forward raw JSON envelope to the model.
	if strings.Contains(msg.Content, "schemaVersion") || strings.Contains(msg.Content, "input_file") {
		t.Fatalf("raw envelope leaked: %q", msg.Content)
	}
}

func TestAssembleUserMessage_ImageSuccessWithFakeSource(t *testing.T) {
	src := &fakeFileSource{
		meta: chatruntime.MultimodalFileMeta{
			ID: mmFileID, WorkspaceID: mmWorkspace, AgentID: mmAgent,
			Status: "READY", StoredObjectID: mmObjectID,
			DeclaredMediaType: "image/png", SizeBytes: int64(len(tinyPNG)),
		},
		body: tinyPNG,
	}
	a := &chatruntime.MultimodalAssembler{
		RuntimeMultimodal: true,
		Files:             src,
		MaxBytes:          1 << 20,
	}
	body := v1Content(
		map[string]string{"type": "text", "text": "What is in this image?"},
		map[string]string{"type": "input_file", "fileId": mmFileID, "mediaType": "image/png"},
	)
	msg, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, body)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != schema.User {
		t.Fatalf("role=%s", msg.Role)
	}
	if len(msg.UserInputMultiContent) != 2 {
		t.Fatalf("parts=%d want 2", len(msg.UserInputMultiContent))
	}
	if msg.UserInputMultiContent[0].Type != schema.ChatMessagePartTypeText ||
		msg.UserInputMultiContent[0].Text != "What is in this image?" {
		t.Fatalf("text part: %+v", msg.UserInputMultiContent[0])
	}
	img := msg.UserInputMultiContent[1]
	if img.Type != schema.ChatMessagePartTypeImageURL || img.Image == nil ||
		img.Image.Base64Data == nil || img.Image.MIMEType != "image/png" {
		t.Fatalf("image part: %+v", img)
	}
	decoded, err := base64.StdEncoding.DecodeString(*img.Image.Base64Data)
	if err != nil || len(decoded) != len(tinyPNG) {
		t.Fatalf("base64 round-trip: err=%v len=%d", err, len(decoded))
	}
	// Assembly must never embed HTTP download URLs.
	raw, _ := json.Marshal(msg)
	if strings.Contains(string(raw), "downloadUrl") ||
		strings.Contains(string(raw), "https://") ||
		strings.Contains(string(raw), "http://") {
		t.Fatalf("message must not contain download URLs: %s", raw)
	}
}

func TestAssembleUserMessage_PDFReturnsModelContentUnsupported(t *testing.T) {
	src := &fakeFileSource{
		meta: chatruntime.MultimodalFileMeta{
			ID: mmFileID, WorkspaceID: mmWorkspace, AgentID: mmAgent,
			Status: "READY", StoredObjectID: mmObjectID,
			DeclaredMediaType: "application/pdf", SizeBytes: 4,
		},
		body: []byte("%PDF"),
	}
	a := &chatruntime.MultimodalAssembler{RuntimeMultimodal: true, Files: src}
	body := v1Content(
		map[string]string{"type": "text", "text": "invoice"},
		map[string]string{"type": "input_file", "fileId": mmFileID, "mediaType": "application/pdf"},
	)
	_, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, body)
	if !errors.Is(err, chatruntime.ErrModelContentUnsupported) {
		t.Fatalf("want MODEL_CONTENT_UNSUPPORTED, got %v", err)
	}
	if !strings.Contains(err.Error(), chatruntime.ErrCodeModelContentUnsupported) {
		t.Fatalf("error should include stable code: %v", err)
	}
}

func TestAssembleUserMessage_InputFileWithoutRuntimeFailsClosed(t *testing.T) {
	a := &chatruntime.MultimodalAssembler{RuntimeMultimodal: false}
	body := v1Content(
		map[string]string{"type": "text", "text": "x"},
		map[string]string{"type": "input_file", "fileId": mmFileID, "mediaType": "image/png"},
	)
	_, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, body)
	if !errors.Is(err, chatruntime.ErrModelContentUnsupported) {
		t.Fatalf("must not silently drop input_file: %v", err)
	}
}

func TestAssembleUserMessage_MissingFileSourceFailsClosed(t *testing.T) {
	a := &chatruntime.MultimodalAssembler{RuntimeMultimodal: true, Files: nil}
	body := v1Content(
		map[string]string{"type": "input_file", "fileId": mmFileID, "mediaType": "image/png"},
	)
	_, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, body)
	if !errors.Is(err, chatruntime.ErrModelContentUnsupported) {
		t.Fatalf("nil Files must fail closed: %v", err)
	}
}

func TestAssembleUserMessage_NeverDropsInputFileWhenOpenFails(t *testing.T) {
	src := &fakeFileSource{
		meta: chatruntime.MultimodalFileMeta{
			ID: mmFileID, WorkspaceID: mmWorkspace, AgentID: mmAgent,
			Status: "READY", StoredObjectID: mmObjectID,
			DeclaredMediaType: "image/png", SizeBytes: 1,
		},
		body: nil,
	}
	// Force empty body after open.
	a := &chatruntime.MultimodalAssembler{RuntimeMultimodal: true, Files: src}
	body := v1Content(
		map[string]string{"type": "text", "text": "see image"},
		map[string]string{"type": "input_file", "fileId": mmFileID},
	)
	msg, err := a.AssembleUserMessage(context.Background(), mmWorkspace, mmAgent, body)
	if err == nil {
		t.Fatalf("empty body must fail, got msg=%+v", msg)
	}
	if !errors.Is(err, chatruntime.ErrModelContentUnsupported) {
		t.Fatalf("want unsupported, got %v", err)
	}
	// Ensure we did not return a text-only message that dropped the file part.
	if msg != nil && len(msg.UserInputMultiContent) == 1 {
		t.Fatal("must not return partial assembly that drops input_file")
	}
}

func TestHasInputFileInContent(t *testing.T) {
	if chatruntime.HasInputFileInContent("plain") {
		t.Fatal("plain")
	}
	if !chatruntime.HasInputFileInContent(v1Content(
		map[string]string{"type": "input_file", "fileId": mmFileID},
	)) {
		t.Fatal("v1 with file")
	}
	if chatruntime.HasInputFileInContent(v1Content(
		map[string]string{"type": "text", "text": "only"},
	)) {
		t.Fatal("text only")
	}
}
