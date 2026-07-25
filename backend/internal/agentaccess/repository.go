package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrRepositoryInvalid  = errors.New("Agent Access repository input is invalid")
	ErrRepositoryNotFound = errors.New("Agent Access record was not found")
	ErrRepositoryConflict = errors.New("Agent Access record conflicts with current state")
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, ErrRepositoryInvalid
	}
	return &Repository{db: db}, nil
}

type ServicePrincipal struct {
	ID              string
	WorkspaceID     string
	Name            string
	Status          Status
	SecurityVersion int64
	CreatedBy       string
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DisabledAt      *time.Time
	LockVersion     int64
}

type CreateServicePrincipalInput struct {
	ID, WorkspaceID, Name, ActorID string
}

func (repository *Repository) CreateServicePrincipal(
	ctx context.Context,
	input CreateServicePrincipalInput,
) (ServicePrincipal, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(input.ID) || !validRepositoryUUID(input.WorkspaceID) ||
		!validRepositoryUUID(input.ActorID) || strings.TrimSpace(input.Name) == "" {
		return ServicePrincipal{}, ErrRepositoryInvalid
	}
	value, err := scanServicePrincipal(repository.db.QueryRowContext(ctx, `
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,$3,$4,$4)
		RETURNING id,workspace_id,name,status,security_version,created_by,updated_by,
		          created_at,updated_at,disabled_at,lock_version
	`, input.ID, input.WorkspaceID, strings.TrimSpace(input.Name), input.ActorID))
	return value, mapRepositoryWrite("create Service Principal", err)
}

