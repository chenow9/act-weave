// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package agentaccess_test

import (
	"database/sql"
	"errors"
	"slices"
	"testing"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"

	"github.com/lib/pq"
)

const (
	accessOwnerID       = "808f1f2e-7b5a-7c3d-8e9f-123456789001"
	accessWorkspaceID   = "808f1f2e-7b5a-7c3d-8e9f-123456789002"
	accessWorkspaceID2  = "808f1f2e-7b5a-7c3d-8e9f-123456789003"
	accessPrincipalID   = "808f1f2e-7b5a-7c3d-8e9f-123456789004"
	accessPrincipalID2  = "808f1f2e-7b5a-7c3d-8e9f-123456789005"
	accessClientRowID   = "808f1f2e-7b5a-7c3d-8e9f-123456789006"
	accessClientRowID2  = "808f1f2e-7b5a-7c3d-8e9f-123456789007"
	accessClientPublic  = "awcl_0123456789abcdef0123456789abcdef"
	accessClientPublic2 = "awcl_abcdef0123456789abcdef0123456789"
)

func TestClientMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean Agent Access migration version 41, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertAgentAccessFixtures(t, db)

	assertAgentAccessAllowLists(t)
	assertAgentAccessSchema(t, db)
	assertAgentAccessValidRows(t, db)
	assertAgentAccessConstraints(t, db)

}

func assertAgentAccessAllowLists(t *testing.T) {
	t.Helper()
	if got := agentaccess.KnownStatuses(); !slices.Equal(got, []agentaccess.Status{
		agentaccess.StatusActive, agentaccess.StatusDisabled,
	}) {
		t.Fatalf("status allowlist=%v", got)
	}
	if got := agentaccess.KnownClientAuthMethods(); !slices.Equal(got, []agentaccess.ClientAuthMethod{
		agentaccess.ClientAuthMethodSecretBasic, agentaccess.ClientAuthMethodPrivateKey,
	}) {
		t.Fatalf("auth method allowlist=%v", got)
	}
	for _, value := range []string{"ACTIVE", "DISABLED"} {
		if _, ok := agentaccess.ParseStatus(value); !ok {
			t.Fatalf("known status %q was not parsed", value)
		}
	}
	for _, value := range []string{"client_secret_basic", "private_key_jwt"} {
		if _, ok := agentaccess.ParseClientAuthMethod(value); !ok {
			t.Fatalf("known auth method %q was not parsed", value)
		}
	}
	if _, ok := agentaccess.ParseStatus("active"); ok {
		t.Fatal("unknown status was accepted")
	}
	if _, ok := agentaccess.ParseClientAuthMethod("none"); ok {
		t.Fatal("unknown auth method was accepted")
	}
}

func assertAgentAccessSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"service_principals", "agent_access_clients"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected Agent Access table %s", table)
		}
		var workspaceRequired bool
		if err := db.QueryRow(`
			SELECT EXISTS(
			 SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name=$1
			   AND column_name='workspace_id' AND is_nullable='NO'
			)
		`, table).Scan(&workspaceRequired); err != nil {
			t.Fatal(err)
		}
		if !workspaceRequired {
			t.Fatalf("%s does not have a required Workspace scope", table)
		}
	}
	for _, index := range []string{
		"service_principals_workspace_status_updated_idx",
		"agent_access_clients_client_id_key",
		"agent_access_clients_workspace_principal_key",
		"agent_access_clients_workspace_status_updated_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected Agent Access index %s", index)
		}
	}
}

func assertAgentAccessValidRows(t *testing.T, db *sql.DB) {
	t.Helper()
	insertServicePrincipal(t, db, accessPrincipalID, accessWorkspaceID, "Primary principal")
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,status,auth_method,
		 trusted_subject_issuer,trusted_subject_jwks_uri,allowed_cors_origins,
		 created_by,updated_by
		) VALUES($1,$2,$3,$4,'Primary client','ACTIVE','client_secret_basic',
		 'https://issuer.example.test','https://issuer.example.test/jwks.json',
		 '["https://app.example.test","https://admin.example.test:8443"]',$5,$5)
	`, accessClientRowID, accessWorkspaceID, accessPrincipalID, accessClientPublic, accessOwnerID); err != nil {
		t.Fatalf("insert valid secret Client: %v", err)
	}

	insertServicePrincipal(t, db, accessPrincipalID2, accessWorkspaceID2, "Key principal")
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,status,auth_method,jwks_uri,
		 created_by,updated_by
		) VALUES($1,$2,$3,$4,'Key client','ACTIVE','private_key_jwt',
		 'https://keys.example.test/client.jwks',$5,$5)
	`, accessClientRowID2, accessWorkspaceID2, accessPrincipalID2, accessClientPublic2, accessOwnerID); err != nil {
		t.Fatalf("insert valid private key Client: %v", err)
	}
}

func assertAgentAccessConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	for name, query := range map[string]string{
		"blank principal name": `INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		 VALUES('808f1f2e-7b5a-7c3d-8e9f-123456789010',$1,'  ',$2,$2)`,
		"invalid principal status": `INSERT INTO service_principals(id,workspace_id,name,status,created_by,updated_by)
		 VALUES('808f1f2e-7b5a-7c3d-8e9f-123456789011',$1,'bad','REVOKED',$2,$2)`,
		"invalid security version": `INSERT INTO service_principals(id,workspace_id,name,security_version,created_by,updated_by)
		 VALUES('808f1f2e-7b5a-7c3d-8e9f-123456789012',$1,'bad',0,$2,$2)`,
		"disabled without timestamp": `INSERT INTO service_principals(id,workspace_id,name,status,created_by,updated_by)
		 VALUES('808f1f2e-7b5a-7c3d-8e9f-123456789013',$1,'bad','DISABLED',$2,$2)`,
	} {
		t.Run(name, func(t *testing.T) {
			assertAgentAccessStatementFails(t, db, query, accessWorkspaceID, accessOwnerID)
		})
	}

	clientFailures := []struct {
		name       string
		publicID   string
		authMethod string
		jwksURI    any
		cors       string
		ttl        int
		status     string
		disabledAt any
	}{
		{name: "guessable Client ID", publicID: "client-1", authMethod: "client_secret_basic", cors: `[]`, ttl: 600, status: "ACTIVE"},
		{name: "unknown status", publicID: accessClientPublic, authMethod: "client_secret_basic", cors: `[]`, ttl: 600, status: "REVOKED"},
		{name: "unknown auth method", publicID: "awcl_11111111111111111111111111111111", authMethod: "none", cors: `[]`, ttl: 600, status: "ACTIVE"},
		{name: "secret with JWKS", publicID: "awcl_22222222222222222222222222222222", authMethod: "client_secret_basic", jwksURI: "https://keys.example.test/jwks", cors: `[]`, ttl: 600, status: "ACTIVE"},
		{name: "private key without JWKS", publicID: "awcl_33333333333333333333333333333333", authMethod: "private_key_jwt", cors: `[]`, ttl: 600, status: "ACTIVE"},
		{name: "wildcard CORS", publicID: "awcl_44444444444444444444444444444444", authMethod: "client_secret_basic", cors: `["*"]`, ttl: 600, status: "ACTIVE"},
		{name: "HTTP CORS", publicID: "awcl_55555555555555555555555555555555", authMethod: "client_secret_basic", cors: `["http://app.example.test"]`, ttl: 600, status: "ACTIVE"},
		{name: "duplicate CORS", publicID: "awcl_66666666666666666666666666666666", authMethod: "client_secret_basic", cors: `["https://app.example.test","https://app.example.test"]`, ttl: 600, status: "ACTIVE"},
		{name: "excessive TTL", publicID: "awcl_77777777777777777777777777777777", authMethod: "client_secret_basic", cors: `[]`, ttl: 901, status: "ACTIVE"},
		{name: "disabled without timestamp", publicID: "awcl_88888888888888888888888888888888", authMethod: "client_secret_basic", cors: `[]`, ttl: 600, status: "DISABLED"},
	}
	for _, failure := range clientFailures {
		t.Run(failure.name, func(t *testing.T) {
			assertAgentAccessStatementFails(t, db, `
				UPDATE agent_access_clients
				SET client_id=$1,status=$2,auth_method=$3,jwks_uri=$4,
				    allowed_cors_origins=$5,token_ttl_seconds=$6,disabled_at=$7
				WHERE id=$8
			`, failure.publicID, failure.status, failure.authMethod, failure.jwksURI,
				failure.cors, failure.ttl, failure.disabledAt, accessClientRowID)
		})
	}

	t.Run("Client ID is globally unique", func(t *testing.T) {
		assertAgentAccessStatementFails(t, db, `
			UPDATE agent_access_clients SET client_id=$1 WHERE id=$2
		`, accessClientPublic, accessClientRowID2)
	})
	t.Run("Principal cannot cross Workspace", func(t *testing.T) {
		assertAgentAccessStatementFails(t, db, `
			UPDATE agent_access_clients SET workspace_id=$1 WHERE id=$2
		`, accessWorkspaceID2, accessClientRowID)
	})
	t.Run("one Client owns one dedicated principal", func(t *testing.T) {
		assertAgentAccessStatementFails(t, db, `
			INSERT INTO agent_access_clients(
			 id,workspace_id,service_principal_id,client_id,name,auth_method,created_by,updated_by
			) VALUES('808f1f2e-7b5a-7c3d-8e9f-123456789030',$1,$2,
			 'awcl_99999999999999999999999999999999','Duplicate principal','client_secret_basic',$3,$3)
		`, accessWorkspaceID, accessPrincipalID, accessOwnerID)
	})
}

func insertAgentAccessFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'access.owner','Access Owner')`, accessOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'access-space','Access Space','PRODUCTION',$3,$3,$3),
		($2,'access-space-two','Access Space Two','PRODUCTION',$3,$3,$3)
	`, accessWorkspaceID, accessWorkspaceID2, accessOwnerID); err != nil {
		t.Fatal(err)
	}
}

func insertServicePrincipal(t *testing.T, db *sql.DB, id, workspaceID, name string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,$3,$4,$4)
	`, id, workspaceID, name, accessOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertAgentAccessStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) {
		t.Fatalf("expected PostgreSQL constraint error, got %T: %v", err, err)
	}
}
