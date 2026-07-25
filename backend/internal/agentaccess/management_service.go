package agentaccess

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"

	"github.com/google/uuid"
)

var (
	ErrManagementInvalid    = errors.New("Agent Access management input is invalid")
	ErrLastActiveCredential = errors.New("last active Agent Access Credential cannot be revoked")
	ErrRotationLimit        = errors.New("Agent Access Credential rotation already has two active credentials")
)

const MaxCredentialRotationOverlap = 24 * time.Hour

type ManagementService struct {
	repository      *Repository
	pepper          []byte
	random          io.Reader
	now             func() time.Time
	audit           ManagementAuditSink
	securityChanges SecurityChangePublisher
}

func NewManagementService(
	repository *Repository,
	pepper []byte,
	options ...ManagementOption,
) (*ManagementService, error) {
	if repository == nil || repository.db == nil || len(pepper) < 32 {
		return nil, ErrManagementInvalid
	}
	service := &ManagementService{
		repository: repository, pepper: append([]byte(nil), pepper...), random: rand.Reader,
		now: func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if option == nil {
			return nil, ErrManagementInvalid
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

type RegisterClientInput struct {
	WorkspaceID, Name, ActorID string
	AuthMethod                 ClientAuthMethod
	JWKSURI                    string
	JWKThumbprint              []byte
	CredentialPublicHint       string
	TrustedSubjectIssuer       string
	TrustedSubjectAudience     string
	TrustedSubjectJWKSURI      string
	TrustedSubjectInlineJWKS   json.RawMessage
	TrustedSubjectAlgorithms   []string
	TrustedSubjectClaimPolicy  agentaccessauth.SubjectClaimPolicy
	AllowedCORSOrigins         []string
	TokenTTLSeconds            int
}

type ClientRegistration struct {
	Principal     ServicePrincipal
	Client        AgentAccessClient
	Credential    Credential
	OneTimeSecret string `json:"secret,omitempty"`
}

func (service *ManagementService) RegisterClient(
	ctx context.Context,
	input RegisterClientInput,
) (ClientRegistration, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(input.WorkspaceID) || !validRepositoryUUID(input.ActorID) ||
		strings.TrimSpace(input.Name) == "" || !knownRepositoryAuthMethod(input.AuthMethod) {
		return ClientRegistration{}, ErrManagementInvalid
	}
	if input.AuthMethod == ClientAuthMethodPrivateKey &&
		(strings.TrimSpace(input.JWKSURI) == "" || len(input.JWKThumbprint) != 32) {
		return ClientRegistration{}, ErrManagementInvalid
	}
	if input.AuthMethod == ClientAuthMethodSecretBasic &&
		(strings.TrimSpace(input.JWKSURI) != "" || len(input.JWKThumbprint) != 0) {
		return ClientRegistration{}, ErrManagementInvalid
	}
	principalID, clientRowID, credentialID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	publicClientID, err := service.generateOpaque("awcl_")
	if err != nil {
		return ClientRegistration{}, err
	}
	credentialType := CredentialTypeJWK
	var secret string
	var secretHash, jwkThumbprint []byte
	publicHint := strings.TrimSpace(input.CredentialPublicHint)
	if input.AuthMethod == ClientAuthMethodSecretBasic {
		credentialType = CredentialTypeClientSecret
		secret, secretHash, publicHint, err = service.generateClientSecret(credentialID)
		if err != nil {
			return ClientRegistration{}, err
		}
	} else {
		jwkThumbprint = append([]byte(nil), input.JWKThumbprint...)
		if publicHint == "" {
			return ClientRegistration{}, ErrManagementInvalid
		}
	}
	origins := input.AllowedCORSOrigins
	if origins == nil {
		origins = []string{}
	}
	if len(origins) > 0 {
		// Reject "*", wildcards, and non-exact origins at registration time.
		normalized, originErr := agentaccessauth.ValidateExactOrigins(origins)
		if originErr != nil {
			return ClientRegistration{}, ErrManagementInvalid
		}
		origins = normalized
	}
	cors, err := marshalManagementJSON(origins)
	if err != nil {
		return ClientRegistration{}, ErrManagementInvalid
	}
	ttl := input.TokenTTLSeconds
	if ttl == 0 {
		ttl = DefaultAccessTokenTTLSeconds
	}
	if !validAccessTokenTTLSeconds(ttl) {
		return ClientRegistration{}, ErrManagementInvalid
	}
	trust, err := encodeTrustedSubjectColumns(TrustedSubjectIssuerConfig{
		Issuer: input.TrustedSubjectIssuer, Audience: input.TrustedSubjectAudience,
		JWKSURI: input.TrustedSubjectJWKSURI, InlineJWKS: input.TrustedSubjectInlineJWKS,
		Algorithms: input.TrustedSubjectAlgorithms, ClaimPolicy: input.TrustedSubjectClaimPolicy,
	})
	if err != nil {
		return ClientRegistration{}, ErrManagementInvalid
	}
	now := service.now()
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ClientRegistration{}, fmt.Errorf("begin Agent Access Client registration: %w", err)
	}
	defer tx.Rollback()
	principal, err := scanServicePrincipal(tx.QueryRowContext(ctx, `
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,$3,$4,$4)
		RETURNING id,workspace_id,name,status,security_version,created_by,updated_by,
		 created_at,updated_at,disabled_at,lock_version
	`, principalID, input.WorkspaceID, strings.TrimSpace(input.Name), input.ActorID))
	if err != nil {
		return ClientRegistration{}, mapRepositoryWrite("register Service Principal", err)
	}
	client, err := scanClient(tx.QueryRowContext(ctx, `
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,jwks_uri,
		 trusted_subject_issuer,trusted_subject_audience,trusted_subject_jwks_uri,
		 trusted_subject_inline_jwks,trusted_subject_algorithms,trusted_subject_claim_policy,
		 allowed_cors_origins,token_ttl_seconds,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
		RETURNING `+clientColumns+`
	`, clientRowID, input.WorkspaceID, principalID, publicClientID,
		strings.TrimSpace(input.Name), input.AuthMethod, nullableRepositoryString(input.JWKSURI),
		trust.issuer, trust.audience, trust.jwksURI, trust.inlineJWKS, trust.algorithms,
		trust.claimPolicy, cors, ttl, input.ActorID))
	if err != nil {
		return ClientRegistration{}, mapRepositoryWrite("register Agent Access Client", err)
	}
	evidence, err := scanCredentialEvidence(tx.QueryRowContext(ctx, `
		INSERT INTO agent_access_credentials(
		 id,workspace_id,client_id,credential_type,secret_hash,jwk_thumbprint,
		 public_hint,valid_from,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id,workspace_id,client_id,credential_type,public_hint,
		 valid_from,expires_at,last_used_at,revoked_at,revoked_by,created_by,created_at,
		 lock_version,secret_hash,jwk_thumbprint,certificate_thumbprint
	`, credentialID, input.WorkspaceID, clientRowID, credentialType,
		nullableRepositoryBytes(secretHash), nullableRepositoryBytes(jwkThumbprint),
		publicHint, now, input.ActorID))
	if err != nil {
		return ClientRegistration{}, mapRepositoryWrite("register Agent Access Credential", err)
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionClientCreated, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "AGENT_ACCESS_CLIENT", ResourceID: client.ID,
		After:    clientManagementAuditState(client, principal.SecurityVersion),
		Metadata: map[string]any{"servicePrincipalId": principal.ID},
	}); err != nil {
		return ClientRegistration{}, err
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionCredentialCreated, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "AGENT_ACCESS_CREDENTIAL", ResourceID: evidence.ID,
		After:    credentialManagementAuditState(evidence.Credential),
		Metadata: map[string]any{"clientId": client.ID, "initial": true},
	}); err != nil {
		return ClientRegistration{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClientRegistration{}, mapRepositoryWrite("commit Agent Access Client registration", err)
	}
	return ClientRegistration{
		Principal: principal, Client: client, Credential: evidence.Credential,
		OneTimeSecret: secret,
	}, nil
}

type AddCredentialInput struct {
	WorkspaceID, ClientID, ActorID string
	Type                           CredentialType
	JWKThumbprint                  []byte
	PublicHint                     string
	ExpiresAt                      *time.Time
	ReplacesCredentialID           string
	ReplacesExpectedLockVersion    int64
	Overlap                        time.Duration
}

type IssuedCredential struct {
	Credential                  Credential
	OneTimeSecret               string     `json:"secret,omitempty"`
	ReplacedCredentialExpiresAt *time.Time `json:"replacedCredentialExpiresAt,omitempty"`
}

func (service *ManagementService) AddCredential(
	ctx context.Context,
	input AddCredentialInput,
) (IssuedCredential, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(input.WorkspaceID) || !validRepositoryUUID(input.ClientID) ||
		!validRepositoryUUID(input.ActorID) {
		return IssuedCredential{}, ErrManagementInvalid
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedCredential{}, err
	}
	defer tx.Rollback()
	var authMethod, status string
	err = tx.QueryRowContext(ctx, `
		SELECT auth_method,status FROM agent_access_clients
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, input.WorkspaceID, input.ClientID).Scan(&authMethod, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return IssuedCredential{}, ErrRepositoryNotFound
	}
	if err != nil {
		return IssuedCredential{}, err
	}
	if status != string(StatusActive) ||
		(authMethod == string(ClientAuthMethodSecretBasic) && input.Type != CredentialTypeClientSecret) ||
		(authMethod == string(ClientAuthMethodPrivateKey) && input.Type != CredentialTypeJWK) {
		return IssuedCredential{}, ErrManagementInvalid
	}
	now := service.now()
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM agent_access_credentials
		WHERE workspace_id=$1 AND client_id=$2 AND credential_type=$3
		 AND revoked_at IS NULL AND valid_from <= $4 AND (expires_at IS NULL OR expires_at > $4)
	`, input.WorkspaceID, input.ClientID, input.Type, now).Scan(&active); err != nil {
		return IssuedCredential{}, err
	}
	if active >= 2 {
		return IssuedCredential{}, ErrRotationLimit
	}
	var replacementExpiry *time.Time
	if active == 1 {
		if !validRepositoryUUID(input.ReplacesCredentialID) ||
			input.ReplacesExpectedLockVersion < 1 || input.Overlap <= 0 ||
			input.Overlap > MaxCredentialRotationOverlap {
			return IssuedCredential{}, ErrManagementInvalid
		}
		var replacedType string
		var replacedLock int64
		var revokedAt sql.NullTime
		if err := tx.QueryRowContext(ctx, `
			SELECT credential_type,lock_version,revoked_at
			FROM agent_access_credentials
			WHERE workspace_id=$1 AND client_id=$2 AND id=$3 FOR UPDATE
		`, input.WorkspaceID, input.ClientID, input.ReplacesCredentialID).
			Scan(&replacedType, &replacedLock, &revokedAt); errors.Is(err, sql.ErrNoRows) {
			return IssuedCredential{}, ErrRepositoryNotFound
		} else if err != nil {
			return IssuedCredential{}, err
		}
		if replacedType != string(input.Type) || replacedLock != input.ReplacesExpectedLockVersion || revokedAt.Valid {
			return IssuedCredential{}, ErrRepositoryConflict
		}
		deadline := now.Add(input.Overlap).UTC()
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_access_credentials
			SET expires_at=$4,lock_version=lock_version+1
			WHERE workspace_id=$1 AND client_id=$2 AND id=$3 AND lock_version=$5
		`, input.WorkspaceID, input.ClientID, input.ReplacesCredentialID,
			deadline, input.ReplacesExpectedLockVersion)
		if err != nil {
			return IssuedCredential{}, mapRepositoryWrite("schedule replaced Credential expiry", err)
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return IssuedCredential{}, ErrRepositoryConflict
		}
		replacementExpiry = &deadline
	} else if input.ReplacesCredentialID != "" || input.ReplacesExpectedLockVersion != 0 || input.Overlap != 0 {
		return IssuedCredential{}, ErrManagementInvalid
	}
	credentialID := uuid.NewString()
	var secret string
	var secretHash, jwk []byte
	publicHint := strings.TrimSpace(input.PublicHint)
	if input.Type == CredentialTypeClientSecret {
		secret, secretHash, publicHint, err = service.generateClientSecret(credentialID)
		if err != nil {
			return IssuedCredential{}, err
		}
	} else {
		if len(input.JWKThumbprint) != 32 || publicHint == "" {
			return IssuedCredential{}, ErrManagementInvalid
		}
		jwk = append([]byte(nil), input.JWKThumbprint...)
	}
	evidence, err := scanCredentialEvidence(tx.QueryRowContext(ctx, `
		INSERT INTO agent_access_credentials(
		 id,workspace_id,client_id,credential_type,secret_hash,jwk_thumbprint,
		 public_hint,valid_from,expires_at,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id,workspace_id,client_id,credential_type,public_hint,
		 valid_from,expires_at,last_used_at,revoked_at,revoked_by,created_by,created_at,
		 lock_version,secret_hash,jwk_thumbprint,certificate_thumbprint
	`, credentialID, input.WorkspaceID, input.ClientID, input.Type,
		nullableRepositoryBytes(secretHash), nullableRepositoryBytes(jwk), publicHint,
		now, input.ExpiresAt, input.ActorID))
	if err != nil {
		return IssuedCredential{}, mapRepositoryWrite("add Agent Access Credential", err)
	}
	action := ActionCredentialCreated
	metadata := map[string]any{"clientId": input.ClientID}
	if input.ReplacesCredentialID != "" {
		action = ActionCredentialRotated
		metadata["replacedCredentialId"] = input.ReplacesCredentialID
		metadata["overlapSeconds"] = int64(input.Overlap / time.Second)
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: action, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "AGENT_ACCESS_CREDENTIAL", ResourceID: evidence.ID,
		After: credentialManagementAuditState(evidence.Credential), Metadata: metadata,
	}); err != nil {
		return IssuedCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return IssuedCredential{}, mapRepositoryWrite("commit Agent Access Credential", err)
	}
	return IssuedCredential{
		Credential: evidence.Credential, OneTimeSecret: secret,
		ReplacedCredentialExpiresAt: replacementExpiry,
	}, nil
}

func (service *ManagementService) RevokeCredential(
	ctx context.Context,
	workspaceID, clientID, credentialID, actorID string,
	expectedLockVersion int64,
) (Credential, ServicePrincipal, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(credentialID) || !validRepositoryUUID(actorID) ||
		expectedLockVersion < 1 {
		return Credential{}, ServicePrincipal{}, ErrManagementInvalid
	}
	now := service.now()
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Credential{}, ServicePrincipal{}, err
	}
	defer tx.Rollback()
	var clientStatus, principalID string
	err = tx.QueryRowContext(ctx, `
		SELECT status,service_principal_id FROM agent_access_clients
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, workspaceID, clientID).Scan(&clientStatus, &principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ServicePrincipal{}, ErrRepositoryNotFound
	}
	if err != nil {
		return Credential{}, ServicePrincipal{}, err
	}
	var credentialType string
	var lockVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT credential_type,lock_version FROM agent_access_credentials
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3 FOR UPDATE
	`, workspaceID, clientID, credentialID).Scan(&credentialType, &lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ServicePrincipal{}, ErrRepositoryNotFound
	}
	if err != nil {
		return Credential{}, ServicePrincipal{}, err
	}
	if lockVersion != expectedLockVersion {
		return Credential{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	if clientStatus == string(StatusActive) {
		var alternatives int
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM agent_access_credentials
			WHERE workspace_id=$1 AND client_id=$2 AND credential_type=$3 AND id<>$4
			 AND revoked_at IS NULL AND valid_from <= $5 AND (expires_at IS NULL OR expires_at > $5)
		`, workspaceID, clientID, credentialType, credentialID, now).Scan(&alternatives); err != nil {
			return Credential{}, ServicePrincipal{}, err
		}
		if alternatives == 0 {
			return Credential{}, ServicePrincipal{}, ErrLastActiveCredential
		}
	}
	evidence, err := scanCredentialEvidence(tx.QueryRowContext(ctx, `
		UPDATE agent_access_credentials
		SET revoked_at=$4,revoked_by=$5,lock_version=lock_version+1
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3 AND lock_version=$6 AND revoked_at IS NULL
		RETURNING id,workspace_id,client_id,credential_type,public_hint,
		 valid_from,expires_at,last_used_at,revoked_at,revoked_by,created_by,created_at,
		 lock_version,secret_hash,jwk_thumbprint,certificate_thumbprint
	`, workspaceID, clientID, credentialID, now, actorID, expectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	if err != nil {
		return Credential{}, ServicePrincipal{}, mapRepositoryWrite("revoke Credential", err)
	}
	principal, err := bumpPrincipalSecurityVersion(ctx, tx, workspaceID, principalID, actorID)
	if err != nil {
		return Credential{}, ServicePrincipal{}, err
	}
	change := SecurityChangeEvent{
		WorkspaceID: workspaceID, ClientID: clientID, SecurityVersion: principal.SecurityVersion,
	}
	credentialAfter := credentialManagementAuditState(evidence.Credential)
	credentialAfter["securityVersion"] = principal.SecurityVersion
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionCredentialRevoked, WorkspaceID: workspaceID, ActorID: actorID,
		ResourceType: "AGENT_ACCESS_CREDENTIAL", ResourceID: evidence.ID,
		Before: map[string]any{
			"revoked": false, "lockVersion": expectedLockVersion,
			"securityVersion": principal.SecurityVersion - 1,
		},
		After:    credentialAfter,
		Metadata: map[string]any{"clientId": clientID}, SecurityChange: &change,
	}); err != nil {
		return Credential{}, ServicePrincipal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Credential{}, ServicePrincipal{}, err
	}
	service.publishSecurityChange(ctx, change)
	return evidence.Credential, principal, nil
}

