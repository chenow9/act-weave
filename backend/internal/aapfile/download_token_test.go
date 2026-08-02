package aapfile_test

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/aapfile"

	"github.com/google/uuid"
)

func TestDownloadTokenLifecycleHardening(t *testing.T) {
	service, _, _, db := newAAPFileService(t)
	ctx := context.Background()
	file := insertReadyAAPFile(t, db)

	t.Run("purpose_mismatch_rejected", func(t *testing.T) {
		minted, err := service.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeClientContent,
			CreatedBy: testServiceID,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Wrong expected purpose conceals as not found.
		_, _, err = service.ResolveDownloadTokenForPurpose(
			ctx, minted.Token.ID, aapfile.DownloadPurposeToolInvoke,
		)
		if err != aapfile.ErrNotFound {
			t.Fatalf("purpose mismatch: err=%v want ErrNotFound", err)
		}
		// Matching purpose succeeds.
		got, _, err := service.ResolveDownloadTokenForPurpose(
			ctx, minted.Token.ID, aapfile.DownloadPurposeClientContent,
		)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != minted.Token.ID {
			t.Fatalf("token id mismatch")
		}
		// Invalid mint purpose.
		_, err = service.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: "evil_purpose", CreatedBy: testServiceID,
		})
		if err != aapfile.ErrInvalid {
			t.Fatalf("invalid purpose mint: err=%v want ErrInvalid", err)
		}
	})

	t.Run("expired_rejects", func(t *testing.T) {
		clock := time.Now().UTC()
		repo, err := aapfile.NewRepository(db)
		if err != nil {
			t.Fatal(err)
		}
		svc, err := aapfile.NewService(
			repo, newFakeStaging(), &fakeSecure{},
			aapfile.WithClock(func() time.Time { return clock }),
		)
		if err != nil {
			t.Fatal(err)
		}
		// Multi-use client_content: resolve must reject after TTL.
		multi, err := svc.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeClientContent,
			CreatedBy: testServiceID, TTL: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Single-use tool_invoke: consume also rejects after TTL.
		single, err := svc.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeToolInvoke,
			CreatedBy: testServiceID, TTL: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Advance past expiry.
		clock = clock.Add(2 * time.Minute)
		_, _, err = svc.ResolveDownloadToken(ctx, multi.Token.ID)
		if err != aapfile.ErrNotFound {
			t.Fatalf("expired resolve: err=%v want ErrNotFound", err)
		}
		_, _, err = svc.ResolveDownloadToken(ctx, single.Token.ID)
		if err != aapfile.ErrNotFound {
			t.Fatalf("expired single resolve: err=%v want ErrNotFound", err)
		}
		if err := svc.ConsumeDownloadToken(ctx, single.Token.ID); err != aapfile.ErrNotFound {
			t.Fatalf("expired consume: err=%v want ErrNotFound", err)
		}
	})

	t.Run("single_use_double_read_fails", func(t *testing.T) {
		minted, err := service.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeToolInvoke,
			CreatedBy: testServiceID, SingleUse: false, // forced true for tool_invoke
		})
		if err != nil {
			t.Fatal(err)
		}
		if !minted.Token.SingleUse {
			t.Fatal("tool_invoke must force single_use")
		}
		token, _, err := service.ResolveDownloadToken(ctx, minted.Token.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ConsumeDownloadToken(ctx, token.ID); err != nil {
			t.Fatalf("first consume: %v", err)
		}
		// Second resolve fails closed (consumed).
		_, _, err = service.ResolveDownloadToken(ctx, token.ID)
		if err != aapfile.ErrNotFound {
			t.Fatalf("second resolve: err=%v want ErrNotFound", err)
		}
		// Second consume CAS fails.
		if err := service.ConsumeDownloadToken(ctx, token.ID); err != aapfile.ErrNotFound {
			t.Fatalf("second consume: err=%v want ErrNotFound", err)
		}
	})

	t.Run("single_use_concurrent_cas", func(t *testing.T) {
		minted, err := service.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeProcessorDelivery,
			CreatedBy: testServiceID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !minted.Token.SingleUse {
			t.Fatal("processor_delivery must force single_use")
		}
		const n = 16
		var okCount atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				if err := service.ConsumeDownloadToken(ctx, minted.Token.ID); err == nil {
					okCount.Add(1)
				}
			}()
		}
		wg.Wait()
		if got := okCount.Load(); got != 1 {
			t.Fatalf("CAS winners=%d want 1", got)
		}
	})

	t.Run("ttl_capped_at_max", func(t *testing.T) {
		minted, err := service.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeClientContent,
			CreatedBy: testServiceID, TTL: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		span := minted.Token.ExpiresAt.Sub(minted.Token.CreatedAt)
		if span > aapfile.MaxDownloadTokenTTL+time.Second {
			t.Fatalf("ttl=%v exceeds max %v", span, aapfile.MaxDownloadTokenTTL)
		}
	})

	t.Run("purge_expired_tokens", func(t *testing.T) {
		// Insert a row that is already expired so purge can delete it.
		expiredID := uuid.Must(uuid.NewV7()).String()
		jti := uuid.Must(uuid.NewV7()).String()
		_, err := db.Exec(`
			INSERT INTO aap_file_download_tokens (
				id, workspace_id, file_id, purpose, jti, single_use,
				max_bytes, expires_at, created_by
			) VALUES ($1,$2,$3,'client_content',$4,false,$5,clock_timestamp() - interval '1 minute',$6)
		`, expiredID, testWorkspaceID, file.ID, jti, file.SizeBytes, testServiceID)
		if err != nil {
			t.Fatal(err)
		}
		// Live token must survive.
		live, err := service.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
			FileID: file.ID, Purpose: aapfile.DownloadPurposeClientContent,
			CreatedBy: testServiceID,
		})
		if err != nil {
			t.Fatal(err)
		}
		n, err := service.PurgeExpiredDownloadTokens(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		if n < 1 {
			t.Fatalf("purged=%d want >=1", n)
		}
		var still int
		if err := db.QueryRow(`SELECT COUNT(*) FROM aap_file_download_tokens WHERE id=$1`, expiredID).
			Scan(&still); err != nil {
			t.Fatal(err)
		}
		if still != 0 {
			t.Fatal("expired token row still present after purge")
		}
		_, _, err = service.ResolveDownloadToken(ctx, live.Token.ID)
		if err != nil {
			t.Fatalf("live token purged incorrectly: %v", err)
		}
		// Second purge is safe (idle or only other expired rows).
		if _, err := service.PurgeExpiredDownloadTokens(ctx, 100); err != nil {
			t.Fatal(err)
		}
	})
}

