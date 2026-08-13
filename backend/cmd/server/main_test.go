package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendconfig "actweave/backend/internal/config"
)

var validStartupSecretMasterKey = base64.StdEncoding.EncodeToString(
	[]byte("0123456789abcdef0123456789abcdef"),
)

type fakeStartupMigrator struct {
	upCalls  int
	upErr    error
	closeErr error
}

type fakeManagedHTTPServer struct {
	serveStarted chan struct{}
	serveDone    chan struct{}
	serveErr     error
	shutdownErr  error
	closeErr     error
	shutdowns    int
	closes       int
}

func newFakeManagedHTTPServer(serveErr error) *fakeManagedHTTPServer {
	return &fakeManagedHTTPServer{
		serveStarted: make(chan struct{}), serveDone: make(chan struct{}), serveErr: serveErr,
	}
}

func (server *fakeManagedHTTPServer) ListenAndServe() error {
	close(server.serveStarted)
	<-server.serveDone
	return server.serveErr
}

func (server *fakeManagedHTTPServer) Shutdown(context.Context) error {
	server.shutdowns++
	close(server.serveDone)
	return server.shutdownErr
}

func (server *fakeManagedHTTPServer) Close() error {
	server.closes++
	select {
	case <-server.serveDone:
	default:
		close(server.serveDone)
	}
	return server.closeErr
}

func TestServeHTTPShutsDownOnContextCancellation(t *testing.T) {
	server := newFakeManagedHTTPServer(http.ErrServerClosed)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, time.Second) }()
	<-server.serveStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
	if server.shutdowns != 1 || server.closes != 0 {
		t.Fatalf("unexpected lifecycle calls: shutdowns=%d closes=%d", server.shutdowns, server.closes)
	}
}

func TestServeHTTPForceClosesWhenGracefulShutdownFails(t *testing.T) {
	shutdownErr := errors.New("shutdown timed out")
	server := newFakeManagedHTTPServer(http.ErrServerClosed)
	server.shutdownErr = shutdownErr
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, time.Second) }()
	<-server.serveStarted
	cancel()
	if err := <-done; !errors.Is(err, shutdownErr) {
		t.Fatalf("expected shutdown error, got %v", err)
	}
	if server.shutdowns != 1 || server.closes != 1 {
		t.Fatalf("unexpected lifecycle calls: shutdowns=%d closes=%d", server.shutdowns, server.closes)
	}
}

func TestServeHTTPReturnsListenFailure(t *testing.T) {
	listenErr := errors.New("bind failed")
	server := newFakeManagedHTTPServer(listenErr)
	close(server.serveDone)
	if err := serveHTTP(context.Background(), server, time.Second); !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error, got %v", err)
	}
}

func (f *fakeStartupMigrator) Up() error {
	f.upCalls++
	return f.upErr
}

func (f *fakeStartupMigrator) Close() error {
	return f.closeErr
}

func TestDefaultServerWiringLeavesChatRuntimeToRouterEinoPath(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	for _, forbidden := range []string{
		"NewOrchestratedRuntimeRunner",
		"tooltranslator.New()",
		"workflowtranslator.New()",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("default server wiring must not install legacy chat runtime dependency %q", forbidden)
		}
	}
}

func TestDefaultServerWiringUsesDomainApplicationComposition(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	value := string(source)
	for _, required := range []string{"application.Open", "app.Handler()"} {
		if !strings.Contains(value, required) {
			t.Fatalf("default server wiring must use domain composition %q", required)
		}
	}
}

