package agentaccessauth_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

const (
	fileOwnerUserID = "b18f1f2e-7b5a-7c3d-8e9f-123456789001"
	fileWorkspaceID = "b18f1f2e-7b5a-7c3d-8e9f-123456789002"
	fileModelID     = "b18f1f2e-7b5a-7c3d-8e9f-123456789003"
	fileAgentID     = "b18f1f2e-7b5a-7c3d-8e9f-123456789004"
	fileClientID    = "b18f1f2e-7b5a-7c3d-8e9f-123456789005"
	fileServiceID   = "b18f1f2e-7b5a-7c3d-8e9f-123456789006"
	fileSubjectID   = "b18f1f2e-7b5a-7c3d-8e9f-123456789007"
	fileCCFileID    = "b18f1f2e-7b5a-7c3d-8e9f-1234567890c1"
	fileSubjectFile = "b18f1f2e-7b5a-7c3d-8e9f-1234567890c2"
)

func TestResolveFileOwnershipSPAndExternalSubject(t *testing.T) {
	db := openFileOwnershipDB(t)
	store, err := agentaccessauth.NewSubjectOwnershipRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Client Credentials path: SUBJECT_OWNED, subject null → owner is SP.
	ccRecord, err := store.ResolveSubjectOwnershipRecord(ctx, agentaccessauth.ActionFileRead,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: fileCCFileID,
		})
	if err != nil {
		t.Fatalf("CC resolve: %v", err)
	}
	if ccRecord.WorkspaceID != fileWorkspaceID || ccRecord.AgentID != fileAgentID ||
		ccRecord.ActorType != "SERVICE_PRINCIPAL" || ccRecord.ActorID != fileServiceID ||
		ccRecord.ClientID != fileClientID || ccRecord.Mode != agentaccessauth.OwnershipModeSubjectOwned ||
		ccRecord.SubjectType != "" || ccRecord.SubjectID != "" || ccRecord.PolicyVersion != 7 ||
		ccRecord.ResourceType != agentaccessauth.ResourceFile || ccRecord.ResourceID != fileCCFileID {
		t.Fatalf("unexpected CC file ownership: %+v", ccRecord)
	}

	// Token Exchange path: SUBJECT_OWNED with External Subject.
	esRecord, err := store.ResolveSubjectOwnershipRecord(ctx, agentaccessauth.ActionFileContent,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: fileSubjectFile,
		})
	if err != nil {
		t.Fatalf("ES resolve: %v", err)
	}
	if esRecord.SubjectType != "EXTERNAL_SUBJECT" || esRecord.SubjectID != fileSubjectID ||
		esRecord.Mode != agentaccessauth.OwnershipModeSubjectOwned || esRecord.ActorID != fileServiceID {
		t.Fatalf("unexpected ES file ownership: %+v", esRecord)
	}

	// Missing file → resource not found ownership error.
	_, err = store.ResolveSubjectOwnershipRecord(ctx, agentaccessauth.ActionFileRead,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: uuid.NewString(),
		})
	if !errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound) {
		t.Fatalf("missing file err=%v", err)
	}
	var ownershipError *agentaccessauth.SubjectOwnershipError
	if !errors.As(err, &ownershipError) ||
		ownershipError.Reason != agentaccessauth.OwnershipReasonResourceNotFound {
		t.Fatalf("ownership error=%+v", ownershipError)
	}
}

func openFileOwnershipDB(t *testing.T) *sql.DB {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number < 6 || version.Dirty {
		t.Fatalf("migrate: %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'file.owner','Owner')`, fileOwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'file-own-ws','File Own WS','SANDBOX',$2,$2,$2)
	`, fileWorkspaceID, fileOwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES ($1,$2,'m','openai','https://models.example.test','m',$3,$3)
	`, fileModelID, fileWorkspaceID, fileOwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'agent',$3,$4,$4)
	`, fileAgentID, fileWorkspaceID, fileModelID, fileOwnerUserID); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(15 * time.Minute)
	// SP-owned (CC) file.
	if _, err := db.Exec(`
		INSERT INTO aap_files (
			id, workspace_id, agent_id, actor_type, actor_id, client_id,
			subject_type, subject_id, ownership_mode, ownership_policy_version,
			status, declared_media_type, size_bytes, staging_bucket, staging_expires_at, purpose
		) VALUES (
			$1,$2,$3,'SERVICE_PRINCIPAL',$4,$5,
			NULL,NULL,'SUBJECT_OWNED',7,
			'PENDING_UPLOAD','image/png',100,'actweave-aap-staging',$6,'GENERAL'
		)
	`, fileCCFileID, fileWorkspaceID, fileAgentID, fileServiceID, fileClientID, expires); err != nil {
		t.Fatal(err)
	}
	// External Subject file.
	if _, err := db.Exec(`
		INSERT INTO aap_files (
			id, workspace_id, agent_id, actor_type, actor_id, client_id,
			subject_type, subject_id, ownership_mode, ownership_policy_version,
			status, declared_media_type, size_bytes, staging_bucket, staging_expires_at, purpose
		) VALUES (
			$1,$2,$3,'SERVICE_PRINCIPAL',$4,$5,
			'EXTERNAL_SUBJECT',$6,'SUBJECT_OWNED',8,
			'PENDING_UPLOAD','image/png',100,'actweave-aap-staging',$7,'GENERAL'
		)
	`, fileSubjectFile, fileWorkspaceID, fileAgentID, fileServiceID, fileClientID, fileSubjectID, expires); err != nil {
		t.Fatal(err)
	}
	return db
}
