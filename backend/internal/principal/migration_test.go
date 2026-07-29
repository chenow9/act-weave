// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package principal_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/principal"
)

const (
	principalUserID       = "198f1f2e-7b5a-7c3d-8e9f-123456789001"
	principalSecondUserID = "198f1f2e-7b5a-7c3d-8e9f-123456789002"
	principalWorkspaceID  = "198f1f2e-7b5a-7c3d-8e9f-123456789003"
	principalOtherSpaceID = "198f1f2e-7b5a-7c3d-8e9f-123456789004"
	principalModelID      = "198f1f2e-7b5a-7c3d-8e9f-123456789005"
	principalOtherModelID = "198f1f2e-7b5a-7c3d-8e9f-123456789006"
	principalAgentID      = "198f1f2e-7b5a-7c3d-8e9f-123456789007"
	principalOtherAgentID = "198f1f2e-7b5a-7c3d-8e9f-123456789008"
	principalServiceID    = "198f1f2e-7b5a-7c3d-8e9f-123456789009"
	principalClientID     = "198f1f2e-7b5a-7c3d-8e9f-12345678900a"
	principalSubjectID    = "198f1f2e-7b5a-7c3d-8e9f-12345678900b"
	principalUserRunID    = "198f1f2e-7b5a-7c3d-8e9f-12345678900c"
	principalServiceRunID = "198f1f2e-7b5a-7c3d-8e9f-12345678900d"
	principalSystemID     = "198f1f2e-7b5a-7c3d-8e9f-12345678900e"
	principalSystemRunID  = "198f1f2e-7b5a-7c3d-8e9f-12345678900f"
	principalCrossRunID   = "198f1f2e-7b5a-7c3d-8e9f-123456789010"
	principalLegacyID     = "198f1f2e-7b5a-7c3d-8e9f-123456789011"
	principalLegacyRunID  = "198f1f2e-7b5a-7c3d-8e9f-123456789012"
)

func TestPrincipalMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected migration 47, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertPrincipalMigrationFixtures(t, db)

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean Principal migration 48, got %+v", version)
	}
	resolver, err := principal.NewResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, constraint := range []string{
		"agent_runs_principal_ref_fk", "workflow_executions_principal_ref_fk",
		"tool_invocations_principal_ref_fk",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)`, constraint).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("execution Principal Ref constraint %s is missing", constraint)
		}
	}
	userRef := principal.Ref{WorkspaceID: principalWorkspaceID, Type: principal.TypeUser, ID: principalUserID}
	serviceRef := principal.Ref{WorkspaceID: principalWorkspaceID, Type: principal.TypeServicePrincipal, ID: principalServiceID}
	subjectRef := principal.Ref{WorkspaceID: principalWorkspaceID, Type: principal.TypeExternalSubject, ID: principalSubjectID}
	for name, test := range map[string]struct {
		ref     principal.Ref
		display string
	}{
		"User":              {ref: userRef, display: "Principal Owner"},
		"Service Principal": {ref: serviceRef, display: "Principal Service"},
		"External Subject":  {ref: subjectRef, display: "ref_principal_subject"},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := resolver.Resolve(ctx, test.ref)
			if err != nil || !value.Active || !value.TargetResolved || value.Legacy ||
				value.DisplayRef != test.display {
				t.Fatalf("resolved %s=%+v err=%v", name, value, err)
			}
		})
	}
	identity, err := principal.NewInvocationIdentity(serviceRef, &subjectRef)
	if err != nil {
		t.Fatal(err)
	}
	actor, subject, err := resolver.ResolveInvocation(ctx, identity)
	if err != nil || actor.ID != principalServiceID || subject == nil || subject.ID != principalSubjectID {
		t.Fatalf("Actor/Subject resolution actor=%+v subject=%+v err=%v", actor, subject, err)
	}

	// The historical User and Service Principal pairs remain byte-for-byte the
	// same and gain referential protection; no proxy/fake User is introduced.
	var userCount, historicalRefs int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT count(*) FROM agent_runs
		WHERE (id=$1 AND triggered_by_type='USER' AND triggered_by_id=$3)
		   OR (id=$2 AND triggered_by_type='SERVICE_PRINCIPAL' AND triggered_by_id=$4)
	`, principalUserRunID, principalServiceRunID, principalUserID, principalServiceID).Scan(&historicalRefs); err != nil {
		t.Fatal(err)
	}
	if userCount != 1 || historicalRefs != 2 {
		t.Fatalf("historical semantics changed users=%d refs=%d", userCount, historicalRefs)
	}
	legacyRef := principal.Ref{
		WorkspaceID: principalOtherSpaceID, Type: principal.TypeServicePrincipal, ID: principalLegacyID,
	}
	legacy, err := resolver.Resolve(ctx, legacyRef)
	if err != nil || !legacy.Legacy || legacy.TargetResolved || legacy.Active {
		t.Fatalf("unresolved historical Principal=%+v err=%v", legacy, err)
	}
	// Typed references make UUID collisions harmless: no User row is created or
	// replaced when a Service Principal happens to use the same UUID.
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'Coincident Service Principal',$1,$1)
	`, principalUserID, principalWorkspaceID); err != nil {
		t.Fatal(err)
	}
	coincidentRef := principal.Ref{
		WorkspaceID: principalWorkspaceID, Type: principal.TypeServicePrincipal, ID: principalUserID,
	}
	coincident, err := resolver.Resolve(ctx, coincidentRef)
	if err != nil || !coincident.Active || coincident.DisplayRef != "Coincident Service Principal" {
		t.Fatalf("coincident typed Principal=%+v err=%v", coincident, err)
	}
	if user, err := resolver.Resolve(ctx, userRef); err != nil || user.DisplayRef != "Principal Owner" {
		t.Fatalf("coincident Service Principal changed User semantics: %+v err=%v", user, err)
	}

	crossService := serviceRef
	crossService.WorkspaceID = principalOtherSpaceID
	if _, err := resolver.Resolve(ctx, crossService); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("cross-Workspace Service Principal resolution err=%v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin)
		VALUES($1,'SERVICE_PRINCIPAL',$2,'DIRECTORY')
	`, principalOtherSpaceID, principalServiceID); err == nil {
		t.Fatal("cross-Workspace Service Principal Ref was accepted")
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,agent_id,trigger_type,triggered_by_type,triggered_by_id,
		 trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,'AAP','SERVICE_PRINCIPAL',$4,'cross-principal','{}','{}')
	`, principalCrossRunID, principalOtherSpaceID, principalOtherAgentID, principalServiceID); err == nil {
		t.Fatal("execution accepted a Principal from another Workspace")
	}

	systemRef := principal.Ref{
		WorkspaceID: principalWorkspaceID, Type: principal.TypeSystem, ID: principalSystemID,
	}
	system, err := resolver.RegisterSystem(ctx, systemRef, "runtime.scheduler")
	if err != nil || !system.Active || system.SystemKey != "runtime.scheduler" {
		t.Fatalf("System Principal=%+v err=%v", system, err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,agent_id,trigger_type,triggered_by_type,triggered_by_id,
		 trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,'SCHEDULE','SYSTEM',$4,'system-principal','{}','{}')
	`, principalSystemRunID, principalWorkspaceID, principalAgentID, principalSystemID); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.RegisterSystem(ctx, systemRef, "runtime.other"); !errors.Is(err, principal.ErrConflict) {
		t.Fatalf("System Principal key replacement err=%v", err)
	}

	// Directory triggers register future Workspace Users without changing the
	// users table or requiring callers to coordinate a second write.
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'principal.second','Second User')`, principalSecondUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members(workspace_id,user_id,role,invited_by)
		VALUES($1,$2,'VIEWER',$3)
	`, principalWorkspaceID, principalSecondUserID, principalUserID); err != nil {
		t.Fatal(err)
	}
	secondRef := principal.Ref{WorkspaceID: principalWorkspaceID, Type: principal.TypeUser, ID: principalSecondUserID}
	if value, err := resolver.Resolve(ctx, secondRef); err != nil || !value.Active {
		t.Fatalf("future User Principal=%+v err=%v", value, err)
	}
	if _, err := db.Exec(`UPDATE principal_refs SET origin='SYSTEM' WHERE workspace_id=$1 AND principal_type='USER' AND principal_id=$2`, principalWorkspaceID, principalUserID); err == nil {
		t.Fatal("Principal Ref mutation was accepted")
	}
	if _, err := db.Exec(`DELETE FROM principal_refs WHERE workspace_id=$1 AND principal_type='USER' AND principal_id=$2`, principalWorkspaceID, principalUserID); err == nil {
		t.Fatal("Principal Ref deletion was accepted")
	}

}

