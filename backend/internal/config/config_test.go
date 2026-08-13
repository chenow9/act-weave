package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfigYAML = `
server:
  address: ":8082"
logging:
  level: info
  format: text
database:
  dsn: postgres://file-database
authentication:
  jwtSecret: file-jwt-secret-that-is-at-least-32-bytes
agentAccess:
  tokenEndpoint: http://127.0.0.1:8082/api/agent-access/v1/oauth/token
  signingKeys:
    algorithm: EdDSA
    activeKeyId: file-aap-signing-key
    privateKeyFile: .local/file-aap-signing-private.pem
    generateIfMissing: true
    maxTokenTtlSeconds: 900
    prepublishedPublicKeys: []
    retiredPublicKeys: []
encryption:
  masterKey: file-master-key
storage:
  minio:
    endpoint: file-minio:9000
    accessKey: file-access
    secretKey: file-secret
    useSSL: false
    region: file-region
bootstrapAdmin:
  username: file-admin
  password: file-admin-password
  displayName: File Administrator
  locale: zh-CN
  timezone: Asia/Singapore
`

func TestAgentAuditDebugDefaultsFalseAndEnvOverrides(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, lookup(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AgentAudit.Debug {
		t.Fatalf("agentAudit.debug must default false, got true")
	}

	withYAML := writeConfig(t, validConfigYAML+"\nagentAudit:\n  debug: true\n")
	fromFile, err := Load(withYAML, lookup(nil))
	if err != nil {
		t.Fatalf("load yaml debug: %v", err)
	}
	if !fromFile.AgentAudit.Debug {
		t.Fatalf("yaml agentAudit.debug=true was not applied")
	}

	envOff, err := Load(withYAML, lookup(map[string]string{"ACTWEAVE_AGENT_AUDIT_DEBUG": "false"}))
	if err != nil {
		t.Fatalf("load env override: %v", err)
	}
	if envOff.AgentAudit.Debug {
		t.Fatalf("env ACTWEAVE_AGENT_AUDIT_DEBUG=false must override yaml true")
	}

	envOn, err := Load(path, lookup(map[string]string{"ACTWEAVE_AGENT_AUDIT_DEBUG": "true"}))
	if err != nil {
		t.Fatalf("load env true: %v", err)
	}
	if !envOn.AgentAudit.Debug {
		t.Fatalf("env ACTWEAVE_AGENT_AUDIT_DEBUG=true must enable debug")
	}
}

func TestToolsAllowForcePublishDefaultsFalseAndEnvOverrides(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, lookup(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Tools.AllowForcePublish {
		t.Fatalf("tools.allowForcePublish must default false")
	}

	withYAML := writeConfig(t, validConfigYAML+"\ntools:\n  allowForcePublish: true\n")
	fromFile, err := Load(withYAML, lookup(nil))
	if err != nil {
		t.Fatalf("load yaml force publish: %v", err)
	}
	if !fromFile.Tools.AllowForcePublish {
		t.Fatalf("yaml tools.allowForcePublish=true was not applied")
	}

	envOff, err := Load(withYAML, lookup(map[string]string{"ACTWEAVE_TOOLS_ALLOW_FORCE_PUBLISH": "false"}))
	if err != nil {
		t.Fatalf("load env override: %v", err)
	}
	if envOff.Tools.AllowForcePublish {
		t.Fatalf("env ACTWEAVE_TOOLS_ALLOW_FORCE_PUBLISH=false must override yaml true")
	}

	envOn, err := Load(path, lookup(map[string]string{"ACTWEAVE_TOOLS_ALLOW_FORCE_PUBLISH": "true"}))
	if err != nil {
		t.Fatalf("load env true: %v", err)
	}
	if !envOn.Tools.AllowForcePublish {
		t.Fatalf("env ACTWEAVE_TOOLS_ALLOW_FORCE_PUBLISH=true must enable force publish")
	}
}

func TestLoadAppliesEnvironmentOverridesAfterFile(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	values := map[string]string{
		"ACTWEAVE_API_ADDR":                          ":9090",
		"ACTWEAVE_LOG_LEVEL":                         "debug",
		"ACTWEAVE_LOG_FORMAT":                        "json",
		"ACTWEAVE_POSTGRES_DSN":                      "postgres://environment-database",
		"ACTWEAVE_JWT_SECRET":                        "environment-jwt-secret-at-least-32-bytes",
		"ACTWEAVE_MODEL_EGRESS_ALLOWED_CIDRS":        "10.0.0.0/8, ::1/128",
		"ACTWEAVE_AAP_TOKEN_ENDPOINT":                "https://api.example.test/api/agent-access/v1/oauth/token",
		"ACTWEAVE_AAP_SIGNING_ACTIVE_KID":            "environment-aap-key",
		"ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE":      "/run/secrets/environment-aap-key.pem",
		"ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING":   "false",
		"ACTWEAVE_AAP_SIGNING_MAX_TOKEN_TTL_SECONDS": "600",
		"ACTWEAVE_SECRET_MASTER_KEY":                 "environment-master-key",
		"ACTWEAVE_MINIO_ENDPOINT":                    "environment-minio:9000",
		"ACTWEAVE_MINIO_ACCESS_KEY":                  "environment-access",
		"ACTWEAVE_MINIO_SECRET_KEY":                  "environment-secret",
		"ACTWEAVE_MINIO_USE_SSL":                     "true",
		"ACTWEAVE_MINIO_REGION":                      "environment-region",
		"ACTWEAVE_BOOTSTRAP_ADMIN_USERNAME":          "environment-admin",
		"ACTWEAVE_BOOTSTRAP_ADMIN_PASSWORD":          "environment-admin-password",
		"ACTWEAVE_BOOTSTRAP_ADMIN_DISPLAY_NAME":      "Environment Administrator",
		"ACTWEAVE_BOOTSTRAP_ADMIN_LOCALE":            "en-US",
		"ACTWEAVE_BOOTSTRAP_ADMIN_TIMEZONE":          "UTC",
	}
	loaded, err := Load(path, lookup(values))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if loaded.Server.Address != ":9090" || loaded.Logging.Level != "debug" || loaded.Logging.Format != "json" ||
		loaded.Database.DSN != "postgres://environment-database" ||
		loaded.Authentication.JWTSecret != "environment-jwt-secret-at-least-32-bytes" ||
		loaded.Encryption.MasterKey != "environment-master-key" {
		t.Fatalf("top-level environment overrides were not applied: %+v", loaded)
	}
	if strings.Join(loaded.Models.Egress.AllowedCIDRs, ",") != "10.0.0.0/8,::1/128" {
		t.Fatalf("model egress CIDR override was not applied: %+v", loaded.Models.Egress)
	}
	keys := loaded.AgentAccess.SigningKeys
	if keys.ActiveKeyID != "environment-aap-key" || keys.PrivateKeyFile != "/run/secrets/environment-aap-key.pem" ||
		keys.GenerateIfMissing || keys.MaxTokenTTLSeconds != 600 || keys.Algorithm != "EdDSA" {
		t.Fatalf("Agent Access signing key overrides were not applied: %+v", keys)
	}
	if loaded.AgentAccess.TokenEndpoint != "https://api.example.test/api/agent-access/v1/oauth/token" {
		t.Fatalf("Agent Access Token Endpoint override was not applied: %q", loaded.AgentAccess.TokenEndpoint)
	}
	minIO := loaded.Storage.MinIO
	if minIO.Endpoint != "environment-minio:9000" || minIO.AccessKey != "environment-access" ||
		minIO.SecretKey != "environment-secret" || !minIO.UseSSL || minIO.Region != "environment-region" {
		t.Fatalf("MinIO environment overrides were not applied: %+v", minIO)
	}
	admin := loaded.BootstrapAdmin
	if admin.Username != "environment-admin" || admin.Password != "environment-admin-password" ||
		admin.DisplayName != "Environment Administrator" || admin.Locale != "en-US" || admin.Timezone != "UTC" {
		t.Fatalf("bootstrap environment overrides were not applied: %+v", admin)
	}
}

func TestLoadFromEnvironmentUsesConfiguredPath(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	loaded, usedPath, err := LoadFromEnvironment(lookup(map[string]string{ConfigFileEnv: path}))
	if err != nil {
		t.Fatalf("load configured path: %v", err)
	}
	if usedPath != path || loaded.Database.DSN != "postgres://file-database" {
		t.Fatalf("unexpected path/configuration: path=%q config=%+v", usedPath, loaded)
	}
}

func TestCheckedInDevelopmentConfigurationIsValid(t *testing.T) {
	loaded, err := Load(filepath.Join("..", "..", "config.yaml"), nil)
	if err != nil {
		t.Fatalf("load checked-in development configuration: %v", err)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("validate checked-in development configuration: %v", err)
	}
	if loaded.Runtime.ModelVerification.TimeoutSeconds != DefaultModelVerificationTimeoutSeconds {
		t.Fatalf("checked-in modelVerification.timeoutSeconds=%d want %d",
			loaded.Runtime.ModelVerification.TimeoutSeconds, DefaultModelVerificationTimeoutSeconds)
	}
	if !loaded.Runtime.ToolDisclosure.Enabled || !loaded.Runtime.ToolDisclosure.AllowAllWorkspaces {
		t.Fatalf("checked-in toolDisclosure must be enabled + allowAll, got %+v", loaded.Runtime.ToolDisclosure)
	}
	if !loaded.Runtime.ToolDisclosure.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") {
		t.Fatal("checked-in toolDisclosure must allow any workspace")
	}
	if loaded.Runtime.SessionContext.Compaction.Enabled ||
		loaded.Runtime.SessionContext.Compaction.Mode != "disabled" {
		t.Fatalf("checked-in sessionContext.compaction must stay off, got %+v",
			loaded.Runtime.SessionContext.Compaction)
	}
}

func TestLoadRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown":  validConfigYAML + "unknownSetting: true\n",
		"multiple": validConfigYAML + "---\nserver:\n  address: ':9999'\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, contents), nil); err == nil {
				t.Fatal("invalid configuration must be rejected")
			}
		})
	}
}

