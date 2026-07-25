package storedobject_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/storedobject"

	"github.com/google/uuid"
)

const (
	artifactModelID   = "d18f1f2e-7b5a-7c3d-8e9f-123456789020"
	artifactAgentID   = "d18f1f2e-7b5a-7c3d-8e9f-123456789021"
	artifactSessionID = "d18f1f2e-7b5a-7c3d-8e9f-123456789022"
	artifactRunID     = "d18f1f2e-7b5a-7c3d-8e9f-123456789023"
	artifactObjectID  = "d18f1f2e-7b5a-7c3d-8e9f-123456789024"
)

var errArtifactReadDenied = errors.New("artifact read denied")

type artifactReadAuthorizer struct {
	calls int
	last  storedobject.ReadAuthorization
	err   error
}

func (authorizer *artifactReadAuthorizer) AuthorizeStoredObjectRead(
	_ context.Context,
	request storedobject.ReadAuthorization,
) error {
	authorizer.calls++
	authorizer.last = request
	return authorizer.err
}

func TestAuxiliaryProtocolItems(t *testing.T) {
	repository, db := newStoredObjectRepository(t)
	insertArtifactProtocolRun(t, db)
	input := permanentStoredObjectInput(artifactObjectID, "prompt/run-output.json")
	input.Kind = storedobject.KindPromptRunOutput
	input.ContentType = "application/json; charset=utf-8"
	object, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	authorizer := &artifactReadAuthorizer{}
	mapper, err := storedobject.NewArtifactProtocolMapper(authorizer)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := storedobject.NewArtifactProtocolProjector(unit, mapper)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	items, err := protocolevent.NewRunItemRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	protocolContext := storedobject.ArtifactProtocolContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: storedObjectWorkspaceID, AgentID: artifactAgentID,
			ConversationID: artifactSessionID, RunID: artifactRunID,
		},
		EventStreamID: artifactRunID, TraceID: "trace-artifact-protocol",
	}

	result, err := projector.ProjectCompleted(context.Background(), storedobject.ProjectArtifactInput{
		Context: protocolContext, Object: object,
		ActorType: "user", ActorID: storedObjectOwnerID, Ordinal: 1,
	})
	if err != nil || len(result.Events) != 1 ||
		result.Projection.SourceType != protocolevent.SourceStoredObject ||
		result.Projection.SourceID != object.ID ||
		result.Projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("artifact projection=%+v err=%v", result, err)
	}
	if authorizer.calls != 1 || authorizer.last.WorkspaceID != object.WorkspaceID ||
		authorizer.last.ObjectID != object.ID || authorizer.last.ActorType != "USER" ||
		authorizer.last.ActorID != storedObjectOwnerID ||
		authorizer.last.Classification != object.Classification || authorizer.last.Kind != object.Kind {
		t.Fatalf("artifact authorization boundary=%+v calls=%d", authorizer.last, authorizer.calls)
	}
	serialized := strings.ToLower(string(result.Events[0].Payload))
	for _, forbidden := range []string{
		strings.ToLower(object.Bucket), strings.ToLower(object.ObjectKey),
		strings.ToLower(object.Classification), strings.ToLower(object.EncryptionKeyID),
		strings.ToLower(object.SHA256), "signedurl", "retentionmode",
	} {
		if forbidden != "" && strings.Contains(serialized, forbidden) {
			t.Fatalf("artifact event leaked %q: %s", forbidden, result.Events[0].Payload)
		}
	}
	decoded, err := result.Events[0].DecodeData()
	if err != nil {
		t.Fatal(err)
	}
	item := decoded.(protocolevent.ItemSnapshotData).Item.(protocolevent.ArtifactItem)
	if item.ID != object.ID || item.ArtifactID != object.ID || item.MediaType != "application/json" ||
		item.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("public artifact item=%+v", item)
	}
	projection, err := items.Get(
		context.Background(), storedObjectWorkspaceID, artifactAgentID, artifactRunID, object.ID,
	)
	if err != nil || projection.Ordinal != 1 || projection.SourceType != protocolevent.SourceStoredObject {
		t.Fatalf("stored artifact item=%+v err=%v", projection, err)
	}

	before, err := reader.HighWatermark(context.Background(), protocolContext.Scope)
	if err != nil || before != 1 {
		t.Fatalf("artifact high watermark=%d err=%v", before, err)
	}
	deniedAuthorizer := &artifactReadAuthorizer{err: errArtifactReadDenied}
	deniedMapper, err := storedobject.NewArtifactProtocolMapper(deniedAuthorizer)
	if err != nil {
		t.Fatal(err)
	}
	deniedProjector, err := storedobject.NewArtifactProtocolProjector(unit, deniedMapper)
	if err != nil {
		t.Fatal(err)
	}
	deniedObject := object
	deniedObject.ID = uuid.NewString()
	if _, err := deniedProjector.ProjectCompleted(context.Background(), storedobject.ProjectArtifactInput{
		Context: protocolContext, Object: deniedObject,
		ActorType: "USER", ActorID: storedObjectOwnerID, Ordinal: 2,
	}); !errors.Is(err, errArtifactReadDenied) {
		t.Fatalf("denied artifact error=%v", err)
	}
	if deniedAuthorizer.calls != 1 || deniedAuthorizer.last.ObjectID != deniedObject.ID {
		t.Fatalf("denied artifact did not authorize independently: %+v", deniedAuthorizer)
	}

	unsupportedKind := object
	unsupportedKind.ID = uuid.NewString()
	unsupportedKind.Kind = storedobject.KindChatMessage
	if _, err := mapper.MapCompleted(
		context.Background(), unsupportedKind, "USER", storedObjectOwnerID,
	); !errors.Is(err, storedobject.ErrArtifactKindUnsupported) {
		t.Fatalf("unknown stored object kind error=%v", err)
	}
	unsupportedMedia := object
	unsupportedMedia.ID = uuid.NewString()
	unsupportedMedia.ContentType = "application/x-actweave-private"
	if _, err := mapper.MapCompleted(
		context.Background(), unsupportedMedia, "USER", storedObjectOwnerID,
	); !errors.Is(err, storedobject.ErrArtifactKindUnsupported) {
		t.Fatalf("unsupported artifact media error=%v", err)
	}
	expired := object
	expired.ID = uuid.NewString()
	expired.RetentionMode = storedobject.RetentionExpiring
	expiredAt := time.Now().UTC().Add(-time.Minute)
	expired.RetentionUntil = &expiredAt
	if _, err := mapper.MapCompleted(
		context.Background(), expired, "USER", storedObjectOwnerID,
	); !errors.Is(err, storedobject.ErrArtifactProtocolInvalid) {
		t.Fatalf("expired artifact error=%v", err)
	}
	wrongWorkspace := protocolContext
	wrongWorkspace.Scope.WorkspaceID = storedObjectOtherWorkspaceID
	if _, err := projector.ProjectCompleted(context.Background(), storedobject.ProjectArtifactInput{
		Context: wrongWorkspace, Object: object,
		ActorType: "USER", ActorID: storedObjectOwnerID, Ordinal: 2,
	}); !errors.Is(err, storedobject.ErrArtifactProtocolInvalid) {
		t.Fatalf("cross-workspace artifact error=%v", err)
	}
	after, err := reader.HighWatermark(context.Background(), protocolContext.Scope)
	if err != nil || after != before {
		t.Fatalf("rejected artifact changed stream %d -> %d err=%v", before, after, err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("unsupported artifact types reached authorizer calls=%d", authorizer.calls)
	}
}

func insertArtifactProtocolRun(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		 VALUES($1,$2,'Artifact Model','openai','https://models.example.test','artifact',$3,$3)`,
			[]any{artifactModelID, storedObjectWorkspaceID, storedObjectOwnerID}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		 VALUES($1,$2,'Artifact Agent',$3,$4,$4)`,
			[]any{artifactAgentID, storedObjectWorkspaceID, artifactModelID, storedObjectOwnerID}},
		{`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		 VALUES($1,$2,$3,'Artifact protocol',$4)`,
			[]any{artifactSessionID, storedObjectWorkspaceID, artifactAgentID, storedObjectOwnerID}},
		{`INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		 ) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-artifact-protocol','{}','{}')`,
			[]any{artifactRunID, storedObjectWorkspaceID, artifactSessionID, artifactAgentID, storedObjectOwnerID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert artifact protocol fixture: %v", err)
		}
	}
}