func TestRunStartupMigratesBeforeServing(t *testing.T) {
	var order []string
	config := validServerConfig()
	config.Database.DSN = "postgres://startup-test"
	err := runStartup(
		context.Background(),
		config,
		func(_ context.Context, dsn string) error {
			if dsn != "postgres://startup-test" {
				t.Fatalf("unexpected migration DSN %q", dsn)
			}
			order = append(order, "migrate")
			return nil
		},
		func(config backendconfig.Config) error {
			if config.Database.DSN != "postgres://startup-test" {
				t.Fatalf("unexpected server DSN %q", config.Database.DSN)
			}
			order = append(order, "serve")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("run startup: %v", err)
	}
	if strings.Join(order, ",") != "migrate,serve" {
		t.Fatalf("expected migration before serving, got %v", order)
	}
}

func TestRunStartupDoesNotServeWhenMigrationFails(t *testing.T) {
	migrationErr := errors.New("database is dirty")
	served := false
	err := runStartup(
		context.Background(),
		validServerConfig(),
		func(context.Context, string) error { return migrationErr },
		func(backendconfig.Config) error {
			served = true
			return nil
		},
	)
	if !errors.Is(err, migrationErr) {
		t.Fatalf("expected migration error, got %v", err)
	}
	if served {
		t.Fatal("server must not start after migration failure")
	}
}

func TestRunStartupRequiresSecretMasterKey(t *testing.T) {
	migrated := false
	served := false
	config := validServerConfig()
	config.Encryption.MasterKey = ""
	err := runStartup(
		context.Background(),
		config,
		func(context.Context, string) error {
			migrated = true
			return nil
		},
		func(backendconfig.Config) error {
			served = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "encryption.masterKey") {
		t.Fatalf("expected missing secret master key error, got %v", err)
	}
	if migrated || served {
		t.Fatalf("missing secret key must not migrate or serve: migrated=%t served=%t", migrated, served)
	}
}

func TestRunStartupRequiresPostgresDSN(t *testing.T) {
	migrated := false
	served := false
	config := validServerConfig()
	config.Database.DSN = ""
	err := runStartup(
		context.Background(),
		config,
		func(context.Context, string) error {
			migrated = true
			return nil
		},
		func(backendconfig.Config) error {
			served = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "database.dsn") {
		t.Fatalf("expected missing DSN error, got %v", err)
	}
	if migrated || served {
		t.Fatalf("missing DSN must not migrate or serve: migrated=%t served=%t", migrated, served)
	}
}

func TestBootstrapAdminConfigIsOptionalAndPreservesValidatedFields(t *testing.T) {
	if config := bootstrapAdminFromConfig(backendconfig.BootstrapAdminConfig{}); config != nil {
		t.Fatalf("empty bootstrap configuration must be disabled: %+v", config)
	}
	config := bootstrapAdminFromConfig(backendconfig.BootstrapAdminConfig{
		Username: " admin ", Password: "strong-admin-password-1", DisplayName: " Administrator ",
		Locale: " zh-CN ", Timezone: " Asia/Singapore ",
	})
	if config == nil || config.Username != "admin" || config.Password != "strong-admin-password-1" ||
		config.DisplayName != "Administrator" || config.Locale != "zh-CN" || config.Timezone != "Asia/Singapore" {
		t.Fatalf("unexpected bootstrap administrator configuration: %+v", config)
	}
}

func TestLoadAgentAccessSigningKeysUsesSeparateStableEdDSAKeyFile(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	privateKeyFile := filepath.Join(t.TempDir(), "keys", "aap-private.pem")
	config := backendconfig.AgentAccessSigningKeysConfig{
		Algorithm: "EdDSA", ActiveKeyID: "server-aap-key", PrivateKeyFile: privateKeyFile,
		GenerateIfMissing: true, MaxTokenTTLSeconds: 600,
	}
	provider, err := loadAgentAccessSigningKeys(config, now)
	if err != nil {
		t.Fatal(err)
	}
	key, err := provider.ActiveSigningKey(now)
	if err != nil || key.KeyID() != "server-aap-key" || key.Algorithm() != "EdDSA" {
		t.Fatalf("unexpected active Agent Access signing key: id=%q alg=%q err=%v", key.KeyID(), key.Algorithm(), err)
	}
	if _, err := os.Stat(privateKeyFile); err != nil {
		t.Fatalf("generated Agent Access key file is missing: %v", err)
	}
}

func TestRunMigrationsJoinsOperationAndCloseErrors(t *testing.T) {
	upErr := errors.New("up failed")
	closeErr := errors.New("close failed")
	fake := &fakeStartupMigrator{upErr: upErr, closeErr: closeErr}
	err := runMigrations(
		context.Background(),
		"postgres://startup-test",
		func(context.Context, string) (startupMigrator, error) {
			return fake, nil
		},
	)
	if fake.upCalls != 1 {
		t.Fatalf("expected one migration attempt, got %d", fake.upCalls)
	}
	if !errors.Is(err, upErr) || !errors.Is(err, closeErr) {
		t.Fatalf("expected joined up and close errors, got %v", err)
	}
}

func validServerConfig() backendconfig.Config {
	return backendconfig.Config{
		Server:         backendconfig.ServerConfig{Address: ":8082"},
		Logging:        backendconfig.LoggingConfig{Level: "info", Format: "text"},
		Database:       backendconfig.DatabaseConfig{DSN: "postgres://startup-test"},
		Authentication: backendconfig.AuthenticationConfig{JWTSecret: "startup-jwt-secret-that-is-at-least-32-bytes"},
		AgentAccess: backendconfig.AgentAccessConfig{TokenEndpoint: "https://api.example.test/api/agent-access/v1/oauth/token", SigningKeys: backendconfig.AgentAccessSigningKeysConfig{
			Algorithm: "EdDSA", ActiveKeyID: "startup-aap-key",
			PrivateKeyFile: "/run/secrets/startup-aap-key.pem", MaxTokenTTLSeconds: 900,
		}},
		// runStartup validates runtime budgets without Load/defaults.
		Runtime: backendconfig.RuntimeConfig{
			Eino: backendconfig.EinoRuntimeTuning{
				MaxIterations:      backendconfig.DefaultEinoMaxIterations,
				MaxToolInvocations: backendconfig.DefaultEinoMaxToolInvocations,
			},
		},
		Encryption: backendconfig.EncryptionConfig{MasterKey: validStartupSecretMasterKey},
		Storage: backendconfig.StorageConfig{MinIO: backendconfig.MinIOConfig{
			Endpoint: "127.0.0.1:9000", AccessKey: "access", SecretKey: "secret",
		}},
	}
}
