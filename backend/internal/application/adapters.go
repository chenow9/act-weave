package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/config"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/openapiimport"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/secret"
	"actweave/backend/internal/serviceendpoint"
	"actweave/backend/internal/sessioncontext"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflowruntime"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type modelConfigVerifier struct {
	client  *http.Client
	secrets *secret.Service
}

type aapRunDispatcher struct {
	runtime agentrun.Runtime
}

func (dispatcher aapRunDispatcher) DispatchRun(input aap.RunDispatch) error {
	if dispatcher.runtime == nil {
		return aap.ErrRunInvalid
	}
	dispatcher.runtime.Enqueue(agentrun.Job{
		WorkspaceID: input.WorkspaceID, SessionID: input.ConversationID,
		RunID: input.RunID, UserMessageID: input.MessageID, ActorID: input.ActorID,
		InitialEventsCommitted: true,
	})
	return nil
}

type agentAccessSecurityPublisher struct {
	source *agentaccessauth.InProcessSecurityChanges
	cache  *agentaccessauth.SecurityVersionCache
}

type agentAccessClientSecretStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessClientSecretStore) LookupClientSecretCredential(
	ctx context.Context,
	credentialID string,
) (agentaccessauth.ClientSecretCredential, error) {
	if store.repository == nil {
		return agentaccessauth.ClientSecretCredential{}, agentaccessauth.ErrClientAuthenticationUnavailable
	}
	record, err := store.repository.FindClientSecretAuthentication(ctx, credentialID)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.ClientSecretCredential{}, agentaccessauth.ErrClientSecretCredentialNotFound
		}
		return agentaccessauth.ClientSecretCredential{}, err
	}
	return agentaccessauth.ClientSecretCredential{
		WorkspaceID: record.WorkspaceID, ClientID: record.ClientID,
		PublicClientID: record.PublicClientID, ServicePrincipalID: record.ServicePrincipalID,
		ServicePrincipalVersion: record.ServicePrincipalVersion,
		ClientActive:            record.ClientStatus == agentaccess.StatusActive,
		ServicePrincipalActive:  record.ServicePrincipalStatus == agentaccess.StatusActive,
		SecretAuthentication:    record.AuthMethod == agentaccess.ClientAuthMethodSecretBasic,
		TokenTTLSeconds:         record.TokenTTLSeconds, CredentialID: record.CredentialID,
		CredentialIsClientSecret: record.CredentialType == agentaccess.CredentialTypeClientSecret,
		SecretHash:               append([]byte(nil), record.SecretHash...), ValidFrom: record.ValidFrom,
		ExpiresAt: record.ExpiresAt, RevokedAt: record.RevokedAt,
	}, nil
}

func (store agentAccessClientSecretStore) MarkClientSecretAuthenticated(
	ctx context.Context,
	credentialID, publicClientID string,
	usedAt time.Time,
) error {
	if store.repository == nil {
		return agentaccessauth.ErrClientAuthenticationUnavailable
	}
	err := store.repository.RecordClientSecretAuthenticated(ctx, credentialID, publicClientID, usedAt)
	if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		return agentaccessauth.ErrClientSecretCredentialNotFound
	}
	return err
}

type agentAccessClientSecretAudit struct {
	sink agentaccess.AuthenticationAuditSink
}

func (adapter agentAccessClientSecretAudit) RecordClientSecretAuthenticationFailure(
	ctx context.Context,
	failure agentaccessauth.ClientSecretAuthenticationFailure,
) error {
	if adapter.sink == nil {
		return errors.New("Agent Access authentication audit is unavailable")
	}
	return adapter.sink.RecordAgentAccessAuthenticationFailure(ctx, agentaccess.AuthenticationFailureAuditEvent{
		WorkspaceID: failure.WorkspaceID, ClientID: failure.ClientID,
		AuthMethod: agentaccess.ClientAuthMethodSecretBasic, ErrorCode: failure.ErrorCode,
		SourceIP: failure.SourceIP, UserAgent: failure.UserAgent,
	})
}

type agentAccessPrivateKeyJWTStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessPrivateKeyJWTStore) LookupPrivateKeyJWTClient(
	ctx context.Context,
	publicClientID string,
) (agentaccessauth.PrivateKeyJWTClient, error) {
	if store.repository == nil {
		return agentaccessauth.PrivateKeyJWTClient{}, agentaccessauth.ErrClientAuthenticationUnavailable
	}
	record, err := store.repository.FindPrivateKeyJWTAuthentication(ctx, publicClientID)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.PrivateKeyJWTClient{}, agentaccessauth.ErrPrivateKeyJWTClientNotFound
		}
		return agentaccessauth.PrivateKeyJWTClient{}, err
	}
	credentials := make([]agentaccessauth.PrivateKeyJWTCredential, 0, len(record.Credentials))
	for _, credential := range record.Credentials {
		credentials = append(credentials, agentaccessauth.PrivateKeyJWTCredential{
			CredentialID:  credential.CredentialID,
			JWKThumbprint: append([]byte(nil), credential.JWKThumbprint...),
			ValidFrom:     credential.ValidFrom, ExpiresAt: credential.ExpiresAt,
			RevokedAt: credential.RevokedAt,
		})
	}
	return agentaccessauth.PrivateKeyJWTClient{
		WorkspaceID: record.WorkspaceID, ClientID: record.ClientID,
		PublicClientID: record.PublicClientID, ServicePrincipalID: record.ServicePrincipalID,
		ServicePrincipalVersion:  record.ServicePrincipalVersion,
		ClientActive:             record.ClientStatus == agentaccess.StatusActive,
		ServicePrincipalActive:   record.ServicePrincipalStatus == agentaccess.StatusActive,
		PrivateKeyAuthentication: record.AuthMethod == agentaccess.ClientAuthMethodPrivateKey,
		JWKSURI:                  record.JWKSURI, TokenTTLSeconds: record.TokenTTLSeconds,
		Credentials: credentials,
	}, nil
}

func (store agentAccessPrivateKeyJWTStore) MarkPrivateKeyJWTAuthenticated(
	ctx context.Context,
	credentialID, publicClientID string,
	usedAt time.Time,
) error {
	if store.repository == nil {
		return agentaccessauth.ErrClientAuthenticationUnavailable
	}
	err := store.repository.RecordPrivateKeyJWTAuthenticated(ctx, credentialID, publicClientID, usedAt)
	if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		return agentaccessauth.ErrPrivateKeyJWTClientNotFound
	}
	return err
}

type agentAccessClientAssertionJTIStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessClientAssertionJTIStore) ClaimClientAssertionJTI(
	ctx context.Context,
	clientID string,
	jtiHash [sha256.Size]byte,
	expiresAt, now time.Time,
) (bool, error) {
	if store.repository == nil {
		return false, agentaccessauth.ErrClientAuthenticationUnavailable
	}
	return store.repository.ClaimClientAssertionJTI(ctx, clientID, jtiHash[:], expiresAt, now)
}

type agentAccessSubjectTokenJTIStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessSubjectTokenJTIStore) ClaimSubjectTokenJTI(
	ctx context.Context,
	clientID string,
	jtiHash [sha256.Size]byte,
	expiresAt, now time.Time,
) (bool, error) {
	if store.repository == nil {
		return false, agentaccessauth.ErrTokenServiceUnavailable
	}
	return store.repository.ClaimSubjectTokenJTI(ctx, clientID, jtiHash[:], expiresAt, now)
}

type agentAccessTokenExchangeTrustStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessTokenExchangeTrustStore) LookupTokenExchangeTrust(
	ctx context.Context,
	client agentaccessauth.AuthenticatedClient,
) (agentaccessauth.TokenExchangeTrust, error) {
	if store.repository == nil {
		return agentaccessauth.TokenExchangeTrust{}, agentaccessauth.ErrTokenServiceUnavailable
	}
	config, err := store.repository.LoadTrustedSubjectIssuerConfig(ctx, client.WorkspaceID, client.ClientID)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.TokenExchangeTrust{}, agentaccessauth.ErrTokenExchangeTrustMissing
		}
		return agentaccessauth.TokenExchangeTrust{}, err
	}
	if !config.Enabled() {
		return agentaccessauth.TokenExchangeTrust{}, agentaccessauth.ErrTokenExchangeTrustMissing
	}
	return agentaccessauth.TokenExchangeTrust{Config: config.AuthConfig()}, nil
}

type agentAccessExternalSubjectMapper struct {
	repository *agentaccess.Repository
}

