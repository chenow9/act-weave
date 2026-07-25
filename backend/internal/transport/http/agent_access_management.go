package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/authz"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AgentAccessManagementService interface {
	RegisterClient(context.Context, agentaccess.RegisterClientInput) (agentaccess.ClientRegistration, error)
	AddCredential(context.Context, agentaccess.AddCredentialInput) (agentaccess.IssuedCredential, error)
	RevokeCredential(context.Context, string, string, string, string, int64) (agentaccess.Credential, agentaccess.ServicePrincipal, error)
	SetClientStatus(context.Context, string, string, string, agentaccess.Status, int64) (agentaccess.AgentAccessClient, agentaccess.ServicePrincipal, error)
	UpdateTrustedSubjectIssuer(context.Context, agentaccess.UpdateTrustedSubjectIssuerInput) (agentaccess.AgentAccessClient, agentaccess.ServicePrincipal, error)
	GrantAgent(context.Context, agentaccess.CreateGrantInput) (agentaccess.AgentGrant, error)
	RevokeGrant(context.Context, string, string, string, string, string, int64) (agentaccess.AgentGrant, agentaccess.ServicePrincipal, error)
	ListExternalSubjectPublicViews(context.Context, string, string) ([]agentaccess.ExternalSubjectPublicView, error)
	GetExternalSubjectPublicView(context.Context, string, string, string) (agentaccess.ExternalSubjectPublicView, error)
	SetExternalSubjectStatus(context.Context, agentaccess.SetExternalSubjectStatusInput) (agentaccess.ExternalSubjectPublicView, error)
	UpdateExternalSubjectDisplayRef(context.Context, agentaccess.UpdateExternalSubjectDisplayRefInput) (agentaccess.ExternalSubjectPublicView, error)
}

type AgentAccessManagementRepository interface {
	ListClients(context.Context, string) ([]agentaccess.AgentAccessClient, error)
	GetClient(context.Context, string, string) (agentaccess.AgentAccessClient, error)
	ListCredentials(context.Context, string, string) ([]agentaccess.Credential, error)
	ListGrants(context.Context, string, string) ([]agentaccess.AgentGrant, error)
	GetGrantByID(context.Context, string, string, string) (agentaccess.AgentGrant, error)
}

type AgentAccessManagementIdempotencyStore interface {
	ClaimManagementCommand(context.Context, agentaccess.ClaimManagementCommandInput) (agentaccess.ManagementCommand, bool, error)
	CompleteManagementCommand(context.Context, string, string, string, []byte, int, json.RawMessage) (agentaccess.ManagementCommand, error)
}

var ErrAgentAccessManagementCommandInProgress = errors.New("Agent Access management command is already in progress")

type managementIdempotencyEntry struct {
	fingerprint [32]byte
	status      int
	body        []byte
}

// AgentAccessManagementRoutes exposes only the user-management plane. Agent
// access tokens are authenticated by the separate data-plane router.
type AgentAccessManagementRoutes struct {
	service    AgentAccessManagementService
	repository AgentAccessManagementRepository
	authorizer WorkspaceAuthorizer
	commands   AgentAccessManagementIdempotencyStore

	idempotencyMu sync.Mutex
	idempotency   map[string]managementIdempotencyEntry
}

func NewAgentAccessManagementRoutes(
	service AgentAccessManagementService,
	repository AgentAccessManagementRepository,
	authorizer WorkspaceAuthorizer,
	commandStores ...AgentAccessManagementIdempotencyStore,
) (*AgentAccessManagementRoutes, error) {
	if service == nil || repository == nil || authorizer == nil || len(commandStores) > 1 {
		return nil, errors.New("Agent Access management route dependencies are required")
	}
	var commands AgentAccessManagementIdempotencyStore
	if len(commandStores) == 1 {
		if commandStores[0] == nil {
			return nil, errors.New("Agent Access management idempotency store is required")
		}
		commands = commandStores[0]
	}
	return &AgentAccessManagementRoutes{
		service: service, repository: repository, authorizer: authorizer,
		commands: commands, idempotency: make(map[string]managementIdempotencyEntry),
	}, nil
}

