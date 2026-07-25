package openapiimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/serviceendpoint"
)

var (
	ErrProviderNotAvailable = errors.New("OPENAPI_PROVIDER_NOT_AVAILABLE")
	ErrProviderInvalid      = errors.New("OPENAPI_PROVIDER_INVALID")
	// ErrOutboundSubjectRequired is returned when background OpenAPI import would
	// need user-scoped outbound identity or machine Secret — fail closed (no Subject).
	ErrOutboundSubjectRequired = errors.New("OUTBOUND_SUBJECT_REQUIRED")
)

type ProviderImportSource struct {
	Provider           provider.Provider
	Connection         *provider.ConnectionContext
	SourceURI          string
	ConfiguredRevision *string
	EgressPolicy       execution.EgressPolicy
}

type ProviderSourceResolver interface {
	ResolveProviderSource(context.Context, string, string, *string) (ProviderImportSource, error)
}

type ProviderSourceRepository struct{ db *sql.DB }

func NewProviderSourceRepository(db *sql.DB) (*ProviderSourceRepository, error) {
	if db == nil {
		return nil, errors.New("openapi provider source database is required")
	}
	return &ProviderSourceRepository{db: db}, nil
}

func (r *ProviderSourceRepository) ValidateFileImportReferences(
	ctx context.Context,
	workspaceID, providerID string,
	connectionID *string,
) error {
	workspaceID, providerID = strings.TrimSpace(workspaceID), strings.TrimSpace(providerID)
	connectionID = normalizeOptional(connectionID)
	if !validUUID(workspaceID) || !validUUID(providerID) ||
		(connectionID != nil && !validUUID(*connectionID)) {
		return ErrInvalid
	}
	var providerKind, providerStatus string
	if err := r.db.QueryRowContext(ctx, `
		SELECT provider_kind,status
		FROM capability_providers
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
	`, workspaceID, providerID).Scan(&providerKind, &providerStatus); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("validate openapi file provider: %w", err)
	}
	if providerKind != string(provider.KindHTTPOpenAPI) || providerStatus != "ACTIVE" {
		return ErrProviderNotAvailable
	}
	if connectionID == nil {
		return nil
	}
	var connectionStatus string
	if err := r.db.QueryRowContext(ctx, `
		SELECT status
		FROM service_connections
		WHERE workspace_id=$1 AND provider_id=$2 AND id=$3 AND deleted_at IS NULL
	`, workspaceID, providerID, *connectionID).Scan(&connectionStatus); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("validate openapi file connection: %w", err)
	}
	if connectionStatus != "VERIFIED" {
		return ErrProviderNotAvailable
	}
	return nil
}