func (store agentAccessExternalSubjectMapper) ResolveActiveExternalSubject(
	ctx context.Context,
	workspaceID, clientID, issuer string,
	subjectHash [sha256.Size]byte,
	seenAt time.Time,
) (agentaccessauth.ExternalSubjectBinding, error) {
	if store.repository == nil {
		return agentaccessauth.ExternalSubjectBinding{}, agentaccessauth.ErrTokenServiceUnavailable
	}
	subject, err := store.repository.ResolveOrCreateExternalSubject(
		ctx, workspaceID, clientID, issuer, subjectHash[:], seenAt,
	)
	if err != nil {
		return agentaccessauth.ExternalSubjectBinding{}, err
	}
	if subject.Status != agentaccess.StatusActive {
		return agentaccessauth.ExternalSubjectBinding{}, agentaccessauth.ErrTokenExchangeSubjectDenied
	}
	return agentaccessauth.ExternalSubjectBinding{
		SubjectID: subject.ID, Active: true,
	}, nil
}

type agentAccessClientCredentialsGrantStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessClientCredentialsGrantStore) ResolveClientCredentialsGrant(
	ctx context.Context,
	client agentaccessauth.AuthenticatedClient,
	agentID string,
	at time.Time,
) (agentaccessauth.ClientCredentialsGrant, error) {
	if store.repository == nil {
		return agentaccessauth.ClientCredentialsGrant{}, agentaccessauth.ErrTokenServiceUnavailable
	}
	record, err := store.repository.ResolveClientCredentialsTokenGrant(
		ctx, client.WorkspaceID, client.ClientID, client.PublicClientID,
		client.ServicePrincipalID, client.CredentialID, agentID,
		client.ServicePrincipalVersion, at,
	)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.ClientCredentialsGrant{}, agentaccessauth.ErrClientCredentialsGrantNotFound
		}
		return agentaccessauth.ClientCredentialsGrant{}, err
	}
	scopes := make([]string, 0, len(record.Scopes))
	for _, scope := range record.Scopes {
		scopes = append(scopes, string(scope))
	}
	return agentaccessauth.ClientCredentialsGrant{
		GrantID: record.GrantID, WorkspaceID: record.WorkspaceID,
		ClientID: record.ClientID, PublicClientID: record.PublicClientID,
		ServicePrincipalID:      record.ServicePrincipalID,
		ServicePrincipalVersion: record.ServicePrincipalVersion,
		AgentID:                 record.AgentID, GrantedScopes: scopes,
		ClientTokenTTL: time.Duration(record.TokenTTLSeconds) * time.Second,
		GrantExpiresAt: record.GrantExpiresAt,
	}, nil
}

type agentAccessAuthorizationStateStore struct {
	repository *agentaccess.Repository
}

type agentAccessStreamAuthorizationStateStore struct {
	repository *agentaccess.Repository
}

func (store agentAccessStreamAuthorizationStateStore) ResolveStreamAuthorizationState(
	ctx context.Context,
	binding agentaccessauth.StreamBinding,
	at time.Time,
) (agentaccessauth.StreamAuthorizationState, error) {
	if store.repository == nil {
		return agentaccessauth.StreamAuthorizationState{}, agentaccessauth.ErrStreamAuthorizationStateNotFound
	}
	record, err := store.repository.ResolveAAPStreamAuthorizationState(
		ctx, binding.WorkspaceID, binding.AgentID, binding.ClientID,
		binding.GrantID, binding.PrincipalID, at,
	)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.StreamAuthorizationState{}, agentaccessauth.ErrStreamAuthorizationStateNotFound
		}
		return agentaccessauth.StreamAuthorizationState{}, err
	}
	return agentaccessauth.StreamAuthorizationState{
		WorkspaceID: record.WorkspaceID, AgentID: record.AgentID,
		ClientID: record.ClientID, GrantID: record.GrantID,
		ServicePrincipalID: record.ServicePrincipalID,
		SecurityVersion:    record.SecurityVersion,
	}, nil
}

// streamAuthorizerRouter keeps the legacy User API's Token-expiry monitoring
// independent while all Grant-bound AAP streams use current repository state.
type streamAuthorizerRouter struct {
	agentAccess agentaccessauth.StreamAuthorizer
	userAPI     agentaccessauth.StreamAuthorizer
}

func (router streamAuthorizerRouter) Reauthorize(
	ctx context.Context,
	binding agentaccessauth.StreamBinding,
	at time.Time,
) error {
	if binding.GrantID == "" && strings.HasPrefix(binding.ClientID, "user-api:") {
		if router.userAPI == nil {
			return agentaccessauth.ErrAuthorizationRevoked
		}
		return router.userAPI.Reauthorize(ctx, binding, at)
	}
	if router.agentAccess == nil {
		return agentaccessauth.ErrAuthorizationRevoked
	}
	return router.agentAccess.Reauthorize(ctx, binding, at)
}

func (store agentAccessAuthorizationStateStore) ResolveAAPAuthorizationState(
	ctx context.Context,
	principal agentaccessauth.AAPAccessTokenPrincipal,
	at time.Time,
) (agentaccessauth.AAPAuthorizationState, error) {
	if store.repository == nil {
		return agentaccessauth.AAPAuthorizationState{}, agentaccessauth.ErrAAPAuthorizationUnavailable
	}
	record, err := store.repository.ResolveAAPAuthorizationState(
		ctx, principal.WorkspaceID, principal.AgentID, principal.AuthorizedParty,
		principal.ServicePrincipalID, at,
	)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.AAPAuthorizationState{}, agentaccessauth.ErrAAPAuthorizationStateNotFound
		}
		return agentaccessauth.AAPAuthorizationState{}, err
	}
	grantScopes := make([]string, 0, len(record.GrantScopes))
	for _, scope := range record.GrantScopes {
		grantScopes = append(grantScopes, string(scope))
	}
	policyScopes := agentAccessPolicyScopes(principal, record.GrantPolicy)
	sharingResources := agentAccessSubjectSharingResources(record.GrantPolicy)
	return agentaccessauth.AAPAuthorizationState{
		WorkspaceID: record.WorkspaceID, AgentID: record.AgentID, ClientID: record.ClientID,
		PublicClientID: record.PublicClientID, ServicePrincipalID: record.ServicePrincipalID,
		CurrentSecurityVersion: record.CurrentSecurityVersion, GrantID: record.GrantID,
		GrantScopes: grantScopes, AgentPolicyScopes: policyScopes,
		SubjectSharingResources: sharingResources,
		WorkspaceVersion:        record.WorkspaceVersion, ClientVersion: record.ClientVersion,
		GrantVersion: record.GrantVersion, AgentPolicyVersion: record.AgentPolicyVersion,
	}, nil
}

func agentAccessSubjectSharingResources(policy agentaccess.GrantPolicy) []string {
	if policy.SubjectSharing == nil || !policy.SubjectSharing.Enabled {
		return nil
	}
	result := make([]string, 0, len(policy.SubjectSharing.Resources))
	for _, resource := range policy.SubjectSharing.Resources {
		result = append(result, string(resource))
	}
	return result
}

func agentAccessPolicyScopes(
	principal agentaccessauth.AAPAccessTokenPrincipal,
	policy agentaccess.GrantPolicy,
) []string {
	result := make([]string, 0, len(agentaccess.KnownAgentScopes()))
	for _, scope := range agentaccess.KnownAgentScopes() {
		if scope == agentaccess.ScopeInteractionDecide &&
			principal.PrincipalID == principal.ServicePrincipalID &&
			(policy.ServiceDecision == nil || !policy.ServiceDecision.Enabled) {
			continue
		}
		result = append(result, string(scope))
	}
	return result
}

type agentAccessAuthorizationAudit struct {
	sink agentaccess.AuthorizationAuditSink
}

func (adapter agentAccessAuthorizationAudit) RecordAAPAuthorizationDenied(
	ctx context.Context,
	denial agentaccessauth.AAPAuthorizationDenial,
) error {
	if adapter.sink == nil {
		return errors.New("Agent Access authorization audit is unavailable")
	}
	return adapter.sink.RecordAgentAccessAuthorizationDenied(ctx, agentaccess.AuthorizationDenialAuditEvent{
		WorkspaceID: denial.WorkspaceID, AgentID: denial.AgentID,
		ServicePrincipalID: denial.ServicePrincipalID, PublicClientID: denial.AuthorizedParty,
		Action: string(denial.Action), RequiredScope: denial.RequiredScope, Reason: denial.Reason,
		ResourceType: string(denial.ResourceType), ResourceID: denial.ResourceID,
	})
}

type agentAccessPrivateKeyJWTAudit struct {
	sink agentaccess.AuthenticationAuditSink
}

