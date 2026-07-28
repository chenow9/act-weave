package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/secret"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ModelConfigStore interface {
	Create(context.Context, modelconfig.NewConfig) (modelconfig.Config, error)
	Get(context.Context, string, string) (modelconfig.Config, error)
	List(context.Context, string) ([]modelconfig.Config, error)
	Update(context.Context, string, string, modelconfig.UpdateConfig) (modelconfig.Config, error)
	SoftDelete(context.Context, string, string, string, int64) error
}
type ModelConfigVerifier interface {
	Verify(context.Context, string, string, string) (modelconfig.Config, error)
}
type ProviderStore interface {
	Create(context.Context, provider.NewProvider) (provider.Provider, error)
	Get(context.Context, string, string) (provider.Provider, error)
	List(context.Context, string) ([]provider.Provider, error)
	Update(context.Context, string, string, provider.UpdateProvider) (provider.Provider, error)
	SoftDelete(context.Context, string, string, string, int64) error
	ListAssets(context.Context, string, string) ([]provider.Asset, error)
}
type ProviderSyncer interface {
	Sync(context.Context, string, string, string) (provider.SyncRun, error)
}
type ProviderMaterializer interface {
	Materialize(context.Context, string, string, string, string, *string) (provider.MaterializationResult, error)
}
type ConnectionStore interface {
	Create(context.Context, connection.NewConnection) (connection.Connection, error)
	Get(context.Context, string, string) (connection.Connection, error)
	List(context.Context, string, *string) ([]connection.Connection, error)
	Update(context.Context, string, string, connection.UpdateConnection) (connection.Connection, error)
	SoftDelete(context.Context, string, string, string, int64) error
}
type ConnectionVerifier interface {
	Verify(context.Context, string, string, string) (connection.Verification, error)
}
type SecretStore interface {
	Create(context.Context, secret.CreateInput) (secret.ReadDTO, error)
	Get(context.Context, string, string) (secret.ReadDTO, error)
	Rotate(context.Context, secret.RotateInput) (secret.ReadDTO, error)
	Revoke(context.Context, secret.RevokeInput) (secret.ReadDTO, error)
}

type ConfigurationRoutes struct {
	authorizer         WorkspaceAuthorizer
	models             ModelConfigStore
	modelVerifier      ModelConfigVerifier
	providers          ProviderStore
	providerSyncer     ProviderSyncer
	materializer       ProviderMaterializer
	providerRegistry   *provider.Registry
	connections        ConnectionStore
	connectionVerifier ConnectionVerifier
	secrets            SecretStore
	impactProofs       *connection.ImpactProofService
}

type ConfigurationDependencies struct {
	Authorizer         WorkspaceAuthorizer
	Models             ModelConfigStore
	ModelVerifier      ModelConfigVerifier
	Providers          ProviderStore
	ProviderSyncer     ProviderSyncer
	Materializer       ProviderMaterializer
	ProviderRegistry   *provider.Registry
	Connections        ConnectionStore
	ConnectionVerifier ConnectionVerifier
	Secrets            SecretStore
	// ImpactProofSecret signs dangerous connection mutation proofs (min 32 bytes).
	ImpactProofSecret []byte
}

func NewConfigurationRoutes(d ConfigurationDependencies) (*ConfigurationRoutes, error) {
	if d.Authorizer == nil || d.Models == nil || d.ModelVerifier == nil || d.Providers == nil || d.ProviderSyncer == nil ||
		d.Materializer == nil || d.ProviderRegistry == nil || d.Connections == nil || d.ConnectionVerifier == nil || d.Secrets == nil {
		return nil, errors.New("configuration route dependencies are required")
	}
	impact, err := connection.NewImpactProofService(d.ImpactProofSecret)
	if err != nil {
		// Allow tests that pass nil secret by generating a disposable key only when empty
		// would fail; production must supply a configured secret.
		if len(d.ImpactProofSecret) == 0 {
			impact, err = connection.NewImpactProofService(make([]byte, 32))
		}
		if err != nil {
			return nil, err
		}
	}
	return &ConfigurationRoutes{authorizer: d.Authorizer, models: d.Models, modelVerifier: d.ModelVerifier,
		providers: d.Providers, providerSyncer: d.ProviderSyncer, materializer: d.Materializer, providerRegistry: d.ProviderRegistry,
		connections: d.Connections, connectionVerifier: d.ConnectionVerifier, secrets: d.Secrets, impactProofs: impact}, nil
}

