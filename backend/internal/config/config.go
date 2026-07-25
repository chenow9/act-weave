package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ConfigFileEnv     = "ACTWEAVE_CONFIG_FILE"
	DefaultConfigFile = "config.yaml"
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Server         ServerConfig         `yaml:"server"`
	Logging        LoggingConfig        `yaml:"logging"`
	Database       DatabaseConfig       `yaml:"database"`
	Authentication AuthenticationConfig `yaml:"authentication"`
	AgentAccess    AgentAccessConfig    `yaml:"agentAccess"`
	// OutboundIdentity is the T1=A Subject Assertion signing domain.
	// Completely separate from agentAccess.signingKeys (AAP inbound trust).
	OutboundIdentity OutboundIdentityConfig `yaml:"outboundIdentity"`
	// AgentAudit controls the platform-admin agent full-trace debug audit surface.
	// Loaded once at process start; changing it requires a restart.
	AgentAudit AgentAuditConfig `yaml:"agentAudit"`
	// Runtime gates agent/workflow execution engines (legacy vs eino).
	// After Load (PR15): agent defaults to enabled+allowAll (eino staged open);
	// workflow engine defaults to wrapper. Explicit agent enabled=false rolls back.
	// Direct zero value (no Load) keeps agent enabled=false. Restart required to change.
	Runtime        RuntimeConfig        `yaml:"runtime"`
	Encryption     EncryptionConfig     `yaml:"encryption"`
	Storage        StorageConfig        `yaml:"storage"`
	BootstrapAdmin BootstrapAdminConfig `yaml:"bootstrapAdmin"`
}

// OutboundIdentityConfig holds the independent outbound assertion signing domain.
type OutboundIdentityConfig struct {
	// Issuer is the fixed iss claim for Subject Assertions
	// (e.g. https://actweave.example/outbound). Never the AAP token issuer.
	Issuer      string                            `yaml:"issuer"`
	SigningKeys OutboundIdentitySigningKeysConfig `yaml:"signingKeys"`
}

// OutboundIdentitySigningKeysConfig mirrors the AAP file-key shape but is a
// separate trust domain (different files, kids, and JWKS endpoint).
type OutboundIdentitySigningKeysConfig struct {
	Algorithm              string                            `yaml:"algorithm"`
	ActiveKeyID            string                            `yaml:"activeKeyId"`
	PrivateKeyFile         string                            `yaml:"privateKeyFile"`
	GenerateIfMissing      bool                              `yaml:"generateIfMissing"`
	MaxAssertionTTLSeconds int                               `yaml:"maxAssertionTtlSeconds"`
	PrepublishedPublicKeys []AgentAccessPublicKeyFileConfig  `yaml:"prepublishedPublicKeys"`
	RetiredPublicKeys      []AgentAccessRetiredKeyFileConfig `yaml:"retiredPublicKeys"`
}