func (adapter agentAccessPrivateKeyJWTAudit) RecordPrivateKeyJWTAuthenticationFailure(
	ctx context.Context,
	failure agentaccessauth.PrivateKeyJWTAuthenticationFailure,
) error {
	if adapter.sink == nil {
		return errors.New("Agent Access authentication audit is unavailable")
	}
	return adapter.sink.RecordAgentAccessAuthenticationFailure(ctx, agentaccess.AuthenticationFailureAuditEvent{
		WorkspaceID: failure.WorkspaceID, ClientID: failure.ClientID,
		AuthMethod: agentaccess.ClientAuthMethodPrivateKey, ErrorCode: failure.ErrorCode,
		SourceIP: failure.SourceIP, UserAgent: failure.UserAgent,
	})
}

type privateKeyJWTJWKSFetcher struct {
	base     *http.Client
	resolver execution.HostResolver
}

func (fetcher privateKeyJWTJWKSFetcher) FetchRemoteJWKS(
	ctx context.Context,
	rawURI string,
	maximumBytes int64,
) (agentaccessauth.RemoteJWKSFetchResult, error) {
	if ctx == nil || maximumBytes < 1 || len(rawURI) > 2048 {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	target, err := url.Parse(rawURI)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	port := 443
	if target.Port() != "" {
		port, err = strconv.Atoi(target.Port())
		if err != nil || port < 1 || port > 65535 {
			return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
		}
	}
	guard, err := execution.NewHTTPNetworkGuard(execution.EgressPolicy{
		AllowedHosts: []string{target.Hostname()}, AllowedPorts: []int{port}, MaxRedirects: 3,
	}, fetcher.resolver)
	if err != nil || guard.ValidateURL(ctx, target) != nil {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	client, err := guard.ProtectClient(fetcher.base, nil)
	if err != nil {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	request.Header.Set("Accept", "application/jwk-set+json, application/json")
	response, err := client.Do(request)
	if err != nil {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	defer response.Body.Close()
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if response.StatusCode != http.StatusOK ||
		(contentType != "application/jwk-set+json" && contentType != "application/json") ||
		response.ContentLength > maximumBytes {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil || int64(len(body)) > maximumBytes {
		return agentaccessauth.RemoteJWKSFetchResult{}, agentaccessauth.ErrRemoteJWKSRejected
	}
	cacheTTL, cacheTTLSet := jwksCacheMaxAge(response.Header.Get("Cache-Control"))
	return agentaccessauth.RemoteJWKSFetchResult{
		Body: body, CacheTTL: cacheTTL, CacheTTLSet: cacheTTLSet,
	}, nil
}

func jwksCacheMaxAge(value string) (time.Duration, bool) {
	for _, directive := range strings.Split(value, ",") {
		directive = strings.TrimSpace(strings.ToLower(directive))
		if directive == "no-store" || directive == "no-cache" {
			return 0, true
		}
		if !strings.HasPrefix(directive, "max-age=") {
			continue
		}
		seconds, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(directive, "max-age="), `"`))
		if err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second, true
		}
	}
	return 0, false
}

func (publisher agentAccessSecurityPublisher) PublishAgentAccessSecurityChange(
	_ context.Context,
	event agentaccess.SecurityChangeEvent,
) error {
	if publisher.source == nil {
		return agentaccessauth.ErrStreamRevalidationInvalid
	}
	change := agentaccessauth.SecurityChange{
		WorkspaceID: event.WorkspaceID, AgentID: event.AgentID,
		ClientID: event.ClientID, GrantID: event.GrantID,
		SecurityVersion: event.SecurityVersion,
	}
	if publisher.cache != nil {
		if err := publisher.cache.Invalidate(change); err != nil {
			return err
		}
	}
	return publisher.source.Publish(change)
}

func (verifier *modelConfigVerifier) Verify(ctx context.Context, config modelconfig.Config) error {
	target, err := modelEndpoint(config.APIBase, "models")
	if err != nil {
		return err
	}
	invoke := func(token []byte) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		if len(token) > 0 {
			request.Header.Set("Authorization", "Bearer "+string(token))
		}
		response, err := verifier.client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return modelconfig.ErrUpstreamAuthentication
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("model verification returned HTTP_STATUS_%d", response.StatusCode)
		}
		return nil
	}
	if config.CredentialSecretID == nil {
		return invoke(nil)
	}
	return verifier.secrets.WithActiveSecret(ctx, config.WorkspaceID, *config.CredentialSecretID, invoke)
}

func modelEndpoint(base, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid model API base")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	return parsed.String(), nil
}

// connectionProviderReader is the Provider lookup surface used by connection
// verification (config/network probe only).
type connectionProviderReader interface {
	Get(context.Context, string, string) (provider.Provider, error)
}

type serviceConnectionVerifier struct {
	client    *http.Client
	providers connectionProviderReader
	injector  *execution.HTTPSecretInjector
}

func (verifier *serviceConnectionVerifier) Verify(ctx context.Context, value connection.Connection) error {
	configuredProvider, err := verifier.providers.Get(ctx, value.WorkspaceID, value.ProviderID)
	if err != nil {
		return err
	}
	if configuredProvider.Status != "ACTIVE" {
		return connection.ErrConflict
	}
	endpoint, err := serviceendpoint.Parse(configuredProvider.EndpointConfig)
	if err != nil {
		return err
	}
	if strings.TrimSpace(endpoint.ServiceBaseURL) == "" {
		return errors.New("provider service base URL is required")
	}
	var network struct {
		Egress  execution.EgressPolicy `json:"egress"`
		Headers map[string]string      `json:"headers"`
	}
	if err := json.Unmarshal(configuredProvider.EndpointConfig, &network); err != nil {
		return err
	}
	target := endpoint.VerificationURL()
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("invalid connection verification URL")
	}
	policy := network.Egress
	if len(value.Policy) > 0 {
		var override struct {
			Egress execution.EgressPolicy `json:"egress"`
		}
		if json.Unmarshal(value.Policy, &override) == nil && egressConfigured(override.Egress) {
			policy = override.Egress
		}
	}
	policy = defaultEgressPolicy(policy, parsed)
	guard, err := execution.NewHTTPNetworkGuard(policy, nil)
	if err != nil {
		return err
	}
	if err := guard.ValidateURL(ctx, parsed); err != nil {
		return err
	}
	providerHeaders := make(map[string]string, len(network.Headers))
	for name, headerValue := range network.Headers {
		providerHeaders[name] = headerValue
	}
	connectionSnapshot := execution.ConnectionSnapshot{
		ID: value.ID, WorkspaceID: value.WorkspaceID, ProviderID: value.ProviderID,
		BaseURL: parsed.String(), Headers: providerHeaders, EgressPolicy: policy,
	}
	probe := func(injected execution.ConnectionSnapshot) error {
		return verifier.probeVerificationHTTP(ctx, endpoint, parsed, guard, injected)
	}
	// T5: dual-mode verification validates config/network/machine trust only.
	// Do not inject final-user passthrough Token or perform Broker user exchange.
	dualMode, err := dualModeConnectionVerification(value)
	if err != nil {
		return err
	}
	if dualMode {
		return probe(connectionSnapshot)
	}
	credential := execution.CredentialReference{
		WorkspaceID: value.WorkspaceID, AuthMode: value.AuthMode,
		AuthConfig:           append(json.RawMessage(nil), value.AuthConfig...),
		ProviderDriverConfig: append(json.RawMessage(nil), configuredProvider.DriverConfig...),
		SecretFingerprint:    value.CredentialFingerprint,
	}
	if value.CredentialSecretID != nil {
		credential.SecretID = *value.CredentialSecretID
	}
	var allowedHeaders struct {
		AllowedCredentialHeaders []string `json:"allowedCredentialHeaders"`
	}
	_ = json.Unmarshal(value.Policy, &allowedHeaders)
	credential.AllowedCredentialHeaders = allowedHeaders.AllowedCredentialHeaders
	return verifier.injector.WithInjectedConnection(ctx, connectionSnapshot, credential, probe)
}

// dualModeConnectionVerification reports whether Verify must skip legacy
// business-secret injection (outbound-user-auth dual-mode path).
func dualModeConnectionVerification(value connection.Connection) (bool, error) {
	identity, err := connection.ParseStoredOutboundIdentity(value.OutboundIdentity)
	if err != nil {
		return false, err
	}
	if identity != nil {
		return true, nil
	}
	// Empty dual-mode path: explicit mode markers without stored identity JSON
	// (or with null identity) still must not go through legacy secret injection.
	authMode := strings.ToUpper(strings.TrimSpace(value.AuthMode))
	if authMode == "BROKER_OBO" || authMode == "REQUEST_PASSTHROUGH" {
		return true, nil
	}
	return false, nil
}

