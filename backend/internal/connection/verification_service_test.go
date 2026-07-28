package connection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

func TestVerifyConnectionCallsUpstreamWithoutDatabaseTransaction(t *testing.T) {
	repository, db := newVerificationRepositoryTest(t)
	created, err := repository.Create(context.Background(), validConnection())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, value Connection) error {
		probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		_, probeErr := db.ExecContext(probeCtx, `
			UPDATE service_connections SET updated_at = updated_at
			WHERE workspace_id=$1 AND id=$2
		`, value.WorkspaceID, value.ID)
		return probeErr
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verification, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, connOwnerID)
	if err != nil {
		t.Fatalf("verify connection: %v", err)
	}
	if verification.Status != "SUCCEEDED" || verification.LatencyMS == nil {
		t.Fatalf("unexpected verification: %+v", verification)
	}
	read, err := repository.Get(context.Background(), created.WorkspaceID, created.ID)
	if err != nil || read.Status != StatusVerified || read.LastErrorCode != nil || read.LockVersion != created.LockVersion+1 {
		t.Fatalf("unexpected verified connection: %+v err=%v", read, err)
	}
}

func TestVerifyConnectionPersistsStableRedactedErrors(t *testing.T) {
	tests := []struct {
		name     string
		error    func(context.Context) error
		code     string
		category string
	}{
		{name: "authentication", error: func(context.Context) error {
			return fmt.Errorf("Authorization Bearer raw-connection-secret: %w", ErrUpstreamAuthentication)
		}, code: ErrorCodeAuthentication, category: "AUTHENTICATION"},
		{name: "network", error: func(context.Context) error {
			return connectionNetworkError{message: "dial raw-connection-secret"}
		}, code: ErrorCodeNetwork, category: "NETWORK"},
		{name: "timeout", error: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, code: ErrorCodeVerificationTimeout, category: "TIMEOUT"},
		{name: "upstream", error: func(context.Context) error {
			return errors.New("raw-connection-secret in upstream response")
		}, code: ErrorCodeUpstream, category: "UPSTREAM"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, db := newVerificationRepositoryTest(t)
			created, err := repository.Create(context.Background(), validConnection())
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, _ Connection) error {
				return test.error(ctx)
			}), 5*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			verification, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, connOwnerID)
			if err != nil {
				t.Fatalf("verify connection failure result: %v", err)
			}
			var diagnostic map[string]string
			if err := json.Unmarshal(verification.Diagnostics, &diagnostic); err != nil {
				t.Fatal(err)
			}
			if verification.Status != "FAILED" || diagnostic["code"] != test.code || diagnostic["category"] != test.category {
				t.Fatalf("unexpected verification error: %+v diagnostics=%v", verification, diagnostic)
			}
			var storedCode string
			var storedDiagnostics string
			if err := db.QueryRow(`
				SELECT c.last_error_code, v.diagnostics::TEXT
				FROM service_connections c
				JOIN connection_verifications v ON v.connection_id=c.id
				WHERE c.id=$1
			`, created.ID).Scan(&storedCode, &storedDiagnostics); err != nil {
				t.Fatal(err)
			}
			if storedCode != test.code || strings.Contains(storedDiagnostics, "raw-connection-secret") {
				t.Fatalf("unsafe or unstable verification persistence: code=%q diagnostics=%s", storedCode, storedDiagnostics)
			}
		})
	}
}

func TestVerifyConnectionRejectsStaleResult(t *testing.T) {
	repository, db := newVerificationRepositoryTest(t)
	created, err := repository.Create(context.Background(), validConnection())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, value Connection) error {
		_, updateErr := db.ExecContext(ctx, `
			UPDATE service_connections SET lock_version=lock_version+1 WHERE id=$1
		`, value.ID)
		return updateErr
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, connOwnerID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale verification conflict, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM connection_verifications WHERE connection_id=$1`, created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale verification transaction was not rolled back: count=%d", count)
	}
}

func newVerificationRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	insertConnectionFixtures(t, db)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

type connectionNetworkError struct{ message string }

func (e connectionNetworkError) Error() string { return e.message }
func (connectionNetworkError) Timeout() bool   { return false }
func (connectionNetworkError) Temporary() bool { return true }