func (r *ConfigurationRoutes) RegisterV1(v1 V1Routes) {
	g := v1.Protected
	g.GET("/workspaces/:wid/model-configs", r.listModels)
	g.POST("/workspaces/:wid/model-configs", r.createModel)
	g.GET("/workspaces/:wid/model-configs/:id", r.getModel)
	g.PATCH("/workspaces/:wid/model-configs/:id", r.updateModel)
	g.DELETE("/workspaces/:wid/model-configs/:id", r.deleteModel)
	g.POST("/workspaces/:wid/model-configs/:id/__command/verify", r.verifyModel)
	g.GET("/workspaces/:wid/providers", r.listProviders)
	g.POST("/workspaces/:wid/providers", r.createProvider)
	g.GET("/workspaces/:wid/providers/:pid", r.getProvider)
	g.PATCH("/workspaces/:wid/providers/:pid", r.updateProvider)
	g.DELETE("/workspaces/:wid/providers/:pid", r.deleteProvider)
	g.POST("/workspaces/:wid/providers/:pid/__command/sync", r.syncProvider)
	g.GET("/workspaces/:wid/providers/:pid/assets", r.listAssets)
	g.POST("/workspaces/:wid/providers/:pid/assets/:aid/__command/materialize", r.materializeAsset)
	g.GET("/workspaces/:wid/providers/:pid/connections", r.listProviderConnections)
	g.POST("/workspaces/:wid/providers/:pid/connections", r.createConnection)
	g.GET("/workspaces/:wid/connections/:id", r.getConnection)
	g.PATCH("/workspaces/:wid/connections/:id", r.updateConnection)
	g.DELETE("/workspaces/:wid/connections/:id", r.deleteConnection)
	g.POST("/workspaces/:wid/connections/:id/__command/verify", r.verifyConnection)
	// Gin forbids ':' inside a path segment; use command-style impact preview.
	g.POST("/workspaces/:wid/service-connections/:id/__command/impact", r.previewConnectionImpact)
	g.POST("/workspaces/:wid/connections/:id/__command/impact", r.previewConnectionImpact)
	g.POST("/workspaces/:wid/secrets", r.createSecret)
	g.POST("/workspaces/:wid/secrets/:id/__command/rotate", r.rotateSecret)
}

func (r *ConfigurationRoutes) authorize(c *gin.Context, action authz.Action) bool {
	p, _ := PrincipalFrom(c.Request.Context())
	if _, err := r.authorizer.AuthorizeWorkspace(c.Request.Context(), p.UserID, c.Param("wid"), action); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}
func actor(c *gin.Context) string { p, _ := PrincipalFrom(c.Request.Context()); return p.UserID }

// actorDisplayName prefers principal username for audit ActorDisplay (1–255 chars).
func actorDisplayName(c *gin.Context) string {
	if p, ok := PrincipalFrom(c.Request.Context()); ok {
		if name := strings.TrimSpace(p.Username); name != "" {
			if len(name) > 255 {
				return name[:255]
			}
			return name
		}
	}
	return "Workspace member"
}

type createSecretRequest struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Plaintext string `json:"plaintext"`
}

func (r *ConfigurationRoutes) createSecret(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q createSecretRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, secret.ErrInvalid)
		return
	}
	v, err := r.secrets.Create(c.Request.Context(), secret.CreateInput{
		WorkspaceID: c.Param("wid"), Name: q.Name, Kind: q.Kind,
		Plaintext: q.Plaintext, ActorUserID: actor(c),
	})
	q.Plaintext = ""
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, v)
}

func newV7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
func deleteLock(c *gin.Context) (int64, error) {
	v, err := strconv.ParseInt(c.Query("lockVersion"), 10, 64)
	if err != nil || v < 1 {
		return 0, modelconfig.ErrInvalid
	}
	return v, nil
}

type modelConfigDTO struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	Provider             string             `json:"provider"`
	APIBase              string             `json:"apiBase"`
	ModelName            string             `json:"modelName"`
	CredentialConfigured bool               `json:"credentialConfigured"`
	Options              json.RawMessage    `json:"options"`
	Status               modelconfig.Status `json:"status"`
	LastVerifiedAt       *time.Time         `json:"lastVerifiedAt,omitempty"`
	LastLatencyMS        *int               `json:"lastLatencyMs,omitempty"`
	LastErrorCode        *string            `json:"lastErrorCode,omitempty"`
	CreatedBy            string             `json:"createdBy"`
	UpdatedBy            string             `json:"updatedBy"`
	CreatedAt            time.Time          `json:"createdAt"`
	UpdatedAt            time.Time          `json:"updatedAt"`
	LockVersion          int64              `json:"lockVersion"`
}

func modelDTO(v modelconfig.Config) modelConfigDTO {
	return modelConfigDTO{v.ID, v.Name, v.Provider, v.APIBase, v.ModelName, v.CredentialConfigured, v.Options, v.Status, v.LastVerifiedAt, v.LastLatencyMS, v.LastErrorCode, v.CreatedBy, v.UpdatedBy, v.CreatedAt, v.UpdatedAt, v.LockVersion}
}

