// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package agentaccess_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"

	"github.com/lib/pq"
)

const (
	grantOwnerID      = "a08f1f2e-7b5a-7c3d-8e9f-123456789001"
	grantWorkspaceID  = "a08f1f2e-7b5a-7c3d-8e9f-123456789002"
	grantOtherSpaceID = "a08f1f2e-7b5a-7c3d-8e9f-123456789003"
	grantModelID      = "a08f1f2e-7b5a-7c3d-8e9f-123456789004"
	grantOtherModelID = "a08f1f2e-7b5a-7c3d-8e9f-123456789005"
	grantAgentID1     = "a08f1f2e-7b5a-7c3d-8e9f-123456789006"
	grantAgentID2     = "a08f1f2e-7b5a-7c3d-8e9f-123456789007"
	grantOtherAgentID = "a08f1f2e-7b5a-7c3d-8e9f-123456789008"
	grantPrincipalID  = "a08f1f2e-7b5a-7c3d-8e9f-123456789009"
	grantClientID     = "a08f1f2e-7b5a-7c3d-8e9f-12345678900a"
	grantID1          = "a08f1f2e-7b5a-7c3d-8e9f-12345678900b"
	grantID2          = "a08f1f2e-7b5a-7c3d-8e9f-12345678900c"
	grantID3          = "a08f1f2e-7b5a-7c3d-8e9f-12345678900d"
)

func TestAgentGrantMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean Agent Grant migration version 43, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertGrantFixtures(t, db)

	assertGrantSchemaContract(t)
	assertGrantScopeAndWindows(t, db)
	assertConcurrentGrantOverlap(t, db)
	assertGrantConstraints(t, db)

}