func (repository *Repository) GetServicePrincipal(
	ctx context.Context,
	workspaceID, principalID string,
) (ServicePrincipal, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(principalID) {
		return ServicePrincipal{}, ErrRepositoryInvalid
	}
	value, err := scanServicePrincipal(repository.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,name,status,security_version,created_by,updated_by,
		       created_at,updated_at,disabled_at,lock_version
		FROM service_principals WHERE workspace_id=$1 AND id=$2
	`, workspaceID, principalID))
	return value, mapRepositoryRead("get Service Principal", err)
}

type UpdateServicePrincipalInput struct {
	Name                string
	Status              Status
	ActorID             string
	ExpectedLockVersion int64
	BumpSecurityVersion bool
}

func (repository *Repository) UpdateServicePrincipalCAS(
	ctx context.Context,
	workspaceID, principalID string,
	input UpdateServicePrincipalInput,
) (ServicePrincipal, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(principalID) ||
		!validRepositoryUUID(input.ActorID) || input.ExpectedLockVersion < 1 ||
		strings.TrimSpace(input.Name) == "" || !knownRepositoryStatus(input.Status) {
		return ServicePrincipal{}, ErrRepositoryInvalid
	}
	if err := repository.assertLockVersion(ctx, "service_principals", workspaceID,
		principalID, input.ExpectedLockVersion); err != nil {
		return ServicePrincipal{}, err
	}
	bump := int64(0)
	if input.BumpSecurityVersion {
		bump = 1
	}
	value, err := scanServicePrincipal(repository.db.QueryRowContext(ctx, `
		UPDATE service_principals
		SET name=$3,status=$4,security_version=security_version+$5,updated_by=$6,
		    updated_at=clock_timestamp(),
		    disabled_at=CASE WHEN $4='DISABLED' THEN coalesce(disabled_at,clock_timestamp()) ELSE NULL END,
		    lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$7
		RETURNING id,workspace_id,name,status,security_version,created_by,updated_by,
		          created_at,updated_at,disabled_at,lock_version
	`, workspaceID, principalID, strings.TrimSpace(input.Name), input.Status,
		bump, input.ActorID, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return ServicePrincipal{}, ErrRepositoryConflict
	}
	return value, mapRepositoryWrite("update Service Principal", err)
}

type AgentAccessClient struct {
	ID                        string
	WorkspaceID               string
	ServicePrincipalID        string
	ClientID                  string
	Name                      string
	Status                    Status
	AuthMethod                ClientAuthMethod
	JWKSURI                   string
	TrustedSubjectIssuer      string
	TrustedSubjectAudience    string
	TrustedSubjectJWKSURI     string
	TrustedSubjectInlineJWKS  json.RawMessage
	TrustedSubjectAlgorithms  []string
	TrustedSubjectClaimPolicy agentaccessauth.SubjectClaimPolicy
	AllowedCORSOrigins        []string
	TokenTTLSeconds           int
	CreatedBy                 string
	UpdatedBy                 string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	DisabledAt                *time.Time
	LockVersion               int64
}

type CreateClientInput struct {
	ID, WorkspaceID, ServicePrincipalID, ClientID, Name string
	AuthMethod                                          ClientAuthMethod
	JWKSURI                                             string
	TrustedSubjectIssuer                                string
	TrustedSubjectAudience                              string
	TrustedSubjectJWKSURI                               string
	TrustedSubjectInlineJWKS                            json.RawMessage
	TrustedSubjectAlgorithms                            []string
	TrustedSubjectClaimPolicy                           agentaccessauth.SubjectClaimPolicy
	AllowedCORSOrigins                                  []string
	TokenTTLSeconds                                     int
	ActorID                                             string
}

func (repository *Repository) CreateClient(
	ctx context.Context,
	input CreateClientInput,
) (AgentAccessClient, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(input.ID) || !validRepositoryUUID(input.WorkspaceID) ||
		!validRepositoryUUID(input.ServicePrincipalID) || !validRepositoryUUID(input.ActorID) ||
		strings.TrimSpace(input.ClientID) == "" || strings.TrimSpace(input.Name) == "" ||
		!knownRepositoryAuthMethod(input.AuthMethod) {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	origins := input.AllowedCORSOrigins
	if origins == nil {
		origins = []string{}
	}
	cors, err := json.Marshal(origins)
	if err != nil {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	ttl := input.TokenTTLSeconds
	if ttl == 0 {
		ttl = DefaultAccessTokenTTLSeconds
	}
	if !validAccessTokenTTLSeconds(ttl) {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	trust, err := encodeTrustedSubjectColumns(TrustedSubjectIssuerConfig{
		Issuer: input.TrustedSubjectIssuer, Audience: input.TrustedSubjectAudience,
		JWKSURI: input.TrustedSubjectJWKSURI, InlineJWKS: input.TrustedSubjectInlineJWKS,
		Algorithms: input.TrustedSubjectAlgorithms, ClaimPolicy: input.TrustedSubjectClaimPolicy,
	})
	if err != nil {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	value, err := scanClient(repository.db.QueryRowContext(ctx, `
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,jwks_uri,
		 trusted_subject_issuer,trusted_subject_audience,trusted_subject_jwks_uri,
		 trusted_subject_inline_jwks,trusted_subject_algorithms,trusted_subject_claim_policy,
		 allowed_cors_origins,token_ttl_seconds,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
		RETURNING `+clientColumns+`
	`, input.ID, input.WorkspaceID, input.ServicePrincipalID,
		strings.TrimSpace(input.ClientID), strings.TrimSpace(input.Name), input.AuthMethod,
		nullableRepositoryString(input.JWKSURI), trust.issuer, trust.audience, trust.jwksURI,
		trust.inlineJWKS, trust.algorithms, trust.claimPolicy, cors, ttl, input.ActorID))
	return value, mapRepositoryWrite("create Agent Access Client", err)
}

func (repository *Repository) GetClient(
	ctx context.Context,
	workspaceID, clientID string,
) (AgentAccessClient, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	value, err := scanClient(repository.db.QueryRowContext(ctx, clientSelect+`
		WHERE c.workspace_id=$1 AND c.id=$2`, workspaceID, clientID))
	return value, mapRepositoryRead("get Agent Access Client", err)
}

func (repository *Repository) GetClientByPublicID(
	ctx context.Context,
	workspaceID, publicClientID string,
) (AgentAccessClient, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || strings.TrimSpace(publicClientID) == "" {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	value, err := scanClient(repository.db.QueryRowContext(ctx, clientSelect+`
		WHERE c.workspace_id=$1 AND c.client_id=$2`, workspaceID, strings.TrimSpace(publicClientID)))
	return value, mapRepositoryRead("get Agent Access Client by public ID", err)
}

func (repository *Repository) ListClients(
	ctx context.Context,
	workspaceID string,
) ([]AgentAccessClient, error) {
	if repository == nil || repository.db == nil || ctx == nil || !validRepositoryUUID(workspaceID) {
		return nil, ErrRepositoryInvalid
	}
	rows, err := repository.db.QueryContext(ctx, clientSelect+`
		WHERE c.workspace_id=$1 ORDER BY c.created_at,c.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list Agent Access Clients: %w", err)
	}
	defer rows.Close()
	values := make([]AgentAccessClient, 0)
	for rows.Next() {
		value, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("list Agent Access Clients: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Agent Access Clients: %w", err)
	}
	return values, nil
}

