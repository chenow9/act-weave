package openapiimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"actweave/backend/internal/execution"
)

const (
	DefaultOpenAPIDocumentMaxBytes = int64(4 << 20)
	maximumOpenAPIDocumentMaxBytes = int64(16 << 20)
)

type ProviderHeaderAuthorizer interface {
	WithProviderHeaders(
		context.Context,
		ProviderImportSource,
		func(map[string]string, []string) error,
	) error
}

type DatabaseProviderHeaderAuthorizer struct {
	db       *sql.DB
	injector execution.SecretInjector
}

func NewDatabaseProviderHeaderAuthorizer(
	db *sql.DB,
	injector execution.SecretInjector,
) (*DatabaseProviderHeaderAuthorizer, error) {
	if db == nil || injector == nil {
		return nil, errors.New("provider header authorizer dependencies are required")
	}
	return &DatabaseProviderHeaderAuthorizer{db: db, injector: injector}, nil
}

func (a *DatabaseProviderHeaderAuthorizer) WithProviderHeaders(
	ctx context.Context,
	source ProviderImportSource,
	invoke func(map[string]string, []string) error,
) error {
	if invoke == nil || !validUUID(source.Provider.WorkspaceID) || !validUUID(source.Provider.ID) {
		return ErrProviderInvalid
	}
	var endpoint struct {
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(source.Provider.EndpointConfig, &endpoint) != nil {
		return ErrProviderInvalid
	}
	providerHeaders := make(map[string]string, len(endpoint.Headers))
	for name, value := range endpoint.Headers {
		providerHeaders[name] = value
	}
	if source.Connection == nil {
		return invoke(providerHeaders, nil)
	}
	var connection execution.ConnectionSnapshot
	var authMode string
	var authConfig, policyJSON json.RawMessage
	var secretID *string
	var status, secretFingerprint string
	// Schema-stable columns only: openapiimport tests may pin pre-000060 migrations.
	// Dual-mode rejection is by auth_mode; machine Secret is never selected here.
	err := a.db.QueryRowContext(ctx, `
		SELECT c.id,c.workspace_id,c.provider_id,c.auth_mode,c.auth_config,
		       c.credential_secret_id,c.policy,c.status,COALESCE(v.fingerprint,'')
		FROM service_connections c
		LEFT JOIN secrets s ON s.workspace_id=c.workspace_id AND s.id=c.credential_secret_id
		LEFT JOIN secret_versions v
		  ON v.workspace_id=s.workspace_id AND v.secret_id=s.id AND v.id=s.active_version_id AND v.revoked_at IS NULL
		WHERE c.workspace_id=$1 AND c.provider_id=$2 AND c.id=$3 AND c.deleted_at IS NULL
	`, source.Provider.WorkspaceID, source.Provider.ID, source.Connection.ID).Scan(
		&connection.ID, &connection.WorkspaceID, &connection.ProviderID,
		&authMode, &authConfig, &secretID, &policyJSON, &status, &secretFingerprint,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve provider document credential: %w", err)
	}
	if status != "VERIFIED" {
		return ErrProviderNotAvailable
	}
	// Background OpenAPI import has no final-user Subject. Dual-mode must fail
	// closed — never use Broker machine trust to fetch protected docs (T5 / §9).
	mode := strings.ToUpper(strings.TrimSpace(authMode))
	if mode == "BROKER_OBO" || mode == "REQUEST_PASSTHROUGH" {
		return ErrOutboundSubjectRequired
	}
	var policy struct {
		AllowedCredentialHeaders []string `json:"allowedCredentialHeaders"`
	}
	if json.Unmarshal(policyJSON, &policy) != nil {
		return ErrProviderInvalid
	}
	connection.Headers = providerHeaders
	connection.EgressPolicy = source.EgressPolicy
	reference := execution.CredentialReference{
		WorkspaceID: connection.WorkspaceID, AuthMode: authMode,
		AuthConfig:               append(json.RawMessage(nil), authConfig...),
		ProviderDriverConfig:     append(json.RawMessage(nil), source.Provider.DriverConfig...),
		SecretFingerprint:        secretFingerprint,
		AllowedCredentialHeaders: append([]string(nil), policy.AllowedCredentialHeaders...),
	}
	if secretID != nil {
		reference.SecretID = *secretID
	}
	return a.injector.WithInjectedConnection(ctx, connection, reference, func(injected execution.ConnectionSnapshot) error {
		return invoke(injected.Headers, injected.SensitiveHeaderNames)
	})
}

type PermanentOpenAPIRawStore interface {
	StorePermanentOpenAPI(context.Context, string, string, []byte) (string, error)
}

type HTTPProviderDocumentLoader struct {
	client     *http.Client
	authorizer ProviderHeaderAuthorizer
	rawStore   PermanentOpenAPIRawStore
	maxBytes   int64
}

func NewHTTPProviderDocumentLoader(
	client *http.Client,
	authorizer ProviderHeaderAuthorizer,
	rawStore PermanentOpenAPIRawStore,
	maxBytes int64,
) (*HTTPProviderDocumentLoader, error) {
	if authorizer == nil || rawStore == nil || maxBytes < 0 || maxBytes > maximumOpenAPIDocumentMaxBytes {
		return nil, errors.New("valid provider document loader dependencies are required")
	}
	if client == nil {
		client = &http.Client{}
	}
	if maxBytes == 0 {
		maxBytes = DefaultOpenAPIDocumentMaxBytes
	}
	return &HTTPProviderDocumentLoader{client: client, authorizer: authorizer, rawStore: rawStore, maxBytes: maxBytes}, nil
}

func (loader *HTTPProviderDocumentLoader) LoadProviderDocument(
	ctx context.Context,
	source ProviderImportSource,
) (LoadedProviderDocument, error) {
	target, err := url.Parse(strings.TrimSpace(source.SourceURI))
	if err != nil || target == nil || target.User != nil || target.Fragment != "" ||
		(target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return LoadedProviderDocument{}, ErrProviderInvalid
	}
	policy, err := resolvedProviderEgressPolicy(source.EgressPolicy, target)
	if err != nil {
		return LoadedProviderDocument{}, err
	}
	guard, err := execution.NewHTTPNetworkGuard(policy, nil)
	if err != nil {
		return LoadedProviderDocument{}, err
	}
	var content []byte
	var contentType, sourceRevision string
	err = loader.authorizer.WithProviderHeaders(ctx, source, func(headers map[string]string, sensitiveHeaders []string) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return ErrProviderInvalid
		}
		request.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*;q=0.1")
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		if err := guard.ValidateURL(ctx, request.URL); err != nil {
			return err
		}
		client, err := guard.ProtectClient(loader.client, sensitiveHeaders)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("request provider openapi document: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("request provider openapi document: HTTP_STATUS_%d", response.StatusCode)
		}
		limited := &io.LimitedReader{R: response.Body, N: loader.maxBytes + 1}
		content, err = io.ReadAll(limited)
		if err != nil {
			return fmt.Errorf("read provider openapi document: %w", err)
		}
		if int64(len(content)) > loader.maxBytes {
			return errors.New("provider openapi document exceeds size limit")
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return errors.New("provider openapi document is empty")
		}
		contentType = strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
		sourceRevision = strings.TrimSpace(response.Header.Get("ETag"))
		if sourceRevision == "" {
			sourceRevision = strings.TrimSpace(response.Header.Get("Last-Modified"))
		}
		return nil
	})
	if err != nil {
		return LoadedProviderDocument{}, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	rawObjectID, err := loader.rawStore.StorePermanentOpenAPI(
		ctx, source.Provider.WorkspaceID, contentType, append([]byte(nil), content...),
	)
	if err != nil {
		return LoadedProviderDocument{}, fmt.Errorf("store provider openapi document: %w", err)
	}
	if !validUUID(rawObjectID) {
		return LoadedProviderDocument{}, ErrProviderInvalid
	}
	fileName := path.Base(target.Path)
	if fileName == "." || fileName == "/" || fileName == "" {
		fileName = "openapi.json"
	}
	return LoadedProviderDocument{
		Content: append([]byte(nil), content...), FileName: fileName,
		RawObjectID: rawObjectID, SourceRevision: sourceRevision,
	}, nil
}

func resolvedProviderEgressPolicy(
	policy execution.EgressPolicy,
	target *url.URL,
) (execution.EgressPolicy, error) {
	resolved := execution.EgressPolicy{
		AllowedHosts: append([]string(nil), policy.AllowedHosts...),
		AllowedPorts: append([]int(nil), policy.AllowedPorts...),
		AllowedCIDRs: append([]string(nil), policy.AllowedCIDRs...),
		MaxRedirects: policy.MaxRedirects,
	}
	if len(resolved.AllowedHosts) == 0 {
		resolved.AllowedHosts = []string{target.Hostname()}
	}
	if len(resolved.AllowedPorts) == 0 {
		port := 80
		if target.Scheme == "https" {
			port = 443
		}
		if target.Port() != "" {
			parsed, err := strconv.Atoi(target.Port())
			if err != nil {
				return resolved, ErrProviderInvalid
			}
			port = parsed
		}
		resolved.AllowedPorts = []int{port}
	}
	return resolved, nil
}
