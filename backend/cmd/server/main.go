package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/application"
	backendconfig "actweave/backend/internal/config"
	"actweave/backend/internal/database"
	"actweave/backend/internal/logging"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/secret"
	"actweave/backend/internal/storedobject"
)

const localSecretMasterKey = "local-master-v1"

type startupMigrator interface {
	Up() error
	Close() error
}

type openStartupMigrator func(context.Context, string) (startupMigrator, error)

func main() {
	runtimeConfig, configPath, configErr := backendconfig.LoadFromEnvironment(os.LookupEnv)
	logLevel, logFormat := "info", "text"
	if configErr == nil {
		logLevel = runtimeConfig.Logging.Level
		logFormat = runtimeConfig.Logging.Format
	}
	logger := logging.New(logging.Config{
		Level: logLevel, Format: logFormat,
	})
	logging.ConfigureDefaults(logger)
	serverLogger := logger.With("component", "server")
	if configErr != nil {
		serverLogger.Error("configuration failed", "event", "server.configuration.failed", "error", configErr.Error())
		os.Exit(1)
	}
	serverLogger.Info("configuration loaded", "event", "server.configuration.loaded", "path", configPath)

	err := runStartup(
		context.Background(),
		runtimeConfig,
		migrateDatabase,
		func(config backendconfig.Config) error {
			agentAccessSigningKeys, err := loadAgentAccessSigningKeys(config.AgentAccess.SigningKeys, time.Now().UTC())
			if err != nil {
				return fmt.Errorf("initialize Agent Access signing keys: %w", err)
			}
			outboundSigningKeys, err := loadOutboundIdentitySigningKeys(config.OutboundIdentity, time.Now().UTC())
			if err != nil {
				return fmt.Errorf("initialize outbound identity signing keys: %w", err)
			}
			app, err := application.Open(context.Background(), application.Config{
				PostgresDSN:              config.Database.DSN,
				JWTSecret:                config.Authentication.JWTSecret,
				SecretMasterKey:          config.Encryption.MasterKey,
				AgentAccessSigningKeys:   agentAccessSigningKeys,
				AgentAccessTokenEndpoint: config.AgentAccess.TokenEndpoint,
				AgentAccessMaxTokenTTL: time.Duration(
					config.AgentAccess.SigningKeys.MaxTokenTTLSeconds,
				) * time.Second,
				AgentAccessFeature: config.AgentAccess.Feature,
				MetricsBearerToken: config.Server.MetricsBearerToken,
				AgentAuditDebug:    config.AgentAudit.Debug,
				// Runtime after Load (PR15): agent engine staged to eino
				// (enabled+allowAll) unless explicitly disabled.
				Runtime: config.Runtime,
				MinIO: storedobject.MinIOConfig{
					Endpoint:  config.Storage.MinIO.Endpoint,
					AccessKey: config.Storage.MinIO.AccessKey,
					SecretKey: config.Storage.MinIO.SecretKey,
					UseSSL:    config.Storage.MinIO.UseSSL,
					Region:    config.Storage.MinIO.Region,
				},
				BootstrapAdmin:              bootstrapAdminFromConfig(config.BootstrapAdmin),
				OutboundIdentitySigningKeys: outboundSigningKeys,
				OutboundIdentityIssuer:      strings.TrimSpace(config.OutboundIdentity.Issuer),
			})
			if err != nil {
				return fmt.Errorf("initialize application: %w", err)
			}
			defer app.Close()

			addr := config.Server.Address
			serverLogger.Info("server listening", "event", "server.listening", "addr", addr)
			if err := http.ListenAndServe(addr, app.Handler()); err != nil {
				return fmt.Errorf("serve HTTP: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		serverLogger.Error("startup failed", "event", "server.startup.failed", "error", err.Error())
		os.Exit(1)
	}
}

func loadAgentAccessSigningKeys(
	config backendconfig.AgentAccessSigningKeysConfig,
	now time.Time,
) (*agentaccessauth.RotatingSigningKeyProvider, error) {
	prepublished := make([]agentaccessauth.PublicKeyFile, 0, len(config.PrepublishedPublicKeys))
	for _, value := range config.PrepublishedPublicKeys {
		prepublished = append(prepublished, agentaccessauth.PublicKeyFile{
			KeyID: value.KeyID, PublicKeyFile: value.PublicKeyFile,
		})
	}
	retired := make([]agentaccessauth.RetiredPublicKeyFile, 0, len(config.RetiredPublicKeys))
	for _, value := range config.RetiredPublicKeys {
		retired = append(retired, agentaccessauth.RetiredPublicKeyFile{
			KeyID: value.KeyID, PublicKeyFile: value.PublicKeyFile,
			RetiredAt: value.RetiredAt, PublishUntil: value.PublishUntil,
		})
	}
	return agentaccessauth.LoadFileSigningKeyProvider(agentaccessauth.FileSigningKeyConfig{
		Algorithm: config.Algorithm, ActiveKeyID: config.ActiveKeyID,
		PrivateKeyFile: config.PrivateKeyFile, GenerateIfMissing: config.GenerateIfMissing,
		MaxTokenTTL:            time.Duration(config.MaxTokenTTLSeconds) * time.Second,
		PrepublishedPublicKeys: prepublished, RetiredPublicKeys: retired,
	}, now)
}

func loadOutboundIdentitySigningKeys(
	config backendconfig.OutboundIdentityConfig,
	now time.Time,
) (*outboundidentity.RotatingSigningKeyProvider, error) {
	// Optional: empty config skips outbound JWKS (unit tests / partial boots).
	if strings.TrimSpace(config.Issuer) == "" &&
		strings.TrimSpace(config.SigningKeys.ActiveKeyID) == "" &&
		strings.TrimSpace(config.SigningKeys.PrivateKeyFile) == "" {
		return nil, nil
	}
	sk := config.SigningKeys
	prepublished := make([]outboundidentity.PublicKeyFile, 0, len(sk.PrepublishedPublicKeys))
	for _, value := range sk.PrepublishedPublicKeys {
		prepublished = append(prepublished, outboundidentity.PublicKeyFile{
			KeyID: value.KeyID, PublicKeyFile: value.PublicKeyFile,
		})
	}
	retired := make([]outboundidentity.RetiredPublicKeyFile, 0, len(sk.RetiredPublicKeys))
	for _, value := range sk.RetiredPublicKeys {
		retired = append(retired, outboundidentity.RetiredPublicKeyFile{
			KeyID: value.KeyID, PublicKeyFile: value.PublicKeyFile,
			RetiredAt: value.RetiredAt, PublishUntil: value.PublishUntil,
		})
	}
	ttl := time.Duration(sk.MaxAssertionTTLSeconds) * time.Second
	if ttl == 0 {
		ttl = outboundidentity.DefaultMaxAssertionTTL
	}
	return outboundidentity.LoadFileSigningKeyProvider(outboundidentity.FileSigningKeyConfig{
		Algorithm: sk.Algorithm, ActiveKeyID: sk.ActiveKeyID,
		PrivateKeyFile: sk.PrivateKeyFile, GenerateIfMissing: sk.GenerateIfMissing,
		MaxAssertionTTL:        ttl,
		PrepublishedPublicKeys: prepublished, RetiredPublicKeys: retired,
	}, now)
}

func bootstrapAdminFromConfig(config backendconfig.BootstrapAdminConfig) *application.BootstrapAdminConfig {
	if config.Username == "" && config.Password == "" {
		return nil
	}
	return &application.BootstrapAdminConfig{
		Username: strings.TrimSpace(config.Username), Password: config.Password,
		DisplayName: strings.TrimSpace(config.DisplayName), Locale: strings.TrimSpace(config.Locale),
		Timezone: strings.TrimSpace(config.Timezone),
	}
}

func runStartup(
	ctx context.Context,
	config backendconfig.Config,
	migrate func(context.Context, string) error,
	serve func(backendconfig.Config) error,
) error {
	if migrate == nil || serve == nil {
		return errors.New("server startup dependencies are not configured")
	}
	if err := config.ValidateServer(); err != nil {
		return fmt.Errorf("validate server configuration: %w", err)
	}
	if _, err := secret.NewLocalEncryptorFromBase64(
		localSecretMasterKey,
		config.Encryption.MasterKey,
	); err != nil {
		return fmt.Errorf("configure secret encryption: %w", err)
	}
	if err := migrate(ctx, config.Database.DSN); err != nil {
		return fmt.Errorf("migrate database before startup: %w", err)
	}
	if err := serve(config); err != nil {
		return err
	}
	return nil
}

func migrateDatabase(ctx context.Context, dsn string) error {
	return runMigrations(ctx, dsn, openDatabaseMigrator)
}

func openDatabaseMigrator(ctx context.Context, dsn string) (startupMigrator, error) {
	return database.Open(ctx, dsn)
}

func runMigrations(
	ctx context.Context,
	dsn string,
	open openStartupMigrator,
) (returnErr error) {
	if open == nil {
		return errors.New("startup migration opener is not configured")
	}
	migrator, err := open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open startup migrator: %w", err)
	}
	defer func() {
		if closeErr := migrator.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close startup migrator: %w", closeErr))
		}
	}()
	if err := migrator.Up(); err != nil {
		return fmt.Errorf("apply startup migrations: %w", err)
	}
	return nil
}
