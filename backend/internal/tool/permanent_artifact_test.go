package tool

import (
	"context"
	"encoding/json"
	"testing"

	"actweave/backend/internal/storedobject"
)

func TestPermanentToolPayloadTestArtifactAdapter(t *testing.T) {
	writer := &recordingToolPayloadWriter{}
	artifacts, err := NewStoredToolTestArtifacts(writer)
	if err != nil {
		t.Fatal(err)
	}
	objectID, err := artifacts.WriteToolTestArtifact(context.Background(), ToolTestArtifact{
		TestID: toolTestSuccessID, WorkspaceID: repositoryWorkspaceID,
		ToolVersionID: repositoryVersionOneID, Request: json.RawMessage(`{"orderId":"A-10293"}`),
		Response: json.RawMessage(`{"status":"ok"}`), RetentionMode: TestRetentionPermanent,
		TestedBy: repositoryOwnerID,
	})
	if err != nil || objectID != toolTestSuccessID {
		t.Fatalf("write tool test artifact: object=%s err=%v", objectID, err)
	}
	if writer.input.Kind != storedobject.KindToolTestPayload ||
		writer.input.CreatedByType != storedobject.CreatorUser ||
		writer.input.CreatedByID != repositoryOwnerID || writer.input.ObjectID != toolTestSuccessID {
		t.Fatalf("tool test payload input mismatch: %+v", writer.input)
	}
}

type recordingToolPayloadWriter struct {
	input storedobject.SensitivePayloadInput
}

func (writer *recordingToolPayloadWriter) Write(
	_ context.Context,
	input storedobject.SensitivePayloadInput,
) (storedobject.SensitivePayloadResult, error) {
	writer.input = input
	return storedobject.SensitivePayloadResult{ObjectID: input.ObjectID}, nil
}
