package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"actweave/backend/internal/storedobject"
)

func TestPermanentContentChatCarrierThreshold(t *testing.T) {
	secure := &chatPermanentFake{}
	content, err := NewStoredMessageContent(secure)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{content: content, inlineMax: 8}
	inline, objectID, length, err := service.prepareMessageContent(context.Background(),
		"548f1f2e-7b5a-7c3d-8e9f-123456789001", "548f1f2e-7b5a-7c3d-8e9f-123456789002",
		"small", "USER", "548f1f2e-7b5a-7c3d-8e9f-123456789003")
	if err != nil || inline != "small" || objectID != "" || length != 5 {
		t.Fatalf("inline carrier: inline=%q object=%q length=%d err=%v", inline, objectID, length, err)
	}
	messageID := "548f1f2e-7b5a-7c3d-8e9f-123456789004"
	inline, objectID, length, err = service.prepareMessageContent(context.Background(),
		messageID, "548f1f2e-7b5a-7c3d-8e9f-123456789002", "oversized message",
		"USER", "548f1f2e-7b5a-7c3d-8e9f-123456789003")
	if err != nil || inline != "" || objectID != messageID || length != 17 {
		t.Fatalf("object carrier: inline=%q object=%q length=%d err=%v", inline, objectID, length, err)
	}
	if secure.input.Kind != storedobject.KindChatMessage ||
		secure.input.RetentionMode != storedobject.RetentionPermanent ||
		secure.input.Classification != storedobject.ClassificationSensitive {
		t.Fatalf("chat object policy mismatch: %+v", secure.input)
	}
}

type chatPermanentFake struct {
	input   storedobject.PutInput
	content []byte
}

func (store *chatPermanentFake) Put(_ context.Context, input storedobject.PutInput) (storedobject.StoredObject, error) {
	content, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	store.input, store.content = input, bytes.Clone(content)
	digest := sha256.Sum256(content)
	return storedobject.StoredObject{
		ID: input.ID, WorkspaceID: input.WorkspaceID, Kind: input.Kind,
		SHA256: hex.EncodeToString(digest[:]), RetentionMode: input.RetentionMode,
		Classification: input.Classification,
	}, nil
}

func (store *chatPermanentFake) Open(_ context.Context, request storedobject.ReadRequest) (storedobject.OpenedObject, error) {
	return storedobject.OpenedObject{
		Metadata: storedobject.StoredObject{ID: request.ObjectID, WorkspaceID: request.WorkspaceID, Kind: storedobject.KindChatMessage},
		Body:     io.NopCloser(bytes.NewReader(store.content)),
	}, nil
}