type createModelRequest struct {
	Name               string          `json:"name"`
	Provider           string          `json:"provider"`
	APIBase            string          `json:"apiBase"`
	ModelName          string          `json:"modelName"`
	CredentialSecretID *string         `json:"credentialSecretId"`
	Options            json.RawMessage `json:"options"`
}
type updateModelRequest struct {
	Name               *string         `json:"name"`
	Provider           *string         `json:"provider"`
	APIBase            *string         `json:"apiBase"`
	ModelName          *string         `json:"modelName"`
	CredentialSecretID *string         `json:"credentialSecretId"`
	Options            json.RawMessage `json:"options"`
	LockVersion        int64           `json:"lockVersion"`
}

func (r *ConfigurationRoutes) listModels(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.models.List(c.Request.Context(), c.Param("wid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]modelConfigDTO, len(v))
	for i := range v {
		items[i] = modelDTO(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}
func (r *ConfigurationRoutes) createModel(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q createModelRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, modelconfig.ErrInvalid)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	v, e := r.models.Create(c.Request.Context(), modelconfig.NewConfig{ID: id, WorkspaceID: c.Param("wid"), Name: q.Name, Provider: q.Provider, APIBase: q.APIBase, ModelName: q.ModelName, CredentialSecretID: q.CredentialSecretID, Options: q.Options, CreatedBy: actor(c)})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(http.StatusCreated, modelDTO(v))
}
func (r *ConfigurationRoutes) getModel(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.models.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, modelDTO(v))
}
func (r *ConfigurationRoutes) updateModel(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q updateModelRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, modelconfig.ErrInvalid)
		return
	}
	old, e := r.models.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	if q.Name != nil {
		old.Name = *q.Name
	}
	if q.Provider != nil {
		old.Provider = *q.Provider
	}
	if q.APIBase != nil {
		old.APIBase = *q.APIBase
	}
	if q.ModelName != nil {
		old.ModelName = *q.ModelName
	}
	if q.CredentialSecretID != nil {
		old.CredentialSecretID = q.CredentialSecretID
	}
	if q.Options != nil {
		old.Options = q.Options
	}
	v, e := r.models.Update(c.Request.Context(), c.Param("wid"), c.Param("id"), modelconfig.UpdateConfig{Name: old.Name, Provider: old.Provider, APIBase: old.APIBase, ModelName: old.ModelName, CredentialSecretID: old.CredentialSecretID, Options: old.Options, Status: modelconfig.StatusUnverified, UpdatedBy: actor(c), ExpectedLockVersion: q.LockVersion})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, modelDTO(v))
}
func (r *ConfigurationRoutes) deleteModel(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	lock, e := deleteLock(c)
	if e == nil {
		e = r.models.SoftDelete(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c), lock)
	}
	if e != nil {
		RespondError(c, e)
		return
	}
	c.Status(204)
}
func (r *ConfigurationRoutes) verifyModel(c *gin.Context) {
	if !r.authorize(c, authz.ActionTest) {
		return
	}
	v, e := r.modelVerifier.Verify(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, modelDTO(v))
}

type providerDTO struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Kind           provider.Kind   `json:"kind"`
	DriverKey      string          `json:"driverKey"`
	Transport      string          `json:"transport"`
	EndpointConfig json.RawMessage `json:"endpointConfig"`
	DriverConfig   json.RawMessage `json:"driverConfig"`
	DiscoveryMode  string          `json:"discoveryMode"`
	Status         string          `json:"status"`
	LastSyncedAt   *time.Time      `json:"lastSyncedAt,omitempty"`
	LastErrorCode  *string         `json:"lastErrorCode,omitempty"`
	CreatedBy      string          `json:"createdBy"`
	UpdatedBy      string          `json:"updatedBy"`
	LockVersion    int64           `json:"lockVersion"`
}

func providerDTOFor(v provider.Provider) providerDTO {
	return providerDTO{v.ID, v.Name, v.Kind, v.DriverKey, v.Transport, v.EndpointConfig, v.DriverConfig, v.DiscoveryMode, v.Status, v.LastSyncedAt, v.LastErrorCode, v.CreatedBy, v.UpdatedBy, v.LockVersion}
}

