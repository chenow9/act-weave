package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/authz"

	"github.com/google/uuid"
)

const (
	managementWorkspaceID  = "a1000000-0000-4000-8000-000000000001"
	managementClientID     = "a1000000-0000-4000-8000-000000000002"
	managementPrincipalID  = "a1000000-0000-4000-8000-000000000003"
	managementCredentialID = "a1000000-0000-4000-8000-000000000004"
)

type managementAuthorizerStub struct {
	action authz.Action
	err    error
}

func (stub *managementAuthorizerStub) AuthorizeWorkspace(
	_ context.Context, _, _ string, action authz.Action,
) (authz.WorkspaceContext, error) {
	stub.action = action
	return authz.WorkspaceContext{}, stub.err
}

type managementServiceStub struct {
	registerCalls int
	statusCalls   int
}

func (stub *managementServiceStub) RegisterClient(
	_ context.Context, input agentaccess.RegisterClientInput,
) (agentaccess.ClientRegistration, error) {
	stub.registerCalls++
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	return agentaccess.ClientRegistration{
		Client: agentaccess.AgentAccessClient{
			ID: managementClientID, WorkspaceID: input.WorkspaceID,
			ServicePrincipalID: managementPrincipalID, ClientID: "awcl_public",
			Name: input.Name, Status: agentaccess.StatusActive, AuthMethod: input.AuthMethod,
			AllowedCORSOrigins: []string{}, TokenTTLSeconds: 600,
			CreatedAt: now, UpdatedAt: now, LockVersion: 1,
		},
		Credential: agentaccess.Credential{
			ID: managementCredentialID, WorkspaceID: input.WorkspaceID, ClientID: managementClientID,
			Type: agentaccess.CredentialTypeClientSecret, PublicHint: "…public",
			ValidFrom: now, CreatedAt: now, LockVersion: 1,
		},
		OneTimeSecret: "awsk_live_once",
	}, nil
}

func (stub *managementServiceStub) AddCredential(
	context.Context, agentaccess.AddCredentialInput,
) (agentaccess.IssuedCredential, error) {
	return agentaccess.IssuedCredential{}, nil
}

func (stub *managementServiceStub) RevokeCredential(
	context.Context, string, string, string, string, int64,
) (agentaccess.Credential, agentaccess.ServicePrincipal, error) {
	return agentaccess.Credential{}, agentaccess.ServicePrincipal{}, nil
}

func (stub *managementServiceStub) SetClientStatus(
	_ context.Context, workspaceID, _ string, _ string, status agentaccess.Status, lockVersion int64,
) (agentaccess.AgentAccessClient, agentaccess.ServicePrincipal, error) {
	stub.statusCalls++
	return agentaccess.AgentAccessClient{
			ID: managementClientID, WorkspaceID: workspaceID, ServicePrincipalID: managementPrincipalID,
			ClientID: "awcl_public", Name: "Business Platform", Status: status,
			AuthMethod: agentaccess.ClientAuthMethodSecretBasic, AllowedCORSOrigins: []string{},
			TokenTTLSeconds: 600, LockVersion: lockVersion + 1,
		}, agentaccess.ServicePrincipal{
			ID: managementPrincipalID, Status: agentaccess.StatusActive,
			SecurityVersion: 2, LockVersion: 2,
		}, nil
}

func (stub *managementServiceStub) UpdateTrustedSubjectIssuer(
	_ context.Context, input agentaccess.UpdateTrustedSubjectIssuerInput,
) (agentaccess.AgentAccessClient, agentaccess.ServicePrincipal, error) {
	return agentaccess.AgentAccessClient{
			ID: managementClientID, WorkspaceID: input.WorkspaceID, ServicePrincipalID: managementPrincipalID,
			ClientID: "awcl_public", Name: "Business Platform", Status: agentaccess.StatusActive,
			AuthMethod: agentaccess.ClientAuthMethodSecretBasic, AllowedCORSOrigins: []string{},
			TokenTTLSeconds: 600, LockVersion: input.ExpectedLockVersion + 1,
			TrustedSubjectIssuer: input.Config.Issuer, TrustedSubjectAudience: input.Config.Audience,
			TrustedSubjectJWKSURI: input.Config.JWKSURI, TrustedSubjectAlgorithms: input.Config.Algorithms,
			TrustedSubjectClaimPolicy: input.Config.ClaimPolicy,
		}, agentaccess.ServicePrincipal{
			ID: managementPrincipalID, Status: agentaccess.StatusActive,
			SecurityVersion: 2, LockVersion: 2,
		}, nil
}

