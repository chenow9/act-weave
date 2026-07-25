package application

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
	httptransport "actweave/backend/internal/transport/http"

	"github.com/golang-jwt/jwt/v5"
)

func TestClientCredentialsTokenEndpointUsesAuthenticatedClientAndCurrentPostgreSQLGrant(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		ownerID       = "e48f1f2e-7b5a-7c3d-8e9f-123456789001"
		workspaceID   = "e48f1f2e-7b5a-7c3d-8e9f-123456789002"
		modelID       = "e48f1f2e-7b5a-7c3d-8e9f-123456789003"
		agentID       = "e48f1f2e-7b5a-7c3d-8e9f-123456789004"
		grantID       = "e48f1f2e-7b5a-7c3d-8e9f-123456789005"
		tokenEndpoint = "https://api.example.test/api/agent-access/v1/oauth/token"
	)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'token.owner','Token Owner')`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'token-space','Token Space','PRODUCTION',$2,$2,$2)
	`, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'Token Model','openai','https://models.example.test','token-model',$3,$3)
	`, modelID, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Token Agent',$3,$4,$4)
	`, agentID, workspaceID, modelID, ownerID); err != nil {
		t.Fatal(err)
	}
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	pepper := bytes.Repeat([]byte{0x62}, 32)
	management, err := agentaccess.NewManagementService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := management.RegisterClient(context.Background(), agentaccess.RegisterClientInput{
		WorkspaceID: workspaceID, Name: "Token Endpoint Client", ActorID: ownerID,
		AuthMethod: agentaccess.ClientAuthMethodSecretBasic, TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	grant, err := management.GrantAgent(context.Background(), agentaccess.CreateGrantInput{
		ID: grantID, WorkspaceID: workspaceID, ClientID: registration.Client.ID,
		AgentID: agentID, Scopes: []agentaccess.AgentScope{
			agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
		}, Policy: agentaccess.GrantPolicy{}, ValidFrom: now.Add(-time.Minute), ActorID: ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	clientAuthenticator, err := agentaccessauth.NewClientSecretAuthenticator(
		agentAccessClientSecretStore{repository: repository}, pepper,
	)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x63}, ed25519.SeedSize))
	keys, err := agentaccessauth.NewRotatingSigningKeyProvider("integration-token-key", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tokenService, err := agentaccessauth.NewClientCredentialsTokenService(
		agentAccessClientCredentialsGrantStore{repository: repository}, keys, tokenEndpoint, 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	tokenRoutes, err := httptransport.NewAgentAccessTokenRoutes(
		clientAuthenticator, rejectPrivateKeyTokenAuthentication{}, tokenService,
		rejectTokenExchange{},
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := httptransport.NewRouter(httptransport.Config{
		AgentAccessRegistrars: []httptransport.AgentAccessV1RouteRegistrar{tokenRoutes},
	})
	if err != nil {
		t.Fatal(err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte(
		url.QueryEscape(registration.Client.ClientID) + ":" + url.QueryEscape(registration.OneTimeSecret),
	))
	authenticated, err := clientAuthenticator.AuthenticateBasic(context.Background(), agentaccessauth.ClientSecretAuthenticationRequest{
		Authorization: "Basic " + basic,
	})
	if err != nil {
		t.Fatalf("authenticate integration Client before HTTP request: %v", err)
	}
	resolved, err := (agentAccessClientCredentialsGrantStore{repository: repository}).ResolveClientCredentialsGrant(
		context.Background(), authenticated, agentID, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("resolve integration Grant before HTTP request: %v", err)
	}
	if _, err := tokenService.IssueClientCredentialsToken(context.Background(), agentaccessauth.ClientCredentialsTokenRequest{
		Client: authenticated, AgentID: agentID, RequestedScopes: []string{"run:create"},
	}); err != nil {
		t.Fatalf("issue integration token before HTTP request: grant=%+v authenticated=%+v err=%v", resolved, authenticated, err)
	}
	request := func(scope string) *http.Request {
		form := url.Values{
			"grant_type": {"client_credentials"}, "agent_id": {agentID}, "scope": {scope},
		}
		value := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token", strings.NewReader(form.Encode()))
		value.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		value.Header.Set("Authorization", "Basic "+basic)
		return value
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request("event:read run:create"))
	if response.Code != http.StatusOK {
		t.Fatalf("Token Endpoint status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Scope       string `json:"scope"`
		Refresh     string `json:"refresh_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" || body.TokenType != "Bearer" || body.ExpiresIn != 600 ||
		body.Scope != "run:create event:read" || body.Refresh != "" {
		t.Fatalf("unexpected Token Endpoint body: %+v", body)
	}
	claims := agentaccessauth.AAPAccessTokenClaims{}
	token, err := jwt.ParseWithClaims(body.AccessToken, &claims, func(*jwt.Token) (any, error) {
		return privateKey.Public(), nil
	}, jwt.WithValidMethods([]string{agentaccessauth.AAPSigningAlgorithm}),
		jwt.WithAudience(agentaccessauth.AAPAccessTokenAudience),
		jwt.WithIssuer("https://api.example.test/api/agent-access/v1/oauth"))
	if err != nil || token == nil || !token.Valid || claims.WorkspaceID != workspaceID ||
		claims.AgentID != agentID || claims.Subject != registration.Principal.ID ||
		claims.AuthorizedParty != registration.Client.ClientID ||
		claims.SecurityVersion != registration.Principal.SecurityVersion {
		t.Fatalf("issued claims=%+v valid=%v err=%v", claims, token != nil && token.Valid, err)
	}

	invalidScope := httptest.NewRecorder()
	router.ServeHTTP(invalidScope, request("interaction:decide"))
	if invalidScope.Code != http.StatusBadRequest || !strings.Contains(invalidScope.Body.String(), `"error":"invalid_scope"`) {
		t.Fatalf("out-of-Grant scope status=%d body=%s", invalidScope.Code, invalidScope.Body.String())
	}
	if _, _, err := management.RevokeGrant(context.Background(), workspaceID,
		registration.Client.ID, agentID, grant.ID, ownerID, grant.LockVersion); err != nil {
		t.Fatal(err)
	}
	revoked := httptest.NewRecorder()
	router.ServeHTTP(revoked, request("run:create"))
	if revoked.Code != http.StatusBadRequest || !strings.Contains(revoked.Body.String(), `"error":"invalid_target"`) {
		t.Fatalf("revoked Grant status=%d body=%s", revoked.Code, revoked.Body.String())
	}
}

type rejectPrivateKeyTokenAuthentication struct{}

func (rejectPrivateKeyTokenAuthentication) Authenticate(
	context.Context,
	agentaccessauth.PrivateKeyJWTAuthenticationRequest,
) (agentaccessauth.AuthenticatedClient, error) {
	return agentaccessauth.AuthenticatedClient{}, agentaccessauth.ErrInvalidClient
}

type rejectTokenExchange struct{}

func (rejectTokenExchange) IssueTokenExchange(
	context.Context,
	agentaccessauth.TokenExchangeRequest,
) (agentaccessauth.TokenExchangeToken, error) {
	return agentaccessauth.TokenExchangeToken{}, agentaccessauth.ErrTokenExchangeRequestInvalid
}
