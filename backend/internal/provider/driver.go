package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/serviceendpoint"
)

var (
	ErrInvalid             = errors.New("invalid provider configuration")
	ErrNotFound            = errors.New("provider resource not found")
	ErrConflict            = errors.New("provider resource conflict")
	ErrKindNotAvailable    = errors.New("PROVIDER_KIND_NOT_AVAILABLE")
	ErrDriverAlreadyExists = errors.New("provider driver already registered")
)

type Kind string

const (
	KindHTTPOpenAPI      Kind = "HTTP_OPENAPI"
	KindInternalRegistry Kind = "INTERNAL_REGISTRY"
	KindMCPServer        Kind = "MCP_SERVER"
	KindOpenConnector    Kind = "OPEN_CONNECTOR"
	KindShellRuntime     Kind = "SHELL_RUNTIME"
)

type Provider struct {
	ID             string
	WorkspaceID    string
	Name           string
	Kind           Kind
	DriverKey      string
	Transport      string
	EndpointConfig json.RawMessage
	DriverConfig   json.RawMessage
	// OutboundIdentityPolicyVersion increments when outbound execution contract changes.
	OutboundIdentityPolicyVersion int64
	DiscoveryMode                 string
	Status                        string
	LastSyncedAt                  *time.Time
	LastErrorCode                 *string
	CreatedBy                     string
	UpdatedBy                     string
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	LockVersion                   int64
	DeletedAt                     *time.Time
}

// ConnectionContext deliberately contains no credential value. The
// connection domain will resolve and inject only the identity metadata needed
// by a driver.
type ConnectionContext struct {
	ID          string
	WorkspaceID string
	Alias       string
	Configured  bool
}

type DiscoveryRequest struct {
	Provider   Provider
	Connection *ConnectionContext
	Cursor     string
	Query      string
	Limit      int
}

type Asset struct {
	ID                       string
	WorkspaceID              string
	ProviderID               string
	Kind                     string
	ExternalID               string
	Name                     string
	Description              string
	InputSchema              json.RawMessage
	OutputSchema             json.RawMessage
	Metadata                 json.RawMessage
	SourceRevision           string
	SourceChecksum           string
	MaterializedCapabilityID *string
	Status                   string
	DiscoveredAt             time.Time
	LastSeenAt               time.Time
}

type SyncRun struct {
	ID              string
	WorkspaceID     string
	ProviderID      string
	Status          string
	CursorBefore    *string
	CursorAfter     *string
	DiscoveredCount int
	ChangedCount    int
	ErrorSummary    json.RawMessage
	StartedBy       string
	StartedAt       time.Time
	FinishedAt      *time.Time
}

type NewProvider struct {
	ID, WorkspaceID, Name        string
	Kind                         Kind
	DriverKey, Transport         string
	EndpointConfig, DriverConfig json.RawMessage
	DiscoveryMode, CreatedBy     string
}

type UpdateProvider struct {
	Name, DriverKey, Transport   string
	EndpointConfig, DriverConfig json.RawMessage
	DiscoveryMode, UpdatedBy     string
	ExpectedLockVersion          int64
}

type DiscoveryPage struct {
	Assets     []Asset
	NextCursor string
}

type Driver interface {
	Kind() Kind
	Validate(context.Context, Provider, *ConnectionContext) error
	Discover(context.Context, DiscoveryRequest) (DiscoveryPage, error)
}

type HTTPAssetDiscoverer interface {
	DiscoverHTTP(context.Context, DiscoveryRequest) (DiscoveryPage, error)
}

// HTTPOpenAPIDriver is functional only with a concrete discoverer. The M7
// OpenAPI parser/import service supplies that implementation; no placeholder
// result is registered here.
type HTTPOpenAPIDriver struct {
	discoverer HTTPAssetDiscoverer
}

func NewHTTPOpenAPIDriver(discoverer HTTPAssetDiscoverer) (*HTTPOpenAPIDriver, error) {
	if discoverer == nil {
		return nil, errors.New("HTTP OpenAPI discoverer is required")
	}
	return &HTTPOpenAPIDriver{discoverer: discoverer}, nil
}

func (d *HTTPOpenAPIDriver) Kind() Kind {
	return KindHTTPOpenAPI
}

