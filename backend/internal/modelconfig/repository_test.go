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
	"time"

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

func TestCreateRejectsNonEmptyDisclosurePolicy(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	input := validNewConfig(repositoryConfigID, "Policy Create")
	raw, err := CanonicalToolDisclosurePolicy(DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	input.ToolDisclosurePolicy = raw
	if _, err := repository.Create(context.Background(), input); !errors.Is(err, ErrToolDisclosureInvalid) {
		t.Fatalf("expected ErrToolDisclosureInvalid, got %v", err)
	}
}

func TestGenericUpdateClearsDisclosurePolicy(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Clear Policy")
	verified := verifyRepositoryConfig(t, repository, created)
	plantFunctionCallingCaps(t, db, verified)
	policy, err := CanonicalToolDisclosurePolicy(DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE model_configs SET tool_disclosure_policy = $2::jsonb WHERE id = $1`, verified.ID, []byte(policy)); err != nil {
		t.Fatal(err)
	}
	got, err := repository.Get(context.Background(), repositoryWorkspaceID, verified.ID)
	if err != nil {
		t.Fatal(err)
	}
	if IsUnsetToolDisclosurePolicy(got.ToolDisclosurePolicy) {
		t.Fatal("planted policy missing")
	}
	updated, err := repository.Update(context.Background(), repositoryWorkspaceID, got.ID, UpdateConfig{
		Name: got.Name, Provider: got.Provider, APIBase: got.APIBase, ModelName: got.ModelName,
		CredentialSecretID: got.CredentialSecretID, Options: got.Options,
		RuntimeCapabilities: got.RuntimeCapabilities, Status: StatusUnverified,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: got.LockVersion,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !IsUnsetToolDisclosurePolicy(updated.ToolDisclosurePolicy) {
		t.Fatalf("generic update must force policy {{}}, got %s", updated.ToolDisclosurePolicy)
	}
	if !IsUnverifiedAgenticCapabilities(updated.AgenticCapabilities) || updated.Status != StatusUnverified {
		t.Fatalf("generic update must clear caps: %+v", updated)
	}
}

func TestUpdateDisclosurePolicyCAS(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Disclosure CAS")
	verified := verifyRepositoryConfig(t, repository, created)
	lastVerified := *verified.LastVerifiedAt
	lastLatency := *verified.LastLatencyMS
	plantFunctionCallingCaps(t, db, verified)
	fc, err := repository.Get(context.Background(), repositoryWorkspaceID, verified.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeCaps, _, err := ParseAgenticCapabilities(fc.AgenticCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := CanonicalToolDisclosurePolicy(DisclosureModePlatformOnDemand)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.UpdateDisclosurePolicy(context.Background(), DisclosurePolicyUpdate{
		WorkspaceID: repositoryWorkspaceID, ConfigID: fc.ID, Policy: policy,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: fc.LockVersion,
	})
	if err != nil {
		t.Fatalf("update disclosure: %v", err)
	}
	if updated.LockVersion != fc.LockVersion+1 {
		t.Fatalf("lock must bump once, got %d want %d", updated.LockVersion, fc.LockVersion+1)
	}
	if updated.Status != StatusVerified {
		t.Fatalf("status changed: %s", updated.Status)
	}
	if updated.LastErrorCode != nil || updated.LastLatencyMS == nil || *updated.LastLatencyMS != lastLatency {
		t.Fatalf("evidence mutated: %+v", updated)
	}
	if updated.LastVerifiedAt == nil || !updated.LastVerifiedAt.Equal(lastVerified) {
		t.Fatalf("last_verified_at mutated: %v vs %v", updated.LastVerifiedAt, lastVerified)
	}
	gotPolicy, _, err := ParseToolDisclosurePolicy(updated.ToolDisclosurePolicy)
	if err != nil || gotPolicy.Mode != DisclosureModePlatformOnDemand {
		t.Fatalf("policy: %+v err=%v", gotPolicy, err)
	}
	afterCaps, _, err := ParseAgenticCapabilities(updated.AgenticCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	if afterCaps.ToolCalling != ToolCallingFunctionCalling {
		t.Fatalf("caps toolCalling changed: %+v", afterCaps)
	}
	if afterCaps.VerifiedConfigDigest != beforeCaps.VerifiedConfigDigest ||
		!afterCaps.VerifiedAt.Equal(beforeCaps.VerifiedAt) {
		t.Fatalf("caps identity changed: %+v", afterCaps)
	}
	if afterCaps.VerifiedLockVersion != fc.LockVersion {
		t.Fatalf("verifiedLockVersion restamp: got %d want %d", afterCaps.VerifiedLockVersion, fc.LockVersion)
	}

	if _, err := repository.UpdateDisclosurePolicy(context.Background(), DisclosurePolicyUpdate{
		WorkspaceID: repositoryWorkspaceID, ConfigID: fc.ID, Policy: policy,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: fc.LockVersion,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale lock want conflict, got %v", err)
	}

	native := verifyRepositoryConfig(t, repository, createRepositoryConfig(t, repository, repositorySecondID, "Native Disclosure"))
	if _, err := repository.UpdateDisclosurePolicy(context.Background(), DisclosurePolicyUpdate{
		WorkspaceID: repositoryWorkspaceID, ConfigID: native.ID, Policy: policy,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: native.LockVersion,
	}); !errors.Is(err, ErrToolDisclosureInvalid) {
		t.Fatalf("native want disclosure invalid, got %v", err)
	}
	unverified := createRepositoryConfig(t, repository, "018f1f2e-7b5a-7c3d-8e9f-e234567890b2", "Unverified Disclosure")
	if _, err := repository.UpdateDisclosurePolicy(context.Background(), DisclosurePolicyUpdate{
		WorkspaceID: repositoryWorkspaceID, ConfigID: unverified.ID, Policy: policy,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: unverified.LockVersion,
	}); !errors.Is(err, ErrToolDisclosureInvalid) {
		t.Fatalf("unverified want disclosure invalid, got %v", err)
	}
}

func TestListAgentsByModelConfig(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "List Agents")
	keepA := "018f1f2e-7b5a-7c3d-8e9f-e234567890c1"
	keepB := "018f1f2e-7b5a-7c3d-8e9f-e234567890c2"
	deleted := "018f1f2e-7b5a-7c3d-8e9f-e234567890c3"
	for _, row := range []struct {
		id, name string
	}{
		{keepA, "Agent A"},
		{keepB, "Agent B"},
		{deleted, "Agent Deleted"},
	} {
		if _, err := db.Exec(`
			INSERT INTO agents (id, workspace_id, name, model_config_id, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $5)
		`, row.id, repositoryWorkspaceID, row.name, created.ID, repositoryOwnerID); err != nil {
			t.Fatalf("insert agent %s: %v", row.name, err)
		}
	}
	if _, err := db.Exec(`UPDATE agents SET deleted_at = clock_timestamp() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}
	ids, err := repository.ListAgentsByModelConfig(context.Background(), repositoryWorkspaceID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != keepA || ids[1] != keepB {
		t.Fatalf("ids=%v", ids)
	}
}

func verifyRepositoryConfig(t *testing.T, repository *Repository, created Config) Config {
	t.Helper()
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return verified
}

func plantFunctionCallingCaps(t *testing.T, db *sql.DB, verified Config) {
	t.Helper()
	doc, _, err := ParseAgenticCapabilities(verified.AgenticCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	v2 := AgenticCapabilities{
		SchemaVersion:        AgenticCapabilitiesSchemaV2,
		Protocol:             AgenticProtocolOpenAIResponsesV1,
		Streaming:            true,
		Usage:                true,
		ToolCalling:          ToolCallingFunctionCalling,
		ReasoningReplay:      AgenticReasoningReplayEncryptedOrNone,
		VerifiedAdapter:      VerifiedAdapterAgenticOpenAIV022,
		VerifiedAt:           doc.VerifiedAt,
		VerifiedLockVersion:  doc.VerifiedLockVersion,
		VerifiedConfigDigest: doc.VerifiedConfigDigest,
	}
	raw, err := json.Marshal(v2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE model_configs SET agentic_capabilities = $2::jsonb WHERE id = $1`, verified.ID, raw); err != nil {
		t.Fatal(err)
	}
}

func newModelConfigRepositoryTest(t *testing.T, checker UsageChecker) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("expected clean model config repository migration version 22, got %+v", version)
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