func TestPrincipalMigrationIdentityValidation(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	workspace := principalWorkspaceID
	actor := principal.Ref{WorkspaceID: workspace, Type: principal.TypeServicePrincipal, ID: principalServiceID}
	subject := principal.Ref{WorkspaceID: workspace, Type: principal.TypeExternalSubject, ID: principalSubjectID}
	identity, err := principal.NewInvocationIdentity(actor, &subject)
	if err != nil || identity.Subject == nil || identity.Subject.ID != subject.ID {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	subject.ID = principalUserID
	if identity.Subject.ID != principalSubjectID {
		t.Fatal("Invocation identity retained a mutable Subject pointer")
	}
	crossSubject := *identity.Subject
	crossSubject.WorkspaceID = principalOtherSpaceID
	if _, err := principal.NewInvocationIdentity(actor, &crossSubject); !errors.Is(err, principal.ErrInvalid) {
		t.Fatalf("cross-Workspace Subject err=%v", err)
	}
	if _, err := principal.NewInvocationIdentity(
		principal.Ref{WorkspaceID: workspace, Type: principal.TypeExternalSubject, ID: principalSubjectID}, nil,
	); !errors.Is(err, principal.ErrInvalid) {
		t.Fatalf("External Subject was accepted as transport Actor: %v", err)
	}
	legacy, err := principal.RefFromLegacy(workspace, "user", principalUserID)
	if err != nil {
		t.Fatal(err)
	}
	typeValue, idValue := legacy.LegacyPair()
	if typeValue != "USER" || idValue != principalUserID {
		t.Fatalf("legacy compatibility mapping=%s/%s", typeValue, idValue)
	}
}

func insertPrincipalMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'principal.owner','Principal Owner')`, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'principal-space','Principal Space','PRODUCTION',$3,$3,$3),
		($2,'principal-other','Principal Other','SANDBOX',$3,$3,$3)
	`, principalWorkspaceID, principalOtherSpaceID, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members(workspace_id,user_id,role,invited_by) VALUES
		($1,$3,'OWNER',$3),($2,$3,'OWNER',$3)
	`, principalWorkspaceID, principalOtherSpaceID, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		($1,$3,'Principal Model','openai','https://models.example.test','principal-model',$5,$5),
		($2,$4,'Other Principal Model','openai','https://models.example.test','other-principal-model',$5,$5)
	`, principalModelID, principalOtherModelID, principalWorkspaceID, principalOtherSpaceID, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Principal Agent',$5,$7,$7),
		($2,$4,'Other Principal Agent',$6,$7,$7)
	`, principalAgentID, principalOtherAgentID, principalWorkspaceID, principalOtherSpaceID,
		principalModelID, principalOtherModelID, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'Principal Service',$3,$3)
	`, principalServiceID, principalWorkspaceID, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,created_by,updated_by
		) VALUES($1,$2,$3,'awcl_principal0123456789abcdef0123456','Principal Client','client_secret_basic',$4,$4)
	`, principalClientID, principalWorkspaceID, principalServiceID, principalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO external_subjects(
		 id,workspace_id,client_id,issuer,subject_hash,display_ref
		) VALUES($1,$2,$3,'https://issuer.principal.test',$4,'ref_principal_subject')
	`, principalSubjectID, principalWorkspaceID, principalClientID, bytes.Repeat([]byte{0x61}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,agent_id,trigger_type,triggered_by_type,triggered_by_id,
		 trace_id,model_snapshot,capability_snapshot
		) VALUES
		($1,$3,$4,'CHAT','USER',$6,'legacy-user','{}','{}'),
		($2,$3,$4,'AAP','SERVICE_PRINCIPAL',$5,'legacy-service','{}','{}'),
		($7,$8,$9,'LEGACY','SERVICE_PRINCIPAL',$10,'legacy-unresolved','{}','{}')
	`, principalUserRunID, principalServiceRunID, principalWorkspaceID,
		principalAgentID, principalServiceID, principalUserID, principalLegacyRunID,
		principalOtherSpaceID, principalOtherAgentID, principalLegacyID); err != nil {
		t.Fatal(err)
	}
}