func (routes *AgentAccessManagementRoutes) RegisterV1(v1 V1Routes) {
	base := "/workspaces/:wid/agent-access"
	v1.Protected.GET(base+"/clients", routes.listClients)
	v1.Protected.POST(base+"/clients", routes.createClient)
	v1.Protected.GET(base+"/clients/:cid", routes.getClient)
	v1.Protected.POST(base+"/clients/:cid/__command/enable", routes.enableClient)
	v1.Protected.POST(base+"/clients/:cid/__command/disable", routes.disableClient)
	v1.Protected.POST(base+"/clients/:cid/__command/update-trusted-subject-issuer", routes.updateTrustedSubjectIssuer)
	v1.Protected.GET(base+"/clients/:cid/credentials", routes.listCredentials)
	v1.Protected.POST(base+"/clients/:cid/credentials", routes.addCredential)
	v1.Protected.POST(base+"/clients/:cid/credentials/:kid/__command/revoke", routes.revokeCredential)
	v1.Protected.GET(base+"/clients/:cid/grants", routes.listGrants)
	v1.Protected.POST(base+"/clients/:cid/grants", routes.createGrant)
	v1.Protected.POST(base+"/clients/:cid/grants/:gid/__command/revoke", routes.revokeGrant)
	v1.Protected.GET(base+"/clients/:cid/external-subjects", routes.listExternalSubjects)
	v1.Protected.GET(base+"/clients/:cid/external-subjects/:sid", routes.getExternalSubject)
	v1.Protected.POST(base+"/clients/:cid/external-subjects/:sid/__command/enable", routes.enableExternalSubject)
	v1.Protected.POST(base+"/clients/:cid/external-subjects/:sid/__command/disable", routes.disableExternalSubject)
	v1.Protected.POST(base+"/clients/:cid/external-subjects/:sid/__command/update-display-ref", routes.updateExternalSubjectDisplayRef)
}

type agentAccessClientDTO struct {
	ID                        string                               `json:"id"`
	WorkspaceID               string                               `json:"workspaceId"`
	ServicePrincipalID        string                               `json:"servicePrincipalId"`
	ClientID                  string                               `json:"clientId"`
	Name                      string                               `json:"name"`
	Status                    agentaccess.Status                   `json:"status"`
	AuthMethod                agentaccess.ClientAuthMethod         `json:"authMethod"`
	JWKSURI                   string                               `json:"jwksUri,omitempty"`
	TrustedSubjectIssuer      string                               `json:"trustedSubjectIssuer,omitempty"`
	TrustedSubjectAudience    string                               `json:"trustedSubjectAudience,omitempty"`
	TrustedSubjectJWKSURI     string                               `json:"trustedSubjectJwksUri,omitempty"`
	TrustedSubjectInlineJWKS  json.RawMessage                      `json:"trustedSubjectInlineJwks,omitempty"`
	TrustedSubjectAlgorithms  []string                             `json:"trustedSubjectAlgorithms,omitempty"`
	TrustedSubjectClaimPolicy *agentaccessauth.SubjectClaimPolicy  `json:"trustedSubjectClaimPolicy,omitempty"`
	AllowedCORSOrigins        []string                             `json:"allowedCorsOrigins"`
	TokenTTLSeconds           int                                  `json:"tokenTtlSeconds"`
	CreatedAt                 time.Time                            `json:"createdAt"`
	UpdatedAt                 time.Time                            `json:"updatedAt"`
	DisabledAt                *time.Time                           `json:"disabledAt,omitempty"`
	LockVersion               int64                                `json:"lockVersion"`
}

