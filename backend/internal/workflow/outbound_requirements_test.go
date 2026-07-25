package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/outboundidentity"
)

const (
	outReqOwnerID     = "c18f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	outReqWorkspaceID = "c18f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	outReqProviderID  = "c18f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	outReqConnID      = "c18f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	outReqMigrConnID  = "c18f1f2e-7b5a-7c3d-8e9f-1234567890a5"
)

func TestEnrichPlanAttachesRequirementsAndRejectsMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedOutboundRequirementsFixtures(t, db)

	loader, err := NewOutboundRequirementsLoader(db)
	if err != nil {
		t.Fatal(err)
	}
	plan := &domain.CompiledExecutionPlan{
		WorkflowID: "wf-1",
		Nodes: []domain.ExecutionPlanNode{{
			NodeID: "tool-1", Type: "Tool",
			Config: map[string]any{"toolId": "tool-x", "connectionId": outReqConnID},
		}},
	}
	if err := loader.EnrichPlan(context.Background(), outReqWorkspaceID, plan); err != nil {
		t.Fatalf("enrich ready plan: %v", err)
	}
	if plan.OutboundRequirements == nil {
		t.Fatal("expected outboundRequirements on plan")
	}
	raw, _ := json.Marshal(plan.OutboundRequirements)
	if !json.Valid(raw) || !strings.Contains(string(raw), "outbound-requirements.v1") {
		t.Fatalf("requirements json: %s", raw)
	}
	for _, banned := range []string{"secretId", "vaultKey", "attachmentId", `"value"`} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("plan requirements leaked %s: %s", banned, raw)
		}
	}

	migrating := &domain.CompiledExecutionPlan{
		WorkflowID: "wf-2",
		Nodes: []domain.ExecutionPlanNode{{
			NodeID: "tool-2", Type: "Tool",
			Config: map[string]any{"toolId": "tool-y", "connectionId": outReqMigrConnID},
		}},
	}
	if err := loader.EnrichPlan(context.Background(), outReqWorkspaceID, migrating); !errors.Is(err, outboundidentity.ErrIdentityMigrationRequired) {
		t.Fatalf("expected migration required, got %v", err)
	}

	empty := &domain.CompiledExecutionPlan{
		WorkflowID: "wf-3",
		Nodes:      []domain.ExecutionPlanNode{{NodeID: "start", Type: "Start"}},
	}
	if err := loader.EnrichPlan(context.Background(), outReqWorkspaceID, empty); err != nil {
		t.Fatal(err)
	}
	if empty.OutboundRequirements != nil {
		t.Fatalf("expected no requirements: %+v", empty.OutboundRequirements)
	}
}

func TestValidatePublishedRequirementsDetectsDrift(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedOutboundRequirementsFixtures(t, db)
	loader, err := NewOutboundRequirementsLoader(db)
	if err != nil {
		t.Fatal(err)
	}
	plan := &domain.CompiledExecutionPlan{
		WorkflowID: "wf-1",
		Nodes: []domain.ExecutionPlanNode{{
			NodeID: "tool-1", Type: "Tool",
			Config: map[string]any{"connectionId": outReqConnID},
		}},
	}
	if err := loader.EnrichPlan(context.Background(), outReqWorkspaceID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE service_connections SET outbound_identity_policy_version=99 WHERE id=$1
	`, outReqConnID); err != nil {
		t.Fatal(err)
	}
	planJSON, _ := json.Marshal(plan)
	if err := loader.ValidatePublishedRequirements(context.Background(), outReqWorkspaceID, planJSON); !errors.Is(err, outboundidentity.ErrIdentityPolicyChanged) {
		t.Fatalf("expected policy changed, got %v", err)
	}
}

func seedOutboundRequirementsFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, username, display_name) VALUES ($1,'out.req','Out Req')`, outReqOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, slug, display_name, mode, owner_user_id, created_by, updated_by)
		VALUES ($1,'out-req','Out Req','PRODUCTION',$2,$2,$2)
	`, outReqWorkspaceID, outReqOwnerID); err != nil {
		t.Fatal(err)
	}
	driver := `{
		"outboundIdentity":{
			"schemaVersion":"outbound-identity.v1",
			"supportedModes":["REQUEST_PASSTHROUGH"],
			"supportedSubjectTypes":["USER"],
			"requestPassthrough":{
				"credentialTypes":["ACCESS_TOKEN"],
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			}
		}
	}`
	if _, err := db.Exec(`
		INSERT INTO capability_providers (
			id,workspace_id,name,provider_kind,driver_key,transport,endpoint_config,driver_config,created_by,updated_by
		) VALUES ($1,$2,'Out Provider','HTTP_OPENAPI','http_openapi','HTTP',
			'{"baseUrl":"https://api.example","sourceUri":"https://api.example/openapi.json"}',$3,$4,$4)
	`, outReqProviderID, outReqWorkspaceID, driver, outReqOwnerID); err != nil {
		t.Fatal(err)
	}
	identity := `{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":600}}`
	if _, err := db.Exec(`
		INSERT INTO service_connections (
			id,workspace_id,provider_id,name,alias,environment,auth_mode,auth_config,
			status,outbound_identity,outbound_identity_policy_version,migration_state,created_by,updated_by
		) VALUES
			($1,$3,$4,'Ready','ready','TEST','OUTBOUND_IDENTITY','{}','VERIFIED',$5,2,'NONE',$6,$6),
			($2,$3,$4,'Migrating','migrating','TEST','OUTBOUND_IDENTITY','{}','DISABLED',$5,1,'MIGRATION_REQUIRED',$6,$6)
	`, outReqConnID, outReqMigrConnID, outReqWorkspaceID, outReqProviderID, identity, outReqOwnerID); err != nil {
		t.Fatal(err)
	}
}
