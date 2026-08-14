package database_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/application"
	"actweave/backend/internal/config"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/redisx"
	"actweave/backend/internal/storedobject"

	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
)

const (
	cleanSchemaAdminUsername = "clean-schema-admin"
	cleanSchemaAdminPassword = "clean-schema-admin-password-1"
)

func TestCleanSchemaAcceptance(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("expected clean latest migration 22, got %+v", version)
	}
	db := testDatabase.Open(t)
	config := cleanSchemaApplicationConfig(t, testDatabase.DSN())

	first, err := application.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("start application on clean schema: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first clean-schema application: %v", err)
	}
	second, err := application.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart application on bootstrapped schema: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	assertOnlyBootstrapAdministrator(t, db)
	assertBusinessTablesEmpty(t, db)
	assertNoLegacySchemaObjects(t, db)

	jwks := cleanSchemaRequest(t, second.Handler(), http.MethodGet,
		"/api/agent-access/v1/.well-known/jwks.json", nil, "")
	if jwks.Code != http.StatusOK || jwks.Header().Get("Content-Type") != "application/jwk-set+json" ||
		!bytes.Contains(jwks.Body.Bytes(), []byte(`"kid":"clean-schema-aap-key"`)) ||
		bytes.Contains(jwks.Body.Bytes(), []byte(`"d"`)) {
		t.Fatalf("unexpected clean-schema public JWKS status=%d headers=%v body=%s",
			jwks.Code, jwks.Header(), jwks.Body.String())
	}

	login := cleanSchemaRequest(t, second.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": cleanSchemaAdminUsername,
		"password": cleanSchemaAdminPassword,
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("bootstrap administrator login status=%d body=%s", login.Code, login.Body.String())
	}
	var loginBody struct {
		AccessToken        string `json:"accessToken"`
		MustChangePassword bool   `json:"mustChangePassword"`
		User               struct {
			ID           string `json:"id"`
			PlatformRole string `json:"platformRole"`
		} `json:"user"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode bootstrap administrator login: %v", err)
	}
	if loginBody.AccessToken == "" || loginBody.User.ID == "" ||
		loginBody.User.PlatformRole != "PLATFORM_ADMIN" || !loginBody.MustChangePassword {
		t.Fatalf("unexpected bootstrap login response: %+v", loginBody)
	}

	// ZKL-63 HIGH-03: bootstrap admins start with mustChangePassword=true and are
	// restricted to the recovery allowlist until they change password.
	const changedBootstrapPassword = "clean-schema-admin-password-2"
	change := cleanSchemaRequest(t, second.Handler(), http.MethodPost, "/api/v1/users/me:change-password", map[string]any{
		"currentPassword": cleanSchemaAdminPassword,
		"newPassword":     changedBootstrapPassword,
	}, loginBody.AccessToken)
	if change.Code != http.StatusNoContent {
		t.Fatalf("bootstrap must-change password status=%d body=%s", change.Code, change.Body.String())
	}
	relogin := cleanSchemaRequest(t, second.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": cleanSchemaAdminUsername,
		"password": changedBootstrapPassword,
	}, "")
	if relogin.Code != http.StatusOK {
		t.Fatalf("bootstrap re-login after password change status=%d body=%s", relogin.Code, relogin.Body.String())
	}
	var unlockedLogin struct {
		AccessToken        string `json:"accessToken"`
		MustChangePassword bool   `json:"mustChangePassword"`
		User               struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(relogin.Body.Bytes(), &unlockedLogin); err != nil {
		t.Fatalf("decode bootstrap re-login: %v", err)
	}
	if unlockedLogin.AccessToken == "" || unlockedLogin.MustChangePassword || unlockedLogin.User.ID != loginBody.User.ID {
		t.Fatalf("unexpected bootstrap re-login response: %+v", unlockedLogin)
	}

	created := cleanSchemaRequest(t, second.Handler(), http.MethodPost, "/api/v1/workspaces", map[string]any{
		"slug": "clean-schema-workspace", "displayName": "Clean Schema Workspace",
		"mode": "SANDBOX", "settings": map[string]any{},
	}, unlockedLogin.AccessToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create workspace after clean bootstrap status=%d body=%s", created.Code, created.Body.String())
	}
	var createdWorkspace struct {
		ID          string `json:"id"`
		OwnerUserID string `json:"ownerUserId"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdWorkspace); err != nil {
		t.Fatalf("decode created workspace: %v", err)
	}
	if createdWorkspace.ID == "" || createdWorkspace.OwnerUserID != loginBody.User.ID {
		t.Fatalf("unexpected clean-schema workspace: %+v", createdWorkspace)
	}
	var workspaceCount, ownerMembershipCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&workspaceCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_members WHERE workspace_id=$1 AND user_id=$2 AND role='OWNER'`,
		createdWorkspace.ID, loginBody.User.ID).Scan(&ownerMembershipCount); err != nil {
		t.Fatal(err)
	}
	if workspaceCount != 1 || ownerMembershipCount != 1 {
		t.Fatalf("workspace bootstrap result count=%d owner memberships=%d", workspaceCount, ownerMembershipCount)
	}
}

func cleanSchemaApplicationConfig(t *testing.T, dsn string) application.Config {
	t.Helper()
	mini := miniredis.RunT(t)
	return application.Config{
		PostgresDSN:              dsn,
		Redis:                    redisx.Config{Addr: mini.Addr(), KeyPrefix: "test"},
		JWTSecret:                "clean-schema-jwt-secret-at-least-32-bytes",
		SecretMasterKey:          "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		AgentAccessSigningKeys:   cleanSchemaAgentAccessSigningKeys(),
		AgentAccessTokenEndpoint: "https://api.example.test/api/agent-access/v1/oauth/token",
		AgentAccessMaxTokenTTL:   15 * time.Minute,
		AgentAccessFeature: config.AAPFeatureRollout{
			Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
		},
		MinIO: storedobject.MinIOConfig{
			Endpoint: "127.0.0.1:1", AccessKey: "test", SecretKey: "test-secret",
		},
		BootstrapAdmin: &application.BootstrapAdminConfig{
			Username: cleanSchemaAdminUsername, Password: cleanSchemaAdminPassword,
			DisplayName: "Clean Schema Administrator", Locale: "zh-CN", Timezone: "Asia/Singapore",
		},
	}
}

func cleanSchemaAgentAccessSigningKeys() agentaccessauth.SigningKeyProvider {
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, ed25519.SeedSize))
	provider, err := agentaccessauth.NewRotatingSigningKeyProvider(
		"clean-schema-aap-key", privateKey, agentaccessauth.DefaultMaxAccessTokenTTL,
	)
	if err != nil {
		panic(err)
	}
	return provider
}

func assertOnlyBootstrapAdministrator(t *testing.T, db *sql.DB) {
	t.Helper()
	var users, credentials, administrators int
	var mustChangePassword bool
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE platform_role='PLATFORM_ADMIN') FROM users`).Scan(
		&users, &administrators,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(bool_and(must_change_password),false) FROM user_credentials`).Scan(
		&credentials, &mustChangePassword,
	); err != nil {
		t.Fatal(err)
	}
	if users != 1 || administrators != 1 || credentials != 1 || !mustChangePassword {
		t.Fatalf("expected one bootstrap administrator and credential, users=%d admins=%d credentials=%d mustChange=%t",
			users, administrators, credentials, mustChangePassword)
	}
}

func assertBusinessTablesEmpty(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`
		SELECT table_name FROM information_schema.tables
		WHERE table_schema='public' AND table_type='BASE TABLE'
		  AND table_name NOT IN ('schema_migrations','users','user_credentials')
		ORDER BY table_name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var nonEmpty []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + pq.QuoteIdentifier(tableName)).Scan(&count); err != nil {
			t.Fatalf("count clean-schema table %s: %v", tableName, err)
		}
		if count != 0 {
			nonEmpty = append(nonEmpty, fmt.Sprintf("%s=%d", tableName, count))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(nonEmpty) != 0 {
		t.Fatalf("clean startup created business data: %v", nonEmpty)
	}
}

func assertNoLegacySchemaObjects(t *testing.T, db *sql.DB) {
	t.Helper()
	var retiredTables int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema='public' AND table_name IN ('actweave_state')
	`).Scan(&retiredTables); err != nil {
		t.Fatal(err)
	}
	var retiredColumns int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema='public' AND (
		 (table_name IN ('tools','workflows','openapi_imports') AND column_name IN ('agent_id','owner')) OR
		 (table_name='workflows' AND column_name IN ('dsl','canvas_graph','readiness')) OR
		 column_name='state_key'
		)
	`).Scan(&retiredColumns); err != nil {
		t.Fatal(err)
	}
	if retiredTables != 0 || retiredColumns != 0 {
		t.Fatalf("clean schema contains legacy objects: tables=%d columns=%d", retiredTables, retiredColumns)
	}
}

func cleanSchemaRequest(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body any,
	accessToken string,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