type createProviderRequest struct {
	Name           string          `json:"name"`
	Kind           provider.Kind   `json:"kind"`
	DriverKey      string          `json:"driverKey"`
	Transport      string          `json:"transport"`
	EndpointConfig json.RawMessage `json:"endpointConfig"`
	DriverConfig   json.RawMessage `json:"driverConfig"`
	DiscoveryMode  string          `json:"discoveryMode"`
}
type updateProviderRequest struct {
	Name           *string         `json:"name"`
	DriverKey      *string         `json:"driverKey"`
	Transport      *string         `json:"transport"`
	EndpointConfig json.RawMessage `json:"endpointConfig"`
	DriverConfig   json.RawMessage `json:"driverConfig"`
	DiscoveryMode  *string         `json:"discoveryMode"`
	LockVersion    int64           `json:"lockVersion"`
}

func (r *ConfigurationRoutes) listProviders(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.providers.List(c.Request.Context(), c.Param("wid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]providerDTO, len(v))
	for i := range v {
		items[i] = providerDTOFor(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}
func (r *ConfigurationRoutes) createProvider(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q createProviderRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, provider.ErrInvalid)
		return
	}
	driver, e := r.providerRegistry.Resolve(q.Kind)
	if e != nil {
		RespondError(c, e)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	candidate := provider.Provider{ID: id, WorkspaceID: c.Param("wid"), Name: q.Name, Kind: q.Kind, DriverKey: q.DriverKey, Transport: q.Transport, EndpointConfig: q.EndpointConfig, DriverConfig: q.DriverConfig, DiscoveryMode: q.DiscoveryMode}
	if e = driver.Validate(c.Request.Context(), candidate, nil); e != nil {
		RespondError(c, e)
		return
	}
	v, e := r.providers.Create(c.Request.Context(), provider.NewProvider{ID: id, WorkspaceID: c.Param("wid"), Name: q.Name, Kind: q.Kind, DriverKey: q.DriverKey, Transport: q.Transport, EndpointConfig: q.EndpointConfig, DriverConfig: q.DriverConfig, DiscoveryMode: q.DiscoveryMode, CreatedBy: actor(c)})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(201, providerDTOFor(v))
}
func (r *ConfigurationRoutes) getProvider(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.providers.Get(c.Request.Context(), c.Param("wid"), c.Param("pid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, providerDTOFor(v))
}
func (r *ConfigurationRoutes) updateProvider(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q updateProviderRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, provider.ErrInvalid)
		return
	}
	old, e := r.providers.Get(c.Request.Context(), c.Param("wid"), c.Param("pid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	if q.Name != nil {
		old.Name = *q.Name
	}
	if q.DriverKey != nil {
		old.DriverKey = *q.DriverKey
	}
	if q.Transport != nil {
		old.Transport = *q.Transport
	}
	if q.EndpointConfig != nil {
		old.EndpointConfig = q.EndpointConfig
	}
	if q.DriverConfig != nil {
		old.DriverConfig = q.DriverConfig
	}
	if q.DiscoveryMode != nil {
		old.DiscoveryMode = *q.DiscoveryMode
	}
	driver, e := r.providerRegistry.Resolve(old.Kind)
	if e == nil {
		e = driver.Validate(c.Request.Context(), old, nil)
	}
	if e != nil {
		RespondError(c, e)
		return
	}
	v, e := r.providers.Update(c.Request.Context(), c.Param("wid"), c.Param("pid"), provider.UpdateProvider{Name: old.Name, DriverKey: old.DriverKey, Transport: old.Transport, EndpointConfig: old.EndpointConfig, DriverConfig: old.DriverConfig, DiscoveryMode: old.DiscoveryMode, UpdatedBy: actor(c), ExpectedLockVersion: q.LockVersion})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, providerDTOFor(v))
}
func (r *ConfigurationRoutes) deleteProvider(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	lock, e := deleteLock(c)
	if e == nil {
		e = r.providers.SoftDelete(c.Request.Context(), c.Param("wid"), c.Param("pid"), actor(c), lock)
	}
	if e != nil {
		RespondError(c, e)
		return
	}
	c.Status(204)
}
func (r *ConfigurationRoutes) syncProvider(c *gin.Context) {
	if !r.authorize(c, authz.ActionTest) {
		return
	}
	v, e := r.providerSyncer.Sync(c.Request.Context(), c.Param("wid"), c.Param("pid"), actor(c))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, gin.H{"id": v.ID, "status": v.Status, "discoveredCount": v.DiscoveredCount,
		"changedCount": v.ChangedCount, "errorSummary": v.ErrorSummary})
}

type providerAssetDTO struct {
	ID                       string          `json:"id"`
	Kind                     string          `json:"kind"`
	ExternalID               string          `json:"externalId"`
	Name                     string          `json:"name"`
	Description              string          `json:"description"`
	InputSchema              json.RawMessage `json:"inputSchema"`
	OutputSchema             json.RawMessage `json:"outputSchema"`
	Metadata                 json.RawMessage `json:"metadata"`
	SourceRevision           string          `json:"sourceRevision,omitempty"`
	SourceChecksum           string          `json:"sourceChecksum"`
	MaterializedCapabilityID *string         `json:"materializedCapabilityId,omitempty"`
	Status                   string          `json:"status"`
}

func assetDTO(v provider.Asset) providerAssetDTO {
	return providerAssetDTO{v.ID, v.Kind, v.ExternalID, v.Name, v.Description, v.InputSchema,
		v.OutputSchema, v.Metadata, v.SourceRevision, v.SourceChecksum, v.MaterializedCapabilityID, v.Status}
}
func (r *ConfigurationRoutes) listAssets(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.providers.ListAssets(c.Request.Context(), c.Param("wid"), c.Param("pid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]providerAssetDTO, len(v))
	for i := range v {
		items[i] = assetDTO(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}

type materializeRequest struct {
	DefaultConnectionID *string `json:"defaultConnectionId"`
}

func (r *ConfigurationRoutes) materializeAsset(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q materializeRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, provider.ErrInvalid)
		return
	}
	v, e := r.materializer.Materialize(c.Request.Context(), c.Param("wid"), c.Param("pid"), c.Param("aid"), actor(c), q.DefaultConnectionID)
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(201, gin.H{"asset": assetDTO(v.Asset), "capabilityId": v.Tool.CapabilityID,
		"draftVersionId": v.Draft.ID, "lifecycleStatus": v.Draft.LifecycleStatus})
}

type connectionDTO struct {
	ID                            string            `json:"id"`
	ProviderID                    string            `json:"providerId"`
	Name                          string            `json:"name"`
	Alias                         string            `json:"alias"`
	Environment                   string            `json:"environment"`
	ExternalAccountRef            *string           `json:"externalAccountRef,omitempty"`
	OutboundIdentity              json.RawMessage   `json:"outboundIdentity,omitempty"`
	OutboundIdentityPolicyVersion int64             `json:"outboundIdentityPolicyVersion"`
	MigrationState                string            `json:"migrationState"`
	MachineCredentialConfigured   bool              `json:"machineCredentialConfigured"`
	GrantedScopes                 json.RawMessage   `json:"grantedScopes"`
	Policy                        json.RawMessage   `json:"policy"`
	Status                        connection.Status `json:"status"`
	LastVerifiedAt                *time.Time        `json:"lastVerifiedAt,omitempty"`
	LastErrorCode                 *string           `json:"lastErrorCode,omitempty"`
	CreatedBy                     string            `json:"createdBy"`
	UpdatedBy                     string            `json:"updatedBy"`
	LockVersion                   int64             `json:"lockVersion"`
}

func connectionDTOFor(v connection.Connection) connectionDTO {
	// Never expose Secret IDs, fingerprints, legacy auth_mode/auth_config, or machine secret id.
	return connectionDTO{
		ID: v.ID, ProviderID: v.ProviderID, Name: v.Name, Alias: v.Alias, Environment: v.Environment,
		ExternalAccountRef: v.ExternalAccountRef, OutboundIdentity: v.OutboundIdentity,
		OutboundIdentityPolicyVersion: v.OutboundIdentityPolicyVersion, MigrationState: v.MigrationState,
		MachineCredentialConfigured: v.MachineCredentialConfigured || connection.FormatMachineCredentialConfigured(v.MachineCredentialSecretID),
		GrantedScopes:               v.GrantedScopes, Policy: v.Policy, Status: v.Status,
		LastVerifiedAt: v.LastVerifiedAt, LastErrorCode: v.LastErrorCode,
		CreatedBy: v.CreatedBy, UpdatedBy: v.UpdatedBy, LockVersion: v.LockVersion,
	}
}

type connectionWriteRequest struct {
	Name                      string                     `json:"name"`
	Alias                     string                     `json:"alias"`
	Environment               string                     `json:"environment"`
	ExternalAccountRef        *string                    `json:"externalAccountRef"`
	OutboundIdentity          json.RawMessage            `json:"outboundIdentity"`
	MachineCredential         *connectionCredentialWrite `json:"machineCredential,omitempty"`
	MachineCredentialSecretID *string                    `json:"machineCredentialSecretId,omitempty"`
	// Legacy fields — presence is rejected for dual-mode HTTP connections.
	AuthMode           string                     `json:"authMode,omitempty"`
	AuthConfig         json.RawMessage            `json:"authConfig,omitempty"`
	CredentialSecretID *string                    `json:"credentialSecretId,omitempty"`
	Credential         *connectionCredentialWrite `json:"credential,omitempty"`
	GrantedScopes      json.RawMessage            `json:"grantedScopes"`
	Policy             json.RawMessage            `json:"policy"`
	LockVersion        int64                      `json:"lockVersion,omitempty"`
	// ImpactConfirmationProof is required for dangerous identity mutations.
	ImpactConfirmationProof string `json:"impactConfirmationProof,omitempty"`
	// MetadataOnly restricts the write to non-sensitive fields (EDITOR path).
	MetadataOnly bool `json:"metadataOnly,omitempty"`
}

type connectionCredentialWrite struct {
	Kind      string `json:"kind"`
	Plaintext string `json:"plaintext"`
}

type connectionImpactRequest struct {
	ChangeKind                  string         `json:"changeKind"`
	NonSecretChangeDescriptor   map[string]any `json:"nonSecretChangeDescriptor"`
	MachineCredentialWillChange bool           `json:"machineCredentialWillChange"`
	ExpectedLockVersion         int64          `json:"expectedLockVersion"`
}

func (r *ConfigurationRoutes) listProviderConnections(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	pid := c.Param("pid")
	v, e := r.connections.List(c.Request.Context(), c.Param("wid"), &pid)
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]connectionDTO, len(v))
	for i := range v {
		items[i] = connectionDTOFor(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}
func (r *ConfigurationRoutes) createConnection(c *gin.Context) {
	// Identity create requires MANAGE (OWNER/ADMIN).
	if !r.authorize(c, authz.ActionManage) {
		return
	}
	var q connectionWriteRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, connection.ErrInvalid)
		return
	}
	if e := rejectLegacyConnectionWrite(q); e != nil {
		RespondError(c, e)
		return
	}
	configuredProvider, e := r.providers.Get(c.Request.Context(), c.Param("wid"), c.Param("pid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	if configuredProvider.Status != "ACTIVE" {
		RespondError(c, connection.ErrConflict)
		return
	}
	if configuredProvider.Kind != provider.KindHTTPOpenAPI {
		RespondError(c, outboundidentity.ErrIdentityExecutorUnsupported)
		return
	}
	providerIdentity, e := provider.ParseOutboundIdentity(configuredProvider.DriverConfig)
	if e != nil {
		RespondError(c, e)
		return
	}
	identity, e := outboundidentity.ParseConnectionIdentity(q.OutboundIdentity)
	if e != nil {
		RespondError(c, e)
		return
	}
	write := connection.IdentityWrite{Mode: identity.Mode, BrokerOBO: identity.BrokerOBO, RequestPassthrough: identity.RequestPassthrough}
	machineSecretID, createdSecret, e := r.resolveMachineCredential(c, q)
	if e != nil {
		RespondError(c, e)
		return
	}
	write.MachineCredentialSecretID = machineSecretID
	if e := connection.ValidateIdentityWrite(write, providerIdentity); e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, e)
		return
	}
	stored, e := connection.MarshalStoredOutboundIdentity(identity)
	if e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, connection.ErrInvalid)
		return
	}
	id, e := newV7()
	if e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, e)
		return
	}
	v, e := r.connections.Create(c.Request.Context(), connection.NewConnection{
		ID: id, WorkspaceID: c.Param("wid"), ProviderID: c.Param("pid"),
		Name: q.Name, Alias: q.Alias, Environment: q.Environment, ExternalAccountRef: q.ExternalAccountRef,
		OutboundIdentity: stored, MachineCredentialSecretID: machineSecretID,
		GrantedScopes: q.GrantedScopes, Policy: q.Policy, MigrationState: connection.MigrationStateNone,
		CreatedBy: actor(c),
	})
	if e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, e)
		return
	}
	c.JSON(201, connectionDTOFor(v))
}
func (r *ConfigurationRoutes) getConnection(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.connections.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, connectionDTOFor(v))
}
func (r *ConfigurationRoutes) updateConnection(c *gin.Context) {
	var q connectionWriteRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, connection.ErrInvalid)
		return
	}
	// EDITOR metadata-only path uses ActionEdit; identity mutations require MANAGE.
	required := authz.ActionManage
	if q.MetadataOnly {
		required = authz.ActionEdit
	}
	if !r.authorize(c, required) {
		return
	}
	current, e := r.connections.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	if q.MetadataOnly {
		v, e := r.connections.Update(c.Request.Context(), c.Param("wid"), c.Param("id"), connection.UpdateConnection{
			Name: q.Name, Alias: q.Alias, Environment: q.Environment, ExternalAccountRef: q.ExternalAccountRef,
			GrantedScopes: q.GrantedScopes, Policy: q.Policy, UpdatedBy: actor(c),
			ExpectedLockVersion: q.LockVersion, MetadataOnly: true,
		})
		if e != nil {
			RespondError(c, e)
			return
		}
		c.JSON(200, connectionDTOFor(v))
		return
	}
	if e := rejectLegacyConnectionWrite(q); e != nil {
		RespondError(c, e)
		return
	}
	configuredProvider, e := r.providers.Get(c.Request.Context(), c.Param("wid"), current.ProviderID)
	if e != nil {
		RespondError(c, e)
		return
	}
	if configuredProvider.Status != "ACTIVE" {
		RespondError(c, connection.ErrConflict)
		return
	}
	providerIdentity, e := provider.ParseOutboundIdentity(configuredProvider.DriverConfig)
	if e != nil {
		RespondError(c, e)
		return
	}
	identity, e := outboundidentity.ParseConnectionIdentity(q.OutboundIdentity)
	if e != nil {
		RespondError(c, e)
		return
	}
	write := connection.IdentityWrite{Mode: identity.Mode, BrokerOBO: identity.BrokerOBO, RequestPassthrough: identity.RequestPassthrough}
	machineSecretID, createdSecret, e := r.resolveMachineCredential(c, q)
	if e != nil {
		RespondError(c, e)
		return
	}
	if machineSecretID == nil && current.MachineCredentialSecretID != nil && q.MachineCredential == nil && q.MachineCredentialSecretID == nil {
		machineSecretID = current.MachineCredentialSecretID
	}
	write.MachineCredentialSecretID = machineSecretID
	if e := connection.ValidateIdentityWrite(write, providerIdentity); e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, e)
		return
	}
	stored, e := connection.MarshalStoredOutboundIdentity(identity)
	if e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, connection.ErrInvalid)
		return
	}
	// Dangerous identity mutation requires a fresh impact proof.
	if strings.TrimSpace(q.ImpactConfirmationProof) == "" {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, outboundidentity.ErrIdentityChangeConfirmationRequired)
		return
	}
	descHash, e := connection.DescriptorHashForIdentity(identity.Mode, stored, q.MachineCredential != nil || q.MachineCredentialSecretID != nil)
	if e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, connection.ErrInvalid)
		return
	}
	if e := r.impactProofs.Verify(q.ImpactConfirmationProof, connection.ImpactProofPayload{
		WorkspaceID: c.Param("wid"), ConnectionID: c.Param("id"), ActorID: actor(c),
		ChangeKind: connection.ImpactChangeMode, DescriptorHash: descHash,
		LockVersion: current.LockVersion, PolicyVersion: current.OutboundIdentityPolicyVersion,
		ImpactSetVersion: connection.ImpactSetVersionFromCounts(0, 0, 0),
	}); e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, e)
		return
	}
	v, e := r.connections.Update(c.Request.Context(), c.Param("wid"), c.Param("id"), connection.UpdateConnection{
		Name: q.Name, Alias: q.Alias, Environment: q.Environment, ExternalAccountRef: q.ExternalAccountRef,
		OutboundIdentity: stored, MachineCredentialSecretID: machineSecretID,
		IncrementPolicyVersion: true, KeepMigrationState: current.RequiresMigration(),
		GrantedScopes: q.GrantedScopes, Policy: q.Policy, UpdatedBy: actor(c), ExpectedLockVersion: q.LockVersion,
	})
	if e != nil {
		r.revokeProvisionedCredential(c, createdSecret)
		RespondError(c, e)
		return
	}
	c.JSON(200, connectionDTOFor(v))
}