func (verifier *serviceConnectionVerifier) probeVerificationHTTP(
	ctx context.Context,
	endpoint serviceendpoint.Config,
	target *url.URL,
	guard *execution.HTTPNetworkGuard,
	snapshot execution.ConnectionSnapshot,
) error {
	request, err := http.NewRequestWithContext(ctx, endpoint.Verification.Method, target.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json, */*;q=0.1")
	for name, headerValue := range snapshot.Headers {
		request.Header.Set(name, headerValue)
	}
	client, err := guard.ProtectClient(verifier.client, snapshot.SensitiveHeaderNames)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		// Keep operator-actionable class without embedding request secrets.
		return fmt.Errorf("connection verification request failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return connection.ErrUpstreamAuthentication
	}
	if !endpoint.Verification.Accepts(response.StatusCode) {
		// Token form is parsed into diagnostics.detail / ops logs (no response body).
		return fmt.Errorf("connection verification returned HTTP_STATUS_%d", response.StatusCode)
	}
	return nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func egressConfigured(policy execution.EgressPolicy) bool {
	return len(policy.AllowedHosts) > 0 || len(policy.AllowedPorts) > 0 ||
		len(policy.AllowedCIDRs) > 0 || policy.MaxRedirects != 0
}

func defaultEgressPolicy(policy execution.EgressPolicy, target *url.URL) execution.EgressPolicy {
	if len(policy.AllowedHosts) == 0 {
		policy.AllowedHosts = []string{target.Hostname()}
	}
	if len(policy.AllowedPorts) == 0 {
		port := 80
		if target.Scheme == "https" {
			port = 443
		}
		if target.Port() != "" {
			if parsed, err := strconv.Atoi(target.Port()); err == nil {
				port = parsed
			}
		}
		policy.AllowedPorts = []int{port}
	}
	return policy
}

type permanentObjectWriter interface {
	Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error)
}

type openAPIRawStore struct {
	db      *sql.DB
	objects permanentObjectWriter
}

func (store *openAPIRawStore) StorePermanentOpenAPI(
	ctx context.Context,
	workspaceID, contentType string,
	content []byte,
) (string, error) {
	var ownerID string
	if err := store.db.QueryRowContext(ctx, `
		SELECT owner_user_id FROM workspaces
		WHERE id=$1 AND deleted_at IS NULL
	`, workspaceID).Scan(&ownerID); err != nil {
		return "", fmt.Errorf("resolve openapi object creator: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	created, err := store.objects.Put(ctx, storedobject.PutInput{
		ID: id.String(), WorkspaceID: workspaceID, Kind: storedobject.KindOpenAPISource,
		ContentType: strings.TrimSpace(contentType), SizeBytes: int64(len(content)),
		SHA256: hex.EncodeToString(digest[:]), Classification: storedobject.ClassificationSensitive,
		RetentionMode: storedobject.RetentionPermanent, CreatedByType: storedobject.CreatorUser,
		CreatedByID: ownerID, Reader: bytes.NewReader(content),
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

type openAPIDiscoverer struct {
	loader *openapiimport.HTTPProviderDocumentLoader
}

func (discoverer *openAPIDiscoverer) DiscoverHTTP(
	ctx context.Context,
	request provider.DiscoveryRequest,
) (provider.DiscoveryPage, error) {
	endpoint, err := serviceendpoint.Parse(request.Provider.EndpointConfig)
	if err != nil {
		return provider.DiscoveryPage{}, provider.ErrInvalid
	}
	var network struct {
		Egress execution.EgressPolicy `json:"egress"`
	}
	_ = json.Unmarshal(request.Provider.EndpointConfig, &network)
	source := openapiimport.ProviderImportSource{
		Provider: request.Provider, Connection: request.Connection,
		SourceURI: endpoint.Discovery.DocumentURL, EgressPolicy: network.Egress,
	}
	if revision := strings.TrimSpace(endpoint.Discovery.SourceRevision); revision != "" {
		source.ConfiguredRevision = &revision
	}
	loaded, err := discoverer.loader.LoadProviderDocument(ctx, source)
	if err != nil {
		return provider.DiscoveryPage{}, err
	}
	parsed, err := openapiimport.ParseDocument(openapiimport.ParseInput{
		FileName: loaded.FileName, Content: loaded.Content,
	})
	if err != nil {
		return provider.DiscoveryPage{}, err
	}
	assets := make([]provider.Asset, 0, len(parsed.Endpoints))
	for _, endpoint := range parsed.Endpoints {
		inputSchema, outputSchema := discoverySchemas(endpoint)
		parameters := discoveryActionParameters(endpoint.RequestParams)
		risk, sideEffect := discoveryRisk(endpoint.Method)
		metadata, marshalErr := json.Marshal(map[string]any{
			"actionConfig": map[string]any{
				"method": strings.ToUpper(strings.TrimSpace(endpoint.Method)),
				"path":   strings.TrimSpace(endpoint.Path), "parameters": parameters,
			},
			"errorMappings": map[string]any{},
			"runtimePolicy": map[string]any{"timeoutMs": 10000, "maxResponseBytes": 1048576},
			"riskLevel":     risk, "sideEffectLevel": sideEffect,
			"requiresConfirmation": false,
		})
		if marshalErr != nil {
			return provider.DiscoveryPage{}, marshalErr
		}
		externalID := strings.TrimSpace(endpoint.OperationID)
		if externalID == "" {
			externalID = strings.TrimSpace(endpoint.ToolIDCandidate)
		}
		name := strings.TrimSpace(endpoint.Summary)
		if name == "" {
			name = externalID
		}
		digest := sha256.Sum256([]byte(strings.Join([]string{
			loaded.SourceRevision, endpoint.Method, endpoint.Path,
			string(inputSchema), string(outputSchema),
		}, "\x00")))
		assets = append(assets, provider.Asset{
			Kind: "TOOL", ExternalID: externalID, Name: name,
			Description: strings.TrimSpace(endpoint.Summary), InputSchema: inputSchema,
			OutputSchema: outputSchema, Metadata: metadata,
			SourceRevision: loaded.SourceRevision, SourceChecksum: hex.EncodeToString(digest[:]),
			Status: "ACTIVE",
		})
	}
	start := 0
	if request.Cursor != "" {
		start, err = strconv.Atoi(request.Cursor)
		if err != nil || start < 0 || start > len(assets) {
			return provider.DiscoveryPage{}, provider.ErrInvalid
		}
	}
	limit := request.Limit
	if limit <= 0 || limit > len(assets)-start {
		limit = len(assets) - start
	}
	end := start + limit
	page := provider.DiscoveryPage{Assets: append([]provider.Asset(nil), assets[start:end]...)}
	if end < len(assets) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func discoverySchemas(endpoint domain.OpenAPIEndpoint) (json.RawMessage, json.RawMessage) {
	properties := make(map[string]any, len(endpoint.RequestParams))
	required := make([]string, 0)
	for index, parameter := range endpoint.RequestParams {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			name = fmt.Sprintf("parameter_%d", index+1)
		}
		baseName := name
		for suffix := 2; properties[name] != nil; suffix++ {
			name = fmt.Sprintf("%s_%d", baseName, suffix)
		}
		property := discoveryParameterSchema(parameter)
		property["x-actweave-location"] = strings.ToLower(strings.TrimSpace(parameter.Location))
		property["x-actweave-parameter-name"] = baseName
		properties[name] = property
		if parameter.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	input := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		input["required"] = required
	}
	outputProperties := make(map[string]any, len(endpoint.ResponseFields))
	for index, field := range endpoint.ResponseFields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			name = fmt.Sprintf("field_%d", index+1)
		}
		baseName := name
		for suffix := 2; outputProperties[name] != nil; suffix++ {
			name = fmt.Sprintf("%s_%d", baseName, suffix)
		}
		outputProperties[name] = discoveryResponseSchema(field)
	}
	inputJSON, _ := json.Marshal(input)
	outputJSON, _ := json.Marshal(map[string]any{"type": "object", "properties": outputProperties})
	return inputJSON, outputJSON
}

func discoveryParameterSchema(parameter domain.ToolParameter) map[string]any {
	value := discoverySchemaBase(parameter.Type, parameter.Description)
	if parameter.DefaultValue != nil {
		value["default"] = parameter.DefaultValue
	}
	if len(parameter.Children) > 0 {
		properties := make(map[string]any, len(parameter.Children))
		required := make([]string, 0)
		for _, child := range parameter.Children {
			properties[child.Name] = discoveryParameterSchema(child)
			if child.Required {
				required = append(required, child.Name)
			}
		}
		value["type"], value["properties"] = "object", properties
		if len(required) > 0 {
			sort.Strings(required)
			value["required"] = required
		}
	}
	if parameter.Item != nil {
		value["type"], value["items"] = "array", discoveryParameterSchema(*parameter.Item)
	}
	return value
}

func discoveryResponseSchema(field domain.ToolResponseField) map[string]any {
	value := discoverySchemaBase(field.Type, field.Description)
	if len(field.Children) > 0 {
		properties := make(map[string]any, len(field.Children))
		for _, child := range field.Children {
			properties[child.Name] = discoveryResponseSchema(child)
		}
		value["type"], value["properties"] = "object", properties
	}
	if field.Item != nil {
		value["type"], value["items"] = "array", discoveryResponseSchema(*field.Item)
	}
	return value
}

func discoverySchemaBase(schemaType, description string) map[string]any {
	value := map[string]any{}
	if schemaType = strings.TrimSpace(schemaType); schemaType != "" {
		value["type"] = schemaType
	}
	if description = strings.TrimSpace(description); description != "" {
		value["description"] = description
	}
	return value
}

func discoveryActionParameters(parameters []domain.ToolParameter) []map[string]any {
	values := make([]map[string]any, 0, len(parameters))
	for index, parameter := range parameters {
		inputName := strings.TrimSpace(parameter.Name)
		if inputName == "" {
			inputName = fmt.Sprintf("parameter_%d", index+1)
		}
		values = append(values, map[string]any{
			"name":  strings.TrimSpace(parameter.Name),
			"in":    strings.ToLower(strings.TrimSpace(parameter.Location)),
			"input": inputName, "required": parameter.Required || strings.EqualFold(parameter.Location, "path"),
		})
	}
	return values
}

func discoveryRisk(method string) (string, string) {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return "LOW", "READ"
	default:
		return "MEDIUM", "WRITE"
	}
}

type modelSnapshotSource struct {
	models *modelconfig.Repository
}

func (source *modelSnapshotSource) Snapshot(
	ctx context.Context,
	workspaceID, modelConfigID string,
) (json.RawMessage, error) {
	config, err := source.models.Get(ctx, workspaceID, modelConfigID)
	if err != nil {
		return nil, err
	}
	return marshalModelSnapshot(config)
}

// AvailableSnapshot is the create-preview model gate: same Workspace, not
// deleted, VERIFIED, and credential configured.
func (source *modelSnapshotSource) AvailableSnapshot(
	ctx context.Context,
	workspaceID, modelConfigID string,
) (json.RawMessage, error) {
	config, err := source.models.Get(ctx, workspaceID, modelConfigID)
	if err != nil {
		return nil, err
	}
	if config.DeletedAt != nil || config.Status != modelconfig.StatusVerified ||
		!config.CredentialConfigured {
		return nil, agent.ErrPromptModelUnavailable
	}
	return marshalModelSnapshot(config)
}

func marshalModelSnapshot(config modelconfig.Config) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"id": config.ID, "provider": config.Provider, "apiBase": config.APIBase,
		"modelName": config.ModelName, "options": json.RawMessage(config.Options),
		"status": config.Status, "lockVersion": config.LockVersion,
	})
}

// modelConfigReader loads a workspace model config for auxiliary LLM calls.
// *modelconfig.Repository satisfies this in production.
type modelConfigReader interface {
	Get(ctx context.Context, workspaceID, configID string) (modelconfig.Config, error)
}

// promptGenerationTimeout matches frontend enhance-prompt axios timeout (210s)
// and modelapi defaultModelTimeout. The shared application HTTP client is only
// 15s and must not be used for LLM Generate.
const promptGenerationTimeout = 210 * time.Second

// promptGenerator uses the shared eino-ext OpenAI ChatModel boundary (same as
// agent chatruntimebridge / smartdag). No parallel hand-rolled Completions client.
type promptGenerator struct {
	models  modelConfigReader
	secrets modelapi.SecretOpener
	client  *http.Client
}

func (generator *promptGenerator) llmHTTPClient() *http.Client {
	// Prefer an explicit stream-safe client. Reject short overall Timeout from the
	// shared app client (15s) which surfaces as PROMPT_GENERATION_TIMEOUT ~15s.
	if generator != nil && generator.client != nil {
		if generator.client.Timeout == 0 || generator.client.Timeout >= promptGenerationTimeout {
			return generator.client
		}
	}
	return modelapi.NewStreamingHTTPClient()
}

func (generator *promptGenerator) Generate(
	ctx context.Context,
	request agent.PromptGenerationRequest,
) (string, error) {
	if generator == nil || generator.models == nil || generator.secrets == nil {
		return "", agent.ErrPromptGeneration
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	modelConfigID := strings.TrimSpace(request.ModelConfigID)
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(request.Agent.WorkspaceID)
	}
	if modelConfigID == "" {
		modelConfigID = strings.TrimSpace(request.Agent.ModelConfigID)
	}
	if workspaceID == "" || modelConfigID == "" {
		return "", agent.ErrInvalid
	}
	cfg, err := generator.models.Get(ctx, workspaceID, modelConfigID)
	if err != nil {
		return "", err
	}
	// Bound wall time even when HTTP client has Timeout=0 (stream-safe client).
	genCtx, cancel := context.WithTimeout(ctx, promptGenerationTimeout)
	defer cancel()
	chatModel, err := modelapi.NewEinoOpenAIChatModel(genCtx, generator.llmHTTPClient(), generator.secrets, cfg)
	if err != nil {
		return "", fmt.Errorf("%w: %v", agent.ErrPromptGeneration, err)
	}
	userPrompt := strings.TrimSpace(request.Input)
	if userPrompt == "" {
		userPrompt = "请为该 Agent 优化系统提示词，使其更清晰、可执行，并使用 Markdown 结构化编写。"
	}
	msg, err := chatModel.Generate(genCtx, []*schema.Message{
		{
			Role: schema.System,
			Content: "你是系统提示词润色助手。根据用户提供的草稿或整理要求，输出优化后的系统提示词。" +
				"必须使用 Markdown 格式组织内容（可用标题、列表、加粗、引用等），结构清晰、便于阅读与维护。" +
				"只返回提示词正文本身，不要额外解释、前言或后记；不要用外层 ``` 代码块把整篇包起来。",
		},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", agent.ErrPromptGeneration, err)
	}
	if msg == nil || strings.TrimSpace(msg.Content) == "" {
		return "", fmt.Errorf("%w: model generation returned no content", agent.ErrPromptGeneration)
	}
	return strings.TrimSpace(msg.Content), nil
}

type bindingConnectionCompatibility struct {
	db *sql.DB
}

func (checker *bindingConnectionCompatibility) ValidateBindingConnection(
	ctx context.Context,
	workspaceID, capabilityID, connectionID string,
) error {
	var compatible bool
	// tools has no deleted_at — soft-delete lives on capabilities.
	if err := checker.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM tools t
			JOIN capabilities cap
			  ON cap.workspace_id=t.workspace_id
			 AND cap.id=t.capability_id
			 AND cap.deleted_at IS NULL
			JOIN service_connections c
			  ON c.workspace_id=t.workspace_id
			 AND c.provider_id=t.provider_id
			 AND c.id=$3
			 AND c.deleted_at IS NULL
			WHERE t.workspace_id=$1 AND t.capability_id=$2
		)
	`, workspaceID, capabilityID, connectionID).Scan(&compatible); err != nil {
		return fmt.Errorf("check binding connection compatibility: %w", err)
	}
	if !compatible {
		return capability.ErrInvalid
	}
	return nil
}

type idempotencyEntry struct {
	inputHash string
	result    execution.InvocationResult
	complete  bool
}

// invocationIdempotencyStore coordinates retries within this process. The
// permanent invocation recorder's database uniqueness constraint is the final
// cross-process guard before an executor performs any side effect.
type invocationIdempotencyStore struct {
	mu      sync.Mutex
	entries map[string]idempotencyEntry
}

func newInvocationIdempotencyStore() *invocationIdempotencyStore {
	return &invocationIdempotencyStore{entries: make(map[string]idempotencyEntry)}
}

func (store *invocationIdempotencyStore) BeginInvocation(
	_ context.Context,
	request execution.IdempotencyRequest,
) (execution.IdempotencyDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := idempotencyMapKey(request)
	entry, exists := store.entries[key]
	if !exists {
		store.entries[key] = idempotencyEntry{inputHash: request.InputHash}
		return execution.IdempotencyDecision{State: execution.IdempotencyNew}, nil
	}
	if entry.inputHash != request.InputHash || !entry.complete {
		return execution.IdempotencyDecision{State: execution.IdempotencyConflict}, nil
	}
	return execution.IdempotencyDecision{State: execution.IdempotencyCached, Result: entry.result}, nil
}

func (store *invocationIdempotencyStore) CompleteInvocation(
	_ context.Context,
	request execution.IdempotencyRequest,
	result execution.InvocationResult,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := idempotencyMapKey(request)
	entry, exists := store.entries[key]
	if !exists || entry.inputHash != request.InputHash {
		return errors.New("idempotency claim does not exist")
	}
	entry.result, entry.complete = result, true
	store.entries[key] = entry
	return nil
}

func (store *invocationIdempotencyStore) FailInvocation(
	_ context.Context,
	request execution.IdempotencyRequest,
	_ string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := idempotencyMapKey(request)
	if entry, exists := store.entries[key]; exists && entry.inputHash == request.InputHash {
		delete(store.entries, key)
	}
	return nil
}

func idempotencyMapKey(request execution.IdempotencyRequest) string {
	return request.WorkspaceID + "\x00" + request.ToolVersionID + "\x00" + request.Key
}

type allowInvocationLimiter struct{}

func (allowInvocationLimiter) AllowInvocation(context.Context, execution.LimitRequest) error {
	return nil
}

type retryWaiter struct{}

func (retryWaiter) WaitBeforeRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt) * 100 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newToolIDs() (openapiimport.ToolIDs, error) {
	capabilityID, err := uuid.NewV7()
	if err != nil {
		return openapiimport.ToolIDs{}, err
	}
	versionID, err := uuid.NewV7()
	if err != nil {
		return openapiimport.ToolIDs{}, err
	}
	return openapiimport.ToolIDs{CapabilityID: capabilityID.String(), VersionID: versionID.String()}, nil
}

type workflowToolInvoker struct {
	invoker *tool.DirectInvocationService
}

func (invoker *workflowToolInvoker) Invoke(
	toolID string,
	input map[string]any,
	invocationContext workflowruntime.ToolInvocationContext,
) (map[string]any, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	invocationID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	traceID := strings.TrimSpace(invocationContext.TraceID)
	if traceID == "" {
		traceID = "workflow-tool/" + strings.TrimSpace(toolID)
	}
	// Preserve "trial" marker so outbound inject maps to WORKFLOW_TRIAL vault roots.
	result, err := invoker.invoker.Invoke(context.Background(), execution.InvokeRequest{
		InvocationID: invocationID.String(), WorkspaceID: invocationContext.WorkspaceID,
		CapabilityID: strings.TrimSpace(toolID), ActorType: defaultWorkflowActorType(invocationContext.ActorType),
		ActorID: invocationContext.UserID, TraceID: traceID,
		Input: payload, IdempotencyKey: workflowIdempotencyKey(invocationContext),
		AgentRunID: invocationContext.AgentRunID, WorkflowExecutionID: invocationContext.WorkflowExecutionID,
		ExecutionStepID:       invocationContext.ExecutionStepID,
		PrincipalSnapshot:     invocationContext.PrincipalSnapshot,
		AuthorizationSnapshot: invocationContext.AuthorizationSnapshot,
	})
	if err != nil {
		return nil, err
	}
	var output map[string]any
	if len(result.Output) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(result.Output, &output); err == nil && output != nil {
		return output, nil
	}
	var value any
	if err := json.Unmarshal(result.Output, &value); err != nil {
		return nil, err
	}
	return map[string]any{"value": value}, nil
}

func defaultWorkflowActorType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "USER"
	}
	return value
}

func workflowIdempotencyKey(invocationContext workflowruntime.ToolInvocationContext) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		invocationContext.WorkflowID, invocationContext.NodeID, invocationContext.TraceID,
	}, "\x00")))
	return "workflow:" + hex.EncodeToString(digest[:])
}

type agentRunSnapshots struct {
	agents         *agent.Repository
	models         *modelconfig.Repository
	catalog        *capability.Catalog
	workspaces     *workspace.Repository
	sessionContext config.SessionContextRollout
}

func (source *agentRunSnapshots) SnapshotAgentRun(
	ctx context.Context,
	workspaceID, agentID string,
) (execution.AgentRunSnapshots, error) {
	configuredAgent, err := source.agents.Get(ctx, workspaceID, agentID)
	if err != nil {
		return execution.AgentRunSnapshots{}, err
	}
	model, err := source.models.Get(ctx, workspaceID, configuredAgent.ModelConfigID)
	if err != nil {
		return execution.AgentRunSnapshots{}, err
	}
	capabilities, err := source.catalog.ListForAgent(ctx, workspaceID, agentID)
	if err != nil {
		return execution.AgentRunSnapshots{}, err
	}
	modelPayload := map[string]any{
		"id": model.ID, "provider": model.Provider, "apiBase": model.APIBase,
		"modelName": model.ModelName, "options": json.RawMessage(model.Options),
		"lockVersion": model.LockVersion,
	}
	if model.CredentialSecretID != nil {
		modelPayload["credentialSecretId"] = *model.CredentialSecretID
	}
	// agent_runs.capability_snapshot and canonicalRunObject require a JSON object.
	// Catalog returns a slice; wrap it so chat SendMessage does not fail with
	// ErrRunInvalid ("invalid run record") after HTTP authorization already passed.
	//
	// Snapshot pins resolved release ids at run start. Chat runtime must never
	// re-FOLLOW_ACTIVE; only these immutable fields are used for tools mapping.
	releases := make([]map[string]any, 0, len(capabilities))
	for _, item := range capabilities {
		entry := map[string]any{
			"capabilityId":         item.CapabilityID,
			"releaseId":            item.ReleaseID,
			"kind":                 item.Kind,
			"callableName":         item.CallableName,
			"callableDescription":  item.CallableDescription,
			"inputSchema":          json.RawMessage(item.InputSchema),
			"outputSchema":         json.RawMessage(item.OutputSchema),
			"riskLevel":            item.RiskLevel,
			"sideEffectLevel":      item.SideEffectLevel,
			"requiresConfirmation": item.RequiresConfirmation,
		}
		if item.ConnectionID != "" {
			entry["connectionId"] = item.ConnectionID
		}
		releases = append(releases, entry)
	}
	capabilitiesJSON, err := json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1",
		"releases":      releases,
	})
	if err != nil {
		return execution.AgentRunSnapshots{}, err
	}

	// Default legacy path (gate off or incomplete config).
	legacy := execution.AgentRunSnapshots{
		SchemaVersion: execution.RunSnapshotSchemaV1,
		Capabilities:  capabilitiesJSON,
		ContextPolicy: json.RawMessage(`{}`),
		Agent:         json.RawMessage(`{}`),
	}

	gate := source.sessionContext.Normalized()
	useV2 := gate.AllowsWorkspace(workspaceID)
	runtimeCaps, capsRaw, capsErr := modelconfig.ParseRuntimeCapabilities(model.RuntimeCapabilities)
	if useV2 && capsErr == nil && runtimeCaps.SchemaVersion == modelconfig.RuntimeCapabilitiesSchemaV1 {
		modelPayload["runtimeCapabilities"] = json.RawMessage(capsRaw)

		var workspacePolicy json.RawMessage
		var workspaceLock int64
		if source.workspaces != nil {
			ws, wsErr := source.workspaces.Get(ctx, workspaceID)
			if wsErr != nil {
				return execution.AgentRunSnapshots{}, wsErr
			}
			workspacePolicy = ws.ContextPolicy
			workspaceLock = ws.LockVersion
		}
		compactionOn := gate.AllowsCompaction(workspaceID)
		resolved, contextJSON, resolveErr := sessioncontext.Resolve(sessioncontext.ResolveInput{
			WorkspacePolicy:            workspacePolicy,
			AgentPolicy:                configuredAgent.ContextPolicy,
			ContextWindowTokens:        runtimeCaps.ContextWindowTokens,
			DefaultOutputReserveTokens: runtimeCaps.DefaultOutputReserveTokens,
			OutputTokenLimitMode:       runtimeCaps.OutputTokenLimitMode,
			TokenizerProfile:           runtimeCaps.TokenizerProfile,
			TokenizerVersion:           runtimeCaps.TokenizerVersion,
			WorkspaceLockVersion:       workspaceLock,
			AgentLockVersion:           configuredAgent.LockVersion,
			RolloutVersion:             gate.RolloutVersion,
			GateEnabled:                true,
			CompactionGateEnabled:      compactionOn,
			CompactionRolloutVersion:   gate.Compaction.Normalized().RolloutVersion,
		})
		if resolveErr == nil && (resolved.SchemaVersion == sessioncontext.SnapshotSchemaV1 ||
			resolved.SchemaVersion == sessioncontext.SnapshotSchemaV2) {
			revision, revErr := source.agents.GetCurrentPromptRevision(ctx, workspaceID, agentID)
			if revErr != nil {
				return execution.AgentRunSnapshots{}, revErr
			}
			agentSnap, agentErr := json.Marshal(map[string]any{
				"schemaVersion":          execution.AgentSnapshotSchemaV1,
				"agentId":                configuredAgent.ID,
				"promptRevisionId":       revision.ID,
				"promptRevisionHash":     revision.ContentSHA256,
				"modelConfigId":          model.ID,
				"modelConfigLockVersion": model.LockVersion,
			})
			if agentErr != nil {
				return execution.AgentRunSnapshots{}, agentErr
			}
			modelJSON, modelErr := json.Marshal(modelPayload)
			if modelErr != nil {
				return execution.AgentRunSnapshots{}, modelErr
			}
			return execution.AgentRunSnapshots{
				SchemaVersion: execution.RunSnapshotSchemaV2,
				Model:         modelJSON,
				Capabilities:  capabilitiesJSON,
				ContextPolicy: contextJSON,
				Agent:         agentSnap,
			}, nil
		}
		// Incomplete policy/model combination → fall back to legacy (do not fail create).
	}

	modelJSON, err := json.Marshal(modelPayload)
	if err != nil {
		return execution.AgentRunSnapshots{}, err
	}
	legacy.Model = modelJSON
	return legacy, nil
}

type runAuthorizer struct {
	authorizer *authz.Service
}

func (source *runAuthorizer) AuthorizeRun(
	ctx context.Context,
	actorType, actorID, workspaceID, action, resourceID string,
) (json.RawMessage, error) {
	if strings.TrimSpace(actorType) != "USER" {
		return nil, authz.ErrDenied
	}
	access, err := source.authorizer.AuthorizeWorkspace(ctx, actorID, workspaceID, authz.ActionExecute)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"workspaceId": access.WorkspaceID, "actorId": access.UserID,
		"actorType": actorType, "role": access.Role, "action": action,
		"resourceId": resourceID, "authorizedAction": access.Action,
	})
}

// productionRuns adapts RunService (authorized start) + RunRepository (get/transition)
// for workflow production :execute (WP2).
type productionRuns struct {
	service *execution.RunService
	repo    *execution.RunRepository
}

func (p *productionRuns) StartWorkflowExecution(
	ctx context.Context,
	request execution.StartWorkflowExecutionRequest,
) (execution.WorkflowExecution, error) {
	return p.service.StartWorkflowExecution(ctx, request)
}

func (p *productionRuns) TransitionWorkflowExecution(
	ctx context.Context,
	workspaceID, executionID string,
	transition execution.RunTransition,
) (execution.WorkflowExecution, error) {
	return p.repo.TransitionWorkflowExecution(ctx, workspaceID, executionID, transition)
}

func (p *productionRuns) GetWorkflowExecution(
	ctx context.Context,
	workspaceID, executionID string,
) (execution.WorkflowExecution, error) {
	return p.repo.GetWorkflowExecution(ctx, workspaceID, executionID)
}

// chatModelTurnRecorder adapts agent.ModelTurnContentService for chatruntime.
type chatModelTurnRecorder struct {
	inner *agent.ModelTurnContentService
}

func (recorder *chatModelTurnRecorder) Record(
	ctx context.Context,
	input chatruntime.ModelTurnRecordInput,
) (execution.AgentRunStep, error) {
	return recorder.inner.Record(ctx, agent.RecordModelTurnInput{
		WorkspaceID: input.WorkspaceID, StepID: input.StepID, Content: input.Content,
		CreatedByType: input.CreatedByType, CreatedByID: input.CreatedByID,
		ExpectedStatus: input.ExpectedStatus, NewStatus: input.NewStatus, ErrorCode: input.ErrorCode,
		Reasoning: input.Reasoning,
	})
}

// workflowCapabilityRunner runs a published WORKFLOW capability as an agent tool
// (P3.4 Console Chat). Avoids tool_invocations FK to tool_versions.
type workflowCapabilityRunner interface {
	Run(ctx context.Context, request workflowruntime.PublishedRunRequest) (workflowruntime.PublishedRunResult, error)
}

// chatToolInvoker adapts the tool resolver + invocation pipeline for chatruntime.
// WORKFLOW capabilities are dispatched to the published-revision runner (not the
// HTTP tool pipeline, which cannot record into tool_invocations).
type chatToolInvoker struct {
	resolver   *tool.InvocationResolver
	pipeline   *execution.InvocationPipeline
	workflows  workflowCapabilityRunner
	authorizer execution.InvocationAuthorizer
}

func (invoker *chatToolInvoker) ResolveInvocation(
	ctx context.Context,
	request execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
	return invoker.resolver.ResolveInvocation(ctx, request)
}

func (invoker *chatToolInvoker) InvokeResolved(
	ctx context.Context,
	request execution.InvokeRequest,
	resolved execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	if strings.EqualFold(strings.TrimSpace(resolved.Snapshot.ExecutorType), execution.ExecutorTypeWORKFLOW) {
		return invoker.invokeWorkflow(ctx, request, resolved)
	}
	return invoker.pipeline.InvokeResolved(ctx, request, resolved)
}

func (invoker *chatToolInvoker) invokeWorkflow(
	ctx context.Context,
	request execution.InvokeRequest,
	resolved execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	if invoker.workflows == nil {
		return execution.PipelineResult{}, execution.NewError(
			execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil,
		)
	}
	if resolved.Snapshot.WorkspaceID != request.WorkspaceID ||
		resolved.Snapshot.CapabilityID != request.CapabilityID ||
		resolved.Snapshot.ReleaseID != request.ReleaseID {
		return execution.PipelineResult{}, execution.NewError(
			execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil,
		)
	}
	if invoker.authorizer != nil {
		// Mirror execution.InvocationPipeline.authorizeInvoke: AAP service
		// principals are not workspace members; PrincipalSnapshot was validated
		// by the pipeline request gate / published-run path.
		switch strings.TrimSpace(request.ActorType) {
		case "SERVICE_PRINCIPAL", "SYSTEM":
			// grant-bound principal; skip user membership lookup
		default:
			if err := invoker.authorizer.AuthorizeInvocation(ctx, request.ActorID, request.WorkspaceID); err != nil {
				return execution.PipelineResult{}, err
			}
		}
	}
	input := map[string]any{}
	if len(request.Input) > 0 {
		if err := json.Unmarshal(request.Input, &input); err != nil {
			return execution.PipelineResult{}, execution.NewError(
				execution.ErrorCodeInputSchema, "VALIDATION", false, 0, err,
			)
		}
	}
	runResult, err := invoker.workflows.Run(ctx, workflowruntime.PublishedRunRequest{
		WorkspaceID: request.WorkspaceID, CapabilityID: request.CapabilityID,
		ReleaseID: request.ReleaseID, ActorID: request.ActorID, ActorType: request.ActorType,
		PrincipalSnapshot:     request.PrincipalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), request.AuthorizationSnapshot...),
		AgentRunID:            request.AgentRunID, WorkflowExecutionID: request.WorkflowExecutionID,
		Input: input,
	})
	if err != nil {
		return execution.PipelineResult{}, err
	}
	output, encodeErr := json.Marshal(map[string]any{
		"status":          string(runResult.Execution.Status),
		"revisionId":      runResult.Snapshot.RevisionID,
		"releaseId":       runResult.Snapshot.ReleaseID,
		"outputSummary":   runResult.Execution.OutputSummary,
		"waitingApproval": runResult.Approval != nil,
	})
	if encodeErr != nil {
		return execution.PipelineResult{}, encodeErr
	}
	// Non-success statuses surface as tool errors so the agent loop can recover;
	// Approval is returned as a successful tool result with waitingApproval=true.
	if runResult.Execution.Status != domain.ExecutionSuccess &&
		runResult.Execution.Status != domain.ExecutionApproval {
		return execution.PipelineResult{
				InvocationResult: execution.InvocationResult{
					InvocationID: request.InvocationID, TraceID: request.TraceID, Output: output,
				},
				Attempts: 1,
			}, execution.NewError(execution.ErrorCodeUpstream, "WORKFLOW", false, 0, errors.New(
				"workflow capability execution status="+string(runResult.Execution.Status),
			))
	}
	return execution.PipelineResult{
		InvocationResult: execution.InvocationResult{
			InvocationID: request.InvocationID, TraceID: request.TraceID, Output: output,
		},
		Attempts: 1,
	}, nil
}

// agentRunGetter loads AgentRun rows for continue dispatch (testable seam).
type agentRunGetter interface {
	GetAgentRun(ctx context.Context, workspaceID, runID string) (execution.AgentRun, error)
}

// approvedInteractionRecorder projects run.resumed + tool completed after approve.
type approvedInteractionRecorder interface {
	RecordApprovedInteraction(
		ctx context.Context,
		run execution.AgentRun,
		interactionID, invocationID, toolName string,
	) error
}

// chatConfirmationAPI is the durable Confirm/Cancel surface used by
// chatConfirmationContinue (testable seam over *chat.ConfirmationService).
type chatConfirmationAPI interface {
	Confirm(ctx context.Context, input chat.ConfirmChatConfirmationInput) (chat.ConfirmedChatConfirmation, error)
	Cancel(ctx context.Context, input chat.CancelChatConfirmationInput) (chat.CancelledChatConfirmation, error)
}

// einoCheckpointDeleter removes gob checkpoints on deny/cancel (optional).
type einoCheckpointDeleter interface {
	Delete(ctx context.Context, checkPointID string) error
}

// chatConfirmationContinue resumes the chat agent after a successful Confirm.
// It shares the same multi-replica runtime continue lease as AAP approval and
// Recovery Worker (Claim/Renew/Complete on runtime_continuation_claims).
//
// ContinueDispatcher: production is eino-only. Nested einoChatResume → eino
// EnqueueContinue; chatLoop-only snapshots are rejected. Dual presence still
// prefers eino via ExtractEinoChatResume (mutual exclusion on embed).
type chatConfirmationContinue struct {
	inner chatConfirmationAPI
	// eino is the production chatruntimebridge Runtime (required for resume).
	eino     agentrun.Runtime
	recovery *execution.ContinuationRecoveryService
	// checkpoints deletes eino gob rows on cancel when einoChatResume is present.
	checkpoints einoCheckpointDeleter
}

type aapInteractionContinuation struct {
	runs     agentRunGetter
	protocol approvedInteractionRecorder
	// eino is the production chatruntimebridge Runtime for einoChatResume continues (PR16).
	eino agentrun.Runtime
	// recovery provides the shared multi-replica continue lease used by both
	// the normal approval path and Recovery Worker.
	recovery *execution.ContinuationRecoveryService
}

// runtimeContinueLease adapts ContinuationRecoveryService to agentrun.ContinueLifecycle.
type runtimeContinueLease struct {
	recovery       *execution.ContinuationRecoveryService
	confirmationID string
	claimID        string
	lease          time.Duration
}

func (lease *runtimeContinueLease) Renew(ctx context.Context) error {
	if lease == nil || lease.recovery == nil {
		return execution.ErrContinuationRecoveryInvalid
	}
	_, err := lease.recovery.RenewRuntimeContinue(
		ctx, lease.confirmationID, lease.claimID, lease.lease,
	)
	return err
}

func (lease *runtimeContinueLease) Complete(ctx context.Context) error {
	if lease == nil || lease.recovery == nil {
		return execution.ErrContinuationRecoveryInvalid
	}
	return lease.recovery.CompleteRuntimeContinue(ctx, lease.confirmationID, lease.claimID)
}

func (service *aapInteractionContinuation) ContinueApprovedInteraction(
	ctx context.Context,
	decision execution.InteractionDecisionResult,
) error {
	if service == nil || service.runs == nil || service.protocol == nil || service.eino == nil {
		return execution.ErrInteractionDecisionInvalid
	}
	snap := decision.Checkpoint.RequestSnapshot
	// PR16 ContinueDispatcher (eino-only production):
	// 1) einoChatResume → eino continue (dual presence still succeeds here)
	// 2) chatLoop-only or missing → invalid
	einoMeta, einoOK := chatruntimebridge.ExtractEinoChatResume(snap)
	if !einoOK {
		return execution.ErrInteractionDecisionInvalid
	}

	run, err := service.runs.GetAgentRun(
		ctx, decision.Confirmation.WorkspaceID, decision.Confirmation.RunID,
	)
	if err != nil {
		return err
	}

	targetName := firstNonEmpty(einoMeta.GatedToolCallID, "tool")
	job := agentrun.Job{
		WorkspaceID: run.WorkspaceID, SessionID: einoMeta.SessionID, RunID: run.ID,
		UserMessageID: einoMeta.UserMessageID, ActorID: einoMeta.ActorID,
	}

	if err := service.protocol.RecordApprovedInteraction(
		ctx, run, decision.Confirmation.ID, decision.Checkpoint.TargetItemID,
		targetName,
	); err != nil {
		return err
	}
	// Shared multi-replica lease: approval path and Recovery Worker both enter
	// here, so only one replica can schedule EnqueueContinue for a confirmation.
	var lifecycle agentrun.ContinueLifecycle
	if service.recovery != nil {
		claim, claimErr := service.recovery.ClaimRuntimeContinue(
			ctx, run.WorkspaceID, decision.Confirmation.ID, run.ID,
			execution.DefaultRuntimeContinueLease,
		)
		if claimErr != nil {
			return claimErr
		}
		lifecycle = &runtimeContinueLease{
			recovery:       service.recovery,
			confirmationID: decision.Confirmation.ID,
			claimID:        claim.ClaimID,
			lease:          execution.DefaultRuntimeContinueLease,
		}
	}
	service.eino.EnqueueContinueWithLifecycle(
		job, decision.Checkpoint.RequestSnapshot, decision.Checkpoint.ResultSnapshot, lifecycle,
	)
	return nil
}

func (service *chatConfirmationContinue) Confirm(
	ctx context.Context,
	input chat.ConfirmChatConfirmationInput,
) (chat.ConfirmedChatConfirmation, error) {
	result, err := service.inner.Confirm(ctx, input)
	if err != nil {
		return chat.ConfirmedChatConfirmation{}, err
	}
	if result.Cached {
		return result, nil
	}
	snap := result.Resume.Checkpoint.RequestSnapshot
	einoMeta, einoOK := chatruntimebridge.ExtractEinoChatResume(snap)
	if !einoOK {
		// PR16: chatLoop-only snapshots are no longer resumable in production.
		if chatruntimebridge.HasChatLoop(snap) {
			return result, fmt.Errorf(
				"legacy chatLoop resume is no longer supported (PR16 eino-only): %w",
				execution.ErrInteractionDecisionInvalid,
			)
		}
		// Decision is durable; missing continue state is a no-op (non-tool confirm).
		return result, nil
	}
	if service.eino == nil {
		return result, fmt.Errorf(
			"einoChatResume present but eino runtime is not configured: %w",
			execution.ErrInteractionDecisionInvalid,
		)
	}
	// Shared multi-replica lease with AAP ContinueApprovedInteraction and
	// Recovery Worker. Confirmation key is the execution confirmation id
	// (same row recovery lists via confirmation_resume_checkpoints).
	var lifecycle agentrun.ContinueLifecycle
	if service.recovery != nil {
		confirmationID := strings.TrimSpace(result.Confirmation.ExecutionConfirmationID)
		if confirmationID == "" {
			confirmationID = strings.TrimSpace(result.Resume.Checkpoint.ConfirmationID)
		}
		claim, claimErr := service.recovery.ClaimRuntimeContinue(
			ctx,
			result.Confirmation.WorkspaceID,
			confirmationID,
			result.Confirmation.RunID,
			execution.DefaultRuntimeContinueLease,
		)
		if claimErr != nil {
			// Decision is already durable. Another replica owns the continue
			// drive — do not double-enqueue. Other claim failures surface so
			// the caller can retry without spawning an unleased continue.
			if errors.Is(claimErr, execution.ErrRuntimeContinueNotClaimed) {
				return result, nil
			}
			return result, claimErr
		}
		lifecycle = &runtimeContinueLease{
			recovery:       service.recovery,
			confirmationID: confirmationID,
			claimID:        claim.ClaimID,
			lease:          execution.DefaultRuntimeContinueLease,
		}
	}

	job := agentrun.Job{
		WorkspaceID: input.WorkspaceID, SessionID: einoMeta.SessionID,
		RunID: result.Confirmation.RunID, UserMessageID: einoMeta.UserMessageID,
		ActorID: firstNonEmpty(input.ActorID, einoMeta.ActorID),
	}
	service.eino.EnqueueContinueWithLifecycle(
		job, result.Resume.Checkpoint.RequestSnapshot, result.Resume.Result, lifecycle,
	)
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (service *chatConfirmationContinue) Cancel(
	ctx context.Context,
	input chat.CancelChatConfirmationInput,
) (chat.CancelledChatConfirmation, error) {
	result, err := service.inner.Cancel(ctx, input)
	if err != nil {
		return result, err
	}
	// Deny/cancel: drop eino gob checkpoint when nested resume meta is present
	// so stale HITL blobs do not survive TTL solely on cancel paths.
	if service.checkpoints != nil {
		snap := result.Checkpoint.RequestSnapshot
		if meta, ok := chatruntimebridge.ExtractEinoChatResume(snap); ok {
			_ = service.checkpoints.Delete(ctx, meta.EinoCheckpointID)
		}
	}
	return result, nil
}
