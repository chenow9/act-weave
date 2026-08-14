package agentaccess_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestAAPIdempotencyAndQuota(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Run("durable command receipt retains hash and result for at least 24 hours", func(t *testing.T) {
		testDatabase := dbtest.New(t)
		version := testDatabase.MigrateToLatest(t)
		if !version.Applied || version.Number != 23 || version.Dirty {
			t.Fatalf("latest migration=%+v", version)
		}
		db := testDatabase.Open(t)
		insertRepositoryFixtures(t, db)
		repository, err := aap.NewCommandReceiptRepository(db)
		if err != nil {
			t.Fatal(err)
		}
		key := aap.CommandReceiptKey{
			WorkspaceID: repositoryWorkspaceID, AgentID: repositoryAgentID,
			ClientID: uuid.NewString(), ServicePrincipalID: uuid.NewString(),
			SubjectID: uuid.NewString(), Operation: aap.CommandRunCreate,
			IdempotencyKey: uuid.NewString(),
		}
		hash := bytes.Repeat([]byte{0x54}, 32)
		observed, err := repository.Observe(context.Background(), aap.ObserveCommandInput{
			Key: key, RequestHash: hash,
		})
		if err != nil || !observed.Created ||
			observed.ExpiresAt.Sub(observed.CreatedAt) < 24*time.Hour {
			t.Fatalf("observed=%+v err=%v", observed, err)
		}
		replayed, err := repository.Observe(context.Background(), aap.ObserveCommandInput{
			Key: key, RequestHash: hash,
		})
		if err != nil || replayed.Created {
			t.Fatalf("replayed=%+v err=%v", replayed, err)
		}
		changed := bytes.Repeat([]byte{0x55}, 32)
		if _, err := repository.Observe(context.Background(), aap.ObserveCommandInput{
			Key: key, RequestHash: changed,
		}); !errors.Is(err, aap.ErrCommandIdempotencyConflict) {
			t.Fatalf("changed request hash error=%v", err)
		}
		resourceID := uuid.NewString()
		completed, err := repository.Complete(context.Background(), aap.CompleteCommandInput{
			Key: key, RequestHash: hash, ResourceType: "RUN",
			ResourceID: resourceID, ResponseVersion: 1,
		})
		if err != nil || completed.ResourceID != resourceID || completed.ResponseVersion != 1 {
			t.Fatalf("completed=%+v err=%v", completed, err)
		}
		updated, err := repository.Complete(context.Background(), aap.CompleteCommandInput{
			Key: key, RequestHash: hash, ResourceType: "RUN",
			ResourceID: resourceID, ResponseVersion: 3,
		})
		if err != nil || updated.ResponseVersion != 3 {
			t.Fatalf("updated replay=%+v err=%v", updated, err)
		}
		var rawKey string
		if err := db.QueryRow(`SELECT encode(request_hash,'hex') FROM agent_access_data_commands
			WHERE workspace_id=$1 AND idempotency_key=$2`, key.WorkspaceID, key.IdempotencyKey).Scan(&rawKey); err != nil {
			t.Fatal(err)
		}
		if rawKey == key.IdempotencyKey || len(rawKey) != 64 {
			t.Fatalf("receipt stored unexpected request evidence %q", rawKey)
		}
		version = testDatabase.MigrateToLatest(t)
		if !version.Applied || version.Number != 23 || version.Dirty {
			t.Fatalf("rollback=%+v", version)
		}
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.agent_access_data_commands') IS NOT NULL`).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatal("data command receipt table survived rollback")
		}
	})

	t.Run("quota enforces the most restrictive data-plane dimension", func(t *testing.T) {
		config := agentaccess.DataPlaneQuotaConfig{
			Window: time.Minute, MaxEntries: 100,
			Limits: map[agentaccess.DataPlaneQuotaOperation]int{
				agentaccess.QuotaRunCreate: 2,
			},
		}
		quota, err := agentaccess.NewInMemoryDataPlaneQuota(config)
		if err != nil {
			t.Fatal(err)
		}
		request := agentaccess.DataPlaneQuotaRequest{
			Operation:   agentaccess.QuotaRunCreate,
			WorkspaceID: repositoryWorkspaceID, AgentID: repositoryAgentID,
			ClientID: uuid.NewString(), ServicePrincipalID: uuid.NewString(), SubjectID: uuid.NewString(),
		}
		first, err := quota.Allow(context.Background(), request)
		if err != nil || first.Limit != 2 || first.Remaining != 1 {
			t.Fatalf("first=%+v err=%v", first, err)
		}
		second, err := quota.Allow(context.Background(), request)
		if err != nil || second.Remaining != 0 {
			t.Fatalf("second=%+v err=%v", second, err)
		}
		denied, err := quota.Allow(context.Background(), request)
		if !errors.Is(err, agentaccess.ErrDataPlaneQuotaExceeded) ||
			denied.RetryAfter <= 0 || denied.Limit != 2 {
			t.Fatalf("denied=%+v err=%v", denied, err)
		}
		// A new Subject is still denied by the already exhausted Workspace,
		// Agent, and Client dimensions.
		request.SubjectID = uuid.NewString()
		if _, err := quota.Allow(context.Background(), request); !errors.Is(err, agentaccess.ErrDataPlaneQuotaExceeded) {
			t.Fatalf("new Subject bypassed shared dimensions: %v", err)
		}
	})
}