// AgentAuditConfig is the global debug switch for agent full-trace audit.
// Zero value is Debug=false (safe default for production).
type AgentAuditConfig struct {
	// Debug enables storing/exposing raw model reasoning and tool payloads for
	// PLATFORM_ADMIN agent-audit APIs. Default false. Env: ACTWEAVE_AGENT_AUDIT_DEBUG.
	Debug bool `yaml:"debug"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
	// MetricsBearerToken protects GET /metrics when set (env ACTWEAVE_METRICS_BEARER_TOKEN).
	// Empty restricts scrape to loopback only.
	MetricsBearerToken string `yaml:"metricsBearerToken"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type AuthenticationConfig struct {
	JWTSecret string `yaml:"jwtSecret"`
}

type AgentAccessConfig struct {
	TokenEndpoint string                       `yaml:"tokenEndpoint"`
	SigningKeys   AgentAccessSigningKeysConfig `yaml:"signingKeys"`
	// Feature controls public AAP data-plane exposure (M10-T8).
	// Zero value is Enabled=false (no public surface) with empty allowlists.
	Feature AAPFeatureRollout `yaml:"feature"`
}

// AAPFeatureRollout is the Workspace/Client gray-release gate for AAP v1.
//
// Semantics:
//   - Enabled=false: entire /api/agent-access/v1 public surface is closed.
//   - Enabled=true + AllowAllWorkspaces/Clients: fully open for that dimension.
//   - Enabled=true + allowlist: only listed IDs may use the data plane.
//   - Empty allowlist with AllowAll*=false denies all (fail-closed gray start).
type AAPFeatureRollout struct {
	Enabled            bool     `yaml:"enabled"`
	AllowAllWorkspaces bool     `yaml:"allowAllWorkspaces"`
	WorkspaceIDs       []string `yaml:"workspaceIds"`
	AllowAllClients    bool     `yaml:"allowAllClients"`
	ClientIDs          []string `yaml:"clientIds"`
}

// PublicSurfaceOpen reports whether any AAP data-plane route may be served.
func (feature AAPFeatureRollout) PublicSurfaceOpen() bool {
	return feature.Enabled
}

// AllowsWorkspace reports whether the workspace may use AAP when the surface is open.
func (feature AAPFeatureRollout) AllowsWorkspace(workspaceID string) bool {
	if !feature.Enabled {
		return false
	}
	workspaceID = strings.ToLower(strings.TrimSpace(workspaceID))
	if workspaceID == "" {
		return false
	}
	if feature.AllowAllWorkspaces {
		return true
	}
	for _, id := range feature.WorkspaceIDs {
		if strings.ToLower(strings.TrimSpace(id)) == workspaceID {
			return true
		}
	}
	return false
}

// AllowsClient reports whether the Agent Access Client (azp) may use AAP.
func (feature AAPFeatureRollout) AllowsClient(clientID string) bool {
	if !feature.Enabled {
		return false
	}
	clientID = strings.ToLower(strings.TrimSpace(clientID))
	if clientID == "" {
		return false
	}
	if feature.AllowAllClients {
		return true
	}
	for _, id := range feature.ClientIDs {
		if strings.ToLower(strings.TrimSpace(id)) == clientID {
			return true
		}
	}
	return false
}

// Normalized returns a copy with trimmed lower-case UUID lists (invalid entries dropped).
func (feature AAPFeatureRollout) Normalized() AAPFeatureRollout {
	out := AAPFeatureRollout{
		Enabled:            feature.Enabled,
		AllowAllWorkspaces: feature.AllowAllWorkspaces,
		AllowAllClients:    feature.AllowAllClients,
	}
	for _, id := range feature.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out.WorkspaceIDs = append(out.WorkspaceIDs, id)
		}
	}
	for _, id := range feature.ClientIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out.ClientIDs = append(out.ClientIDs, id)
		}
	}
	return out
}

type AgentAccessSigningKeysConfig struct {
	Algorithm              string                            `yaml:"algorithm"`
	ActiveKeyID            string                            `yaml:"activeKeyId"`
	PrivateKeyFile         string                            `yaml:"privateKeyFile"`
	GenerateIfMissing      bool                              `yaml:"generateIfMissing"`
	MaxTokenTTLSeconds     int                               `yaml:"maxTokenTtlSeconds"`
	PrepublishedPublicKeys []AgentAccessPublicKeyFileConfig  `yaml:"prepublishedPublicKeys"`
	RetiredPublicKeys      []AgentAccessRetiredKeyFileConfig `yaml:"retiredPublicKeys"`
}

type AgentAccessPublicKeyFileConfig struct {
	KeyID         string `yaml:"keyId"`
	PublicKeyFile string `yaml:"publicKeyFile"`
}

type AgentAccessRetiredKeyFileConfig struct {
	KeyID         string    `yaml:"keyId"`
	PublicKeyFile string    `yaml:"publicKeyFile"`
	RetiredAt     time.Time `yaml:"retiredAt"`
	PublishUntil  time.Time `yaml:"publishUntil"`
}

type EncryptionConfig struct {
	MasterKey string `yaml:"masterKey"`
}

