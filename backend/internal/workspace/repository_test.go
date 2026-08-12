package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	repositoryOwnerID      = "018f1f2e-7b5a-7c3d-8e9f-8234567890ab"
	repositoryWorkspaceID  = "018f1f2e-7b5a-7c3d-8e9f-8234567890ac"
	repositorySecondID     = "018f1f2e-7b5a-7c3d-8e9f-8234567890ad"
	repositoryDefaultAgent = "018f1f2e-7b5a-7c3d-8e9f-8234567890ae"
	repositoryDefaultModel = "018f1f2e-7b5a-7c3d-8e9f-8234567890af"
)

func TestCreateWorkspaceAtomicallyCreatesOwnerAndDefaults(t *testing.T) {
	db := newWorkspaceRepositoryDatabase(t)
	if _, err := db.Exec(`
		CREATE TABLE workspace_creation_probe (
			workspace_id UUID PRIMARY KEY
		)
	`); err != nil {
		t.Fatalf("create workspace hook probe: %v", err)
	}

	defaultAgentID := repositoryDefaultAgent
	defaultModelID := repositoryDefaultModel
	hook := CreationHookFunc(func(
		ctx context.Context,
		tx *sql.Tx,
		created Workspace,
	) (CreationDefaults, error) {
		var ownerCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM workspace_members
			WHERE workspace_id = $1 AND user_id = $2 AND role = 'OWNER'
		`, created.ID, created.OwnerUserID).Scan(&ownerCount); err != nil {
			return CreationDefaults{}, err
		}
		if ownerCount != 1 {
			return CreationDefaults{}, errors.New("owner member not visible to creation hook")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_creation_probe (workspace_id) VALUES ($1)
		`, created.ID); err != nil {
			return CreationDefaults{}, err
		}
		// Baseline schema defers FK checks for default agent/model until commit.
		// Create the referenced rows inside the same transaction so commit succeeds.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO model_configs(
				id, workspace_id, name, provider, api_base, model_name, created_by, updated_by
			) VALUES ($1, $2, 'Default Model', 'openai', 'https://models.example.test', 'default', $3, $3)
		`, defaultModelID, created.ID, created.OwnerUserID); err != nil {
			return CreationDefaults{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agents(
				id, workspace_id, name, model_config_id, created_by, updated_by
			) VALUES ($1, $2, 'Default Agent', $3, $4, $4)
		`, defaultAgentID, created.ID, defaultModelID, created.OwnerUserID); err != nil {
			return CreationDefaults{}, err
		}
		return CreationDefaults{
			DefaultAgentID:       &defaultAgentID,
			DefaultModelConfigID: &defaultModelID,
		}, nil
	})
	repository, err := NewRepository(db, hook)
	if err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}

	created, err := repository.Create(context.Background(), NewWorkspace{
		ID:          repositoryWorkspaceID,
		Slug:        " Operations ",
		DisplayName: " Operations Workspace ",
		Mode:        ModeSandbox,
		OwnerUserID: repositoryOwnerID,
		CreatedBy:   repositoryOwnerID,
		Settings:    json.RawMessage(`{"schemaVersion":"workspace.settings.v1"}`),
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if created.Slug != "Operations" || created.DisplayName != "Operations Workspace" ||
		created.Status != StatusActive || created.Mode != ModeSandbox || created.LockVersion != 1 {
		t.Fatalf("unexpected created workspace: %+v", created)
	}
	if created.DefaultAgentID == nil || *created.DefaultAgentID != repositoryDefaultAgent ||
		created.DefaultModelConfigID == nil || *created.DefaultModelConfigID != repositoryDefaultModel {
		t.Fatalf("workspace defaults were not persisted: %+v", created)
	}

	var memberRole Role
	var invitedBy string
	if err := db.QueryRow(`
		SELECT role, invited_by
		FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2
	`, created.ID, repositoryOwnerID).Scan(&memberRole, &invitedBy); err != nil {
		t.Fatalf("read workspace owner member: %v", err)
	}
	if memberRole != RoleOwner || invitedBy != repositoryOwnerID {
		t.Fatalf("unexpected owner member role=%q invitedBy=%q", memberRole, invitedBy)
	}
	assertRowCount(t, db, "workspace_creation_probe", "workspace_id", created.ID, 1)
	assertWorkspaceModelHasNoDerivedFacts(t)
}

func TestCreateWorkspaceRollsBackWhenOwnerMemberInsertFails(t *testing.T) {
	db := newWorkspaceRepositoryDatabase(t)
	if _, err := db.Exec(`
		CREATE FUNCTION reject_workspace_owner_member() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'owner member rejected' USING ERRCODE = '23514';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_workspace_owner_member
		BEFORE INSERT ON workspace_members
		FOR EACH ROW EXECUTE FUNCTION reject_workspace_owner_member();
	`); err != nil {
		t.Fatalf("create member rejection trigger: %v", err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}

	_, err = repository.Create(context.Background(), validNewWorkspace(repositoryWorkspaceID, "rollback-member"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected owner member failure to map to invalid, got %v", err)
	}
	assertRowCount(t, db, "workspaces", "id", repositoryWorkspaceID, 0)
	assertRowCount(t, db, "workspace_members", "workspace_id", repositoryWorkspaceID, 0)
}

func TestCreateWorkspaceRollsBackWhenCreationHookFails(t *testing.T) {
	db := newWorkspaceRepositoryDatabase(t)
	if _, err := db.Exec(`
		CREATE TABLE workspace_creation_probe (
			workspace_id UUID PRIMARY KEY
		)
	`); err != nil {
		t.Fatalf("create workspace hook probe: %v", err)
	}
	hookFailure := errors.New("default agent creation failed")
	hook := CreationHookFunc(func(
		ctx context.Context,
		tx *sql.Tx,
		created Workspace,
	) (CreationDefaults, error) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_creation_probe (workspace_id) VALUES ($1)
		`, created.ID); err != nil {
			return CreationDefaults{}, err
		}
		return CreationDefaults{}, hookFailure
	})
	repository, err := NewRepository(db, hook)
	if err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}

	_, err = repository.Create(context.Background(), validNewWorkspace(repositoryWorkspaceID, "rollback-hook"))
	if !errors.Is(err, hookFailure) {
		t.Fatalf("expected hook failure, got %v", err)
	}
	assertRowCount(t, db, "workspaces", "id", repositoryWorkspaceID, 0)
	assertRowCount(t, db, "workspace_members", "workspace_id", repositoryWorkspaceID, 0)
	assertRowCount(t, db, "workspace_creation_probe", "workspace_id", repositoryWorkspaceID, 0)
}

func TestCreateWorkspaceEnforcesCaseInsensitiveSlugUniqueness(t *testing.T) {
	db := newWorkspaceRepositoryDatabaseAtLatestMigration(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}
	if _, err := repository.Create(
		context.Background(),
		validNewWorkspace(repositoryWorkspaceID, "Operations"),
	); err != nil {
		t.Fatalf("create first workspace: %v", err)
	}
	if _, err := repository.Create(
		context.Background(),
		validNewWorkspace(repositorySecondID, "OPERATIONS"),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected case-insensitive slug conflict, got %v", err)
	}
	assertRowCount(t, db, "workspaces", "id", repositorySecondID, 0)
	assertRowCount(t, db, "workspace_members", "workspace_id", repositorySecondID, 0)
}

func TestCreateWorkspaceAllowsReusingSlugAfterSoftDelete(t *testing.T) {
	db := newWorkspaceRepositoryDatabaseAtLatestMigration(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}
	created, err := repository.Create(
		context.Background(),
		validNewWorkspace(repositoryWorkspaceID, "Operations"),
	)
	if err != nil {
		t.Fatalf("create first workspace: %v", err)
	}
	if err := repository.SoftDelete(
		context.Background(), created.ID, repositoryOwnerID, created.LockVersion,
	); err != nil {
		t.Fatalf("soft delete workspace: %v", err)
	}
	if _, err := repository.Create(
		context.Background(),
		validNewWorkspace(repositorySecondID, "OPERATIONS"),
	); err != nil {
		t.Fatalf("recreate workspace with soft-deleted slug: %v", err)
	}
	assertRowCount(t, db, "workspaces", "slug", "Operations", 2)
}

func newWorkspaceRepositoryDatabase(t *testing.T) *sql.DB {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected clean workspace repository migration version 5, got %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'workspace.repository.owner', 'Workspace Repository Owner')
	`, repositoryOwnerID); err != nil {
		t.Fatalf("insert workspace repository owner: %v", err)
	}
	return db
}

func newWorkspaceRepositoryDatabaseAtLatestMigration(t *testing.T) *sql.DB {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected clean latest workspace migration version 35, got %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'workspace.repository.owner', 'Workspace Repository Owner')
	`, repositoryOwnerID); err != nil {
		t.Fatalf("insert workspace repository owner: %v", err)
	}
	return db
}

func validNewWorkspace(id string, slug string) NewWorkspace {
	return NewWorkspace{
		ID:          id,
		Slug:        slug,
		DisplayName: "Repository Workspace",
		OwnerUserID: repositoryOwnerID,
		CreatedBy:   repositoryOwnerID,
	}
}

func assertRowCount(
	t *testing.T,
	db *sql.DB,
	table string,
	column string,
	id string,
	expected int,
) {
	t.Helper()
	query := "SELECT COUNT(*) FROM " + table + " WHERE " + column + " = $1"
	var count int
	if err := db.QueryRow(query, id).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if count != expected {
		t.Fatalf("expected %d %s rows for %s, got %d", expected, table, id, count)
	}
}

func assertWorkspaceModelHasNoDerivedFacts(t *testing.T) {
	t.Helper()
	typeOfWorkspace := reflect.TypeOf(Workspace{})
	for _, forbidden := range []string{"Health", "HealthScore", "AgentCount", "ToolCount", "WorkflowCount"} {
		if _, exists := typeOfWorkspace.FieldByName(forbidden); exists {
			t.Fatalf("Workspace persists derived fact %q", forbidden)
		}
	}
}
