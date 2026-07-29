package secret

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	serviceSecretOwnerID     = "018f1f2e-7b5a-7c3d-8e9f-c234567890ab"
	serviceSecretWorkspaceID = "018f1f2e-7b5a-7c3d-8e9f-c234567890ac"
	serviceUnknownActorID    = "018f1f2e-7b5a-7c3d-8e9f-c234567890ad"
	serviceUnknownSpaceID    = "018f1f2e-7b5a-7c3d-8e9f-c234567890ae"
)

func TestRotateAndRevokeSecretAtomically(t *testing.T) {
	service, encryptor, db := newSecretServiceTest(t)
	ctx := context.Background()
	initialPlaintext := "initial-super-secret"
	created, err := service.Create(ctx, CreateInput{
		WorkspaceID: serviceSecretWorkspaceID,
		Name:        "Model API Key",
		Kind:        "API_KEY",
		Plaintext:   initialPlaintext,
		ActorUserID: serviceSecretOwnerID,
	})
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if !created.Configured || created.Fingerprint == "" || created.ActiveVersionNo == nil ||
		*created.ActiveVersionNo != 1 || created.LockVersion != 1 {
		t.Fatalf("unexpected created secret DTO: %+v", created)
	}
	assertReadDTOIsRedacted(t, created, initialPlaintext)
	assertStoredSecretDecrypts(t, db, encryptor, created.ID, initialPlaintext)

	rotatedPlaintext := "rotated-super-secret"
	rotated, err := service.Rotate(ctx, RotateInput{
		WorkspaceID:         serviceSecretWorkspaceID,
		SecretID:            created.ID,
		Plaintext:           rotatedPlaintext,
		ActorUserID:         serviceSecretOwnerID,
		ExpectedLockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	if !rotated.Configured || rotated.ActiveVersionNo == nil || *rotated.ActiveVersionNo != 2 ||
		rotated.LockVersion != 2 || rotated.Fingerprint == created.Fingerprint {
		t.Fatalf("unexpected rotated secret DTO: %+v", rotated)
	}
	assertReadDTOIsRedacted(t, rotated, rotatedPlaintext)
	assertStoredSecretDecrypts(t, db, encryptor, created.ID, rotatedPlaintext)
	assertSecretVersionCounts(t, db, created.ID, 2, 1)

	if _, err := service.Rotate(ctx, RotateInput{
		WorkspaceID:         serviceSecretWorkspaceID,
		SecretID:            created.ID,
		Plaintext:           "****",
		ActorUserID:         serviceSecretOwnerID,
		ExpectedLockVersion: rotated.LockVersion,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected masked placeholder rejection, got %v", err)
	}
	assertSecretVersionCounts(t, db, created.ID, 2, 1)

	revoked, err := service.Revoke(ctx, RevokeInput{
		WorkspaceID:         serviceSecretWorkspaceID,
		SecretID:            created.ID,
		ActorUserID:         serviceSecretOwnerID,
		ExpectedLockVersion: rotated.LockVersion,
	})
	if err != nil {
		t.Fatalf("revoke secret: %v", err)
	}
	if revoked.Configured || revoked.Fingerprint != "" || revoked.ActiveVersionNo != nil ||
		revoked.LockVersion != 3 {
		t.Fatalf("unexpected revoked secret DTO: %+v", revoked)
	}
	readBack, err := service.Get(ctx, serviceSecretWorkspaceID, created.ID)
	if err != nil {
		t.Fatalf("get revoked secret: %v", err)
	}
	if readBack.Configured || readBack.Fingerprint != "" || readBack.ActiveVersionNo != nil {
		t.Fatalf("revoked read DTO exposed version metadata: %+v", readBack)
	}
	if _, err := service.Revoke(ctx, RevokeInput{
		WorkspaceID:         serviceSecretWorkspaceID,
		SecretID:            created.ID,
		ActorUserID:         serviceSecretOwnerID,
		ExpectedLockVersion: revoked.LockVersion,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected repeated revoke conflict, got %v", err)
	}
	if _, err := service.Get(ctx, serviceUnknownSpaceID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected workspace-scoped secret miss, got %v", err)
	}
}

func TestRotateSecretRollsBackOnActorConstraintFailure(t *testing.T) {
	service, _, db := newSecretServiceTest(t)
	created := createServiceSecret(t, service)
	_, err := service.Rotate(context.Background(), RotateInput{
		WorkspaceID:         serviceSecretWorkspaceID,
		SecretID:            created.ID,
		Plaintext:           "must-roll-back",
		ActorUserID:         serviceUnknownActorID,
		ExpectedLockVersion: created.LockVersion,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected actor FK failure, got %v", err)
	}
	assertSecretVersionCounts(t, db, created.ID, 1, 1)
	readBack, err := service.Get(context.Background(), serviceSecretWorkspaceID, created.ID)
	if err != nil {
		t.Fatalf("get secret after rollback: %v", err)
	}
	if readBack.LockVersion != 1 || readBack.Fingerprint != created.Fingerprint {
		t.Fatalf("failed rotation changed active secret: before=%+v after=%+v", created, readBack)
	}
}

func TestRotateSecretConcurrentCompareAndSwap(t *testing.T) {
	service, _, db := newSecretServiceTest(t)
	created := createServiceSecret(t, service)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, plaintext := range []string{"concurrent-one", "concurrent-two"} {
		workers.Add(1)
		go func(plaintext string) {
			defer workers.Done()
			<-start
			_, err := service.Rotate(context.Background(), RotateInput{
				WorkspaceID:         serviceSecretWorkspaceID,
				SecretID:            created.ID,
				Plaintext:           plaintext,
				ActorUserID:         serviceSecretOwnerID,
				ExpectedLockVersion: created.LockVersion,
			})
			results <- err
		}(plaintext)
	}
	close(start)
	workers.Wait()
	close(results)

	var successes int
	var conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent rotation result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one rotation and one conflict, got success=%d conflict=%d", successes, conflicts)
	}
	assertSecretVersionCounts(t, db, created.ID, 2, 1)
}

func TestRedactRejectsMaskedSecretOnCreate(t *testing.T) {
	service, _, db := newSecretServiceTest(t)
	for _, placeholder := range []string{"****", "********", "••••"} {
		if _, err := service.Create(context.Background(), CreateInput{
			WorkspaceID: serviceSecretWorkspaceID,
			Name:        "Masked Secret",
			Kind:        "API_KEY",
			Plaintext:   placeholder,
			ActorUserID: serviceSecretOwnerID,
		}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected placeholder %q rejection, got %v", placeholder, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM secrets`).Scan(&count); err != nil {
		t.Fatalf("count rejected masked secrets: %v", err)
	}
	if count != 0 {
		t.Fatalf("masked placeholders created %d secrets", count)
	}
}

func TestActiveSecretResolutionIsWorkspaceScopedAndWipesPlaintext(t *testing.T) {
	service, _, _ := newSecretServiceTest(t)
	created, err := service.Create(context.Background(), CreateInput{
		WorkspaceID: serviceSecretWorkspaceID, Name: "Runtime Credential", Kind: "API_KEY",
		Plaintext: "runtime-only-secret", ActorUserID: serviceSecretOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var callbackBuffer []byte
	err = service.WithActiveSecret(context.Background(), serviceSecretWorkspaceID, created.ID, func(plaintext []byte) error {
		if string(plaintext) != "runtime-only-secret" {
			t.Fatalf("unexpected active plaintext inside callback: %q", plaintext)
		}
		callbackBuffer = plaintext
		return nil
	})
	if err != nil {
		t.Fatalf("resolve active secret: %v", err)
	}
	if bytes.Contains(callbackBuffer, []byte("runtime-only-secret")) {
		t.Fatalf("active plaintext buffer was not wiped after callback: %q", callbackBuffer)
	}
	if err := service.WithActiveSecret(context.Background(), serviceUnknownSpaceID, created.ID, func([]byte) error {
		return nil
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace active secret miss, got %v", err)
	}
	if _, err := service.Revoke(context.Background(), RevokeInput{
		WorkspaceID: serviceSecretWorkspaceID, SecretID: created.ID,
		ActorUserID: serviceSecretOwnerID, ExpectedLockVersion: created.LockVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.WithActiveSecret(context.Background(), serviceSecretWorkspaceID, created.ID, func([]byte) error {
		return nil
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected revoked secret to be unavailable, got %v", err)
	}
}

func newSecretServiceTest(t *testing.T) (*Service, *LocalEncryptor, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean secret service migration version 6, got %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'secret.service.owner', 'Secret Service Owner')
	`, serviceSecretOwnerID); err != nil {
		t.Fatalf("insert secret service owner: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES ($1, 'secret-service', 'Secret Service', 'PRODUCTION', $2, $2, $2)
	`, serviceSecretWorkspaceID, serviceSecretOwnerID); err != nil {
		t.Fatalf("insert secret service workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1, $2, 'OWNER', $2)
	`, serviceSecretWorkspaceID, serviceSecretOwnerID); err != nil {
		t.Fatalf("insert secret service member: %v", err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create secret repository: %v", err)
	}
	encryptor, err := NewLocalEncryptor("local-test-v1", testMasterKey)
	if err != nil {
		t.Fatalf("create secret test encryptor: %v", err)
	}
	service, err := NewService(repository, encryptor)
	if err != nil {
		t.Fatalf("create secret service: %v", err)
	}
	return service, encryptor, db
}

func createServiceSecret(t *testing.T, service *Service) ReadDTO {
	t.Helper()
	created, err := service.Create(context.Background(), CreateInput{
		WorkspaceID: serviceSecretWorkspaceID,
		Name:        "Concurrent Secret",
		Kind:        "API_KEY",
		Plaintext:   "initial-concurrent",
		ActorUserID: serviceSecretOwnerID,
	})
	if err != nil {
		t.Fatalf("create service secret: %v", err)
	}
	return created
}

func assertStoredSecretDecrypts(
	t *testing.T,
	db *sql.DB,
	encryptor *LocalEncryptor,
	secretID string,
	expected string,
) {
	t.Helper()
	var versionID string
	var protected EncryptedValue
	if err := db.QueryRow(`
		SELECT v.id, v.ciphertext, v.nonce, v.key_id
		FROM secrets AS s
		JOIN secret_versions AS v ON v.id = s.active_version_id
		WHERE s.workspace_id = $1 AND s.id = $2
	`, serviceSecretWorkspaceID, secretID).Scan(
		&versionID,
		&protected.Ciphertext,
		&protected.Nonce,
		&protected.KeyID,
	); err != nil {
		t.Fatalf("read stored active secret: %v", err)
	}
	if bytes.Contains(protected.Ciphertext, []byte(expected)) {
		t.Fatal("stored ciphertext contains plaintext")
	}
	plaintext, err := encryptor.Decrypt(
		context.Background(),
		protected,
		associatedData(serviceSecretWorkspaceID, secretID, versionID),
	)
	if err != nil {
		t.Fatalf("decrypt stored secret: %v", err)
	}
	defer wipe(plaintext)
	if string(plaintext) != expected {
		t.Fatalf("unexpected stored secret plaintext %q", plaintext)
	}
}

func assertSecretVersionCounts(
	t *testing.T,
	db *sql.DB,
	secretID string,
	total int,
	unrevoked int,
) {
	t.Helper()
	var storedTotal int
	var storedUnrevoked int
	if err := db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE revoked_at IS NULL)
		FROM secret_versions
		WHERE secret_id = $1
	`, secretID).Scan(&storedTotal, &storedUnrevoked); err != nil {
		t.Fatalf("count secret versions: %v", err)
	}
	if storedTotal != total || storedUnrevoked != unrevoked {
		t.Fatalf("unexpected secret versions total=%d unrevoked=%d", storedTotal, storedUnrevoked)
	}
}

func assertReadDTOIsRedacted(t *testing.T, dto ReadDTO, plaintext string) {
	t.Helper()
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal secret read DTO: %v", err)
	}
	for _, forbidden := range []string{plaintext, "ciphertext", "nonce", "keyId"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("secret read DTO exposed %q: %s", forbidden, encoded)
		}
	}
}
