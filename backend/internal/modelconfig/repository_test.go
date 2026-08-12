package modelconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	repositoryOwnerID      = "018f1f2e-7b5a-7c3d-8e9f-e234567890ab"
	repositoryWorkspaceID  = "018f1f2e-7b5a-7c3d-8e9f-e234567890ac"
	repositoryOtherSpaceID = "018f1f2e-7b5a-7c3d-8e9f-e234567890ad"
	repositorySecretID     = "018f1f2e-7b5a-7c3d-8e9f-e234567890ae"
	repositorySecretVerID  = "018f1f2e-7b5a-7c3d-8e9f-e234567890af"
	repositoryConfigID     = "018f1f2e-7b5a-7c3d-8e9f-e234567890b0"
	repositorySecondID     = "018f1f2e-7b5a-7c3d-8e9f-e234567890b1"
)

func TestModelConfigRepositoryCRUDAndOptimisticLock(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Primary Model")
	if created.Status != StatusUnverified || created.LockVersion != 1 || !created.CredentialConfigured {
		t.Fatalf("unexpected created model config: %+v", created)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	for _, forbidden := range []string{"ciphertext", "nonce", "super-secret-value"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("model config exposed %q: %s", forbidden, encoded)
		}
	}

	readBack, err := repository.Get(context.Background(), repositoryWorkspaceID, created.ID)
	if err != nil {
		t.Fatalf("get model config: %v", err)
	}
	if readBack.ID != created.ID || !readBack.CredentialConfigured {
		t.Fatalf("unexpected read model config: %+v", readBack)
	}
	if _, err := repository.Get(context.Background(), repositoryOtherSpaceID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace config miss, got %v", err)
	}

	updated, err := repository.Update(context.Background(), repositoryWorkspaceID, created.ID, UpdateConfig{
		Name:                "Primary Model Updated",
		Provider:            "OPENAI_COMPATIBLE",
		APIBase:             "https://updated.example/v1",
		ModelName:           "gpt-updated",
		CredentialSecretID:  stringPointer(repositorySecretID),
		Options:             json.RawMessage(`{"temperature":0.2}`),
		Status:              StatusDisabled,
		UpdatedBy:           repositoryOwnerID,
		ExpectedLockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("update model config: %v", err)
	}
	if updated.LockVersion != 2 || updated.Status != StatusDisabled || updated.Name != "Primary Model Updated" {
		t.Fatalf("unexpected updated model config: %+v", updated)
	}
	if _, err := repository.Update(context.Background(), repositoryWorkspaceID, created.ID, UpdateConfig{
		Name:                "Stale",
		Provider:            "OPENAI_COMPATIBLE",
		APIBase:             "https://stale.example/v1",
		ModelName:           "stale",
		UpdatedBy:           repositoryOwnerID,
		ExpectedLockVersion: created.LockVersion,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale model config conflict, got %v", err)
	}
	listed, err := repository.List(context.Background(), repositoryWorkspaceID)
	if err != nil {
		t.Fatalf("list model configs: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("unexpected model config list: %+v", listed)
	}
	var storedSecretID string
	if err := db.QueryRow(`SELECT credential_secret_id FROM model_configs WHERE id = $1`, created.ID).Scan(&storedSecretID); err != nil {
		t.Fatalf("read model config credential FK: %v", err)
	}
	if storedSecretID != repositorySecretID {
		t.Fatalf("unexpected model config secret FK %q", storedSecretID)
	}
}

func TestModelConfigDeleteProtectsWorkspaceAndAgentUsage(t *testing.T) {
	checkerInUse := true
	checker := UsageCheckerFunc(func(context.Context, *sql.Tx, string, string) (bool, error) {
		return checkerInUse, nil
	})
	repository, db := newModelConfigRepositoryTest(t, checker)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Protected Model")
	if _, err := db.Exec(`
		UPDATE workspaces SET default_model_config_id = $2 WHERE id = $1
	`, repositoryWorkspaceID, created.ID); err != nil {
		t.Fatalf("set default model config: %v", err)
	}
	if err := repository.SoftDelete(context.Background(), repositoryWorkspaceID, created.ID, repositoryOwnerID, 1); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected workspace usage protection, got %v", err)
	}
	if _, err := db.Exec(`
		UPDATE workspaces SET default_model_config_id = NULL WHERE id = $1
	`, repositoryWorkspaceID); err != nil {
		t.Fatalf("clear default model config: %v", err)
	}
	if err := repository.SoftDelete(context.Background(), repositoryWorkspaceID, created.ID, repositoryOwnerID, 1); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected agent usage protection, got %v", err)
	}
	checkerInUse = false
	if err := repository.SoftDelete(context.Background(), repositoryWorkspaceID, created.ID, repositoryOwnerID, 1); err != nil {
		t.Fatalf("soft delete unused model config: %v", err)
	}
	if _, err := repository.Get(context.Background(), repositoryWorkspaceID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted model config to be hidden, got %v", err)
	}
	listed, err := repository.List(context.Background(), repositoryWorkspaceID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("deleted model config remained in list: configs=%+v err=%v", listed, err)
	}
	if _, err := repository.Create(context.Background(), validNewConfig(repositorySecondID, "PROTECTED MODEL")); err != nil {
		t.Fatalf("reuse soft-deleted model config name: %v", err)
	}
}

func TestModelConfigConcurrentUpdatesOnlyOneWins(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Concurrent Model")
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"Concurrent One", "Concurrent Two"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			<-start
			_, err := repository.Update(context.Background(), repositoryWorkspaceID, created.ID, UpdateConfig{
				Name:                name,
				Provider:            "OPENAI_COMPATIBLE",
				APIBase:             "https://models.example/v1",
				ModelName:           "gpt-concurrent",
				CredentialSecretID:  stringPointer(repositorySecretID),
				Status:              StatusUnverified,
				UpdatedBy:           repositoryOwnerID,
				ExpectedLockVersion: created.LockVersion,
			})
			results <- err
		}(name)
	}
	close(start)
	workers.Wait()
	close(results)
	var success int
	var conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected concurrent update result: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("expected one update and one conflict, got success=%d conflict=%d", success, conflict)
	}
}

