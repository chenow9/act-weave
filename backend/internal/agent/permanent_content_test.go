package agent

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

	"actweave/backend/internal/execution"
	"actweave/backend/internal/storedobject"

	"github.com/google/uuid"
)

func TestPermanentContentPromptAndModelTurn(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	created, _, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Permanent Content Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceRevisionID,
		InitialPrompt: "Original prompt", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := newPermanentContentFake(db)
	promptObjects, err := NewStoredPromptObjectStore(objects)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPromptService(repository, promptObjects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"permanent-model"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "Generated permanent prompt", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, output, err := service.Run(context.Background(), serviceWorkspaceID, created.ID,
		"PREVIEW", "Permanent prompt input", "trace-permanent-content", serviceOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if run.InputLength != int64(len("Permanent prompt input")) ||
		run.InputSHA256 != promptContentHash("Permanent prompt input") ||
		run.OutputObjectID == nil || run.OutputSHA256 == nil || run.OutputLength == nil ||
		*run.OutputSHA256 != promptContentHash(output) || *run.OutputLength != int64(len(output)) {
		t.Fatalf("prompt evidence mismatch: %+v", run)
	}
	read, err := promptObjects.GetPermanent(context.Background(), serviceWorkspaceID,
		*run.OutputObjectID, serviceOwnerID)
	if err != nil || string(read) != output {
		t.Fatalf("read permanent prompt output: content=%q err=%v", read, err)
	}

	sessionID, agentRunID, stepID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'model turn',$4)
	`, sessionID, serviceWorkspaceID, created.ID, serviceOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-model-turn','{}','{}')
	`, agentRunID, serviceWorkspaceID, sessionID, created.ID, serviceOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_run_steps(id,workspace_id,run_id,sequence_no,step_type,status)
		VALUES($1,$2,$3,1,'MODEL','RUNNING')
	`, stepID, serviceWorkspaceID, agentRunID); err != nil {
		t.Fatal(err)
	}
	runs, _ := execution.NewRunRepository(db)
	turns, _ := NewModelTurnContentService(objects, runs)
	turn := []byte(`{"role":"assistant","content":"full private model turn"}`)
	step, err := turns.Record(context.Background(), RecordModelTurnInput{
		WorkspaceID: serviceWorkspaceID, StepID: stepID, Content: turn,
		CreatedByType: storedobject.CreatorUser, CreatedByID: serviceOwnerID,
		ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(turn)
	if step.RawObjectID != stepID || step.RawSHA256 != hex.EncodeToString(digest[:]) ||
		step.RawLength != int64(len(turn)) || strings.Contains(string(step.OutputSummary), "full private") {
		t.Fatalf("model turn evidence mismatch: %+v", step)
	}
}

type permanentContentFake struct {
	mu       sync.Mutex
	db       *sql.DB
	contents map[string][]byte
	metadata map[string]storedobject.StoredObject
}

func newPermanentContentFake(db *sql.DB) *permanentContentFake {
	return &permanentContentFake{db: db, contents: map[string][]byte{}, metadata: map[string]storedobject.StoredObject{}}
}

func (store *permanentContentFake) Put(_ context.Context, input storedobject.PutInput) (storedobject.StoredObject, error) {
	content, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.contents[input.ID]; exists {
		return storedobject.StoredObject{}, storedobject.ErrConflict
	}
	digest := sha256.Sum256(content)
	metadata := storedobject.StoredObject{
		ID: input.ID, WorkspaceID: input.WorkspaceID, Bucket: storedobject.BucketExecutions,
		ObjectKey: input.WorkspaceID + "/permanent-content/" + input.ID, Kind: input.Kind,
		ContentType: input.ContentType, SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:]),
		EncryptionKeyID: "permanent-content-test-key-v1", Classification: input.Classification,
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
	store.contents[input.ID] = bytes.Clone(content)
	store.metadata[input.ID] = metadata
	return metadata, nil
}

func (store *permanentContentFake) Open(_ context.Context, request storedobject.ReadRequest) (storedobject.OpenedObject, error) {
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
