package execution_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/storedobject"
)

func TestPermanentToolPayloadInvocationRecorder(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 61 || version.Dirty {
		t.Fatalf("permanent tool payload migration = %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	repository, _ := execution.NewToolInvocationRepository(db)
	secure := newRecorderSecureFake(db)
	payloads, _ := storedobject.NewSensitivePayloadWriter(secure,
		storedobject.NewJSONSecretScrubber("literal-connection-secret"))
	recorder, _ := execution.NewPermanentInvocationRecorder(repository, payloads)

	tests := []struct {
		id        string
		status    string
		errorCode string
	}{
		{id: "f28f1f2e-7b5a-7c3d-8e9f-123456789001", status: "SUCCEEDED"},
		{id: "f28f1f2e-7b5a-7c3d-8e9f-123456789002", status: "FAILED", errorCode: "UPSTREAM_UNAVAILABLE"},
	}
	for _, test := range tests {
		record := execution.InvocationRecord{
			InvocationID: test.id, WorkspaceID: executionWorkspaceID,
			CapabilityID: invocationToolID, ReleaseID: invocationReleaseID,
			ToolVersionID: invocationVersionID, ProviderID: invocationProviderID,
			ConnectionID: invocationConnectionID, ActorType: "USER", ActorID: executionOwnerID,
			TraceID: "trace-permanent-" + test.status, IdempotencyKey: "permanent-" + test.status,
			Status: "RUNNING", InputSummary: json.RawMessage(`{"keys":["orderId"],"byteSize":65}`),
			Input:         json.RawMessage(`{"orderId":"A-10293","password":"literal-connection-secret"}`),
			RetentionMode: execution.InvocationRetentionMode,
		}
		if err := recorder.InvocationStarted(context.Background(), record); err != nil {
			t.Fatalf("start %s invocation: %v", test.status, err)
		}
		record.Status, record.ErrorCode = test.status, test.errorCode
		record.OutputSummary = json.RawMessage(`{"httpStatus":200,"byteSize":55}`)
		record.Output = json.RawMessage(`{"status":"ok","access_token":"upstream-secret"}`)
		if err := recorder.InvocationFinished(context.Background(), record); err != nil {
			t.Fatalf("finish %s invocation: %v", test.status, err)
		}
		stored, err := repository.Get(context.Background(), executionWorkspaceID, test.id)
		if err != nil || stored.Status != test.status || stored.RawObjectID != test.id ||
			strings.Contains(string(stored.InputSummary), "A-10293") ||
			strings.Contains(string(stored.OutputSummary), "upstream-secret") {
			t.Fatalf("invocation evidence mismatch: %+v err=%v", stored, err)
		}
		payload := secure.content(test.id)
		if !bytes.Contains(payload, []byte("A-10293")) ||
			bytes.Contains(payload, []byte("literal-connection-secret")) ||
			bytes.Contains(payload, []byte("upstream-secret")) {
			t.Fatalf("scrubbed permanent %s payload mismatch: %s", test.status, payload)
		}
	}

}

type recorderSecureFake struct {
	mu       sync.Mutex
	db       *sql.DB
	contents map[string][]byte
	metadata map[string]storedobject.StoredObject
}

func newRecorderSecureFake(db *sql.DB) *recorderSecureFake {
	return &recorderSecureFake{db: db, contents: map[string][]byte{}, metadata: map[string]storedobject.StoredObject{}}
}

func (store *recorderSecureFake) Put(_ context.Context, input storedobject.PutInput) (storedobject.StoredObject, error) {
	payload, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.contents[input.ID]; exists {
		return storedobject.StoredObject{}, storedobject.ErrConflict
	}
	digest := sha256.Sum256(payload)
	metadata := storedobject.StoredObject{
		ID: input.ID, WorkspaceID: input.WorkspaceID, Bucket: storedobject.BucketExecutions,
		ObjectKey: input.WorkspaceID + "/tool-invocation/" + input.ID, Kind: input.Kind,
		ContentType: input.ContentType, SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
		EncryptionKeyID: "recorder-test-key-v1", Classification: input.Classification,
		RetentionMode: input.RetentionMode, CreatedByType: input.CreatedByType, CreatedByID: input.CreatedByID,
	}
	if _, err := store.db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, metadata.ID, metadata.WorkspaceID, metadata.Bucket, metadata.ObjectKey,
		metadata.Kind, metadata.ContentType, metadata.SizeBytes, metadata.SHA256,
		metadata.EncryptionKeyID, metadata.Classification, metadata.RetentionMode,
		metadata.CreatedByType, metadata.CreatedByID); err != nil {
		return storedobject.StoredObject{}, err
	}
	store.contents[input.ID] = bytes.Clone(payload)
	store.metadata[input.ID] = metadata
	return metadata, nil
}

func (store *recorderSecureFake) Open(_ context.Context, request storedobject.ReadRequest) (storedobject.OpenedObject, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	metadata, exists := store.metadata[request.ObjectID]
	if !exists || metadata.WorkspaceID != request.WorkspaceID {
		return storedobject.OpenedObject{}, storedobject.ErrNotFound
	}
	return storedobject.OpenedObject{
		Metadata: metadata, Body: io.NopCloser(bytes.NewReader(store.contents[request.ObjectID])),
	}, nil
}

func (store *recorderSecureFake) content(objectID string) []byte {
	store.mu.Lock()
	defer store.mu.Unlock()
	return bytes.Clone(store.contents[objectID])
}