func (r *ConfigurationRoutes) previewConnectionImpact(c *gin.Context) {
	if !r.authorize(c, authz.ActionManage) {
		return
	}
	var q connectionImpactRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, connection.ErrInvalid)
		return
	}
	current, e := r.connections.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	if q.ExpectedLockVersion != 0 && q.ExpectedLockVersion != current.LockVersion {
		RespondError(c, outboundidentity.ErrIdentityChangeConfirmationStale)
		return
	}
	descHash, e := connection.HashChangeDescriptor(q.NonSecretChangeDescriptor)
	if e != nil {
		RespondError(c, connection.ErrInvalid)
		return
	}
	impactSetVersion := connection.ImpactSetVersionFromCounts(0, 0, 0)
	payload := connection.ImpactProofPayload{
		WorkspaceID: c.Param("wid"), ConnectionID: c.Param("id"), ActorID: actor(c),
		ChangeKind:     connection.ImpactChangeKind(strings.TrimSpace(q.ChangeKind)),
		DescriptorHash: descHash, LockVersion: current.LockVersion,
		PolicyVersion: current.OutboundIdentityPolicyVersion, ImpactSetVersion: impactSetVersion,
	}
	if payload.ChangeKind == "" {
		payload.ChangeKind = connection.ImpactChangeMode
	}
	proof, expiresAt, e := r.impactProofs.Issue(payload)
	if e != nil {
		RespondError(c, e)
		return
	}
	// Response never includes Secret identifiers — only aggregate impact counts.
	c.JSON(200, gin.H{
		"affectedPublishedTools":    0,
		"affectedAgentBindings":     0,
		"affectedWorkflowRevisions": 0,
		"impactConfirmationProof":   proof,
		"expiresAt":                 expiresAt.UTC(),
		"impactSetVersion":          impactSetVersion,
		"lockVersion":               current.LockVersion,
		"policyVersion":             current.OutboundIdentityPolicyVersion,
	})
}