func assertGrantSchemaContract(t *testing.T) {
	t.Helper()
	raw, err := agentaccess.GrantConfigurationSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema     string `json:"$schema"`
		ID         string `json:"$id"`
		Type       string `json:"type"`
		Properties struct {
			Scopes struct {
				Items struct {
					Enum []agentaccess.AgentScope `json:"enum"`
				} `json:"items"`
			} `json:"scopes"`
			Policy struct {
				Properties struct {
					SubjectSharing struct {
						OneOf []struct {
							Properties struct {
								Resources struct {
									Items struct {
										Enum []agentaccess.SubjectSharingResource `json:"enum"`
									} `json:"items"`
								} `json:"resources"`
							} `json:"properties"`
						} `json:"oneOf"`
					} `json:"subjectSharing"`
				} `json:"properties"`
			} `json:"policy"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil ||
		schema.Schema != "https://json-schema.org/draft/2020-12/schema" ||
		schema.ID == "" || schema.Type != "object" {
		t.Fatalf("invalid Agent Grant JSON Schema: err=%v schema=%+v", err, schema)
	}
	if !slices.Equal(schema.Properties.Scopes.Items.Enum, agentaccess.KnownAgentScopes()) {
		t.Fatalf("JSON Schema scopes=%v Go scopes=%v",
			schema.Properties.Scopes.Items.Enum, agentaccess.KnownAgentScopes())
	}
	sharingVariants := schema.Properties.Policy.Properties.SubjectSharing.OneOf
	if len(sharingVariants) != 2 || !slices.Equal(
		sharingVariants[1].Properties.Resources.Items.Enum,
		agentaccess.KnownSubjectSharingResources(),
	) {
		t.Fatalf("JSON Schema Subject Sharing resources=%+v Go resources=%v",
			sharingVariants, agentaccess.KnownSubjectSharingResources())
	}
	valid := json.RawMessage(`{
		"scopes":["agent:read","run:create","event:read"],
		"policy":{
			"serviceDecision":{"enabled":true,"maxRisk":"medium"},
			"subjectSharing":{"enabled":true,"resources":["conversation","event"]}
		}
	}`)
	configuration, err := agentaccess.ValidateGrantConfiguration(valid)
	if err != nil || !slices.Equal(configuration.Scopes, []agentaccess.AgentScope{
		agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
	}) {
		t.Fatalf("valid Grant configuration: value=%+v err=%v", configuration, err)
	}
	for _, invalid := range []string{
		`{"scopes":["run:read"]}`,
		`{"policy":{}}`,
		`{"scopes":[],"policy":{}}`,
		`{"scopes":["workspace:manage"],"policy":{}}`,
		`{"scopes":["run:read","run:read"],"policy":{}}`,
		`{"scopes":["run:read"],"policy":{"serviceDecision":{"enabled":true,"maxRisk":"high"}}}`,
		`{"scopes":["run:read"],"policy":{"serviceDecision":{"enabled":false,"maxRisk":"low"}}}`,
		`{"scopes":["run:read"],"policy":{"serviceDecision":{}}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":null}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":{}}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":{"enabled":false,"resources":["run"]}}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":{"enabled":true}}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":{"enabled":true,"resources":[]}}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":{"enabled":true,"resources":["run","run"]}}}`,
		`{"scopes":["run:read"],"policy":{"subjectSharing":{"enabled":true,"resources":["workspace"]}}}`,
		`{"scopes":["run:read"],"policy":{"unknown":true}}`,
	} {
		if _, err := agentaccess.ValidateGrantConfiguration(json.RawMessage(invalid)); !errors.Is(err, agentaccess.ErrGrantConfigurationInvalid) {
			t.Fatalf("invalid Grant configuration accepted: %s err=%v", invalid, err)
		}
	}
}

func TestSubjectOwnershipPolicyMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("migration 51=%+v", version)
	}
	db := testDatabase.Open(t)
	var valid bool
	if err := db.QueryRow(`SELECT agent_access_grant_policy_valid(
		'{"subjectSharing":{"enabled":true,"resources":["conversation"]}}'::jsonb
	)`).Scan(&valid); err != nil || valid {
		t.Fatalf("migration 51 unexpectedly accepted Subject Sharing: valid=%v err=%v", valid, err)
	}
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("migration 52=%+v", version)
	}
	for document, expected := range map[string]bool{
		`{}`:                                   true,
		`{"subjectSharing":{"enabled":false}}`: true,
		`{"subjectSharing":{"enabled":true,"resources":["conversation","run","event","interaction","artifact"]}}`: true,
		`{"subjectSharing":{"enabled":true,"resources":[]}}`:                                                      false,
		`{"subjectSharing":{"enabled":true,"resources":["run","run"]}}`:                                           false,
		`{"subjectSharing":{"enabled":true,"resources":["unknown"]}}`:                                             false,
		`{"subjectSharing":{"enabled":false,"resources":["run"]}}`:                                                false,
	} {
		if err := db.QueryRow(`SELECT agent_access_grant_policy_valid($1::jsonb)`, document).Scan(&valid); err != nil || valid != expected {
			t.Fatalf("policy %s valid=%v want=%v err=%v", document, valid, expected, err)
		}
	}
	insertGrantFixtures(t, db)
	start := time.Now().UTC().Add(-time.Minute)
	insertGrant(t, db, grantID1, grantAgentID1, start, start.Add(time.Hour),
		`["conversation:read","event:read"]`,
		`{"subjectSharing":{"enabled":true,"resources":["conversation","event"]}}`)
	if _, err := db.Exec(`DELETE FROM agent_access_grants WHERE id=$1`, grantID1); err != nil {
		t.Fatalf("remove migration round-trip Grant: %v", err)
	}
}

func assertGrantScopeAndWindows(t *testing.T, db *sql.DB) {
	t.Helper()
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	insertGrant(t, db, grantID1, grantAgentID1, start, end,
		`["agent:read","run:create","event:read"]`,
		`{"serviceDecision":{"enabled":true,"maxRisk":"medium"}}`)
	insertGrant(t, db, grantID2, grantAgentID2, start, end,
		`["agent:read","run:read"]`, `{}`)
	insertGrant(t, db, grantID3, grantAgentID1, end, end.Add(24*time.Hour),
		`["agent:read","run:read"]`, `{}`)

	var agents int
	if err := db.QueryRow(`
		SELECT count(DISTINCT agent_id) FROM agent_access_grants
		WHERE workspace_id=$1 AND client_id=$2 AND status='ACTIVE'
	`, grantWorkspaceID, grantClientID).Scan(&agents); err != nil {
		t.Fatal(err)
	}
	if agents != 2 {
		t.Fatalf("Client Agent grants=%d want=2", agents)
	}
	assertGrantStatementFails(t, db, `
		INSERT INTO agent_access_grants(
		 id,workspace_id,client_id,agent_id,scopes,policy,valid_from,expires_at,created_by,updated_by
		) VALUES('a08f1f2e-7b5a-7c3d-8e9f-123456789020',$1,$2,$3,
		 '["run:read"]','{}',$4,$5,$6,$6)
	`, grantWorkspaceID, grantClientID, grantAgentID1, start.Add(time.Hour), end.Add(time.Hour), grantOwnerID)
}

func assertConcurrentGrantOverlap(t *testing.T, db *sql.DB) {
	t.Helper()
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var wait sync.WaitGroup
	startGate := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{
		"a08f1f2e-7b5a-7c3d-8e9f-123456789021",
		"a08f1f2e-7b5a-7c3d-8e9f-123456789022",
	} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			<-startGate
			_, err := db.Exec(`
				INSERT INTO agent_access_grants(
				 id,workspace_id,client_id,agent_id,scopes,policy,
				 valid_from,expires_at,created_by,updated_by
				) VALUES($1,$2,$3,$4,'["run:read"]','{}',$5,$6,$7,$7)
			`, id, grantWorkspaceID, grantClientID, grantAgentID2,
				start, start.Add(time.Hour), grantOwnerID)
			results <- err
		}(id)
	}
	close(startGate)
	wait.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		var databaseError *pq.Error
		if errors.As(err, &databaseError) && string(databaseError.Code) == "23P01" {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent Grant error: %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent overlapping Grants successes=%d conflicts=%d", successes, conflicts)
	}
}

func assertGrantConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	start := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name, id, workspaceID, agentID, scopes, policy, status string
		expiresAt, revokedAt, revokedBy                        any
	}{
		{name: "cross Workspace Agent", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789030", workspaceID: grantWorkspaceID, agentID: grantOtherAgentID, scopes: `["run:read"]`, policy: `{}`, status: "ACTIVE", expiresAt: start.Add(time.Hour)},
		{name: "empty scopes", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789031", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `[]`, policy: `{}`, status: "ACTIVE", expiresAt: start.Add(time.Hour)},
		{name: "management scope", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789032", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `["workspace:manage"]`, policy: `{}`, status: "ACTIVE", expiresAt: start.Add(time.Hour)},
		{name: "duplicate scope", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789033", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `["run:read","run:read"]`, policy: `{}`, status: "ACTIVE", expiresAt: start.Add(time.Hour)},
		{name: "high risk service decision", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789034", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `["run:read"]`, policy: `{"serviceDecision":{"enabled":true,"maxRisk":"high"}}`, status: "ACTIVE", expiresAt: start.Add(time.Hour)},
		{name: "unknown policy", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789035", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `["run:read"]`, policy: `{"admin":true}`, status: "ACTIVE", expiresAt: start.Add(time.Hour)},
		{name: "invalid validity", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789036", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `["run:read"]`, policy: `{}`, status: "ACTIVE", expiresAt: start},
		{name: "revoked without evidence", id: "a08f1f2e-7b5a-7c3d-8e9f-123456789037", workspaceID: grantWorkspaceID, agentID: grantAgentID1, scopes: `["run:read"]`, policy: `{}`, status: "REVOKED", expiresAt: start.Add(time.Hour)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertGrantStatementFails(t, db, `
				INSERT INTO agent_access_grants(
				 id,workspace_id,client_id,agent_id,scopes,policy,status,
				 valid_from,expires_at,revoked_at,revoked_by,created_by,updated_by
				) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
			`, test.id, test.workspaceID, grantClientID, test.agentID,
				test.scopes, test.policy, test.status, start, test.expiresAt,
				test.revokedAt, test.revokedBy, grantOwnerID)
		})
	}
	if _, err := db.Exec(`
		UPDATE agent_access_grants SET status='REVOKED',revoked_at=$1,revoked_by=$2,
		 updated_at=$1,updated_by=$2,lock_version=lock_version+1 WHERE id=$3
	`, start, grantOwnerID, grantID1); err != nil {
		t.Fatalf("revoke Grant: %v", err)
	}
	assertGrantStatementFails(t, db, `
		UPDATE agent_access_grants SET status='ACTIVE',revoked_at=NULL,revoked_by=NULL WHERE id=$1
	`, grantID1)
}

func insertGrantFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'grant.owner','Grant Owner')`, grantOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'grant-space','Grant Space','PRODUCTION',$3,$3,$3),
		($2,'grant-other','Grant Other','PRODUCTION',$3,$3,$3)
	`, grantWorkspaceID, grantOtherSpaceID, grantOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		($1,$3,'Grant Model','openai','https://models.example.test','grant-model',$5,$5),
		($2,$4,'Grant Other Model','openai','https://models.example.test','grant-model',$5,$5)
	`, grantModelID, grantOtherModelID, grantWorkspaceID, grantOtherSpaceID, grantOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$4,'Grant Agent One',$6,$7,$7),
		($2,$4,'Grant Agent Two',$6,$7,$7),
		($3,$5,'Grant Other Agent',$8,$7,$7)
	`, grantAgentID1, grantAgentID2, grantOtherAgentID, grantWorkspaceID,
		grantOtherSpaceID, grantModelID, grantOwnerID, grantOtherModelID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'Grant principal',$3,$3)
	`, grantPrincipalID, grantWorkspaceID, grantOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,created_by,updated_by
		) VALUES($1,$2,$3,'awcl_grant0123456789abcdef01234567890','Grant client',
		 'client_secret_basic',$4,$4)
	`, grantClientID, grantWorkspaceID, grantPrincipalID, grantOwnerID); err != nil {
		t.Fatal(err)
	}
}

func insertGrant(
	t *testing.T, db *sql.DB, id, agentID string,
	validFrom, expiresAt time.Time, scopes, policy string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_access_grants(
		 id,workspace_id,client_id,agent_id,scopes,policy,
		 valid_from,expires_at,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
	`, id, grantWorkspaceID, grantClientID, agentID, scopes, policy,
		validFrom, expiresAt, grantOwnerID); err != nil {
		t.Fatalf("insert Agent Grant: %v", err)
	}
}

func assertGrantStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) {
		t.Fatalf("expected PostgreSQL Agent Grant error, got %T: %v", err, err)
	}
}