func (stub *managementServiceStub) ListExternalSubjectPublicViews(
	context.Context, string, string,
) ([]agentaccess.ExternalSubjectPublicView, error) {
	return []agentaccess.ExternalSubjectPublicView{}, nil
}

func (stub *managementServiceStub) GetExternalSubjectPublicView(
	context.Context, string, string, string,
) (agentaccess.ExternalSubjectPublicView, error) {
	return agentaccess.ExternalSubjectPublicView{
		ID: "d08f1f2e-7b5a-7c3d-8e9f-1234567890aa", WorkspaceID: managementWorkspaceID,
		ClientID: managementClientID, Issuer: "https://idp.example.test",
		DisplayRef: "ref_customer_1", Status: agentaccess.StatusActive, LockVersion: 1,
	}, nil
}

func (stub *managementServiceStub) SetExternalSubjectStatus(
	_ context.Context, input agentaccess.SetExternalSubjectStatusInput,
) (agentaccess.ExternalSubjectPublicView, error) {
	return agentaccess.ExternalSubjectPublicView{
		ID: input.SubjectID, WorkspaceID: input.WorkspaceID, ClientID: input.ClientID,
		Issuer: "https://idp.example.test", Status: input.Status,
		LockVersion: input.ExpectedLockVersion + 1,
	}, nil
}

func (stub *managementServiceStub) UpdateExternalSubjectDisplayRef(
	_ context.Context, input agentaccess.UpdateExternalSubjectDisplayRefInput,
) (agentaccess.ExternalSubjectPublicView, error) {
	return agentaccess.ExternalSubjectPublicView{
		ID: input.SubjectID, WorkspaceID: input.WorkspaceID, ClientID: input.ClientID,
		Issuer: "https://idp.example.test", DisplayRef: input.DisplayRef,
		Status: agentaccess.StatusActive, LockVersion: input.ExpectedLockVersion + 1,
	}, nil
}

func (stub *managementServiceStub) GrantAgent(
	context.Context, agentaccess.CreateGrantInput,
) (agentaccess.AgentGrant, error) {
	return agentaccess.AgentGrant{}, nil
}

func (stub *managementServiceStub) RevokeGrant(
	context.Context, string, string, string, string, string, int64,
) (agentaccess.AgentGrant, agentaccess.ServicePrincipal, error) {
	return agentaccess.AgentGrant{}, agentaccess.ServicePrincipal{}, nil
}

type managementRepositoryStub struct{}

func (managementRepositoryStub) ListClients(context.Context, string) ([]agentaccess.AgentAccessClient, error) {
	return []agentaccess.AgentAccessClient{}, nil
}

func (managementRepositoryStub) GetClient(context.Context, string, string) (agentaccess.AgentAccessClient, error) {
	return agentaccess.AgentAccessClient{ID: managementClientID}, nil
}

func (managementRepositoryStub) ListCredentials(context.Context, string, string) ([]agentaccess.Credential, error) {
	return []agentaccess.Credential{{
		ID: managementCredentialID, Type: agentaccess.CredentialTypeClientSecret,
		PublicHint: "…public", ValidFrom: time.Now().UTC(), CreatedAt: time.Now().UTC(), LockVersion: 1,
	}}, nil
}

func (managementRepositoryStub) ListGrants(context.Context, string, string) ([]agentaccess.AgentGrant, error) {
	return []agentaccess.AgentGrant{}, nil
}

func (managementRepositoryStub) GetGrantByID(context.Context, string, string, string) (agentaccess.AgentGrant, error) {
	return agentaccess.AgentGrant{}, nil
}

type managementCommandStoreStub struct {
	commands      map[string]*agentaccess.ManagementCommand
	completeCalls int
}

