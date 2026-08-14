package agentaccess_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestManagementCommandRepository(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean latest schema (migration 6), got %+v", version)
	}
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := uuid.NewString()
	hash := bytes.Repeat([]byte{0x45}, 32)
	input := agentaccess.ClaimManagementCommandInput{
		WorkspaceID: repositoryWorkspaceID, ActorID: repositoryOwnerID,
		IdempotencyKey: key, Operation: "create-client", RequestHash: hash,
	}

	claimed, created, err := repository.ClaimManagementCommand(ctx, input)
	if err != nil || !created || claimed.State != agentaccess.ManagementCommandPending ||
		!bytes.Equal(claimed.RequestHash, hash) {
		t.Fatalf("claim=%+v created=%v err=%v", claimed, created, err)
	}
	replay, created, err := repository.ClaimManagementCommand(ctx, input)
	if err != nil || created || replay.State != agentaccess.ManagementCommandPending {
		t.Fatalf("pending replay=%+v created=%v err=%v", replay, created, err)
	}

	publicResponse := json.RawMessage(`{"client":{"id":"public-client"},"credential":{"publicHint":"…safe"}}`)
	completed, err := repository.CompleteManagementCommand(
		ctx, input.WorkspaceID, input.ActorID, input.IdempotencyKey,
		hash, 201, publicResponse,
	)
	if err != nil || completed.State != agentaccess.ManagementCommandCompleted ||
		completed.ResponseStatus != 201 || completed.CompletedAt == nil ||
		!json.Valid(completed.ResponseBody) {
		t.Fatalf("complete=%+v err=%v", completed, err)
	}
	if bytes.Contains(completed.ResponseBody, []byte("secret")) {
		t.Fatalf("persisted replay body contains secret field: %s", completed.ResponseBody)
	}
	loaded, created, err := repository.ClaimManagementCommand(ctx, input)
	if err != nil || created || loaded.State != agentaccess.ManagementCommandCompleted ||
		!bytes.Equal(loaded.ResponseBody, completed.ResponseBody) {
		t.Fatalf("completed replay=%+v created=%v err=%v", loaded, created, err)
	}
	if _, err := repository.CompleteManagementCommand(
		ctx, input.WorkspaceID, input.ActorID, input.IdempotencyKey,
		hash, 201, publicResponse,
	); !errors.Is(err, agentaccess.ErrRepositoryConflict) {
		t.Fatalf("second completion error=%v want conflict", err)
	}

}