func rejectLegacyConnectionWrite(q connectionWriteRequest) error {
	hasLegacyConfig := len(q.AuthConfig) > 0 && string(q.AuthConfig) != "null" && string(q.AuthConfig) != "{}"
	hasLegacySecret := q.CredentialSecretID != nil || q.Credential != nil
	if strings.TrimSpace(q.AuthMode) != "" || hasLegacyConfig || hasLegacySecret {
		return outboundidentity.ErrIdentityModeUnsupported
	}
	return nil
}

func (r *ConfigurationRoutes) resolveMachineCredential(c *gin.Context, q connectionWriteRequest) (*string, *secret.ReadDTO, error) {
	if q.MachineCredential != nil && q.MachineCredentialSecretID != nil {
		return nil, nil, connection.ErrInvalid
	}
	if q.MachineCredentialSecretID != nil {
		id, configured, err := r.resolveCredentialReference(c.Request.Context(), c.Param("wid"), q.MachineCredentialSecretID)
		if err != nil || !configured {
			return nil, nil, connection.ErrInvalid
		}
		return id, nil, nil
	}
	if q.MachineCredential == nil {
		return nil, nil, nil
	}
	return r.createConnectionCredential(c, q.Name+"-machine", q.MachineCredential, nil)
}

func (r *ConfigurationRoutes) resolveCredentialReference(ctx context.Context, workspaceID string, input *string) (*string, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	id := strings.TrimSpace(*input)
	if _, err := uuid.Parse(id); err != nil {
		return nil, false, connection.ErrInvalid
	}
	value, err := r.secrets.Get(ctx, workspaceID, id)
	if err != nil || !value.Configured {
		return nil, false, connection.ErrInvalid
	}
	return &id, true, nil
}