func TestLoadRejectsInvalidEnvironmentBooleanWithoutLeakingValue(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	_, err := Load(path, lookup(map[string]string{"ACTWEAVE_MINIO_USE_SSL": "secret-not-a-bool"}))
	if err == nil || !strings.Contains(err.Error(), "ACTWEAVE_MINIO_USE_SSL") {
		t.Fatalf("expected boolean override error, got %v", err)
	}
	if strings.Contains(err.Error(), "secret-not-a-bool") {
		t.Fatalf("configuration error leaked override value: %v", err)
	}
}

func TestValidationRejectsUnsafeAgentAccessSigningKeyConfiguration(t *testing.T) {
	loaded, err := Load(writeConfig(t, validConfigYAML), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AgentAccessSigningKeysConfig){
		"symmetric algorithm": func(value *AgentAccessSigningKeysConfig) { value.Algorithm = "HS256" },
		"missing file":        func(value *AgentAccessSigningKeysConfig) { value.PrivateKeyFile = "" },
		"short TTL":           func(value *AgentAccessSigningKeysConfig) { value.MaxTokenTTLSeconds = 299 },
		"long TTL":            func(value *AgentAccessSigningKeysConfig) { value.MaxTokenTTLSeconds = 901 },
		"unsafe kid":          func(value *AgentAccessSigningKeysConfig) { value.ActiveKeyID = "../../key" },
	} {
		t.Run(name, func(t *testing.T) {
			config := loaded
			mutate(&config.AgentAccess.SigningKeys)
			if err := config.ValidateServer(); err == nil {
				t.Fatal("unsafe Agent Access signing key configuration must fail")
			}
		})
	}
	loaded.AgentAccess.TokenEndpoint = "http://metadata.google.internal/api/agent-access/v1/oauth/token"
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("non-loopback HTTP Agent Access Token Endpoint must fail")
	}
}