func (service *ManagementService) SetClientStatus(
	ctx context.Context,
	workspaceID, clientID, actorID string,
	status Status,
	expectedLockVersion int64,
) (AgentAccessClient, ServicePrincipal, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(actorID) || !knownRepositoryStatus(status) || expectedLockVersion < 1 {
		return AgentAccessClient{}, ServicePrincipal{}, ErrManagementInvalid
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	defer tx.Rollback()
	var principalID, currentStatus, authMethod string
	var actualLock int64
	err = tx.QueryRowContext(ctx, `
		SELECT service_principal_id,lock_version,status,auth_method FROM agent_access_clients
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, workspaceID, clientID).Scan(&principalID, &actualLock, &currentStatus, &authMethod)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAccessClient{}, ServicePrincipal{}, ErrRepositoryNotFound
	}
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	if actualLock != expectedLockVersion {
		return AgentAccessClient{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	if currentStatus == string(status) {
		return AgentAccessClient{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	if status == StatusActive {
		credentialType := CredentialTypeClientSecret
		if authMethod == string(ClientAuthMethodPrivateKey) {
			credentialType = CredentialTypeJWK
		}
		var active int
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM agent_access_credentials
			WHERE workspace_id=$1 AND client_id=$2 AND credential_type=$3
			 AND revoked_at IS NULL AND valid_from <= $4 AND (expires_at IS NULL OR expires_at > $4)
		`, workspaceID, clientID, credentialType, service.now()).Scan(&active); err != nil {
			return AgentAccessClient{}, ServicePrincipal{}, err
		}
		if active == 0 {
			return AgentAccessClient{}, ServicePrincipal{}, ErrLastActiveCredential
		}
	}
	client, err := scanClient(tx.QueryRowContext(ctx, `
		UPDATE agent_access_clients c
		SET status=$3,disabled_at=CASE WHEN $3='DISABLED' THEN clock_timestamp() ELSE NULL END,
		 updated_at=clock_timestamp(),updated_by=$4,lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$5
		RETURNING `+clientReturningColumns+`
	`, workspaceID, clientID, status, actorID, expectedLockVersion))
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, mapRepositoryWrite("set Client status", err)
	}
	principal, err := bumpPrincipalSecurityVersion(ctx, tx, workspaceID, principalID, actorID)
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	change := SecurityChangeEvent{
		WorkspaceID: workspaceID, ClientID: clientID, SecurityVersion: principal.SecurityVersion,
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionClientStatusChanged, WorkspaceID: workspaceID, ActorID: actorID,
		ResourceType: "AGENT_ACCESS_CLIENT", ResourceID: client.ID,
		Before: map[string]any{
			"status": currentStatus, "lockVersion": expectedLockVersion,
			"securityVersion": principal.SecurityVersion - 1,
		},
		After:          clientManagementAuditState(client, principal.SecurityVersion),
		SecurityChange: &change,
	}); err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	service.publishSecurityChange(ctx, change)
	return client, principal, nil
}