type agentAccessCredentialDTO struct {
	ID          string                     `json:"id"`
	Type        agentaccess.CredentialType `json:"type"`
	PublicHint  string                     `json:"publicHint"`
	ValidFrom   time.Time                  `json:"validFrom"`
	ExpiresAt   *time.Time                 `json:"expiresAt,omitempty"`
	LastUsedAt  *time.Time                 `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time                 `json:"revokedAt,omitempty"`
	CreatedAt   time.Time                  `json:"createdAt"`
	LockVersion int64                      `json:"lockVersion"`
}

type agentAccessGrantDTO struct {
	ID          string                   `json:"id"`
	AgentID     string                   `json:"agentId"`
	Scopes      []agentaccess.AgentScope `json:"scopes"`
	Policy      agentaccess.GrantPolicy  `json:"policy"`
	Status      agentaccess.GrantStatus  `json:"status"`
	ValidFrom   time.Time                `json:"validFrom"`
	ExpiresAt   *time.Time               `json:"expiresAt,omitempty"`
	RevokedAt   *time.Time               `json:"revokedAt,omitempty"`
	CreatedAt   time.Time                `json:"createdAt"`
	UpdatedAt   time.Time                `json:"updatedAt"`
	LockVersion int64                    `json:"lockVersion"`
}

type agentAccessPrincipalStateDTO struct {
	ID              string             `json:"id"`
	Status          agentaccess.Status `json:"status"`
	SecurityVersion int64              `json:"securityVersion"`
	LockVersion     int64              `json:"lockVersion"`
}

func toAgentAccessClientDTO(value agentaccess.AgentAccessClient) agentAccessClientDTO {
	dto := agentAccessClientDTO{
		ID: value.ID, WorkspaceID: value.WorkspaceID, ServicePrincipalID: value.ServicePrincipalID,
		ClientID: value.ClientID, Name: value.Name, Status: value.Status, AuthMethod: value.AuthMethod,
		JWKSURI: value.JWKSURI, TrustedSubjectIssuer: value.TrustedSubjectIssuer,
		TrustedSubjectAudience: value.TrustedSubjectAudience,
		TrustedSubjectJWKSURI:  value.TrustedSubjectJWKSURI,
		TrustedSubjectInlineJWKS: append(json.RawMessage(nil), value.TrustedSubjectInlineJWKS...),
		TrustedSubjectAlgorithms: append([]string(nil), value.TrustedSubjectAlgorithms...),
		AllowedCORSOrigins: append(
			make([]string, 0, len(value.AllowedCORSOrigins)), value.AllowedCORSOrigins...,
		),
		TokenTTLSeconds: value.TokenTTLSeconds, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		DisabledAt: value.DisabledAt, LockVersion: value.LockVersion,
	}
	if value.TrustedSubjectClaimPolicy != (agentaccessauth.SubjectClaimPolicy{}) {
		policy := value.TrustedSubjectClaimPolicy
		dto.TrustedSubjectClaimPolicy = &policy
	}
	return dto
}

func toAgentAccessCredentialDTO(value agentaccess.Credential) agentAccessCredentialDTO {
	return agentAccessCredentialDTO{
		ID: value.ID, Type: value.Type, PublicHint: value.PublicHint, ValidFrom: value.ValidFrom,
		ExpiresAt: value.ExpiresAt, LastUsedAt: value.LastUsedAt, RevokedAt: value.RevokedAt,
		CreatedAt: value.CreatedAt, LockVersion: value.LockVersion,
	}
}

func toAgentAccessGrantDTO(value agentaccess.AgentGrant) agentAccessGrantDTO {
	return agentAccessGrantDTO{
		ID: value.ID, AgentID: value.AgentID,
		Scopes: append(make([]agentaccess.AgentScope, 0, len(value.Scopes)), value.Scopes...),
		Policy: value.Policy, Status: value.Status, ValidFrom: value.ValidFrom, ExpiresAt: value.ExpiresAt,
		RevokedAt: value.RevokedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		LockVersion: value.LockVersion,
	}
}

func toAgentAccessPrincipalStateDTO(value agentaccess.ServicePrincipal) agentAccessPrincipalStateDTO {
	return agentAccessPrincipalStateDTO{
		ID: value.ID, Status: value.Status, SecurityVersion: value.SecurityVersion,
		LockVersion: value.LockVersion,
	}
}

func (routes *AgentAccessManagementRoutes) authorize(c *gin.Context) (Principal, bool) {
	principal, ok := PrincipalFrom(c.Request.Context())
	if !ok {
		RespondError(c, ErrUnauthenticated)
		return Principal{}, false
	}
	if _, err := routes.authorizer.AuthorizeWorkspace(
		c.Request.Context(), principal.UserID, c.Param("wid"), authz.ActionManage,
	); err != nil {
		RespondError(c, err)
		return Principal{}, false
	}
	return principal, true
}

func (routes *AgentAccessManagementRoutes) listClients(c *gin.Context) {
	if _, ok := routes.authorize(c); !ok {
		return
	}
	values, err := routes.repository.ListClients(c.Request.Context(), c.Param("wid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]agentAccessClientDTO, 0, len(values))
	for _, value := range values {
		items = append(items, toAgentAccessClientDTO(value))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (routes *AgentAccessManagementRoutes) getClient(c *gin.Context) {
	if _, ok := routes.authorize(c); !ok {
		return
	}
	value, err := routes.repository.GetClient(c.Request.Context(), c.Param("wid"), c.Param("cid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"client": toAgentAccessClientDTO(value)})
}

type createAgentAccessClientRequest struct {
	Name                      string                              `json:"name"`
	AuthMethod                agentaccess.ClientAuthMethod        `json:"authMethod"`
	JWKSURI                   string                              `json:"jwksUri,omitempty"`
	JWKThumbprint             string                              `json:"jwkThumbprint,omitempty"`
	CredentialPublicHint      string                              `json:"credentialPublicHint,omitempty"`
	TrustedSubjectIssuer      string                              `json:"trustedSubjectIssuer,omitempty"`
	TrustedSubjectAudience    string                              `json:"trustedSubjectAudience,omitempty"`
	TrustedSubjectJWKSURI     string                              `json:"trustedSubjectJwksUri,omitempty"`
	TrustedSubjectInlineJWKS  json.RawMessage                     `json:"trustedSubjectInlineJwks,omitempty"`
	TrustedSubjectAlgorithms  []string                            `json:"trustedSubjectAlgorithms,omitempty"`
	TrustedSubjectClaimPolicy *agentaccessauth.SubjectClaimPolicy `json:"trustedSubjectClaimPolicy,omitempty"`
	AllowedCORSOrigins        []string                            `json:"allowedCorsOrigins"`
	TokenTTLSeconds           int                                 `json:"tokenTtlSeconds,omitempty"`
}

func (routes *AgentAccessManagementRoutes) createClient(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request createAgentAccessClientRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	thumbprint, valid := decodeThumbprint(request.JWKThumbprint)
	if !valid {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "create-client", request, func() (int, any, any, error) {
		registerInput := agentaccess.RegisterClientInput{
			WorkspaceID: c.Param("wid"), Name: request.Name, ActorID: principal.UserID,
			AuthMethod: request.AuthMethod, JWKSURI: request.JWKSURI, JWKThumbprint: thumbprint,
			CredentialPublicHint:     request.CredentialPublicHint,
			TrustedSubjectIssuer:     request.TrustedSubjectIssuer,
			TrustedSubjectAudience:   request.TrustedSubjectAudience,
			TrustedSubjectJWKSURI:    request.TrustedSubjectJWKSURI,
			TrustedSubjectInlineJWKS: append(json.RawMessage(nil), request.TrustedSubjectInlineJWKS...),
			TrustedSubjectAlgorithms: append([]string(nil), request.TrustedSubjectAlgorithms...),
			AllowedCORSOrigins:       request.AllowedCORSOrigins, TokenTTLSeconds: request.TokenTTLSeconds,
		}
		if request.TrustedSubjectClaimPolicy != nil {
			registerInput.TrustedSubjectClaimPolicy = *request.TrustedSubjectClaimPolicy
		}
		value, err := routes.service.RegisterClient(c.Request.Context(), registerInput)
		if err != nil {
			return 0, nil, nil, err
		}
		public := gin.H{
			"client":     toAgentAccessClientDTO(value.Client),
			"credential": toAgentAccessCredentialDTO(value.Credential),
		}
		initial := gin.H{
			"client": public["client"], "credential": public["credential"],
		}
		if value.OneTimeSecret != "" {
			initial["secret"] = value.OneTimeSecret
		}
		return http.StatusCreated, initial, public, nil
	})
}

type clientStatusCommandRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (routes *AgentAccessManagementRoutes) enableClient(c *gin.Context) {
	routes.setClientStatus(c, agentaccess.StatusActive)
}

func (routes *AgentAccessManagementRoutes) disableClient(c *gin.Context) {
	routes.setClientStatus(c, agentaccess.StatusDisabled)
}

func (routes *AgentAccessManagementRoutes) setClientStatus(c *gin.Context, status agentaccess.Status) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request clientStatusCommandRequest
	if decodeJSON(c, &request) != nil || request.LockVersion < 1 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "set-client-status:"+string(status)+":"+c.Param("cid"), request,
		func() (int, any, any, error) {
			client, principalState, err := routes.service.SetClientStatus(
				c.Request.Context(), c.Param("wid"), c.Param("cid"), principal.UserID,
				status, request.LockVersion,
			)
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{
				"client":           toAgentAccessClientDTO(client),
				"servicePrincipal": toAgentAccessPrincipalStateDTO(principalState),
			}
			return http.StatusOK, body, body, nil
		})
}

type updateTrustedSubjectIssuerRequest struct {
	LockVersion               int64                               `json:"lockVersion"`
	Clear                     bool                                `json:"clear,omitempty"`
	TrustedSubjectIssuer      string                              `json:"trustedSubjectIssuer,omitempty"`
	TrustedSubjectAudience    string                              `json:"trustedSubjectAudience,omitempty"`
	TrustedSubjectJWKSURI     string                              `json:"trustedSubjectJwksUri,omitempty"`
	TrustedSubjectInlineJWKS  json.RawMessage                     `json:"trustedSubjectInlineJwks,omitempty"`
	TrustedSubjectAlgorithms  []string                            `json:"trustedSubjectAlgorithms,omitempty"`
	TrustedSubjectClaimPolicy *agentaccessauth.SubjectClaimPolicy `json:"trustedSubjectClaimPolicy,omitempty"`
}

func (routes *AgentAccessManagementRoutes) updateTrustedSubjectIssuer(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request updateTrustedSubjectIssuerRequest
	if decodeJSON(c, &request) != nil || request.LockVersion < 1 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "update-trusted-subject-issuer:"+c.Param("cid"), request,
		func() (int, any, any, error) {
			input := agentaccess.UpdateTrustedSubjectIssuerInput{
				WorkspaceID: c.Param("wid"), ClientID: c.Param("cid"),
				ActorID: principal.UserID, ExpectedLockVersion: request.LockVersion,
				Clear: request.Clear,
				Config: agentaccess.TrustedSubjectIssuerConfig{
					Issuer: request.TrustedSubjectIssuer, Audience: request.TrustedSubjectAudience,
					JWKSURI: request.TrustedSubjectJWKSURI,
					InlineJWKS: append(json.RawMessage(nil), request.TrustedSubjectInlineJWKS...),
					Algorithms: append([]string(nil), request.TrustedSubjectAlgorithms...),
				},
			}
			if request.TrustedSubjectClaimPolicy != nil {
				input.Config.ClaimPolicy = *request.TrustedSubjectClaimPolicy
			}
			client, principalState, err := routes.service.UpdateTrustedSubjectIssuer(
				c.Request.Context(), input,
			)
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{
				"client":           toAgentAccessClientDTO(client),
				"servicePrincipal": toAgentAccessPrincipalStateDTO(principalState),
			}
			return http.StatusOK, body, body, nil
		})
}

func (routes *AgentAccessManagementRoutes) listCredentials(c *gin.Context) {
	if _, ok := routes.authorize(c); !ok {
		return
	}
	values, err := routes.repository.ListCredentials(c.Request.Context(), c.Param("wid"), c.Param("cid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]agentAccessCredentialDTO, 0, len(values))
	for _, value := range values {
		items = append(items, toAgentAccessCredentialDTO(value))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type addAgentAccessCredentialRequest struct {
	Type                 agentaccess.CredentialType `json:"type"`
	JWKThumbprint        string                     `json:"jwkThumbprint,omitempty"`
	PublicHint           string                     `json:"publicHint,omitempty"`
	ExpiresAt            *time.Time                 `json:"expiresAt,omitempty"`
	ReplacesCredentialID string                     `json:"replacesCredentialId,omitempty"`
	ReplacesLockVersion  int64                      `json:"replacesLockVersion,omitempty"`
	OverlapSeconds       int64                      `json:"overlapSeconds,omitempty"`
}

func (routes *AgentAccessManagementRoutes) addCredential(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request addAgentAccessCredentialRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	thumbprint, valid := decodeThumbprint(request.JWKThumbprint)
	if !valid || request.OverlapSeconds < 0 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "add-credential:"+c.Param("cid"), request,
		func() (int, any, any, error) {
			value, err := routes.service.AddCredential(c.Request.Context(), agentaccess.AddCredentialInput{
				WorkspaceID: c.Param("wid"), ClientID: c.Param("cid"), ActorID: principal.UserID,
				Type: request.Type, JWKThumbprint: thumbprint, PublicHint: request.PublicHint,
				ExpiresAt: request.ExpiresAt, ReplacesCredentialID: request.ReplacesCredentialID,
				ReplacesExpectedLockVersion: request.ReplacesLockVersion,
				Overlap:                     time.Duration(request.OverlapSeconds) * time.Second,
			})
			if err != nil {
				return 0, nil, nil, err
			}
			public := gin.H{
				"credential":                  toAgentAccessCredentialDTO(value.Credential),
				"replacedCredentialExpiresAt": value.ReplacedCredentialExpiresAt,
			}
			initial := gin.H{
				"credential":                  public["credential"],
				"replacedCredentialExpiresAt": public["replacedCredentialExpiresAt"],
			}
			if value.OneTimeSecret != "" {
				initial["secret"] = value.OneTimeSecret
			}
			return http.StatusCreated, initial, public, nil
		})
}

type revokeAgentAccessRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (routes *AgentAccessManagementRoutes) revokeCredential(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request revokeAgentAccessRequest
	if decodeJSON(c, &request) != nil || request.LockVersion < 1 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "revoke-credential:"+c.Param("cid")+":"+c.Param("kid"), request,
		func() (int, any, any, error) {
			credential, principalState, err := routes.service.RevokeCredential(
				c.Request.Context(), c.Param("wid"), c.Param("cid"), c.Param("kid"),
				principal.UserID, request.LockVersion,
			)
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{
				"credential":       toAgentAccessCredentialDTO(credential),
				"servicePrincipal": toAgentAccessPrincipalStateDTO(principalState),
			}
			return http.StatusOK, body, body, nil
		})
}

func (routes *AgentAccessManagementRoutes) listGrants(c *gin.Context) {
	if _, ok := routes.authorize(c); !ok {
		return
	}
	values, err := routes.repository.ListGrants(c.Request.Context(), c.Param("wid"), c.Param("cid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]agentAccessGrantDTO, 0, len(values))
	for _, value := range values {
		items = append(items, toAgentAccessGrantDTO(value))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createAgentAccessGrantRequest struct {
	AgentID   string                   `json:"agentId"`
	Scopes    []agentaccess.AgentScope `json:"scopes"`
	Policy    agentaccess.GrantPolicy  `json:"policy"`
	ValidFrom *time.Time               `json:"validFrom,omitempty"`
	ExpiresAt *time.Time               `json:"expiresAt,omitempty"`
}

func (routes *AgentAccessManagementRoutes) createGrant(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request createAgentAccessGrantRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "create-grant:"+c.Param("cid"), request,
		func() (int, any, any, error) {
			validFrom := time.Now().UTC()
			if request.ValidFrom != nil {
				validFrom = request.ValidFrom.UTC()
			}
			value, err := routes.service.GrantAgent(c.Request.Context(), agentaccess.CreateGrantInput{
				ID: uuid.NewString(), WorkspaceID: c.Param("wid"), ClientID: c.Param("cid"),
				AgentID: request.AgentID, Scopes: request.Scopes, Policy: request.Policy,
				ValidFrom: validFrom, ExpiresAt: request.ExpiresAt, ActorID: principal.UserID,
			})
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{"grant": toAgentAccessGrantDTO(value)}
			return http.StatusCreated, body, body, nil
		})
}

func (routes *AgentAccessManagementRoutes) revokeGrant(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request revokeAgentAccessRequest
	if decodeJSON(c, &request) != nil || request.LockVersion < 1 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "revoke-grant:"+c.Param("cid")+":"+c.Param("gid"), request,
		func() (int, any, any, error) {
			current, err := routes.repository.GetGrantByID(
				c.Request.Context(), c.Param("wid"), c.Param("cid"), c.Param("gid"),
			)
			if err != nil {
				return 0, nil, nil, err
			}
			grant, principalState, err := routes.service.RevokeGrant(
				c.Request.Context(), c.Param("wid"), c.Param("cid"), current.AgentID,
				c.Param("gid"), principal.UserID, request.LockVersion,
			)
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{
				"grant":            toAgentAccessGrantDTO(grant),
				"servicePrincipal": toAgentAccessPrincipalStateDTO(principalState),
			}
			return http.StatusOK, body, body, nil
		})
}

type agentAccessExternalSubjectDTO struct {
	ID          string             `json:"id"`
	WorkspaceID string             `json:"workspaceId"`
	ClientID    string             `json:"clientId"`
	Issuer      string             `json:"issuer"`
	DisplayRef  string             `json:"displayRef,omitempty"`
	Status      agentaccess.Status `json:"status"`
	FirstSeenAt time.Time          `json:"firstSeenAt"`
	LastSeenAt  time.Time          `json:"lastSeenAt"`
	DisabledAt  *time.Time         `json:"disabledAt,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	LockVersion int64              `json:"lockVersion"`
}