func TestValidationSeparatesMigrationAndServerRequirements(t *testing.T) {
	loaded, err := Load(writeConfig(t, validConfigYAML), nil)
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if err := loaded.ValidateDatabase(); err != nil {
		t.Fatalf("validate database configuration: %v", err)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("validate server configuration: %v", err)
	}

	migrationOnly := Config{Database: DatabaseConfig{DSN: "postgres://migration-only"}}
	if err := migrationOnly.ValidateDatabase(); err != nil {
		t.Fatalf("migration-only configuration should be valid: %v", err)
	}
	if err := migrationOnly.ValidateServer(); err == nil {
		t.Fatal("migration-only configuration must not satisfy server validation")
	}

	loaded.BootstrapAdmin.Password = ""
	if err := loaded.ValidateServer(); err == nil || !strings.Contains(err.Error(), "bootstrapAdmin") {
		t.Fatalf("partial bootstrap credentials must fail, got %v", err)
	}
	loaded.BootstrapAdmin = BootstrapAdminConfig{}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("disabled bootstrap configuration should be valid: %v", err)
	}
}

func TestValidationRejectsInvalidModelEgressCIDR(t *testing.T) {
	loaded, err := Load(writeConfig(t, validConfigYAML), nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Models.Egress.AllowedCIDRs = []string{"not-a-cidr"}
	if err := loaded.ValidateServer(); err == nil || !strings.Contains(err.Error(), "models.egress.allowedCidrs") {
		t.Fatalf("expected invalid model egress CIDR error, got %v", err)
	}
}

func TestEnvironmentCanClearFileValueAndValidationCatchesIt(t *testing.T) {
	loaded, err := Load(writeConfig(t, validConfigYAML), lookup(map[string]string{
		"ACTWEAVE_POSTGRES_DSN": "",
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if err := loaded.ValidateDatabase(); err == nil {
		t.Fatal("an explicitly empty environment override must not fall back to the file")
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write test configuration: %v", err)
	}
	return path
}

func lookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