type StorageConfig struct {
	MinIO MinIOConfig `yaml:"minio"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	UseSSL    bool   `yaml:"useSSL"`
	Region    string `yaml:"region"`
}

type BootstrapAdminConfig struct {
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	DisplayName string `yaml:"displayName"`
	Locale      string `yaml:"locale"`
	Timezone    string `yaml:"timezone"`
}

func LoadFromEnvironment(lookup LookupEnv) (Config, string, error) {
	path := DefaultConfigFile
	if lookup != nil {
		if value, ok := lookup(ConfigFileEnv); ok {
			path = strings.TrimSpace(value)
			if path == "" {
				return Config{}, "", fmt.Errorf("%s must not be empty", ConfigFileEnv)
			}
		}
	}
	loaded, err := Load(path, lookup)
	if err != nil {
		return Config{}, path, err
	}
	return loaded, path, nil
}

func Load(path string, lookup LookupEnv) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("configuration file path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration file %q: %w", path, err)
	}
	defer file.Close()

	var loaded Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&loaded); err != nil {
		return Config{}, fmt.Errorf("decode configuration file %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode configuration file %q: multiple YAML documents are not allowed", path)
		}
		return Config{}, fmt.Errorf("decode configuration file %q: %w", path, err)
	}
	if err := loaded.applyEnvironment(lookup); err != nil {
		return Config{}, err
	}
	loaded.applyRuntimeDefaults()
	return loaded, nil
}

func (config *Config) applyEnvironment(lookup LookupEnv) error {
	if config == nil || lookup == nil {
		return nil
	}
	overrides := []struct {
		name   string
		target *string
	}{
		{name: "ACTWEAVE_API_ADDR", target: &config.Server.Address},
		{name: "ACTWEAVE_METRICS_BEARER_TOKEN", target: &config.Server.MetricsBearerToken},
		{name: "ACTWEAVE_LOG_LEVEL", target: &config.Logging.Level},
		{name: "ACTWEAVE_LOG_FORMAT", target: &config.Logging.Format},
		{name: "ACTWEAVE_POSTGRES_DSN", target: &config.Database.DSN},
		{name: "ACTWEAVE_JWT_SECRET", target: &config.Authentication.JWTSecret},
		{name: "ACTWEAVE_AAP_TOKEN_ENDPOINT", target: &config.AgentAccess.TokenEndpoint},
		{name: "ACTWEAVE_AAP_SIGNING_ACTIVE_KID", target: &config.AgentAccess.SigningKeys.ActiveKeyID},
		{name: "ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE", target: &config.AgentAccess.SigningKeys.PrivateKeyFile},
		{name: "ACTWEAVE_OUTBOUND_IDENTITY_ISSUER", target: &config.OutboundIdentity.Issuer},
		{name: "ACTWEAVE_OUTBOUND_SIGNING_ACTIVE_KID", target: &config.OutboundIdentity.SigningKeys.ActiveKeyID},
		{name: "ACTWEAVE_OUTBOUND_SIGNING_PRIVATE_KEY_FILE", target: &config.OutboundIdentity.SigningKeys.PrivateKeyFile},
		{name: "ACTWEAVE_SECRET_MASTER_KEY", target: &config.Encryption.MasterKey},
		{name: "ACTWEAVE_MINIO_ENDPOINT", target: &config.Storage.MinIO.Endpoint},
		{name: "ACTWEAVE_MINIO_ACCESS_KEY", target: &config.Storage.MinIO.AccessKey},
		{name: "ACTWEAVE_MINIO_SECRET_KEY", target: &config.Storage.MinIO.SecretKey},
		{name: "ACTWEAVE_MINIO_REGION", target: &config.Storage.MinIO.Region},
		{name: "ACTWEAVE_BOOTSTRAP_ADMIN_USERNAME", target: &config.BootstrapAdmin.Username},
		{name: "ACTWEAVE_BOOTSTRAP_ADMIN_PASSWORD", target: &config.BootstrapAdmin.Password},
		{name: "ACTWEAVE_BOOTSTRAP_ADMIN_DISPLAY_NAME", target: &config.BootstrapAdmin.DisplayName},
		{name: "ACTWEAVE_BOOTSTRAP_ADMIN_LOCALE", target: &config.BootstrapAdmin.Locale},
		{name: "ACTWEAVE_BOOTSTRAP_ADMIN_TIMEZONE", target: &config.BootstrapAdmin.Timezone},
	}
	for _, override := range overrides {
		if value, ok := lookup(override.name); ok {
			*override.target = value
		}
	}
	if raw, ok := lookup("ACTWEAVE_MINIO_USE_SSL"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_MINIO_USE_SSL must be a boolean")
		}
		config.Storage.MinIO.UseSSL = value
	}
	if raw, ok := lookup("ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING must be a boolean")
		}
		config.AgentAccess.SigningKeys.GenerateIfMissing = value
	}
	if raw, ok := lookup("ACTWEAVE_AAP_SIGNING_MAX_TOKEN_TTL_SECONDS"); ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_AAP_SIGNING_MAX_TOKEN_TTL_SECONDS must be an integer")
		}
		config.AgentAccess.SigningKeys.MaxTokenTTLSeconds = value
	}
	if raw, ok := lookup("ACTWEAVE_AAP_FEATURE_ENABLED"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_AAP_FEATURE_ENABLED must be a boolean")
		}
		config.AgentAccess.Feature.Enabled = value
	}
	if raw, ok := lookup("ACTWEAVE_AAP_FEATURE_ALLOW_ALL_WORKSPACES"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_AAP_FEATURE_ALLOW_ALL_WORKSPACES must be a boolean")
		}
		config.AgentAccess.Feature.AllowAllWorkspaces = value
	}
	if raw, ok := lookup("ACTWEAVE_AAP_FEATURE_ALLOW_ALL_CLIENTS"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_AAP_FEATURE_ALLOW_ALL_CLIENTS must be a boolean")
		}
		config.AgentAccess.Feature.AllowAllClients = value
	}
	if raw, ok := lookup("ACTWEAVE_AAP_FEATURE_WORKSPACE_IDS"); ok {
		config.AgentAccess.Feature.WorkspaceIDs = splitCSV(raw)
	}
	if raw, ok := lookup("ACTWEAVE_AAP_FEATURE_CLIENT_IDS"); ok {
		config.AgentAccess.Feature.ClientIDs = splitCSV(raw)
	}
	if raw, ok := lookup("ACTWEAVE_AGENT_AUDIT_DEBUG"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_AGENT_AUDIT_DEBUG must be a boolean")
		}
		config.AgentAudit.Debug = value
	}
	if err := config.applyRuntimeEnvironment(lookup); err != nil {
		return err
	}
	return nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (config Config) ValidateDatabase() error {
	if strings.TrimSpace(config.Database.DSN) == "" {
		return errors.New("database.dsn is required")
	}
	return nil
}

func (config Config) ValidateServer() error {
	if err := config.ValidateDatabase(); err != nil {
		return err
	}
	if strings.TrimSpace(config.Server.Address) == "" {
		return errors.New("server.address is required")
	}
	switch strings.ToLower(strings.TrimSpace(config.Logging.Level)) {
	case "debug", "info", "warn", "warning", "error":
	default:
		return errors.New("logging.level must be debug, info, warn, warning, or error")
	}
	switch strings.ToLower(strings.TrimSpace(config.Logging.Format)) {
	case "", "text", "json":
	default:
		return errors.New("logging.format must be text or json")
	}
	if len(config.Authentication.JWTSecret) < 32 {
		return errors.New("authentication.jwtSecret must contain at least 32 bytes")
	}
	if err := validateAgentAccessSigningKeys(config.AgentAccess.SigningKeys); err != nil {
		return err
	}
	if err := validateOutboundIdentityConfig(config.OutboundIdentity); err != nil {
		return err
	}
	if err := validateAAPFeatureRollout(config.AgentAccess.Feature); err != nil {
		return err
	}
	if err := validateRuntimeConfig(config.Runtime); err != nil {
		return err
	}
	if !validAgentAccessTokenEndpoint(config.AgentAccess.TokenEndpoint) {
		return errors.New("agentAccess.tokenEndpoint must be an absolute HTTPS URL (loopback HTTP is development-only)")
	}
	if strings.TrimSpace(config.Encryption.MasterKey) == "" {
		return errors.New("encryption.masterKey is required")
	}
	minIO := config.Storage.MinIO
	if strings.TrimSpace(minIO.Endpoint) == "" || strings.TrimSpace(minIO.AccessKey) == "" ||
		strings.TrimSpace(minIO.SecretKey) == "" {
		return errors.New("storage.minio endpoint, accessKey, and secretKey are required")
	}
	admin := config.BootstrapAdmin
	if admin.Username == "" && admin.Password == "" {
		return nil
	}
	if strings.TrimSpace(admin.Username) == "" || len(admin.Password) < 12 {
		return errors.New("bootstrapAdmin username and a password of at least 12 characters are required together")
	}
	if strings.TrimSpace(admin.DisplayName) == "" || strings.TrimSpace(admin.Locale) == "" ||
		strings.TrimSpace(admin.Timezone) == "" {
		return errors.New("bootstrapAdmin displayName, locale, and timezone are required when bootstrap is enabled")
	}
	return nil
}

func validateOutboundIdentityConfig(config OutboundIdentityConfig) error {
	// Empty block is allowed for tests that never load full production config;
	// production config.yaml always supplies issuer + keys. When any field is set,
	// the full outbound domain must be valid and distinct from AAP.
	if strings.TrimSpace(config.Issuer) == "" &&
		strings.TrimSpace(config.SigningKeys.ActiveKeyID) == "" &&
		strings.TrimSpace(config.SigningKeys.PrivateKeyFile) == "" {
		return nil
	}
	if strings.TrimSpace(config.Issuer) == "" {
		return errors.New("outboundIdentity.issuer is required when signing keys are configured")
	}
	if !strings.HasPrefix(strings.TrimSpace(config.Issuer), "https://") &&
		!strings.HasPrefix(strings.TrimSpace(config.Issuer), "http://127.0.0.1") &&
		!strings.HasPrefix(strings.TrimSpace(config.Issuer), "http://localhost") {
		return errors.New("outboundIdentity.issuer must be an absolute HTTPS URL (loopback HTTP is development-only)")
	}
	sk := config.SigningKeys
	if sk.Algorithm != "EdDSA" {
		return errors.New("outboundIdentity.signingKeys.algorithm must be EdDSA")
	}
	if !validAgentAccessKeyID(sk.ActiveKeyID) || strings.TrimSpace(sk.PrivateKeyFile) == "" {
		return errors.New("outboundIdentity.signingKeys activeKeyId and privateKeyFile are required")
	}
	ttl := sk.MaxAssertionTTLSeconds
	if ttl == 0 {
		ttl = 60
	}
	if ttl < 1 || ttl > 60 {
		return errors.New("outboundIdentity.signingKeys.maxAssertionTtlSeconds must be between 1 and 60")
	}
	seen := map[string]struct{}{sk.ActiveKeyID: {}}
	for _, value := range sk.PrepublishedPublicKeys {
		if !validAgentAccessKeyID(value.KeyID) || strings.TrimSpace(value.PublicKeyFile) == "" {
			return errors.New("outboundIdentity prepublished public key ID and file are required")
		}
		if _, duplicate := seen[value.KeyID]; duplicate {
			return errors.New("outboundIdentity signing key IDs must be unique")
		}
		seen[value.KeyID] = struct{}{}
	}
	retention := time.Duration(ttl)*time.Second + 5*time.Second + 5*time.Minute
	for _, value := range sk.RetiredPublicKeys {
		if !validAgentAccessKeyID(value.KeyID) || strings.TrimSpace(value.PublicKeyFile) == "" ||
			value.RetiredAt.IsZero() || value.PublishUntil.IsZero() {
			return errors.New("outboundIdentity retired public key lifecycle is incomplete")
		}
		if value.PublishUntil.Before(value.RetiredAt.Add(retention)) {
			return errors.New("outboundIdentity retired key publish window must cover assertion TTL, skew, and Broker JWKS cache")
		}
		if _, duplicate := seen[value.KeyID]; duplicate {
			return errors.New("outboundIdentity signing key IDs must be unique")
		}
		seen[value.KeyID] = struct{}{}
	}
	return nil
}

func validateAgentAccessSigningKeys(config AgentAccessSigningKeysConfig) error {
	if config.Algorithm != "EdDSA" {
		return errors.New("agentAccess.signingKeys.algorithm must be EdDSA")
	}
	if !validAgentAccessKeyID(config.ActiveKeyID) || strings.TrimSpace(config.PrivateKeyFile) == "" {
		return errors.New("agentAccess.signingKeys activeKeyId and privateKeyFile are required")
	}
	if config.MaxTokenTTLSeconds < 300 || config.MaxTokenTTLSeconds > 900 {
		return errors.New("agentAccess.signingKeys.maxTokenTtlSeconds must be between 300 and 900")
	}
	seen := map[string]struct{}{config.ActiveKeyID: {}}
	for _, value := range config.PrepublishedPublicKeys {
		if !validAgentAccessKeyID(value.KeyID) || strings.TrimSpace(value.PublicKeyFile) == "" {
			return errors.New("agentAccess prepublished public key ID and file are required")
		}
		if _, duplicate := seen[value.KeyID]; duplicate {
			return errors.New("agentAccess signing key IDs must be unique")
		}
		seen[value.KeyID] = struct{}{}
	}
	retention := time.Duration(config.MaxTokenTTLSeconds)*time.Second + 5*time.Second
	for _, value := range config.RetiredPublicKeys {
		if !validAgentAccessKeyID(value.KeyID) || strings.TrimSpace(value.PublicKeyFile) == "" ||
			value.RetiredAt.IsZero() || value.PublishUntil.IsZero() {
			return errors.New("agentAccess retired public key lifecycle is incomplete")
		}
		if _, duplicate := seen[value.KeyID]; duplicate {
			return errors.New("agentAccess signing key IDs must be unique")
		}
		if value.PublishUntil.Before(value.RetiredAt.Add(retention)) {
			return errors.New("agentAccess retired public key must cover maximum token TTL and clock skew")
		}
		seen[value.KeyID] = struct{}{}
	}
	return nil
}

func validateAAPFeatureRollout(feature AAPFeatureRollout) error {
	if feature.AllowAllWorkspaces && len(feature.WorkspaceIDs) > 0 {
		return errors.New("agentAccess.feature.workspaceIds must be empty when allowAllWorkspaces is true")
	}
	if feature.AllowAllClients && len(feature.ClientIDs) > 0 {
		return errors.New("agentAccess.feature.clientIds must be empty when allowAllClients is true")
	}
	const maxAllowlist = 10_000
	if len(feature.WorkspaceIDs) > maxAllowlist || len(feature.ClientIDs) > maxAllowlist {
		return errors.New("agentAccess.feature allowlists exceed the configured maximum")
	}
	seenWS := make(map[string]struct{}, len(feature.WorkspaceIDs))
	for _, id := range feature.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return errors.New("agentAccess.feature.workspaceIds must not contain empty values")
		}
		if _, dup := seenWS[id]; dup {
			return errors.New("agentAccess.feature.workspaceIds must be unique")
		}
		seenWS[id] = struct{}{}
	}
	seenCL := make(map[string]struct{}, len(feature.ClientIDs))
	for _, id := range feature.ClientIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return errors.New("agentAccess.feature.clientIds must not contain empty values")
		}
		if _, dup := seenCL[id]; dup {
			return errors.New("agentAccess.feature.clientIds must be unique")
		}
		seenCL[id] = struct{}{}
	}
	return nil
}

func validAgentAccessKeyID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func validAgentAccessTokenEndpoint(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasSuffix(parsed.Path, "/api/agent-access/v1/oauth/token") {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	address := net.ParseIP(host)
	return parsed.Scheme == "http" && (host == "localhost" || address != nil && address.IsLoopback())
}