func insertReadyAAPFile(t *testing.T, db *sql.DB) aapfile.File {
	t.Helper()
	fileID := uuid.Must(uuid.NewV7()).String()
	objID := uuid.Must(uuid.NewV7()).String()
	size := int64(len(pngBytes))
	_, err := db.Exec(`
		INSERT INTO aap_files (
			id, workspace_id, agent_id, actor_type, actor_id, client_id,
			ownership_mode, ownership_policy_version, status,
			filename, declared_media_type, size_bytes, sha256,
			staging_bucket, staging_expires_at, stored_object_id, purpose,
			processing_version, ready_at
		) VALUES (
			$1,$2,$3,'SERVICE_PRINCIPAL',$4,$5,
			'SUBJECT_OWNED',7,'READY',
			'pixel.png','image/png',$6,NULL,
			'aap-files-staging',clock_timestamp() + interval '1 hour',$7,'GENERAL',
			1,clock_timestamp()
		)
	`, fileID, testWorkspaceID, testAgentID, testServiceID, testClientID, size, objID)
	if err != nil {
		t.Fatalf("insert READY file: %v", err)
	}
	return aapfile.File{
		ID: fileID, WorkspaceID: testWorkspaceID, AgentID: testAgentID,
		Status: aapfile.StatusReady, SizeBytes: size, StoredObjectID: &objID,
		DeclaredMediaType: "image/png",
	}
}