func (service *ManagementService) GrantAgent(
	ctx context.Context,
	input CreateGrantInput,
) (AgentGrant, error) {
	if service == nil || service.repository == nil || !validCreateGrantInput(ctx, input) {
		return AgentGrant{}, ErrManagementInvalid
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentGrant{}, err
	}
	defer tx.Rollback()
	grant, err := createGrantWithQueryer(ctx, tx, input)
	if err != nil {
		return AgentGrant{}, err
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionGrantCreated, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "AGENT_ACCESS_GRANT", ResourceID: grant.ID,
		After:    grantManagementAuditState(grant),
		Metadata: map[string]any{"clientId": input.ClientID, "agentId": input.AgentID},
	}); err != nil {
		return AgentGrant{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentGrant{}, err
	}
	return grant, nil
}

func (service *ManagementService) RevokeGrant(
	ctx context.Context,
	workspaceID, clientID, agentID, grantID, actorID string,
	expectedLockVersion int64,
) (AgentGrant, ServicePrincipal, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(agentID) || !validRepositoryUUID(grantID) ||
		!validRepositoryUUID(actorID) || expectedLockVersion < 1 {
		return AgentGrant{}, ServicePrincipal{}, ErrManagementInvalid
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentGrant{}, ServicePrincipal{}, err
	}
	defer tx.Rollback()
	var principalID string
	if err := tx.QueryRowContext(ctx, `
		SELECT service_principal_id FROM agent_access_clients
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, workspaceID, clientID).Scan(&principalID); errors.Is(err, sql.ErrNoRows) {
		return AgentGrant{}, ServicePrincipal{}, ErrRepositoryNotFound
	} else if err != nil {
		return AgentGrant{}, ServicePrincipal{}, err
	}
	var actualLock int64
	if err := tx.QueryRowContext(ctx, `
		SELECT lock_version FROM agent_access_grants
		WHERE workspace_id=$1 AND client_id=$2 AND agent_id=$3 AND id=$4 FOR UPDATE
	`, workspaceID, clientID, agentID, grantID).Scan(&actualLock); errors.Is(err, sql.ErrNoRows) {
		return AgentGrant{}, ServicePrincipal{}, ErrRepositoryNotFound
	} else if err != nil {
		return AgentGrant{}, ServicePrincipal{}, err
	}
	if actualLock != expectedLockVersion {
		return AgentGrant{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	now := service.now()
	grant, err := scanGrant(tx.QueryRowContext(ctx, `
		UPDATE agent_access_grants
		SET status='REVOKED',revoked_at=$5,revoked_by=$6,updated_at=clock_timestamp(),
		 updated_by=$6,lock_version=lock_version+1
		WHERE workspace_id=$1 AND client_id=$2 AND agent_id=$3 AND id=$4
		 AND lock_version=$7 AND status='ACTIVE'
		RETURNING id,workspace_id,client_id,agent_id,scopes,policy,status,valid_from,
		 expires_at,revoked_at,revoked_by,created_by,updated_by,created_at,updated_at,lock_version
	`, workspaceID, clientID, agentID, grantID, now, actorID, expectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentGrant{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	if err != nil {
		return AgentGrant{}, ServicePrincipal{}, mapRepositoryWrite("revoke Grant", err)
	}
	principal, err := bumpPrincipalSecurityVersion(ctx, tx, workspaceID, principalID, actorID)
	if err != nil {
		return AgentGrant{}, ServicePrincipal{}, err
	}
	change := SecurityChangeEvent{
		WorkspaceID: workspaceID, AgentID: agentID, ClientID: clientID,
		GrantID: grantID, SecurityVersion: principal.SecurityVersion,
	}
	after := grantManagementAuditState(grant)
	after["securityVersion"] = principal.SecurityVersion
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionGrantRevoked, WorkspaceID: workspaceID, ActorID: actorID,
		ResourceType: "AGENT_ACCESS_GRANT", ResourceID: grant.ID,
		Before: map[string]any{
			"status": GrantStatusActive, "lockVersion": expectedLockVersion,
			"securityVersion": principal.SecurityVersion - 1,
		},
		After: after, Metadata: map[string]any{"clientId": clientID, "agentId": agentID},
		SecurityChange: &change,
	}); err != nil {
		return AgentGrant{}, ServicePrincipal{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentGrant{}, ServicePrincipal{}, err
	}
	service.publishSecurityChange(ctx, change)
	return grant, principal, nil
}

type UpdateTrustedSubjectIssuerInput struct {
	WorkspaceID, ClientID, ActorID string
	ExpectedLockVersion            int64
	// Clear removes all trust fields. When false, Config must be valid and complete.
	Clear  bool
	Config TrustedSubjectIssuerConfig
}

func (service *ManagementService) UpdateTrustedSubjectIssuer(
	ctx context.Context,
	input UpdateTrustedSubjectIssuerInput,
) (AgentAccessClient, ServicePrincipal, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(input.WorkspaceID) || !validRepositoryUUID(input.ClientID) ||
		!validRepositoryUUID(input.ActorID) || input.ExpectedLockVersion < 1 {
		return AgentAccessClient{}, ServicePrincipal{}, ErrManagementInvalid
	}
	var trust trustedSubjectColumns
	var nextConfig TrustedSubjectIssuerConfig
	if input.Clear {
		if input.Config.Enabled() {
			return AgentAccessClient{}, ServicePrincipal{}, ErrManagementInvalid
		}
	} else {
		normalized, err := normalizeTrustedSubjectIssuerConfig(input.Config)
		if err != nil || !normalized.Enabled() {
			return AgentAccessClient{}, ServicePrincipal{}, ErrManagementInvalid
		}
		encoded, err := encodeTrustedSubjectColumns(normalized)
		if err != nil {
			return AgentAccessClient{}, ServicePrincipal{}, ErrManagementInvalid
		}
		trust, nextConfig = encoded, normalized
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	defer tx.Rollback()
	var principalID string
	var actualLock int64
	var currentStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT service_principal_id,lock_version,status FROM agent_access_clients
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, input.WorkspaceID, input.ClientID).Scan(&principalID, &actualLock, &currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAccessClient{}, ServicePrincipal{}, ErrRepositoryNotFound
	}
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	if actualLock != input.ExpectedLockVersion {
		return AgentAccessClient{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	beforeClient, err := scanClient(tx.QueryRowContext(ctx, clientSelect+`
		WHERE c.workspace_id=$1 AND c.id=$2`, input.WorkspaceID, input.ClientID))
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, mapRepositoryRead("load Client before trust update", err)
	}
	client, err := scanClient(tx.QueryRowContext(ctx, `
		UPDATE agent_access_clients c
		SET trusted_subject_issuer=$3,trusted_subject_audience=$4,trusted_subject_jwks_uri=$5,
		 trusted_subject_inline_jwks=$6,trusted_subject_algorithms=$7,trusted_subject_claim_policy=$8,
		 updated_by=$9,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$10
		RETURNING `+clientReturningColumns+`
	`, input.WorkspaceID, input.ClientID, trust.issuer, trust.audience, trust.jwksURI,
		trust.inlineJWKS, trust.algorithms, trust.claimPolicy, input.ActorID, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAccessClient{}, ServicePrincipal{}, ErrRepositoryConflict
	}
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, mapRepositoryWrite("update Trusted Subject Issuer", err)
	}
	principal, err := bumpPrincipalSecurityVersion(ctx, tx, input.WorkspaceID, principalID, input.ActorID)
	if err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	change := SecurityChangeEvent{
		WorkspaceID: input.WorkspaceID, ClientID: input.ClientID, SecurityVersion: principal.SecurityVersion,
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionTrustedSubjectIssuerUpdated, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "AGENT_ACCESS_CLIENT", ResourceID: client.ID,
		Before: clientTrustedSubjectConfig(beforeClient).PublicAuditState(),
		After:  nextConfig.PublicAuditState(),
		Metadata: map[string]any{
			"clientId": client.ID, "lockVersion": client.LockVersion,
			"securityVersion": principal.SecurityVersion, "cleared": input.Clear,
			"status": currentStatus,
		},
		SecurityChange: &change,
	}); err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentAccessClient{}, ServicePrincipal{}, err
	}
	service.publishSecurityChange(ctx, change)
	return client, principal, nil
}

// ListExternalSubjectPublicViews returns management-safe External Subject rows
// for a Client. Subject hashes and raw external subject values are never exposed.
func (service *ManagementService) ListExternalSubjectPublicViews(
	ctx context.Context,
	workspaceID, clientID string,
) ([]ExternalSubjectPublicView, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) {
		return nil, ErrManagementInvalid
	}
	if _, err := service.repository.GetClient(ctx, workspaceID, clientID); err != nil {
		return nil, err
	}
	values, err := service.repository.ListExternalSubjects(ctx, workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	views := make([]ExternalSubjectPublicView, 0, len(values))
	for _, value := range values {
		views = append(views, ExternalSubjectToPublicView(value))
	}
	return views, nil
}

func (service *ManagementService) GetExternalSubjectPublicView(
	ctx context.Context,
	workspaceID, clientID, subjectID string,
) (ExternalSubjectPublicView, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(subjectID) {
		return ExternalSubjectPublicView{}, ErrManagementInvalid
	}
	value, err := service.repository.GetExternalSubject(ctx, workspaceID, clientID, subjectID)
	if err != nil {
		return ExternalSubjectPublicView{}, err
	}
	return ExternalSubjectToPublicView(value), nil
}

type SetExternalSubjectStatusInput struct {
	WorkspaceID, ClientID, SubjectID, ActorID string
	Status                                    Status
	ExpectedLockVersion                       int64
}

// SetExternalSubjectStatus disables or re-enables an External Subject under the
// Workspace retention policy. Disable blocks new Token Exchange; historical
// Runs remain auditable by External Subject ID without expanding visibility.
func (service *ManagementService) SetExternalSubjectStatus(
	ctx context.Context,
	input SetExternalSubjectStatusInput,
) (ExternalSubjectPublicView, error) {
	policy := DefaultWorkspaceExternalSubjectRetentionPolicy()
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(input.WorkspaceID) || !validRepositoryUUID(input.ClientID) ||
		!validRepositoryUUID(input.SubjectID) || !validRepositoryUUID(input.ActorID) ||
		!knownRepositoryStatus(input.Status) || input.ExpectedLockVersion < 1 ||
		!policy.Valid() {
		return ExternalSubjectPublicView{}, ErrManagementInvalid
	}
	if input.Status == StatusActive && !policy.AllowReenable {
		return ExternalSubjectPublicView{}, ErrManagementInvalid
	}
	current, err := service.repository.GetExternalSubject(
		ctx, input.WorkspaceID, input.ClientID, input.SubjectID,
	)
	if err != nil {
		return ExternalSubjectPublicView{}, err
	}
	if current.LockVersion != input.ExpectedLockVersion {
		return ExternalSubjectPublicView{}, ErrRepositoryConflict
	}
	if current.Status == input.Status {
		return ExternalSubjectPublicView{}, ErrRepositoryConflict
	}
	displayRef := current.DisplayRef
	if input.Status == StatusDisabled && policy.ClearDisplayOnDisable {
		displayRef = ""
	}
	now := service.now()
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ExternalSubjectPublicView{}, err
	}
	defer tx.Rollback()
	// Ensure Client scope still exists in the same transaction.
	var clientExists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
		 SELECT 1 FROM agent_access_clients WHERE workspace_id=$1 AND id=$2
		)`, input.WorkspaceID, input.ClientID,
	).Scan(&clientExists); err != nil {
		return ExternalSubjectPublicView{}, err
	}
	if !clientExists {
		return ExternalSubjectPublicView{}, ErrRepositoryNotFound
	}
	updated, err := scanExternalSubject(tx.QueryRowContext(ctx, `
		UPDATE external_subjects
		SET display_ref=$3,status=$4,last_seen_at=GREATEST(last_seen_at,$5),
		 updated_at=clock_timestamp(),
		 disabled_at=CASE WHEN $4='DISABLED' THEN coalesce(disabled_at,clock_timestamp()) ELSE NULL END,
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND client_id=$6 AND lock_version=$7
		RETURNING id,workspace_id,client_id,issuer,subject_hash,display_ref,status,
		 first_seen_at,last_seen_at,disabled_at,created_at,updated_at,lock_version
	`, input.WorkspaceID, input.SubjectID, nullableRepositoryString(displayRef), input.Status,
		now.UTC(), input.ClientID, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubjectPublicView{}, ErrRepositoryConflict
	}
	if err != nil {
		return ExternalSubjectPublicView{}, mapRepositoryWrite("set External Subject status", err)
	}
	before := ExternalSubjectToPublicView(current).AuditState()
	after := ExternalSubjectToPublicView(updated).AuditState()
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionExternalSubjectStatusChanged, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "EXTERNAL_SUBJECT", ResourceID: updated.ID,
		Before: before, After: after,
		Metadata: map[string]any{
			"clientId": input.ClientID, "retention": policy,
		},
	}); err != nil {
		return ExternalSubjectPublicView{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExternalSubjectPublicView{}, err
	}
	return ExternalSubjectToPublicView(updated), nil
}