func (d *HTTPOpenAPIDriver) Validate(
	_ context.Context,
	provider Provider,
	connection *ConnectionContext,
) error {
	if strings.TrimSpace(provider.ID) == "" || strings.TrimSpace(provider.WorkspaceID) == "" ||
		provider.Kind != KindHTTPOpenAPI || strings.TrimSpace(provider.DriverKey) == "" ||
		!strings.EqualFold(strings.TrimSpace(provider.Transport), "HTTP") ||
		!jsonObject(provider.EndpointConfig) || !jsonObject(provider.DriverConfig) ||
		containsSensitiveKey(provider.EndpointConfig) || containsSensitiveKey(provider.DriverConfig) {
		return ErrInvalid
	}
	// Dual-mode only: require outbound-identity.v1; reject service-auth.v1 / legacy dual-read.
	if err := validateHTTPOutboundIdentity(provider.DriverConfig); err != nil {
		return err
	}
	endpoint, err := serviceendpoint.Parse(provider.EndpointConfig)
	if err != nil {
		return ErrInvalid
	}
	mode := strings.TrimSpace(provider.DiscoveryMode)
	if mode != "" && mode != "MANUAL" && !endpoint.HasDiscovery() {
		return ErrInvalid
	}
	if connection != nil && (strings.TrimSpace(connection.ID) == "" ||
		connection.WorkspaceID != provider.WorkspaceID) {
		return ErrInvalid
	}
	return nil
}

// validateHTTPOutboundIdentity requires a strict outbound-identity.v1 contract in
// driver_config.outboundIdentity and rejects legacy authentication contracts.
func validateHTTPOutboundIdentity(driverConfig json.RawMessage) error {
	var envelope struct {
		OutboundIdentity json.RawMessage `json:"outboundIdentity"`
		Authentication   json.RawMessage `json:"authentication"`
	}
	if len(strings.TrimSpace(string(driverConfig))) == 0 {
		return outboundidentity.ErrIdentityPolicyInvalid
	}
	if json.Unmarshal(driverConfig, &envelope) != nil {
		return ErrInvalid
	}
	if len(envelope.Authentication) > 0 && string(envelope.Authentication) != "null" {
		// Legacy service-auth.v1 must not be written for HTTP providers.
		return outboundidentity.ErrIdentityModeUnsupported
	}
	if len(envelope.OutboundIdentity) == 0 || string(envelope.OutboundIdentity) == "null" {
		return outboundidentity.ErrIdentityPolicyInvalid
	}
	if _, err := outboundidentity.ParseProviderIdentity(envelope.OutboundIdentity); err != nil {
		return err
	}
	return nil
}

// ParseOutboundIdentity extracts the normalized Provider contract from driver_config.
func ParseOutboundIdentity(driverConfig json.RawMessage) (outboundidentity.ProviderIdentity, error) {
	var envelope struct {
		OutboundIdentity json.RawMessage `json:"outboundIdentity"`
	}
	if json.Unmarshal(driverConfig, &envelope) != nil || len(envelope.OutboundIdentity) == 0 {
		return outboundidentity.ProviderIdentity{}, outboundidentity.ErrIdentityPolicyInvalid
	}
	return outboundidentity.ParseProviderIdentity(envelope.OutboundIdentity)
}

func (d *HTTPOpenAPIDriver) Discover(
	ctx context.Context,
	request DiscoveryRequest,
) (DiscoveryPage, error) {
	if err := d.Validate(ctx, request.Provider, request.Connection); err != nil {
		return DiscoveryPage{}, err
	}
	if request.Limit < 0 {
		return DiscoveryPage{}, ErrInvalid
	}
	page, err := d.discoverer.DiscoverHTTP(ctx, request)
	if err != nil {
		return DiscoveryPage{}, fmt.Errorf("discover HTTP OpenAPI assets: %w", err)
	}
	return page, nil
}

func jsonObject(value json.RawMessage) bool {
	if len(value) == 0 {
		return true
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func containsSensitiveKey(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return true
	}
	var walk func(any) bool
	walk = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				for _, forbidden := range []string{
					"password", "secretvalue", "tokenvalue", "apikey", "authorization", "refreshtoken",
				} {
					if strings.Contains(normalized, forbidden) {
						return true
					}
				}
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(decoded)
}