func (stub *managementCommandStoreStub) ClaimManagementCommand(
	_ context.Context, input agentaccess.ClaimManagementCommandInput,
) (agentaccess.ManagementCommand, bool, error) {
	if stub.commands == nil {
		stub.commands = make(map[string]*agentaccess.ManagementCommand)
	}
	if command := stub.commands[input.IdempotencyKey]; command != nil {
		return *command, false, nil
	}
	command := &agentaccess.ManagementCommand{
		WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		IdempotencyKey: input.IdempotencyKey, Operation: input.Operation,
		RequestHash: append([]byte(nil), input.RequestHash...),
		State:       agentaccess.ManagementCommandPending,
	}
	stub.commands[input.IdempotencyKey] = command
	return *command, true, nil
}

func (stub *managementCommandStoreStub) CompleteManagementCommand(
	_ context.Context, _, _, key string, _ []byte, status int, body json.RawMessage,
) (agentaccess.ManagementCommand, error) {
	stub.completeCalls++
	command := stub.commands[key]
	command.State = agentaccess.ManagementCommandCompleted
	command.ResponseStatus = status
	command.ResponseBody = append(json.RawMessage(nil), body...)
	return *command, nil
}

func TestAgentAccessManagementRoutes(t *testing.T) {
	service := &managementServiceStub{}
	authorizer := &managementAuthorizerStub{}
	commandStore := &managementCommandStoreStub{}
	routes, err := NewAgentAccessManagementRoutes(
		service, managementRepositoryStub{}, authorizer, commandStore,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		Authenticator: contractAuthenticator{}, Registrars: []V1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + managementWorkspaceID + "/agent-access/clients"

	t.Run("requires authenticated Workspace MANAGE", func(t *testing.T) {
		response := managementRequest(router, http.MethodGet, base, nil, "", "")
		assertErrorResponse(t, response, http.StatusUnauthorized, "UNAUTHENTICATED")
		response = managementRequest(router, http.MethodGet, base, nil, "contract-token", "")
		if response.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
		}
		if authorizer.action != authz.ActionManage {
			t.Fatalf("authorization action=%q want=%q", authorizer.action, authz.ActionManage)
		}
	})

	t.Run("creation requires idempotency and returns secret once", func(t *testing.T) {
		body := map[string]any{
			"name": "Business Platform", "authMethod": "client_secret_basic",
			"allowedCorsOrigins": []string{},
		}
		response := managementRequest(router, http.MethodPost, base, body, "contract-token", "")
		assertErrorResponse(t, response, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		if service.registerCalls != 0 {
			t.Fatal("registration executed without idempotency key")
		}

		key := uuid.NewString()
		response = managementRequest(router, http.MethodPost, base, body, "contract-token", key)
		if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), "awsk_live_once") {
			t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
		}
		response = managementRequest(router, http.MethodPost, base, body, "contract-token", key)
		var replay map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &replay); err != nil {
			t.Fatal(err)
		}
		_, hasSecret := replay["secret"]
		if response.Code != http.StatusCreated || hasSecret || strings.Contains(response.Body.String(), "awsk_live_once") {
			t.Fatalf("replay exposed one-time secret: %s", response.Body.String())
		}
		if service.registerCalls != 1 {
			t.Fatalf("registration calls=%d want=1", service.registerCalls)
		}
		if commandStore.completeCalls != 1 || bytes.Contains(commandStore.commands[key].ResponseBody, []byte("awsk_live_once")) {
			t.Fatalf("durable replay was not completed safely: %+v", commandStore.commands[key])
		}

		restartedRoutes, err := NewAgentAccessManagementRoutes(
			service, managementRepositoryStub{}, authorizer, commandStore,
		)
		if err != nil {
			t.Fatal(err)
		}
		restartedRouter, err := NewRouter(Config{
			Authenticator: contractAuthenticator{}, Registrars: []V1RouteRegistrar{restartedRoutes},
		})
		if err != nil {
			t.Fatal(err)
		}
		response = managementRequest(restartedRouter, http.MethodPost, base, body, "contract-token", key)
		if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "awsk_live_once") ||
			service.registerCalls != 1 {
			t.Fatalf("restart replay status=%d calls=%d body=%s", response.Code, service.registerCalls, response.Body.String())
		}

		changed := map[string]any{
			"name": "Different Platform", "authMethod": "client_secret_basic",
			"allowedCorsOrigins": []string{},
		}
		response = managementRequest(router, http.MethodPost, base, changed, "contract-token", key)
		assertErrorResponse(t, response, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	})

	t.Run("credential DTO is public metadata only", func(t *testing.T) {
		response := managementRequest(router, http.MethodGet, base+"/"+managementClientID+"/credentials", nil, "contract-token", "")
		if response.Code != http.StatusOK {
			t.Fatalf("credential list status=%d body=%s", response.Code, response.Body.String())
		}
		var payload struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Items) != 1 {
			t.Fatalf("decode credential DTO: %v body=%s", err, response.Body.String())
		}
		for _, forbidden := range []string{"secret", "secretHash", "hash", "jwkThumbprint", "thumbprint"} {
			if _, exists := payload.Items[0][forbidden]; exists {
				t.Fatalf("credential DTO exposed %q: %s", forbidden, response.Body.String())
			}
		}
		if payload.Items[0]["publicHint"] != "…public" {
			t.Fatalf("credential DTO omitted public hint: %s", response.Body.String())
		}
	})

	t.Run("state command requires lock version", func(t *testing.T) {
		path := base + "/" + managementClientID + "/__command/disable"
		response := managementRequest(router, http.MethodPost, path, map[string]any{"lockVersion": 0}, "contract-token", uuid.NewString())
		assertErrorResponse(t, response, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		response = managementRequest(router, http.MethodPost, path, map[string]any{"lockVersion": 1}, "contract-token", uuid.NewString())
		if response.Code != http.StatusOK || service.statusCalls != 1 {
			t.Fatalf("disable status=%d calls=%d body=%s", response.Code, service.statusCalls, response.Body.String())
		}
	})

	t.Run("update trusted subject issuer happy path", func(t *testing.T) {
		path := base + "/" + managementClientID + "/__command/update-trusted-subject-issuer"
		body := map[string]any{
			"lockVersion": 1,
			"trustedSubjectIssuer": "https://idp.partner.example.test",
			"trustedSubjectAudience": "actweave-partner-subject",
			"trustedSubjectJwksUri": "https://idp.partner.example.test/jwks.json",
			"trustedSubjectAlgorithms": []string{"EdDSA"},
			"trustedSubjectClaimPolicy": map[string]any{
				"subjectClaim": "sub", "requireJti": true,
				"maxSubjectBytes": 256, "maxTokenTTLSeconds": 3600,
			},
		}
		response := managementRequest(router, http.MethodPost, path, body, "contract-token", uuid.NewString())
		if response.Code != http.StatusOK {
			t.Fatalf("update trusted subject issuer status=%d body=%s", response.Code, response.Body.String())
		}
		var payload struct {
			Client struct {
				TrustedSubjectIssuer   string   `json:"trustedSubjectIssuer"`
				TrustedSubjectAudience string   `json:"trustedSubjectAudience"`
				TrustedSubjectJWKSURI  string   `json:"trustedSubjectJwksUri"`
				TrustedSubjectAlgorithms []string `json:"trustedSubjectAlgorithms"`
			} `json:"client"`
			ServicePrincipal struct {
				SecurityVersion int64 `json:"securityVersion"`
			} `json:"servicePrincipal"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Client.TrustedSubjectIssuer != "https://idp.partner.example.test" ||
			payload.Client.TrustedSubjectAudience != "actweave-partner-subject" ||
			payload.Client.TrustedSubjectJWKSURI != "https://idp.partner.example.test/jwks.json" ||
			len(payload.Client.TrustedSubjectAlgorithms) != 1 ||
			payload.ServicePrincipal.SecurityVersion != 2 {
			t.Fatalf("unexpected trust update payload: %+v body=%s", payload, response.Body.String())
		}
		for _, forbidden := range []string{"subject_token", "subjectToken", `"subject":`} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("management response leaked %q: %s", forbidden, response.Body.String())
			}
		}
	})
}

func managementRequest(
	router http.Handler, method, path string, body any, token, idempotencyKey string,
) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	request.Header.Set("X-Request-ID", "request-agent-access-management")
	request.Header.Set("X-Trace-ID", "trace-agent-access-management")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