type UpdateExternalSubjectDisplayRefInput struct {
	WorkspaceID, ClientID, SubjectID, ActorID string
	DisplayRef                                string
	ExpectedLockVersion                       int64
}

func (service *ManagementService) UpdateExternalSubjectDisplayRef(
	ctx context.Context,
	input UpdateExternalSubjectDisplayRefInput,
) (ExternalSubjectPublicView, error) {
	if service == nil || service.repository == nil || ctx == nil ||
		!validRepositoryUUID(input.WorkspaceID) || !validRepositoryUUID(input.ClientID) ||
		!validRepositoryUUID(input.SubjectID) || !validRepositoryUUID(input.ActorID) ||
		input.ExpectedLockVersion < 1 || !ValidExternalSubjectDisplayRef(input.DisplayRef) {
		return ExternalSubjectPublicView{}, ErrManagementInvalid
	}
	current, err := service.repository.GetExternalSubject(
		ctx, input.WorkspaceID, input.ClientID, input.SubjectID,
	)
	if err != nil {
		return ExternalSubjectPublicView{}, err
	}
	if current.LockVersion != input.ExpectedLockVersion {
		return ExternalSubjectPublicView{}, ErrRepositoryConflict
	}
	if current.Status != StatusActive {
		return ExternalSubjectPublicView{}, ErrManagementInvalid
	}
	now := service.now()
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ExternalSubjectPublicView{}, err
	}
	defer tx.Rollback()
	updated, err := scanExternalSubject(tx.QueryRowContext(ctx, `
		UPDATE external_subjects
		SET display_ref=$3,last_seen_at=GREATEST(last_seen_at,$4),
		 updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND client_id=$5 AND lock_version=$6 AND status='ACTIVE'
		RETURNING id,workspace_id,client_id,issuer,subject_hash,display_ref,status,
		 first_seen_at,last_seen_at,disabled_at,created_at,updated_at,lock_version
	`, input.WorkspaceID, input.SubjectID, nullableRepositoryString(input.DisplayRef),
		now.UTC(), input.ClientID, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubjectPublicView{}, ErrRepositoryConflict
	}
	if err != nil {
		return ExternalSubjectPublicView{}, mapRepositoryWrite("update External Subject display ref", err)
	}
	if err := service.recordManagementAudit(ctx, tx, ManagementAuditEvent{
		Action: ActionExternalSubjectDisplayUpdated, WorkspaceID: input.WorkspaceID, ActorID: input.ActorID,
		ResourceType: "EXTERNAL_SUBJECT", ResourceID: updated.ID,
		Before: ExternalSubjectToPublicView(current).AuditState(),
		After:  ExternalSubjectToPublicView(updated).AuditState(),
		Metadata: map[string]any{"clientId": input.ClientID},
	}); err != nil {
		return ExternalSubjectPublicView{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExternalSubjectPublicView{}, err
	}
	return ExternalSubjectToPublicView(updated), nil
}

func bumpPrincipalSecurityVersion(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, principalID, actorID string,
) (ServicePrincipal, error) {
	value, err := scanServicePrincipal(tx.QueryRowContext(ctx, `
		UPDATE service_principals
		SET security_version=security_version+1,updated_by=$3,
		 updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2
		RETURNING id,workspace_id,name,status,security_version,created_by,updated_by,
		 created_at,updated_at,disabled_at,lock_version
	`, workspaceID, principalID, actorID))
	return value, mapRepositoryWrite("bump Service Principal security version", err)
}

func (service *ManagementService) recordManagementAudit(
	ctx context.Context,
	tx *sql.Tx,
	event ManagementAuditEvent,
) error {
	if service.audit == nil {
		return nil
	}
	return service.audit.RecordAgentAccessManagement(ctx, tx, event)
}

func (service *ManagementService) publishSecurityChange(
	ctx context.Context,
	event SecurityChangeEvent,
) {
	if service.securityChanges == nil {
		return
	}
	// The same transaction already persisted a durable security-change outbox
	// event. This immediate signal only accelerates local SSE revalidation and
	// therefore must not turn a committed mutation into an apparent failure.
	_ = service.securityChanges.PublishAgentAccessSecurityChange(ctx, event)
}

func clientManagementAuditState(client AgentAccessClient, securityVersion int64) map[string]any {
	return map[string]any{
		"status": string(client.Status), "authMethod": string(client.AuthMethod),
		"tokenTtlSeconds": client.TokenTTLSeconds, "lockVersion": client.LockVersion,
		"securityVersion": securityVersion,
	}
}

func credentialManagementAuditState(credential Credential) map[string]any {
	return map[string]any{
		"credentialType": string(credential.Type), "publicHint": credential.PublicHint,
		"validFrom": credential.ValidFrom, "expiresAt": credential.ExpiresAt,
		"revokedAt": credential.RevokedAt, "lockVersion": credential.LockVersion,
	}
}

func grantManagementAuditState(grant AgentGrant) map[string]any {
	return map[string]any{
		"status": string(grant.Status), "agentId": grant.AgentID,
		"scopes": append([]AgentScope(nil), grant.Scopes...), "policy": grant.Policy,
		"validFrom": grant.ValidFrom, "expiresAt": grant.ExpiresAt,
		"revokedAt": grant.RevokedAt, "lockVersion": grant.LockVersion,
	}
}

func (service *ManagementService) generateOpaque(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(service.random, raw); err != nil {
		return "", fmt.Errorf("generate Agent Access identifier: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (service *ManagementService) generateClientSecret(
	credentialID string,
) (secret string, hash []byte, publicHint string, err error) {
	// The public format uses '_' as a field delimiter. Base64URL may also emit
	// '_', so reject those encodings to keep the credential ID and random part
	// unambiguous without reducing the underlying 32-byte random input.
	var randomPart string
	for attempt := 0; attempt < 128; attempt++ {
		randomPart, err = service.generateOpaque("")
		if err != nil {
			return "", nil, "", err
		}
		if !strings.Contains(randomPart, "_") {
			break
		}
		randomPart = ""
	}
	if randomPart == "" {
		return "", nil, "", errors.New("generate unambiguous Agent Access Secret")
	}
	secret = "awsk_live_" + credentialID + "_" + randomPart
	mac := hmac.New(sha256.New, service.pepper)
	_, _ = mac.Write([]byte(secret))
	hash = mac.Sum(nil)
	publicHint = "…" + randomPart[len(randomPart)-6:]
	return secret, hash, publicHint, nil
}

func marshalManagementJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