func (r *ProviderSourceRepository) ResolveProviderSource(
	ctx context.Context,
	workspaceID, providerID string,
	connectionID *string,
) (ProviderImportSource, error) {
	workspaceID, providerID = strings.TrimSpace(workspaceID), strings.TrimSpace(providerID)
	connectionID = normalizeOptional(connectionID)
	if !validUUID(workspaceID) || !validUUID(providerID) ||
		(connectionID != nil && !validUUID(*connectionID)) {
		return ProviderImportSource{}, ErrInvalid
	}
	var source ProviderImportSource
	var kind, status string
	if err := r.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,provider_kind,driver_key,transport,endpoint_config,driver_config,status
		FROM capability_providers
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
	`, workspaceID, providerID).Scan(
		&source.Provider.ID, &source.Provider.WorkspaceID, &kind,
		&source.Provider.DriverKey, &source.Provider.Transport,
		&source.Provider.EndpointConfig, &source.Provider.DriverConfig, &status,
	); errors.Is(err, sql.ErrNoRows) {
		return ProviderImportSource{}, ErrNotFound
	} else if err != nil {
		return ProviderImportSource{}, fmt.Errorf("resolve openapi provider: %w", err)
	}
	source.Provider.Kind = provider.Kind(kind)
	if status != "ACTIVE" || source.Provider.Kind != provider.KindHTTPOpenAPI {
		return ProviderImportSource{}, ErrProviderNotAvailable
	}
	endpointConfig, endpointErr := serviceendpoint.Parse(source.Provider.EndpointConfig)
	if endpointErr != nil {
		return ProviderImportSource{}, ErrProviderInvalid
	}
	parsedURI, err := url.Parse(endpointConfig.Discovery.DocumentURL)
	if err != nil || parsedURI == nil || parsedURI.User != nil ||
		(parsedURI.Scheme != "http" && parsedURI.Scheme != "https") || strings.TrimSpace(parsedURI.Host) == "" {
		return ProviderImportSource{}, ErrProviderInvalid
	}
	source.SourceURI = parsedURI.String()
	var network struct {
		Egress execution.EgressPolicy `json:"egress"`
	}
	if json.Unmarshal(source.Provider.EndpointConfig, &network) != nil {
		return ProviderImportSource{}, ErrProviderInvalid
	}
	source.EgressPolicy = network.Egress
	if revision := strings.TrimSpace(endpointConfig.Discovery.SourceRevision); revision != "" {
		source.ConfiguredRevision = &revision
	}

	if connectionID == nil {
		return source, nil
	}
	connection := provider.ConnectionContext{}
	var connectionStatus string
	var connectionPolicyJSON json.RawMessage
	if err := r.db.QueryRowContext(ctx, `
		SELECT c.id,c.workspace_id,c.alias::TEXT,
		       (v.id IS NOT NULL AND v.revoked_at IS NULL),c.status,c.policy
		FROM service_connections c
		LEFT JOIN secrets s
		  ON s.workspace_id=c.workspace_id AND s.id=c.credential_secret_id
		LEFT JOIN secret_versions v
		  ON v.workspace_id=s.workspace_id AND v.secret_id=s.id AND v.id=s.active_version_id
		WHERE c.workspace_id=$1 AND c.provider_id=$2 AND c.id=$3 AND c.deleted_at IS NULL
	`, workspaceID, providerID, *connectionID).Scan(
		&connection.ID, &connection.WorkspaceID, &connection.Alias,
		&connection.Configured, &connectionStatus, &connectionPolicyJSON,
	); errors.Is(err, sql.ErrNoRows) {
		return ProviderImportSource{}, ErrNotFound
	} else if err != nil {
		return ProviderImportSource{}, fmt.Errorf("resolve openapi provider connection: %w", err)
	}
	if connectionStatus != "VERIFIED" {
		return ProviderImportSource{}, ErrProviderNotAvailable
	}
	var connectionPolicy struct {
		Egress execution.EgressPolicy `json:"egress"`
	}
	if json.Unmarshal(connectionPolicyJSON, &connectionPolicy) != nil {
		return ProviderImportSource{}, ErrProviderInvalid
	}
	if len(connectionPolicy.Egress.AllowedHosts) > 0 || len(connectionPolicy.Egress.AllowedPorts) > 0 ||
		len(connectionPolicy.Egress.AllowedCIDRs) > 0 || connectionPolicy.Egress.MaxRedirects != 0 {
		source.EgressPolicy = connectionPolicy.Egress
	}
	source.Connection = &connection
	return source, nil
}

type LoadedProviderDocument struct {
	Content        []byte
	FileName       string
	RawObjectID    string
	SourceRevision string
}

// ProviderDocumentLoader performs the guarded network request and permanent
// raw-object write. It receives connection identity metadata but never a
// credential value; credential resolution remains inside the loader boundary.
type ProviderDocumentLoader interface {
	LoadProviderDocument(context.Context, ProviderImportSource) (LoadedProviderDocument, error)
}

type ProviderImportRequest struct {
	ImportID     string
	WorkspaceID  string
	ProviderID   string
	ConnectionID *string
	CreatedBy    string
}

type ProviderImportService struct {
	sources  ProviderSourceResolver
	registry *provider.Registry
	loader   ProviderDocumentLoader
	parser   *ParseService
}

func NewProviderImportService(
	sources ProviderSourceResolver,
	registry *provider.Registry,
	loader ProviderDocumentLoader,
	parser *ParseService,
) (*ProviderImportService, error) {
	if sources == nil || registry == nil || loader == nil || parser == nil {
		return nil, errors.New("openapi provider import dependencies are required")
	}
	return &ProviderImportService{sources: sources, registry: registry, loader: loader, parser: parser}, nil
}

func (s *ProviderImportService) Import(
	ctx context.Context,
	request ProviderImportRequest,
) (ParseOutcome, error) {
	request.ImportID = strings.TrimSpace(request.ImportID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.ConnectionID = normalizeOptional(request.ConnectionID)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	if !validUUID(request.ImportID) || !validUUID(request.WorkspaceID) ||
		!validUUID(request.ProviderID) || !validUUID(request.CreatedBy) ||
		(request.ConnectionID != nil && !validUUID(*request.ConnectionID)) {
		return ParseOutcome{}, ErrInvalid
	}
	source, err := s.sources.ResolveProviderSource(ctx, request.WorkspaceID, request.ProviderID, request.ConnectionID)
	if err != nil {
		return ParseOutcome{}, err
	}
	if source.Provider.Kind != provider.KindHTTPOpenAPI {
		return ParseOutcome{}, ErrProviderNotAvailable
	}
	driver, err := s.registry.Resolve(source.Provider.Kind)
	if err != nil {
		return ParseOutcome{}, ErrProviderNotAvailable
	}
	if err := driver.Validate(ctx, source.Provider, source.Connection); err != nil {
		return ParseOutcome{}, ErrProviderInvalid
	}
	loaded, err := s.loader.LoadProviderDocument(ctx, source)
	if err != nil {
		return ParseOutcome{}, fmt.Errorf("load openapi provider document: %w", err)
	}
	loaded.FileName = strings.TrimSpace(loaded.FileName)
	loaded.RawObjectID = strings.TrimSpace(loaded.RawObjectID)
	loaded.SourceRevision = strings.TrimSpace(loaded.SourceRevision)
	if len(loaded.Content) == 0 || !validUUID(loaded.RawObjectID) {
		return ParseOutcome{}, ErrProviderInvalid
	}
	if loaded.FileName == "" {
		loaded.FileName = path.Base(strings.TrimSpace(source.SourceURI))
		if loaded.FileName == "." || loaded.FileName == "/" || loaded.FileName == "" {
			loaded.FileName = "openapi.json"
		}
	}
	revision := normalizeOptional(&loaded.SourceRevision)
	if revision == nil {
		revision = source.ConfiguredRevision
	}
	sourceURI := source.SourceURI
	return s.parser.Parse(ctx, ParseRequest{
		ImportID: request.ImportID, WorkspaceID: request.WorkspaceID,
		ProviderID: &request.ProviderID, ConnectionID: request.ConnectionID,
		SourceType: SourceTypeURL, SourceURI: &sourceURI, SourceRevision: revision,
		FileName: loaded.FileName, RawObjectID: loaded.RawObjectID,
		Content: append([]byte(nil), loaded.Content...), CreatedBy: request.CreatedBy,
	})
}