// ListExactCORSOriginBindings returns per-Client exact Origin rows for ACTIVE
// Agent Access Clients. CORS middleware must isolate reflection by Client
// (and Workspace for preflight) — never a global Origin union.
func (repository *Repository) ListExactCORSOriginBindings(
	ctx context.Context,
) ([]agentaccessauth.CORSOriginBinding, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return nil, ErrRepositoryInvalid
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id, workspace_id, client_id, allowed_cors_origins
		FROM agent_access_clients
		WHERE status = 'ACTIVE'
	`)
	if err != nil {
		return nil, fmt.Errorf("list Agent Access CORS origin bindings: %w", err)
	}
	defer rows.Close()
	out := make([]agentaccessauth.CORSOriginBinding, 0)
	for rows.Next() {
		var internalID, workspaceID, publicClientID string
		var raw []byte
		if scanErr := rows.Scan(&internalID, &workspaceID, &publicClientID, &raw); scanErr != nil {
			return nil, fmt.Errorf("list Agent Access CORS origin bindings: %w", scanErr)
		}
		if len(raw) == 0 {
			continue
		}
		var origins []string
		if unmarshalErr := json.Unmarshal(raw, &origins); unmarshalErr != nil {
			continue
		}
		for _, origin := range origins {
			origin = strings.TrimSpace(origin)
			if origin == "" {
				continue
			}
			out = append(out, agentaccessauth.CORSOriginBinding{
				Origin: origin, WorkspaceID: strings.TrimSpace(workspaceID),
				PublicClientID:   strings.TrimSpace(publicClientID),
				InternalClientID: strings.TrimSpace(internalID),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Agent Access CORS origin bindings: %w", err)
	}
	return out, nil
}

type UpdateClientInput struct {
	Name                      string
	Status                    Status
	AuthMethod                ClientAuthMethod
	JWKSURI                   string
	TrustedSubjectIssuer      string
	TrustedSubjectAudience    string
	TrustedSubjectJWKSURI     string
	TrustedSubjectInlineJWKS  json.RawMessage
	TrustedSubjectAlgorithms  []string
	TrustedSubjectClaimPolicy agentaccessauth.SubjectClaimPolicy
	AllowedCORSOrigins        []string
	TokenTTLSeconds           int
	ActorID                   string
	ExpectedLockVersion       int64
}

func (repository *Repository) UpdateClientCAS(
	ctx context.Context,
	workspaceID, clientID string,
	input UpdateClientInput,
) (AgentAccessClient, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(input.ActorID) || input.ExpectedLockVersion < 1 ||
		strings.TrimSpace(input.Name) == "" || !knownRepositoryStatus(input.Status) ||
		!knownRepositoryAuthMethod(input.AuthMethod) || !validAccessTokenTTLSeconds(input.TokenTTLSeconds) {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	if err := repository.assertLockVersion(ctx, "agent_access_clients", workspaceID,
		clientID, input.ExpectedLockVersion); err != nil {
		return AgentAccessClient{}, err
	}
	origins := input.AllowedCORSOrigins
	if origins == nil {
		origins = []string{}
	}
	cors, err := json.Marshal(origins)
	if err != nil {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	trust, err := encodeTrustedSubjectColumns(TrustedSubjectIssuerConfig{
		Issuer: input.TrustedSubjectIssuer, Audience: input.TrustedSubjectAudience,
		JWKSURI: input.TrustedSubjectJWKSURI, InlineJWKS: input.TrustedSubjectInlineJWKS,
		Algorithms: input.TrustedSubjectAlgorithms, ClaimPolicy: input.TrustedSubjectClaimPolicy,
	})
	if err != nil {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	value, err := scanClient(repository.db.QueryRowContext(ctx, `
		UPDATE agent_access_clients c
		SET name=$3,status=$4,auth_method=$5,jwks_uri=$6,
		 trusted_subject_issuer=$7,trusted_subject_audience=$8,trusted_subject_jwks_uri=$9,
		 trusted_subject_inline_jwks=$10,trusted_subject_algorithms=$11,trusted_subject_claim_policy=$12,
		 allowed_cors_origins=$13,token_ttl_seconds=$14,updated_by=$15,
		 updated_at=clock_timestamp(),
		 disabled_at=CASE WHEN $4='DISABLED' THEN coalesce(disabled_at,clock_timestamp()) ELSE NULL END,
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$16
		RETURNING `+clientReturningColumns+`
	`, workspaceID, clientID, strings.TrimSpace(input.Name), input.Status, input.AuthMethod,
		nullableRepositoryString(input.JWKSURI), trust.issuer, trust.audience, trust.jwksURI,
		trust.inlineJWKS, trust.algorithms, trust.claimPolicy, cors, input.TokenTTLSeconds,
		input.ActorID, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAccessClient{}, ErrRepositoryConflict
	}
	return value, mapRepositoryWrite("update Agent Access Client", err)
}

const clientColumns = `
	id,workspace_id,service_principal_id,client_id,name,status,auth_method,
	jwks_uri,trusted_subject_issuer,trusted_subject_audience,trusted_subject_jwks_uri,
	trusted_subject_inline_jwks,trusted_subject_algorithms,trusted_subject_claim_policy,
	allowed_cors_origins,token_ttl_seconds,created_by,updated_by,created_at,updated_at,
	disabled_at,lock_version`

const clientReturningColumns = `
	c.id,c.workspace_id,c.service_principal_id,c.client_id,c.name,c.status,c.auth_method,
	c.jwks_uri,c.trusted_subject_issuer,c.trusted_subject_audience,c.trusted_subject_jwks_uri,
	c.trusted_subject_inline_jwks,c.trusted_subject_algorithms,c.trusted_subject_claim_policy,
	c.allowed_cors_origins,c.token_ttl_seconds,c.created_by,c.updated_by,c.created_at,c.updated_at,
	c.disabled_at,c.lock_version`

const clientSelect = `
	SELECT ` + clientReturningColumns + `
	FROM agent_access_clients c `

type repositoryScanner interface{ Scan(...any) error }

func scanServicePrincipal(scanner repositoryScanner) (ServicePrincipal, error) {
	var value ServicePrincipal
	var status string
	var disabled sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.Name, &status, &value.SecurityVersion,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&disabled, &value.LockVersion,
	)
	if err != nil {
		return ServicePrincipal{}, err
	}
	parsed, ok := ParseStatus(status)
	if !ok {
		return ServicePrincipal{}, ErrRepositoryInvalid
	}
	value.Status = parsed
	value.DisabledAt = repositoryTimePointer(disabled)
	return value, nil
}

func scanClient(scanner repositoryScanner) (AgentAccessClient, error) {
	var value AgentAccessClient
	var status, method string
	var jwks, issuer, audience, subjectJWKS sql.NullString
	var inlineJWKS, algorithms, claimPolicy, cors []byte
	var disabled sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.ServicePrincipalID, &value.ClientID,
		&value.Name, &status, &method, &jwks, &issuer, &audience, &subjectJWKS,
		&inlineJWKS, &algorithms, &claimPolicy, &cors,
		&value.TokenTTLSeconds, &value.CreatedBy, &value.UpdatedBy,
		&value.CreatedAt, &value.UpdatedAt, &disabled, &value.LockVersion,
	)
	if err != nil {
		return AgentAccessClient{}, err
	}
	parsedStatus, ok := ParseStatus(status)
	if !ok {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	parsedMethod, ok := ParseClientAuthMethod(method)
	if !ok || json.Unmarshal(cors, &value.AllowedCORSOrigins) != nil {
		return AgentAccessClient{}, ErrRepositoryInvalid
	}
	value.Status, value.AuthMethod = parsedStatus, parsedMethod
	value.JWKSURI, value.TrustedSubjectIssuer = jwks.String, issuer.String
	value.TrustedSubjectAudience = audience.String
	value.TrustedSubjectJWKSURI = subjectJWKS.String
	if len(inlineJWKS) > 0 {
		value.TrustedSubjectInlineJWKS = append(json.RawMessage(nil), inlineJWKS...)
	}
	if len(algorithms) > 0 {
		if err := json.Unmarshal(algorithms, &value.TrustedSubjectAlgorithms); err != nil {
			return AgentAccessClient{}, ErrRepositoryInvalid
		}
	}
	if len(claimPolicy) > 0 {
		policy, err := agentaccessauth.ParseSubjectClaimPolicy(claimPolicy)
		if err != nil {
			return AgentAccessClient{}, ErrRepositoryInvalid
		}
		value.TrustedSubjectClaimPolicy = policy
	}
	value.DisabledAt = repositoryTimePointer(disabled)
	return value, nil
}

type trustedSubjectColumns struct {
	issuer, audience, jwksURI           any
	inlineJWKS, algorithms, claimPolicy any
}

func encodeTrustedSubjectColumns(config TrustedSubjectIssuerConfig) (trustedSubjectColumns, error) {
	normalized, err := normalizeTrustedSubjectIssuerConfig(config)
	if err != nil {
		return trustedSubjectColumns{}, err
	}
	if !normalized.Enabled() {
		return trustedSubjectColumns{}, nil
	}
	algorithms, err := json.Marshal(normalized.Algorithms)
	if err != nil {
		return trustedSubjectColumns{}, ErrRepositoryInvalid
	}
	policy, err := agentaccessauth.MarshalSubjectClaimPolicy(normalized.ClaimPolicy)
	if err != nil {
		return trustedSubjectColumns{}, ErrRepositoryInvalid
	}
	var inline any
	if len(normalized.InlineJWKS) > 0 {
		inline = []byte(normalized.InlineJWKS)
	}
	return trustedSubjectColumns{
		issuer:     nullableRepositoryString(normalized.Issuer),
		audience:   nullableRepositoryString(normalized.Audience),
		jwksURI:    nullableRepositoryString(normalized.JWKSURI),
		inlineJWKS: inline, algorithms: algorithms, claimPolicy: policy,
	}, nil
}

func (repository *Repository) assertLockVersion(
	ctx context.Context,
	table, workspaceID, id string,
	expected int64,
) error {
	allowed := map[string]bool{
		"service_principals": true, "agent_access_clients": true,
		"agent_access_credentials": true, "agent_access_grants": true,
		"external_subjects": true,
	}
	if !allowed[table] {
		return ErrRepositoryInvalid
	}
	var actual int64
	err := repository.db.QueryRowContext(ctx,
		`SELECT lock_version FROM `+pq.QuoteIdentifier(table)+` WHERE workspace_id=$1 AND id=$2`,
		workspaceID, id,
	).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRepositoryNotFound
	}
	if err != nil {
		return fmt.Errorf("read Agent Access lock version: %w", err)
	}
	if actual != expected {
		return ErrRepositoryConflict
	}
	return nil
}

func mapRepositoryRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRepositoryNotFound
	}
	if errors.Is(err, ErrRepositoryInvalid) {
		return ErrRepositoryInvalid
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapRepositoryWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRepositoryNotFound
	}
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		switch string(databaseError.Code) {
		case "23503":
			return ErrRepositoryNotFound
		case "23505", "23P01", "40001", "55000":
			return ErrRepositoryConflict
		case "22000", "22023", "23502", "23514":
			return ErrRepositoryInvalid
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func validRepositoryUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func knownRepositoryStatus(status Status) bool {
	_, ok := ParseStatus(string(status))
	return ok
}

func knownRepositoryAuthMethod(method ClientAuthMethod) bool {
	_, ok := ParseClientAuthMethod(string(method))
	return ok
}

func nullableRepositoryString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func repositoryTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
