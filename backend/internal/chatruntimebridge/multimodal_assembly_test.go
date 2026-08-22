package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/chatruntime"
)

const (
	mmTestWS     = "d41f1f2e-7b5a-7c3d-8e9f-123456789001"
	mmTestAgent  = "d41f1f2e-7b5a-7c3d-8e9f-123456789002"
	mmTestFileID = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f1"
	mmTestObjID  = "d41f1f2e-7b5a-7c3d-8e9f-1234567890b1"
)

type bridgeFakeFiles struct {
	meta chatruntime.MultimodalFileMeta
	body []byte
}

func (f *bridgeFakeFiles) GetFile(_ context.Context, _, fileID string) (chatruntime.MultimodalFileMeta, error) {
	if fileID != f.meta.ID {
		return chatruntime.MultimodalFileMeta{}, errors.New("not found")
	}
	return f.meta, nil
}

func (f *bridgeFakeFiles) OpenFileBytes(_ context.Context, _, objectID string) ([]byte, error) {
	if objectID != f.meta.StoredObjectID {
		return nil, errors.New("missing object")
	}
	return append([]byte(nil), f.body...), nil
}

func TestAssembleUserSchemaMessage_ImageSuccess(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}
	src := &bridgeFakeFiles{
		meta: chatruntime.MultimodalFileMeta{
			ID: mmTestFileID, WorkspaceID: mmTestWS, AgentID: mmTestAgent,
			Status: "READY", StoredObjectID: mmTestObjID,
			DeclaredMediaType: "image/png", SizeBytes: int64(len(png)),
		},
		body: png,
	}
	b := &Bridge{
		multimodal: &chatruntime.MultimodalAssembler{
			RuntimeMultimodal: true, Files: src, MaxBytes: 1 << 20,
		},
	}
	body, _ := json.Marshal(map[string]any{
		"schemaVersion": chatruntime.MessageContentSchemaVersion,
		"parts": []map[string]string{
			{"type": "text", "text": "caption"},
			{"type": "input_file", "fileId": mmTestFileID, "mediaType": "image/png"},
		},
	})
	msg, err := b.assembleUserSchemaMessage(context.Background(), mmTestWS, mmTestAgent, string(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.UserInputMultiContent) != 2 {
		t.Fatalf("parts=%d", len(msg.UserInputMultiContent))
	}
	if msg.UserInputMultiContent[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("want image, got %s", msg.UserInputMultiContent[1].Type)
	}
	raw, _ := json.Marshal(msg)
	if strings.Contains(string(raw), "downloadUrl") || strings.Contains(string(raw), "https://") {
		t.Fatalf("must not embed download URLs: %s", raw)
	}
}

func TestAssembleUserSchemaMessage_PDFYieldsListing(t *testing.T) {
	src := &bridgeFakeFiles{
		meta: chatruntime.MultimodalFileMeta{
			ID: mmTestFileID, WorkspaceID: mmTestWS, AgentID: mmTestAgent,
			Status: "READY", StoredObjectID: mmTestObjID, Filename: "invoice.pdf",
			DeclaredMediaType: "application/pdf", SizeBytes: 4,
		},
		body: []byte("%PDF"),
	}
	b := &Bridge{
		multimodal: &chatruntime.MultimodalAssembler{
			RuntimeMultimodal: false, Files: src,
		},
	}
	body, _ := json.Marshal(map[string]any{
		"schemaVersion": chatruntime.MessageContentSchemaVersion,
		"parts": []map[string]string{
			{"type": "text", "text": "invoice"},
			{"type": "input_file", "fileId": mmTestFileID, "mediaType": "application/pdf"},
		},
	})
	msg, err := b.assembleUserSchemaMessage(context.Background(), mmTestWS, mmTestAgent, string(body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Content, "invoice.pdf") || !strings.Contains(msg.Content, mmTestFileID) {
		t.Fatalf("listing=%q", msg.Content)
	}
	raw, _ := json.Marshal(msg)
	if strings.Contains(string(raw), "downloadUrl") || strings.Contains(string(raw), "https://") {
		t.Fatalf("must not embed download URLs: %s", raw)
	}
}

func TestAssembleUserSchemaMessage_NilAssemblerTextOnly(t *testing.T) {
	b := &Bridge{multimodal: nil}
	body, _ := json.Marshal(map[string]any{
		"schemaVersion": chatruntime.MessageContentSchemaVersion,
		"parts":         []map[string]string{{"type": "text", "text": "hi"}},
	})
	msg, err := b.assembleUserSchemaMessage(context.Background(), mmTestWS, mmTestAgent, string(body))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "hi" {
		t.Fatalf("content=%q", msg.Content)
	}
}
