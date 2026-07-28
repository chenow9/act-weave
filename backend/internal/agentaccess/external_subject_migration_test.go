// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package agentaccess_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"

	"github.com/lib/pq"
)

const (
	subjectOwnerID      = "b08f1f2e-7b5a-7c3d-8e9f-123456789001"
	subjectWorkspaceID  = "b08f1f2e-7b5a-7c3d-8e9f-123456789002"
	subjectOtherSpaceID = "b08f1f2e-7b5a-7c3d-8e9f-123456789003"
	subjectPrincipalID1 = "b08f1f2e-7b5a-7c3d-8e9f-123456789004"
	subjectPrincipalID2 = "b08f1f2e-7b5a-7c3d-8e9f-123456789005"
	subjectClientID1    = "b08f1f2e-7b5a-7c3d-8e9f-123456789006"
	subjectClientID2    = "b08f1f2e-7b5a-7c3d-8e9f-123456789007"
	externalSubjectID1  = "b08f1f2e-7b5a-7c3d-8e9f-123456789008"
	externalSubjectID2  = "b08f1f2e-7b5a-7c3d-8e9f-123456789009"
)

func TestExternalSubjectMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("expected clean External Subject migration version 44, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertExternalSubjectFixtures(t, db)

	assertExternalSubjectPrivacySchema(t, db)
	assertExternalSubjectIdentity(t, db)
	assertExternalSubjectConstraints(t, db)

}

func assertExternalSubjectPrivacySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`
		SELECT column_name,data_type
		FROM information_schema.columns
		WHERE table_schema='public' AND table_name='external_subjects'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var foundHash, foundDisplayRef bool
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "subject_hash":
			foundHash = dataType == "bytea"
		case "display_ref":
			foundDisplayRef = true
		case "subject", "subject_id", "email", "phone", "phone_number":
			t.Fatalf("raw Subject/PII column exists: %s %s", name, dataType)
		default:
			if strings.Contains(name, "email") || strings.Contains(name, "phone") {
				t.Fatalf("PII-shaped External Subject column exists: %s", name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundHash || !foundDisplayRef {
		t.Fatalf("privacy columns hash=%t displayRef=%t", foundHash, foundDisplayRef)
	}
}

func assertExternalSubjectIdentity(t *testing.T, db *sql.DB) {
	t.Helper()
	firstSeen := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	hash := bytes.Repeat([]byte{0x51}, 32)
	insertExternalSubject(t, db, externalSubjectID1, subjectClientID1, hash, "ref_customer_51", firstSeen)
	// The same issuer/hash is scoped by Client and may map independently for a
	// second business platform without weakening the unique identity key.
	insertExternalSubject(t, db, externalSubjectID2, subjectClientID2, hash, "ref_customer_51", firstSeen)

	seenAgain := firstSeen.Add(time.Hour)
	if _, err := db.Exec(`
		UPDATE external_subjects
		SET last_seen_at=$1,display_ref='ref_customer_updated',updated_at=$1,lock_version=lock_version+1
		WHERE id=$2
	`, seenAgain, externalSubjectID1); err != nil {
		t.Fatalf("update External Subject operational fields: %v", err)
	}
	var gotHash []byte
	var gotIssuer string
	var gotFirstSeen, gotLastSeen time.Time
	if err := db.QueryRow(`
		SELECT issuer,subject_hash,first_seen_at,last_seen_at
		FROM external_subjects WHERE id=$1
	`, externalSubjectID1).Scan(&gotIssuer, &gotHash, &gotFirstSeen, &gotLastSeen); err != nil {
		t.Fatal(err)
	}
	if gotIssuer != "https://issuer.example.test" || !bytes.Equal(gotHash, hash) ||
		!gotFirstSeen.Equal(firstSeen) || !gotLastSeen.Equal(seenAgain) {
		t.Fatal("last_seen/display_ref update changed External Subject identity")
	}
	assertExternalSubjectStatementFails(t, db, `
		UPDATE external_subjects SET issuer='https://other-issuer.example.test' WHERE id=$1
	`, externalSubjectID1)
	assertExternalSubjectStatementFails(t, db, `
		INSERT INTO external_subjects(
		 id,workspace_id,client_id,issuer,subject_hash,first_seen_at,last_seen_at
		) VALUES('b08f1f2e-7b5a-7c3d-8e9f-123456789010',$1,$2,
		 'https://issuer.example.test',$3,$4,$4)
	`, subjectWorkspaceID, subjectClientID1, hash, firstSeen)
}

func assertExternalSubjectConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	seen := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	validHash := bytes.Repeat([]byte{0x61}, 32)
	tests := []struct {
		name, id, workspaceID, issuer, displayRef, status string
		hash, firstSeen, lastSeen, disabledAt             any
	}{
		{name: "cross Workspace Client", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789020", workspaceID: subjectOtherSpaceID, issuer: "https://issuer.example.test", displayRef: "ref_cross", status: "ACTIVE", hash: validHash, firstSeen: seen, lastSeen: seen},
		{name: "HTTP issuer", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789021", workspaceID: subjectWorkspaceID, issuer: "http://issuer.example.test", displayRef: "ref_http", status: "ACTIVE", hash: validHash, firstSeen: seen, lastSeen: seen},
		{name: "short hash", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789022", workspaceID: subjectWorkspaceID, issuer: "https://issuer-two.example.test", displayRef: "ref_short", status: "ACTIVE", hash: []byte("short"), firstSeen: seen, lastSeen: seen},
		{name: "email Display Ref", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789023", workspaceID: subjectWorkspaceID, issuer: "https://issuer-three.example.test", displayRef: "user@example.test", status: "ACTIVE", hash: validHash, firstSeen: seen, lastSeen: seen},
		{name: "last seen before first", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789024", workspaceID: subjectWorkspaceID, issuer: "https://issuer-four.example.test", displayRef: "ref_order", status: "ACTIVE", hash: validHash, firstSeen: seen, lastSeen: seen.Add(-time.Second)},
		{name: "disabled without timestamp", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789025", workspaceID: subjectWorkspaceID, issuer: "https://issuer-five.example.test", displayRef: "ref_disabled", status: "DISABLED", hash: validHash, firstSeen: seen, lastSeen: seen},
		{name: "unknown status", id: "b08f1f2e-7b5a-7c3d-8e9f-123456789026", workspaceID: subjectWorkspaceID, issuer: "https://issuer-six.example.test", displayRef: "ref_status", status: "REVOKED", hash: validHash, firstSeen: seen, lastSeen: seen},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertExternalSubjectStatementFails(t, db, `
				INSERT INTO external_subjects(
				 id,workspace_id,client_id,issuer,subject_hash,display_ref,status,
				 first_seen_at,last_seen_at,disabled_at,created_at,updated_at
				) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$8,$8)
			`, test.id, test.workspaceID, subjectClientID1, test.issuer, test.hash,
				test.displayRef, test.status, test.firstSeen, test.lastSeen, test.disabledAt)
		})
	}
}

func insertExternalSubjectFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'subject.owner','Subject Owner')`, subjectOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'subject-space','Subject Space','PRODUCTION',$3,$3,$3),
		($2,'subject-other','Subject Other','PRODUCTION',$3,$3,$3)
	`, subjectWorkspaceID, subjectOtherSpaceID, subjectOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by) VALUES
		($1,$3,'Subject principal one',$4,$4),
		($2,$3,'Subject principal two',$4,$4)
	`, subjectPrincipalID1, subjectPrincipalID2, subjectWorkspaceID, subjectOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,created_by,updated_by
		) VALUES
		($1,$3,$4,'awcl_subject0123456789abcdef012345678','Subject client one','client_secret_basic',$6,$6),
		($2,$3,$5,'awcl_subjectabcdef0123456789abcdef012','Subject client two','client_secret_basic',$6,$6)
	`, subjectClientID1, subjectClientID2, subjectWorkspaceID,
		subjectPrincipalID1, subjectPrincipalID2, subjectOwnerID); err != nil {
		t.Fatal(err)
	}
}

func insertExternalSubject(
	t *testing.T, db *sql.DB, id, clientID string, hash []byte, displayRef string, seen time.Time,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO external_subjects(
		 id,workspace_id,client_id,issuer,subject_hash,display_ref,
		 first_seen_at,last_seen_at,created_at,updated_at
		) VALUES($1,$2,$3,'https://issuer.example.test',$4,$5,$6,$6,$6,$6)
	`, id, subjectWorkspaceID, clientID, hash, displayRef, seen); err != nil {
		t.Fatalf("insert External Subject: %v", err)
	}
}

func assertExternalSubjectStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) {
		t.Fatalf("expected PostgreSQL External Subject error, got %T: %v", err, err)
	}
}