func toAgentAccessExternalSubjectDTO(value agentaccess.ExternalSubjectPublicView) agentAccessExternalSubjectDTO {
	return agentAccessExternalSubjectDTO{
		ID: value.ID, WorkspaceID: value.WorkspaceID, ClientID: value.ClientID,
		Issuer: value.Issuer, DisplayRef: value.DisplayRef, Status: value.Status,
		FirstSeenAt: value.FirstSeenAt, LastSeenAt: value.LastSeenAt,
		DisabledAt: value.DisabledAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		LockVersion: value.LockVersion,
	}
}

func (routes *AgentAccessManagementRoutes) listExternalSubjects(c *gin.Context) {
	if _, ok := routes.authorize(c); !ok {
		return
	}
	values, err := routes.service.ListExternalSubjectPublicViews(
		c.Request.Context(), c.Param("wid"), c.Param("cid"),
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]agentAccessExternalSubjectDTO, 0, len(values))
	for _, value := range values {
		items = append(items, toAgentAccessExternalSubjectDTO(value))
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"retentionPolicy": agentaccess.DefaultWorkspaceExternalSubjectRetentionPolicy(),
	})
}

func (routes *AgentAccessManagementRoutes) getExternalSubject(c *gin.Context) {
	if _, ok := routes.authorize(c); !ok {
		return
	}
	value, err := routes.service.GetExternalSubjectPublicView(
		c.Request.Context(), c.Param("wid"), c.Param("cid"), c.Param("sid"),
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"externalSubject": toAgentAccessExternalSubjectDTO(value)})
}

type externalSubjectStatusCommandRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (routes *AgentAccessManagementRoutes) enableExternalSubject(c *gin.Context) {
	routes.setExternalSubjectStatus(c, agentaccess.StatusActive)
}

func (routes *AgentAccessManagementRoutes) disableExternalSubject(c *gin.Context) {
	routes.setExternalSubjectStatus(c, agentaccess.StatusDisabled)
}

func (routes *AgentAccessManagementRoutes) setExternalSubjectStatus(
	c *gin.Context,
	status agentaccess.Status,
) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request externalSubjectStatusCommandRequest
	if decodeJSON(c, &request) != nil || request.LockVersion < 1 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "set-external-subject-status:"+string(status)+":"+c.Param("sid"), request,
		func() (int, any, any, error) {
			value, err := routes.service.SetExternalSubjectStatus(c.Request.Context(), agentaccess.SetExternalSubjectStatusInput{
				WorkspaceID: c.Param("wid"), ClientID: c.Param("cid"), SubjectID: c.Param("sid"),
				ActorID: principal.UserID, Status: status, ExpectedLockVersion: request.LockVersion,
			})
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{"externalSubject": toAgentAccessExternalSubjectDTO(value)}
			return http.StatusOK, body, body, nil
		})
}

type updateExternalSubjectDisplayRefRequest struct {
	LockVersion int64  `json:"lockVersion"`
	DisplayRef  string `json:"displayRef"`
}

