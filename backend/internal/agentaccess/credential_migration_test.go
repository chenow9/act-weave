// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package agentaccess_test

import (
	"bytes"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"

	"github.com/lib/pq"
)

const (
	credentialOwnerID     = "908f1f2e-7b5a-7c3d-8e9f-123456789001"
	credentialWorkspaceID = "908f1f2e-7b5a-7c3d-8e9f-123456789002"
	credentialOtherSpace  = "908f1f2e-7b5a-7c3d-8e9f-123456789003"
	credentialPrincipalID = "908f1f2e-7b5a-7c3d-8e9f-123456789004"
	credentialClientID    = "908f1f2e-7b5a-7c3d-8e9f-123456789005"
	credentialID1         = "908f1f2e-7b5a-7c3d-8e9f-123456789006"
	credentialID2         = "908f1f2e-7b5a-7c3d-8e9f-123456789007"
)

func TestCredentialMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("expected clean Credential migration version 42, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertCredentialFixtures(t, db)

	assertCredentialAllowList(t)
	assertCredentialSchemaHasNoPlaintext(t, db)
	assertCredentialRotationAndUsage(t, db)
	assertCredentialConstraints(t, db)

}

func assertCredentialAllowList(t *testing.T) {
	t.Helper()
	want := []agentaccess.CredentialType{
		agentaccess.CredentialTypeClientSecret,
		agentaccess.CredentialTypeJWK,
		agentaccess.CredentialTypeMTLSCertificate,
	}
	if got := agentaccess.KnownCredentialTypes(); !slices.Equal(got, want) {
		t.Fatalf("Credential type allowlist=%v want=%v", got, want)
	}
	for _, value := range want {
		if parsed, ok := agentaccess.ParseCredentialType(string(value)); !ok || parsed != value {
			t.Fatalf("Credential type %q parsed as %q ok=%t", value, parsed, ok)
		}
	}
	if _, ok := agentaccess.ParseCredentialType("password"); ok {
		t.Fatal("unknown Credential type was accepted")
	}
}