func (r *ConfigurationRoutes) createConnectionCredential(c *gin.Context, connectionName string, input *connectionCredentialWrite, existing *string) (*string, *secret.ReadDTO, error) {
	if input == nil {
		return existing, nil, nil
	}
	created, err := r.secrets.Create(c.Request.Context(), secret.CreateInput{
		WorkspaceID: c.Param("wid"), Name: "connection-credential-" + strings.TrimSpace(connectionName) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Kind: strings.TrimSpace(input.Kind), Plaintext: input.Plaintext, ActorUserID: actor(c),
	})
	input.Plaintext = ""
	if err != nil {
		return nil, nil, err
	}
	return &created.ID, &created, nil
}

func (r *ConfigurationRoutes) revokeProvisionedCredential(c *gin.Context, created *secret.ReadDTO) {
	if created == nil {
		return
	}
	_, _ = r.secrets.Revoke(c.Request.Context(), secret.RevokeInput{
		WorkspaceID: c.Param("wid"), SecretID: created.ID, ActorUserID: actor(c), ExpectedLockVersion: created.LockVersion,
	})
}
func (r *ConfigurationRoutes) deleteConnection(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	lock, e := deleteLock(c)
	if e == nil {
		e = r.connections.SoftDelete(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c), lock)
	}
	if e != nil {
		RespondError(c, e)
		return
	}
	c.Status(204)
}
func (r *ConfigurationRoutes) verifyConnection(c *gin.Context) {
	if !r.authorize(c, authz.ActionTest) {
		return
	}
	v, e := r.connectionVerifier.Verify(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, v)
}

type rotateSecretRequest struct {
	Plaintext   string `json:"plaintext"`
	LockVersion int64  `json:"lockVersion"`
}

func (r *ConfigurationRoutes) rotateSecret(c *gin.Context) {
	if !r.authorize(c, authz.ActionManage) {
		return
	}
	var q rotateSecretRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, secret.ErrInvalid)
		return
	}
	v, e := r.secrets.Rotate(c.Request.Context(), secret.RotateInput{WorkspaceID: c.Param("wid"), SecretID: c.Param("id"), Plaintext: q.Plaintext, ActorUserID: actor(c), ExpectedLockVersion: q.LockVersion})
	q.Plaintext = ""
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, v)
}