func TestConfigurationSecurityAcceptanceRejectsSecretsInModelOptions(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	for index, options := range []json.RawMessage{
		json.RawMessage(`{"apiKey":"raw-model-secret"}`),
		json.RawMessage(`{"nested":{"refresh_token":"raw-model-secret"}}`),
		json.RawMessage(`{"headers":{"Authorization":"Bearer raw-model-secret"}}`),
	} {
		input := validNewConfig(repositoryConfigID, "Unsafe Model")
		input.ID = fmt.Sprintf("018f1f2e-7b5a-7c3d-8e9f-e23456789%03d", index+2)
		input.Options = options
		if _, err := repository.Create(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected sensitive model options rejection for %s, got %v", options, err)
		}
	}
}

func newModelConfigRepositoryTest(t *testing.T, checker UsageChecker) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected clean model config repository migration version 20, got %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'model.repository.owner', 'Model Repository Owner')
	`, repositoryOwnerID); err != nil {
		t.Fatalf("insert model repository owner: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES
			($1, 'model-repository', 'Model Repository', 'PRODUCTION', $3, $3, $3),
			($2, 'model-repository-other', 'Model Repository Other', 'SANDBOX', $3, $3, $3)
	`, repositoryWorkspaceID, repositoryOtherSpaceID, repositoryOwnerID); err != nil {
		t.Fatalf("insert model repository workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO secrets (id, workspace_id, name, kind, created_by, updated_by)
		VALUES ($1, $2, 'Model Repository Secret', 'API_KEY', $3, $3)
	`, repositorySecretID, repositoryWorkspaceID, repositoryOwnerID); err != nil {
		t.Fatalf("insert model repository secret: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, 1, $4, $5, 'local-test-v1', 'hmac-sha256:test', $6)
	`, repositorySecretVerID, repositoryWorkspaceID, repositorySecretID, []byte("ciphertext"), []byte("nonce"), repositoryOwnerID); err != nil {
		t.Fatalf("insert model repository secret version: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE secrets SET active_version_id = $2 WHERE id = $1
	`, repositorySecretID, repositorySecretVerID); err != nil {
		t.Fatalf("activate model repository secret: %v", err)
	}
	var repository *Repository
	var err error
	if checker == nil {
		repository, err = NewRepository(db)
	} else {
		repository, err = NewRepository(db, checker)
	}
	if err != nil {
		t.Fatalf("create model config repository: %v", err)
	}
	return repository, db
}

func createRepositoryConfig(t *testing.T, repository *Repository, id string, name string) Config {
	t.Helper()
	created, err := repository.Create(context.Background(), validNewConfig(id, name))
	if err != nil {
		t.Fatalf("create repository model config: %v", err)
	}
	return created
}

func validNewConfig(id string, name string) NewConfig {
	return NewConfig{
		ID:                 id,
		WorkspaceID:        repositoryWorkspaceID,
		Name:               name,
		Provider:           "OPENAI_COMPATIBLE",
		APIBase:            "https://models.example/v1",
		ModelName:          "gpt-test",
		CredentialSecretID: stringPointer(repositorySecretID),
		Options:            json.RawMessage(`{"temperature":0}`),
		CreatedBy:          repositoryOwnerID,
	}
}

func stringPointer(value string) *string {
	return &value
}