func assertCredentialSchemaHasNoPlaintext(t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.agent_access_credentials') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("Credential table was not created")
	}
	rows, err := db.Query(`
		SELECT column_name,data_type
		FROM information_schema.columns
		WHERE table_schema='public' AND table_name='agent_access_credentials'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	allowedSensitive := map[string]string{
		"secret_hash": "bytea", "jwk_thumbprint": "bytea", "certificate_thumbprint": "bytea",
	}
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatal(err)
		}
		if expected, sensitive := allowedSensitive[name]; sensitive {
			if dataType != expected {
				t.Fatalf("sensitive column %s type=%s want=%s", name, dataType, expected)
			}
			continue
		}
		for _, forbidden := range []string{"secret", "private_key", "certificate_pem", "jwk_json"} {
			if name == forbidden {
				t.Fatalf("plaintext-capable Credential column exists: %s %s", name, dataType)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertCredentialRotationAndUsage(t *testing.T, db *sql.DB) {
	t.Helper()
	validFrom := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	expiresAt := validFrom.Add(24 * time.Hour)
	insertSecretCredential(t, db, credentialID1, bytes.Repeat([]byte{0x11}, 32), "…1111", validFrom, expiresAt)
	insertSecretCredential(t, db, credentialID2, bytes.Repeat([]byte{0x22}, 32), "…2222", validFrom, expiresAt)

	var active int
	if err := db.QueryRow(`
		SELECT count(*) FROM agent_access_credentials
		WHERE workspace_id=$1 AND client_id=$2 AND credential_type='client_secret'
		  AND revoked_at IS NULL AND valid_from <= $3 AND (expires_at IS NULL OR expires_at > $3)
	`, credentialWorkspaceID, credentialClientID, validFrom.Add(time.Hour)).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("active rotation Credentials=%d want=2", active)
	}

	usedAt := validFrom.Add(2 * time.Hour)
	if _, err := db.Exec(`UPDATE agent_access_credentials SET last_used_at=$1 WHERE id=$2`, usedAt, credentialID1); err != nil {
		t.Fatalf("update operational last_used_at: %v", err)
	}
	var secretHash []byte
	var gotValidFrom, gotExpiresAt time.Time
	if err := db.QueryRow(`
		SELECT secret_hash,valid_from,expires_at FROM agent_access_credentials WHERE id=$1
	`, credentialID1).Scan(&secretHash, &gotValidFrom, &gotExpiresAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secretHash, bytes.Repeat([]byte{0x11}, 32)) ||
		!gotValidFrom.Equal(validFrom) || !gotExpiresAt.Equal(expiresAt) {
		t.Fatal("last_used_at changed authentication facts")
	}

	assertCredentialStatementFails(t, db, `
		UPDATE agent_access_credentials SET secret_hash=$1 WHERE id=$2
	`, bytes.Repeat([]byte{0x33}, 32), credentialID1)
	if _, err := db.Exec(`
		UPDATE agent_access_credentials
		SET revoked_at=$1,revoked_by=$2,lock_version=lock_version+1 WHERE id=$3
	`, validFrom.Add(3*time.Hour), credentialOwnerID, credentialID1); err != nil {
		t.Fatalf("revoke Credential: %v", err)
	}
	assertCredentialStatementFails(t, db, `
		UPDATE agent_access_credentials SET revoked_at=NULL,revoked_by=NULL WHERE id=$1
	`, credentialID1)
	assertCredentialStatementFails(t, db, `DELETE FROM agent_access_credentials WHERE id=$1`, credentialID1)
}

func assertCredentialConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	validFrom := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	material := bytes.Repeat([]byte{0x44}, 32)
	tests := []struct {
		name             string
		id               string
		workspaceID      string
		credentialType   string
		secretHash       any
		jwkThumbprint    any
		certificateThumb any
		expiresAt        any
		revokedAt        any
		revokedBy        any
	}{
		{name: "cross Workspace Client", id: "908f1f2e-7b5a-7c3d-8e9f-123456789010", workspaceID: credentialOtherSpace, credentialType: "client_secret", secretHash: material},
		{name: "unknown type", id: "908f1f2e-7b5a-7c3d-8e9f-123456789011", workspaceID: credentialWorkspaceID, credentialType: "password", secretHash: material},
		{name: "short hash", id: "908f1f2e-7b5a-7c3d-8e9f-123456789012", workspaceID: credentialWorkspaceID, credentialType: "client_secret", secretHash: []byte("short")},
		{name: "mixed material", id: "908f1f2e-7b5a-7c3d-8e9f-123456789013", workspaceID: credentialWorkspaceID, credentialType: "client_secret", secretHash: material, jwkThumbprint: material},
		{name: "missing JWK thumbprint", id: "908f1f2e-7b5a-7c3d-8e9f-123456789014", workspaceID: credentialWorkspaceID, credentialType: "jwk"},
		{name: "invalid validity", id: "908f1f2e-7b5a-7c3d-8e9f-123456789015", workspaceID: credentialWorkspaceID, credentialType: "jwk", jwkThumbprint: material, expiresAt: validFrom},
		{name: "revocation pair", id: "908f1f2e-7b5a-7c3d-8e9f-123456789016", workspaceID: credentialWorkspaceID, credentialType: "mtls_certificate", certificateThumb: material, revokedAt: validFrom.Add(time.Hour)},
		{name: "revoked before valid", id: "908f1f2e-7b5a-7c3d-8e9f-123456789017", workspaceID: credentialWorkspaceID, credentialType: "jwk", jwkThumbprint: material, revokedAt: validFrom.Add(-time.Hour), revokedBy: credentialOwnerID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCredentialStatementFails(t, db, `
				INSERT INTO agent_access_credentials(
				 id,workspace_id,client_id,credential_type,secret_hash,jwk_thumbprint,
				 certificate_thumbprint,public_hint,valid_from,expires_at,
				 revoked_at,revoked_by,created_by
				) VALUES($1,$2,$3,$4,$5,$6,$7,'probe',$8,$9,$10,$11,$12)
			`, test.id, test.workspaceID, credentialClientID, test.credentialType,
				test.secretHash, test.jwkThumbprint, test.certificateThumb,
				validFrom, test.expiresAt, test.revokedAt, test.revokedBy, credentialOwnerID)
		})
	}
}

func insertCredentialFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'credential.owner','Credential Owner')`, credentialOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'credential-space','Credential Space','PRODUCTION',$3,$3,$3),
		($2,'credential-other','Credential Other','PRODUCTION',$3,$3,$3)
	`, credentialWorkspaceID, credentialOtherSpace, credentialOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'Credential principal',$3,$3)
	`, credentialPrincipalID, credentialWorkspaceID, credentialOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,created_by,updated_by
		) VALUES($1,$2,$3,'awcl_credential0123456789abcdef012345','Credential client',
		 'client_secret_basic',$4,$4)
	`, credentialClientID, credentialWorkspaceID, credentialPrincipalID, credentialOwnerID); err != nil {
		t.Fatal(err)
	}
}

func insertSecretCredential(
	t *testing.T,
	db *sql.DB,
	id string,
	hash []byte,
	hint string,
	validFrom, expiresAt time.Time,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_access_credentials(
		 id,workspace_id,client_id,credential_type,secret_hash,public_hint,
		 valid_from,expires_at,created_by
		) VALUES($1,$2,$3,'client_secret',$4,$5,$6,$7,$8)
	`, id, credentialWorkspaceID, credentialClientID, hash, hint,
		validFrom, expiresAt, credentialOwnerID); err != nil {
		t.Fatalf("insert rotation Credential: %v", err)
	}
}

func assertCredentialStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) {
		t.Fatalf("expected PostgreSQL Credential error, got %T: %v", err, err)
	}
}