func (routes *AgentAccessManagementRoutes) updateExternalSubjectDisplayRef(c *gin.Context) {
	principal, ok := routes.authorize(c)
	if !ok {
		return
	}
	var request updateExternalSubjectDisplayRefRequest
	if decodeJSON(c, &request) != nil || request.LockVersion < 1 {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	routes.executeIdempotent(c, "update-external-subject-display-ref:"+c.Param("sid"), request,
		func() (int, any, any, error) {
			value, err := routes.service.UpdateExternalSubjectDisplayRef(c.Request.Context(), agentaccess.UpdateExternalSubjectDisplayRefInput{
				WorkspaceID: c.Param("wid"), ClientID: c.Param("cid"), SubjectID: c.Param("sid"),
				ActorID: principal.UserID, DisplayRef: request.DisplayRef,
				ExpectedLockVersion: request.LockVersion,
			})
			if err != nil {
				return 0, nil, nil, err
			}
			body := gin.H{"externalSubject": toAgentAccessExternalSubjectDTO(value)}
			return http.StatusOK, body, body, nil
		})
}

func decodeThumbprint(value string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, false
	}
	return decoded, true
}

func (routes *AgentAccessManagementRoutes) executeIdempotent(
	c *gin.Context,
	operation string,
	request any,
	action func() (status int, initialBody any, replayBody any, err error),
) {
	operation = strings.ToLower(operation)
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if _, err := uuid.Parse(key); err != nil {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	raw, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Request   any    `json:"request"`
	}{Operation: operation, Request: request})
	if err != nil {
		RespondError(c, agentaccess.ErrManagementInvalid)
		return
	}
	fingerprint := sha256.Sum256(raw)
	cacheKey := principal.UserID + ":" + c.Param("wid") + ":" + key

	// Serialize an identical management command so concurrent retries cannot
	// execute twice. Successful replay bodies deliberately exclude new secrets.
	routes.idempotencyMu.Lock()
	defer routes.idempotencyMu.Unlock()
	if existing, exists := routes.idempotency[cacheKey]; exists {
		if existing.fingerprint != fingerprint {
			RespondError(c, ErrAAPRunIdempotencyConflict)
			return
		}
		c.Data(existing.status, "application/json; charset=utf-8", existing.body)
		return
	}
	if routes.commands != nil {
		command, claimed, err := routes.commands.ClaimManagementCommand(
			c.Request.Context(), agentaccess.ClaimManagementCommandInput{
				WorkspaceID: c.Param("wid"), ActorID: principal.UserID,
				IdempotencyKey: key, Operation: operation, RequestHash: fingerprint[:],
			},
		)
		if err != nil {
			RespondError(c, err)
			return
		}
		if !claimed {
			if command.Operation != operation || !bytes.Equal(command.RequestHash, fingerprint[:]) {
				RespondError(c, ErrAAPRunIdempotencyConflict)
				return
			}
			if command.State != agentaccess.ManagementCommandCompleted {
				RespondError(c, ErrAgentAccessManagementCommandInProgress)
				return
			}
			routes.idempotency[cacheKey] = managementIdempotencyEntry{
				fingerprint: fingerprint, status: command.ResponseStatus,
				body: append([]byte(nil), command.ResponseBody...),
			}
			c.Data(command.ResponseStatus, "application/json; charset=utf-8", command.ResponseBody)
			return
		}
	}
	status, initialBody, replayBody, err := action()
	if err != nil {
		RespondError(c, err)
		return
	}
	replay, err := json.Marshal(replayBody)
	if err != nil {
		RespondError(c, err)
		return
	}
	if routes.commands != nil {
		if _, err := routes.commands.CompleteManagementCommand(
			c.Request.Context(), c.Param("wid"), principal.UserID, key,
			fingerprint[:], status, replay,
		); err != nil {
			RespondError(c, err)
			return
		}
	}
	routes.idempotency[cacheKey] = managementIdempotencyEntry{
		fingerprint: fingerprint, status: status, body: replay,
	}
	c.JSON(status, initialBody)
}
