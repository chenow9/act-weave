package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestVerifyModelConfigCallsUpstreamWithoutDatabaseTransaction(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Verify Model")
	service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, config Config) error {
		probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		_, probeErr := db.ExecContext(probeCtx, `
			UPDATE model_configs SET updated_at = updated_at
			WHERE workspace_id = $1 AND id = $2
		`, config.WorkspaceID, config.ID)
		return probeErr
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("verify model config: %v", err)
	}
	if verified.Status != StatusVerified || verified.LastVerifiedAt == nil ||
		verified.LastLatencyMS == nil || verified.LastErrorCode != nil || verified.LockVersion != created.LockVersion+1 {
		t.Fatalf("unexpected verified model config: %+v", verified)
	}
}

func TestVerifyModelConfigPersistsStableRedactedErrors(t *testing.T) {
	tests := []struct {
		name  string
		error func(context.Context) error
		code  string
	}{
		{name: "authentication", error: func(context.Context) error {
			return fmt.Errorf("Authorization Bearer raw-model-secret: %w", ErrUpstreamAuthentication)
		}, code: ErrorCodeAuthentication},
		{name: "network", error: func(context.Context) error {
			return testNetworkError{message: "dial included raw-model-secret"}
		}, code: ErrorCodeNetwork},
		{name: "timeout", error: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, code: ErrorCodeVerificationTimeout},
		{name: "upstream", error: func(context.Context) error {
			return errors.New("raw-model-secret in response body")
		}, code: ErrorCodeUpstream},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, db := newModelConfigRepositoryTest(t, nil)
			created := createRepositoryConfig(t, repository, repositoryConfigID, "Verify Error Model")
			service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, _ Config) error {
				return test.error(ctx)
			}), 5*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
			if err != nil {
				t.Fatalf("verify model config failure result: %v", err)
			}
			if verified.Status != StatusError || verified.LastErrorCode == nil || *verified.LastErrorCode != test.code {
				t.Fatalf("unexpected verification error state: %+v", verified)
			}
			var storedCode string
			if err := db.QueryRow(`SELECT last_error_code FROM model_configs WHERE id=$1`, created.ID).Scan(&storedCode); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(storedCode, "raw-model-secret") || storedCode != test.code {
				t.Fatalf("unsafe or unstable stored error code %q", storedCode)
			}
		})
	}
}

func TestVerifyModelConfigRejectsStaleResult(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Verify Stale Model")
	service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, config Config) error {
		_, updateErr := repository.Update(ctx, config.WorkspaceID, config.ID, UpdateConfig{
			Name: "Changed During Verification", Provider: config.Provider, APIBase: config.APIBase,
			ModelName: config.ModelName, CredentialSecretID: config.CredentialSecretID,
			Options: config.Options, Status: StatusUnverified, UpdatedBy: repositoryOwnerID,
			ExpectedLockVersion: config.LockVersion,
		})
		return updateErr
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale verification conflict, got %v", err)
	}
}

type testNetworkError struct{ message string }

func (e testNetworkError) Error() string { return e.message }
func (testNetworkError) Timeout() bool   { return false }
func (testNetworkError) Temporary() bool { return true }
