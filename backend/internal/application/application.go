package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/agentaudit"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/audit"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/config"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/identity"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/openapiimport"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/secret"
	"actweave/backend/internal/smartdag"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/toolruntime"
	httptransport "actweave/backend/internal/transport/http"
	ssetransport "actweave/backend/internal/transport/sse"
	"actweave/backend/internal/workflow"
	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workflowruntime"
	"actweave/backend/internal/workspace"
	"actweave/backend/internal/workspaceoverview"

	"github.com/cloudwego/eino/components/model"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

type BootstrapAdminConfig struct {
	Username    string
	Password    string
	DisplayName string
	Locale      string
	Timezone    string
}

type Config struct {
	PostgresDSN              string
	JWTSecret                string
	SecretMasterKey          string
	AgentAccessSigningKeys   agentaccessauth.SigningKeyProvider
	AgentAccessTokenEndpoint string
	AgentAccessMaxTokenTTL   time.Duration
	// AgentAccessFeature gates the public AAP data plane (M10-T8). Zero value keeps
	// surface closed; set Enabled+allow-all for local/dev full open.
	AgentAccessFeature config.AAPFeatureRollout
	// MetricsBearerToken protects GET /metrics when set; empty = loopback only.
	MetricsBearerToken string
	// AgentAuditDebug enables agent full-trace debug audit capture/exposure.
	AgentAuditDebug bool
	// Runtime selects agent/workflow engines. After config Load (PR15/PR16/P0),
	// agent production path is always ADK eino;
	// workflow defaults to engine=eino (compose). Zero-value RuntimeConfig
	// without Load keeps agent Enabled=false for tests inspecting the flag only.
	Runtime        config.RuntimeConfig
	MinIO          storedobject.MinIOConfig
	HTTPClient     *http.Client
	BootstrapAdmin *BootstrapAdminConfig
	// OutboundRuntimeInstanceID is the stable deploy-configured instance id for T2=A
	// affinity (not request-sourced). Empty disables runtime instance registration.
	OutboundRuntimeInstanceID string
	// OutboundRuntimeInternalAddress is the deploy-configured internal mesh address
	// (https://… or host:port). Never taken from request input.
	OutboundRuntimeInternalAddress string
	// OutboundIdentitySigningKeys is the independent T1=A Subject Assertion key
	// domain (not AAP). Nil skips JWKS registration and Broker assertion issuer.
	OutboundIdentitySigningKeys outboundidentity.SigningKeyProvider
	// OutboundIdentityIssuer is the fixed iss for Subject Assertions.
	OutboundIdentityIssuer string
	// PreviewPurge paces the create-preview body cleanup worker (ZKL-69).
	// Zero values use agent.DefaultPreviewPurgeConfig.
	PreviewPurge agent.PreviewPurgeConfig
}

type Application struct {
	db                         *sql.DB
	handler                    http.Handler
	eventNotifier              *protocolevent.InProcessLiveNotifier
	securityChanges            *agentaccessauth.InProcessSecurityChanges
	clientSecretAuthenticator  *agentaccessauth.ClientSecretAuthenticator
	privateKeyJWTAuthenticator *agentaccessauth.PrivateKeyJWTAuthenticator
	agentAccessAuthorizer      *agentaccessauth.AAPAuthorizationService
	securityVersionCache       *agentaccessauth.SecurityVersionCache
	recoveryWorker             *execution.RecoveryWorker
	previewPurgeWorker         *agent.PreviewPurgeWorker
	// outbound runtime lifecycle (optional; nil when affinity not configured)
	outboundRuntime *outboundRuntimeLifecycle
}

func (application *Application) Handler() http.Handler { return application.handler }

func (application *Application) Close() error {
	if application == nil || application.db == nil {
		return nil
	}
	if application.recoveryWorker != nil {
		application.recoveryWorker.Stop()
	}
	if application.previewPurgeWorker != nil {
		application.previewPurgeWorker.Stop()
	}
	if application.outboundRuntime != nil {
		application.outboundRuntime.Stop()
	}
	var notifierErr error
	if application.eventNotifier != nil {
		notifierErr = application.eventNotifier.Close()
	}
	var securityChangesErr error
	if application.securityChanges != nil {
		securityChangesErr = application.securityChanges.Close()
	}
	return errors.Join(notifierErr, securityChangesErr, application.db.Close())
}

func Open(ctx context.Context, config Config) (_ *Application, returnErr error) {
	if config.PostgresDSN == "" || config.JWTSecret == "" || config.SecretMasterKey == "" ||
		config.AgentAccessSigningKeys == nil || config.AgentAccessTokenEndpoint == "" ||
		config.AgentAccessMaxTokenTTL < agentaccessauth.MinimumAccessTokenTTL ||
		config.AgentAccessMaxTokenTTL > agentaccessauth.DefaultMaxAccessTokenTTL {
		return nil, errors.New("application database, JWT, encryption, and Agent Access signing configuration are required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	db, err := sql.Open("postgres", config.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("open application database: %w", err)
	}
	defer func() {
		if returnErr != nil {
			_ = db.Close()
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping application database: %w", err)
	}

	events, err := audit.NewRepository(db)
	if err != nil {
		return nil, err
	}
	outbox, err := audit.NewOutboxRepository(db)
	if err != nil {
		return nil, err
	}
	builder, err := audit.NewBuilder(audit.DefaultInlineDetailBytes)
	if err != nil {
		return nil, err
	}
	auditRecorder, err := audit.NewRecorder(events, outbox, builder)
	if err != nil {
		return nil, err
	}
	securityChanges := agentaccessauth.NewInProcessSecurityChanges()
	securityVersionCache, err := agentaccessauth.NewSecurityVersionCache(30 * time.Second)
	if err != nil {
		return nil, err
	}

	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		return nil, err
	}
	authorizer, err := authz.NewService(workspaceRepository, auditRecorder)
	if err != nil {
		return nil, err
	}

	secretRepository, err := secret.NewRepository(db)
	if err != nil {
		return nil, err
	}
	secretEncryptor, err := secret.NewLocalEncryptorFromBase64("local-master-v1", config.SecretMasterKey)
	if err != nil {
		return nil, err
	}
	secretService, err := secret.NewService(secretRepository, secretEncryptor)
	if err != nil {
		return nil, err
	}
	legacySecretInjector, err := execution.NewHTTPSecretInjector(secretService)
	if err != nil {
		return nil, err
	}
	// Dual-mode OutboundIdentityInjector: Broker + Vault after confirmation.
	// Legacy injector remains only as a residual path and fails closed for dual-mode.
	var secretInjector execution.SecretInjector = legacySecretInjector
	outboundVault, vaultErr := outboundidentity.NewRuntimeCredentialVault(
		"app-boot", nil, outboundidentity.VaultConfig{},
	)
	if vaultErr == nil && config.OutboundIdentitySigningKeys != nil &&
		strings.TrimSpace(config.OutboundIdentityIssuer) != "" {
		assertionIssuer, issuerErr := outboundidentity.NewAssertionIssuer(
			config.OutboundIdentitySigningKeys, config.OutboundIdentityIssuer, nil,
		)
		if issuerErr == nil {
			brokerClient, brokerErr := outboundidentity.NewBrokerClient(assertionIssuer)
			if brokerErr == nil {
				outboundCache := outboundidentity.NewBrokerTokenCache(nil)
				machineLookup := machineCredentialResolver{db: db}
				if dual, dualErr := execution.NewOutboundIdentityInjector(execution.OutboundIdentityInjectorConfig{
					Legacy: legacySecretInjector, Vault: outboundVault, Broker: brokerClient,
					Cache: outboundCache, MachineSecrets: secretService,
					MachineCredentialLookup: machineLookup, BootID: outboundVault.BootID(),
				}); dualErr == nil {
					secretInjector = dual
				}
			}
		}
	}
	objectRepository, err := storedobject.NewRepository(db)
	if err != nil {
		return nil, err
	}
	objectAuthorizer, err := storedobject.NewWorkspaceReadAuthorizer(authorizer)
	if err != nil {
		return nil, err
	}
	objectStore, err := storedobject.NewMinIOStore(config.MinIO, objectRepository, objectAuthorizer)
	if err != nil {
		return nil, err
	}
	objectCipher, err := storedobject.NewLocalChunkCipherFromBase64("local-master-v1", config.SecretMasterKey)
	if err != nil {
		return nil, err
	}
	secureObjects, err := storedobject.NewSecureStore(objectStore, objectCipher)
	if err != nil {
		return nil, err
	}

	identityRepository, err := identity.NewRepository(db)
	if err != nil {
		return nil, err
	}
	passwords, err := authn.NewPasswordManager(authn.DefaultArgon2idParams())
	if err != nil {
		return nil, err
	}
	accessTokens, err := authn.NewAccessTokenManager(config.JWTSecret, "actweave", 0)
	if err != nil {
		return nil, err
	}
	authService, err := authn.NewService(identityRepository, passwords, accessTokens,
		authn.NewRefreshTokenManager(), authn.ServiceConfig{
			RefreshTTL:    7 * 24 * time.Hour,
			LockoutPolicy: authn.LockoutPolicy{MaxFailedAttempts: 5, Duration: 15 * time.Minute},
		}, authn.WithAuthenticationAudit(auditRecorder), authn.WithIdentityManagementAudit(auditRecorder))
	if err != nil {
		return nil, err
	}
	if config.BootstrapAdmin != nil {
		adminID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("allocate bootstrap administrator ID: %w", err)
		}
		if _, _, err := authService.BootstrapAdmin(ctx, authn.CreateUserRequest{
			ID: adminID.String(), Username: config.BootstrapAdmin.Username,
			DisplayName: config.BootstrapAdmin.DisplayName, Password: config.BootstrapAdmin.Password,
			Status: identity.StatusActive, PlatformRole: identity.PlatformRoleAdmin,
			Locale: config.BootstrapAdmin.Locale, Timezone: config.BootstrapAdmin.Timezone,
			MustChangePassword: true,
		}); err != nil {
			return nil, fmt.Errorf("bootstrap platform administrator: %w", err)
		}
	}
	authenticator, err := httptransport.NewAccessTokenAuthenticator(authService)
	if err != nil {
		return nil, err
	}
	authRoutes, err := httptransport.NewAuthUserRoutes(authService)
	if err != nil {
		return nil, err
	}
	workspaceRoutes, err := httptransport.NewWorkspaceRoutes(workspaceRepository, authorizer, identityRepository)
	if err != nil {
		return nil, err
	}
	agentAccessRepository, err := agentaccess.NewRepository(db)
	if err != nil {
		return nil, err
	}
	agentAccessPepper := sha256.Sum256([]byte(config.SecretMasterKey + "\x00agent-access-credential-pepper-v1"))
	agentAccessManagement, err := agentaccess.NewManagementService(
		agentAccessRepository, agentAccessPepper[:],
		agentaccess.WithManagementAudit(auditRecorder),
		agentaccess.WithSecurityChangePublisher(agentAccessSecurityPublisher{
			source: securityChanges, cache: securityVersionCache,
		}),
	)
	if err != nil {
		return nil, err
	}
	clientAuthenticationLimiter, err := agentaccessauth.NewInMemoryClientAuthenticationLimiter(
		agentaccessauth.DefaultClientAuthenticationMaxFailures,
		agentaccessauth.DefaultClientAuthenticationWindow,
		agentaccessauth.DefaultClientAuthenticationLimiterEntries,
	)
	if err != nil {
		return nil, err
	}
	clientSecretAuthenticator, err := agentaccessauth.NewClientSecretAuthenticator(
		agentAccessClientSecretStore{repository: agentAccessRepository}, agentAccessPepper[:],
		agentaccessauth.WithClientSecretAuthenticationLimiter(clientAuthenticationLimiter),
		agentaccessauth.WithClientSecretAuthenticationAudit(agentAccessClientSecretAudit{sink: auditRecorder}),
	)
	if err != nil {
		return nil, err
	}
	remoteJWKS, err := agentaccessauth.NewRemoteJWKSCache(
		privateKeyJWTJWKSFetcher{base: client}, agentaccessauth.DefaultRemoteJWKSMaxBytes,
		agentaccessauth.DefaultRemoteJWKSMaxKeys, agentaccessauth.DefaultRemoteJWKSCacheEntries,
	)
	if err != nil {
		return nil, err
	}
	privateKeyJWTAuthenticator, err := agentaccessauth.NewPrivateKeyJWTAuthenticator(
		agentAccessPrivateKeyJWTStore{repository: agentAccessRepository}, remoteJWKS,
		agentAccessClientAssertionJTIStore{repository: agentAccessRepository},
		config.AgentAccessTokenEndpoint,
		agentaccessauth.WithPrivateKeyJWTAuthenticationLimiter(clientAuthenticationLimiter),
		agentaccessauth.WithPrivateKeyJWTAuthenticationAudit(agentAccessPrivateKeyJWTAudit{sink: auditRecorder}),
	)
	if err != nil {
		return nil, err
	}
	clientCredentialsTokens, err := agentaccessauth.NewClientCredentialsTokenService(
		agentAccessClientCredentialsGrantStore{repository: agentAccessRepository},
		config.AgentAccessSigningKeys, config.AgentAccessTokenEndpoint,
		config.AgentAccessMaxTokenTTL,
	)
	if err != nil {
		return nil, err
	}
	subjectTokenVerifier, err := agentaccessauth.NewTrustedSubjectTokenVerifier(remoteJWKS)
	if err != nil {
		return nil, err
	}
	tokenExchangeTokens, err := agentaccessauth.NewTokenExchangeService(
		agentAccessTokenExchangeTrustStore{repository: agentAccessRepository},
		agentAccessExternalSubjectMapper{repository: agentAccessRepository},
		subjectTokenVerifier,
		agentAccessSubjectTokenJTIStore{repository: agentAccessRepository},
		agentAccessClientCredentialsGrantStore{repository: agentAccessRepository},
		config.AgentAccessSigningKeys, agentAccessPepper[:],
		config.AgentAccessTokenEndpoint, config.AgentAccessMaxTokenTTL,
	)
	if err != nil {
		return nil, err
	}
	agentAccessTokenVerifier, err := agentaccessauth.NewAAPAccessTokenVerifier(
		config.AgentAccessSigningKeys, config.AgentAccessTokenEndpoint,
		config.AgentAccessMaxTokenTTL,
	)
	if err != nil {
		return nil, err
	}
	subjectOwnershipRepository, err := agentaccessauth.NewSubjectOwnershipRepository(db)
	if err != nil {
		return nil, err
	}
	subjectOwnershipPolicy, err := agentaccessauth.NewSubjectOwnershipPolicy(subjectOwnershipRepository)
	if err != nil {
		return nil, err
	}
	agentAccessAuthorizer, err := agentaccessauth.NewAAPAuthorizationService(
		agentAccessAuthorizationStateStore{repository: agentAccessRepository},
		subjectOwnershipPolicy,
		agentaccessauth.WithAAPAuthorizationAudit(agentAccessAuthorizationAudit{sink: auditRecorder}),
	)
	if err != nil {
		return nil, err
	}
	agentAccessRoutes, err := httptransport.NewAgentAccessManagementRoutes(
		agentAccessManagement, agentAccessRepository, authorizer, agentAccessRepository,
	)
	if err != nil {
		return nil, err
	}
	agentAccessJWKSRoutes, err := httptransport.NewAgentAccessJWKSRoutes(config.AgentAccessSigningKeys)
	if err != nil {
		return nil, err
	}
	agentAccessTokenRoutes, err := httptransport.NewAgentAccessTokenRoutes(
		clientSecretAuthenticator, privateKeyJWTAuthenticator, clientCredentialsTokens,
		tokenExchangeTokens,
	)
	if err != nil {
		return nil, err
	}
	// Production baseline: client × IP × grant token endpoint rate limits.
	// Multi-replica deployments should inject a Redis/Gateway TokenEndpointLimiter.
	tokenIssueLimiter, err := agentaccessauth.NewInMemoryTokenEndpointLimiter(
		agentaccessauth.DefaultTokenEndpointLimiterConfig(),
	)
	if err != nil {
		return nil, err
	}
	if err := agentAccessTokenRoutes.ConfigureTokenIssueLimiter(tokenIssueLimiter); err != nil {
		return nil, err
	}

	modelRepository, err := modelconfig.NewRepository(db)
	if err != nil {
		return nil, err
	}
	modelVerifier, err := modelconfig.NewVerificationService(modelRepository,
		&modelConfigVerifier{client: client, secrets: secretService}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	providerRepository, err := provider.NewRepository(db)
	if err != nil {
		return nil, err
	}
	connectionRepository, err := connection.NewRepository(db)
	if err != nil {
		return nil, err
	}
	connectionVerifier, err := connection.NewVerificationService(connectionRepository,
		&serviceConnectionVerifier{client: client, providers: providerRepository, injector: legacySecretInjector}, 20*time.Second)
	if err != nil {
		return nil, err
	}

	rawOpenAPI := &openAPIRawStore{db: db, objects: secureObjects}
	headerAuthorizer, err := openapiimport.NewDatabaseProviderHeaderAuthorizer(db, secretInjector)
	if err != nil {
		return nil, err
	}
	documentLoader, err := openapiimport.NewHTTPProviderDocumentLoader(client, headerAuthorizer, rawOpenAPI, 0)
	if err != nil {
		return nil, err
	}
	providerRegistry, err := provider.NewPhaseOneRegistry(&openAPIDiscoverer{loader: documentLoader})
	if err != nil {
		return nil, err
	}
	providerSyncer, err := provider.NewSyncService(providerRepository, providerRegistry)
	if err != nil {
		return nil, err
	}
	toolRepository, err := tool.NewRepository(db)
	if err != nil {
		return nil, err
	}
	providerMaterializer, err := provider.NewMaterializationService(providerRepository, toolRepository)
	if err != nil {
		return nil, err
	}
	configurationRoutes, err := httptransport.NewConfigurationRoutes(httptransport.ConfigurationDependencies{
		Authorizer: authorizer, Models: modelRepository, ModelVerifier: modelVerifier,
		Providers: providerRepository, ProviderSyncer: providerSyncer,
		Materializer: providerMaterializer, ProviderRegistry: providerRegistry,
		Connections: connectionRepository, ConnectionVerifier: connectionVerifier, Secrets: secretService,
	})
	if err != nil {
		return nil, err
	}

	agentRepository, err := agent.NewRepository(db)
	if err != nil {
		return nil, err
	}
	promptObjects, err := agent.NewStoredPromptObjectStore(secureObjects)
	if err != nil {
		return nil, err
	}
	promptService, err := agent.NewPromptService(agentRepository, promptObjects,
		&modelSnapshotSource{models: modelRepository},
		&promptGenerator{models: modelRepository, secrets: secretService, client: client})
	if err != nil {
		return nil, err
	}
	currentPromptQuery, err := agent.NewCurrentPromptQuery(agentRepository, auditRecorder)
	if err != nil {
		return nil, err
	}
	agentCreationService, err := agent.NewCreationService(agentRepository, objectRepository)
	if err != nil {
		return nil, err
	}
	agentCreationService = agentCreationService.WithAuditor(auditRecorder)
	capabilityRepository, err := capability.NewRepository(db)
	if err != nil {
		return nil, err
	}
	bindingService, err := capability.NewBindingService(capabilityRepository,
		&bindingConnectionCompatibility{db: db})
	if err != nil {
		return nil, err
	}
	capabilityCatalog, err := capability.NewCatalog(capabilityRepository, capabilityRepository)
	if err != nil {
		return nil, err
	}
	agentRoutes, err := httptransport.NewAgentCapabilityRoutes(authorizer, agentRepository,
		promptService, capabilityRepository, capabilityCatalog, bindingService)
	if err != nil {
		return nil, err
	}
	agentRoutes = agentRoutes.WithCurrentPromptReader(currentPromptQuery).
		WithCreationService(agentCreationService)
	aapAgentProfileRoutes, err := httptransport.NewAAPAgentProfileRoutes(
		agentAccessAuthorizer, agentRepository, capabilityCatalog,
	)
	if err != nil {
		return nil, err
	}

	executorRegistry, err := toolruntime.NewExecutorRegistry(client)
	if err != nil {
		return nil, err
	}
	payloadWriter, err := storedobject.NewSensitivePayloadWriter(secureObjects, storedobject.NewJSONSecretScrubber())
	if err != nil {
		return nil, err
	}
	toolArtifacts, err := tool.NewStoredToolTestArtifacts(payloadWriter)
	if err != nil {
		return nil, err
	}
	toolTests, err := tool.NewTestServiceWithInjector(toolRepository, executorRegistry, toolArtifacts, secretInjector)
	if err != nil {
		return nil, err
	}
	// REQUEST_PASSTHROUGH tool tests / direct invokes share the process vault
	// with OutboundIdentityInjector (attach → borrow → inject → cleanup).
	if vaultErr == nil && outboundVault != nil {
		if toolAttacher, attErr := outboundidentity.NewBindingAttacher(outboundVault, nil); attErr == nil {
			bootID := outboundVault.BootID()
			toolTests = toolTests.WithBindingAttacher(toolAttacher, bootID)
		}
	}
	toolPublisher, err := tool.NewPublishService(toolRepository, authorizer, auditRecorder)
	if err != nil {
		return nil, err
	}
	invocationResolver, err := tool.NewInvocationResolver(db)
	if err != nil {
		return nil, err
	}
	invocationAuthorizer, err := tool.NewWorkspaceInvocationAuthorizer(authorizer)
	if err != nil {
		return nil, err
	}
	executionConfirmationRepository, err := execution.NewConfirmationRepository(db)
	if err != nil {
		return nil, err
	}
	executionConfirmations, err := execution.NewConfirmationService(executionConfirmationRepository)
	if err != nil {
		return nil, err
	}
	invocationRepository, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		return nil, err
	}
	invocationRecorder, err := execution.NewPermanentInvocationRecorder(invocationRepository, payloadWriter)
	if err != nil {
		return nil, err
	}
	invocationPipeline, err := execution.NewInvocationPipeline(invocationAuthorizer, invocationResolver,
		executionConfirmations, newInvocationIdempotencyStore(), allowInvocationLimiter{}, secretInjector,
		executorRegistry, invocationRecorder, retryWaiter{})
	if err != nil {
		return nil, err
	}
	directInvoker, err := tool.NewDirectInvocationService(invocationResolver, invocationPipeline)
	if err != nil {
		return nil, err
	}
	if vaultErr == nil && outboundVault != nil {
		if invokeAttacher, attErr := outboundidentity.NewBindingAttacher(outboundVault, nil); attErr == nil {
			directInvoker = directInvoker.WithBindingAttacher(invokeAttacher, outboundVault.BootID())
		}
	}

	openAPIRepository, err := openapiimport.NewRepository(db)
	if err != nil {
		return nil, err
	}
	openAPIParser, err := openapiimport.NewParseService(openAPIRepository,
		openapiimport.KinOpenAPIParser{}, openapiimport.UUIDv7Generator)
	if err != nil {
		return nil, err
	}
	providerSources, err := openapiimport.NewProviderSourceRepository(db)
	if err != nil {
		return nil, err
	}
	openAPIImporter, err := openapiimport.NewProviderImportService(providerSources,
		providerRegistry, documentLoader, openAPIParser)
	if err != nil {
		return nil, err
	}
	openAPIFileImporter, err := openapiimport.NewFileImportService(providerSources, rawOpenAPI, openAPIParser, 0)
	if err != nil {
		return nil, err
	}
	openAPIGenerator, err := openapiimport.NewGenerationService(db, toolRepository, newToolIDs)
	if err != nil {
		return nil, err
	}
	toolRoutes, err := httptransport.NewToolOpenAPIRoutes(httptransport.ToolOpenAPIDependencies{
		Authorizer: authorizer, Tools: toolRepository, Tests: toolTests,
		TestConnections: invocationResolver, Publisher: toolPublisher, Invoker: directInvoker,
		Imports: openAPIRepository, Importer: openAPIImporter, FileImporter: openAPIFileImporter, Generator: openAPIGenerator,
	})
	if err != nil {
		return nil, err
	}

	workflowRepository, err := workflow.NewRepository(db)
	if err != nil {
		return nil, err
	}
	workflowCompiler, err := workflow.NewCompilationService(workflowRepository, workflowcompiler.New())
	if err != nil {
		return nil, err
	}
	workflowInvoker := &workflowToolInvoker{invoker: directInvoker}
	// P0: config-driven executor. Load defaults stage engine=eino (compose);
	// explicit engine=wrapper is the PlanRunner rollback valve. Compose paths
	// require a durable CheckPointStore for Approval StatefulInterrupt.
	workflowExecutorCfg := workflowruntime.ExecutorFactoryConfig{Invoker: workflowInvoker}
	workflowEngine := strings.ToLower(strings.TrimSpace(config.Runtime.Workflow.Engine))
	if workflowEngine == "" {
		workflowEngine = workflowruntime.EngineWrapper
	}
	if workflowEngine != workflowruntime.EngineWrapper {
		store, storeErr := einoruntime.NewPostgresCheckPointStore(db)
		if storeErr != nil {
			return nil, fmt.Errorf("workflow compose checkpoint store required when engine=%s: %w",
				workflowEngine, storeErr)
		}
		workflowExecutorCfg.CheckPointStore = store
	}
	workflowPlanRunner := workflowruntime.NewExecutorFromConfig(
		config.Runtime.Workflow, workflowExecutorCfg,
	)
	// Published WORKFLOW-as-tool for Console Chat (P3.4) and confirmation resume.
	publishedWorkflowRunner, err := workflowruntime.NewPublishedRevisionRunner(workflowRepository, workflowPlanRunner)
	if err != nil {
		return nil, err
	}
	// Composite invoker: TOOL → HTTP pipeline; WORKFLOW → published revision runner.
	// Used by chatruntimebridge and tool confirmation resume (same side-effect path).
	chatInvoker := &chatToolInvoker{
		resolver: invocationResolver, pipeline: invocationPipeline,
		workflows: publishedWorkflowRunner, authorizer: invocationAuthorizer,
	}
	workflowTrialRunner, err := workflow.NewRuntimeTrialRunner(workflowPlanRunner)
	if err != nil {
		return nil, err
	}
	// Dual-mode trial: BindingAttacher + terminal cleanup when vault is available.
	var workflowTrials httptransport.WorkflowTrialer
	outboundReqsLoader, loaderErr := workflow.NewOutboundRequirementsLoader(db)
	trialConnLookup, lookupErr := workflow.NewDBTrialConnectionLookup(db)
	if vaultErr == nil && outboundVault != nil && loaderErr == nil && lookupErr == nil {
		// Prefer dual-mode trial when process has a vault (even without Broker keys).
		// BootID is process-local; affinity optional when runtime instance empty.
		cleaner := &execution.RootOutboundLifecycle{
			Vault: outboundVault, BootID: outboundVault.BootID(),
		}
		attacher, attErr := outboundidentity.NewBindingAttacher(outboundVault, nil)
		if attErr == nil {
			if dualTrial, dualErr := workflow.NewOutboundTrialService(
				workflowRepository, workflowTrialRunner, attacher, outboundReqsLoader,
				cleaner, trialConnLookup,
			); dualErr == nil {
				workflowTrials = dualTrial
			}
		}
	}
	if workflowTrials == nil {
		// Fallback: Broker-only / no-passthrough trials without Vault attach.
		basicTrials, basicErr := workflow.NewTrialService(workflowRepository, workflowTrialRunner)
		if basicErr != nil {
			return nil, basicErr
		}
		workflowTrials = basicTrials
	}
	workflowPublisher, err := workflow.NewPublishService(workflowRepository, authorizer, auditRecorder)
	if err != nil {
		return nil, err
	}
	workflowActivator, err := workflow.NewActivationService(workflowRepository, authorizer, auditRecorder)
	if err != nil {
		return nil, err
	}
	workflowReadiness, err := workflow.NewReadinessService(workflowRepository)
	if err != nil {
		return nil, err
	}
	workflowGenerator, err := smartdag.NewService(toolRepository, workflowRepository, smartdag.UUIDv7Generator)
	if err != nil {
		return nil, err
	}
	// workflowRoutes is constructed after runService so production :execute can start
	// durable WorkflowExecution rows (WP2). Placeholder nil until then.
	var workflowRoutes *httptransport.WorkflowRoutes

	// SmartGenerateSession multi-turn path (D15 / smart-dag.v2). Memory-safe SQL store when DB present.
	// Production GraphModel is PlatformChatModel only (D2/D3) — no silent CatalogGraphModel rules path.
	agentModelGate, err := smartdag.NewAgentModelGate(agentRepository, modelRepository)
	if err != nil {
		return nil, err
	}
	// Stream-safe client shared with agent chatruntimebridge (no overall Timeout).
	modelHTTP := modelapi.NewStreamingHTTPClient()
	platformGraphModel, err := smartdag.NewPlatformChatGraphModel(smartdag.PlatformChatGraphModelDeps{
		Models: modelRepository,
		Tools:  toolRepository,
		Build: func(ctx context.Context, cfg modelconfig.Config) (smartdag.ChatModel, error) {
			return modelapi.NewEinoOpenAIChatModel(ctx, modelHTTP, secretService, cfg)
		},
	})
	if err != nil {
		return nil, err
	}
	generateSessionStore, err := smartdag.NewSQLSessionRepository(db)
	if err != nil {
		return nil, err
	}
	turnService, err := smartdag.NewTurnService(smartdag.TurnServiceDeps{
		Model:   platformGraphModel,
		Drafts:  workflowRepository,
		Prompts: smartdag.NewMemorySystemPromptStore(),
		Gate:    agentModelGate,
		Tools:   toolRepository,
		NextID:  smartdag.UUIDv7Generator,
	})
	if err != nil {
		return nil, err
	}
	sessionLocker, err := smartdag.NewSQLSessionLocker(db)
	if err != nil {
		return nil, err
	}
	generateSessionService, err := smartdag.NewSessionService(smartdag.SessionServiceDeps{
		Sessions: generateSessionStore,
		Turns:    turnService,
		Gate:     agentModelGate,
		Prompts:  smartdag.NewMemorySystemPromptStore(),
		Drafts:   workflowRepository,
		Locker:   sessionLocker,
		NextID:   smartdag.UUIDv7Generator,
	})
	if err != nil {
		return nil, err
	}
	generateSessionRoutes, err := httptransport.NewGenerateSessionRoutes(httptransport.GenerateSessionDependencies{
		Authorizer: authorizer,
		Sessions:   generateSessionService,
	})
	if err != nil {
		return nil, err
	}

	runRepository, err := execution.NewRunRepository(db)
	if err != nil {
		return nil, err
	}
	runService, err := execution.NewRunService(runRepository,
		&agentRunSnapshots{agents: agentRepository, models: modelRepository, catalog: capabilityCatalog},
		&runAuthorizer{authorizer: authorizer})
	if err != nil {
		return nil, err
	}
	productionPlanRunner, err := workflow.NewRuntimeProductionPlanRunner(workflowPlanRunner)
	if err != nil {
		return nil, err
	}
	productionExecute, err := workflow.NewProductionExecuteService(
		workflowRepository,
		&productionRuns{service: runService, repo: runRepository},
		productionPlanRunner,
		workflow.NewMemoryProductionIdempotencyStore(),
	)
	if err != nil {
		return nil, err
	}
	if outboundReqsLoader != nil {
		var cleaner *execution.RootOutboundLifecycle
		bootID := ""
		if vaultErr == nil && outboundVault != nil {
			bootID = outboundVault.BootID()
			cleaner = &execution.RootOutboundLifecycle{Vault: outboundVault, BootID: bootID}
		}
		_ = productionExecute.ConfigureOutbound(outboundReqsLoader, cleaner, bootID)
	}
	// Confirmation resume is wired after resumeService is constructed (below).
	// workflowRoutes Production is reassigned after ConfigureConfirmationResume.
	workflowRoutes, err = httptransport.NewWorkflowRoutes(httptransport.WorkflowDependencies{
		Authorizer: authorizer, Store: workflowRepository, Compiler: workflowCompiler,
		Trials: workflowTrials, Publisher: workflowPublisher,
		Activator: workflowActivator, Readiness: workflowReadiness, Generator: workflowGenerator,
		Production: productionExecute,
	})
	if err != nil {
		return nil, err
	}
	chatRepository, err := chat.NewRepository(db)
	if err != nil {
		return nil, err
	}
	chatObjects, err := chat.NewStoredMessageContent(secureObjects)
	if err != nil {
		return nil, err
	}
	chatService, err := chat.NewService(chatRepository, runRepository, runService,
		chat.WithPermanentContent(chatObjects, chat.DefaultInlineContentBytes), chat.WithAuditSink(auditRecorder))
	if err != nil {
		return nil, err
	}
	aapConversations, err := aap.NewConversationService(chatRepository, runRepository)
	if err != nil {
		return nil, err
	}
	aapCommandReceipts, err := aap.NewCommandReceiptRepository(db)
	if err != nil {
		return nil, err
	}
	if err := aapConversations.ConfigureCommandReceipts(aapCommandReceipts); err != nil {
		return nil, err
	}
	aapRateQuota, err := agentaccess.NewInMemoryDataPlaneQuota(
		agentaccess.DefaultDataPlaneQuotaConfig(),
	)
	if err != nil {
		return nil, err
	}
	aapRunConcurrencyQuota, err := agentaccess.NewPostgresRunConcurrencyQuota(
		db, agentaccess.DefaultRunConcurrencyQuotaConfig(),
	)
	if err != nil {
		return nil, err
	}
	aapCommandQuota, err := agentaccess.NewCompositeDataPlaneQuota(
		aapRateQuota, aapRunConcurrencyQuota,
	)
	if err != nil {
		return nil, err
	}
	aapConversationRoutes, err := httptransport.NewAAPConversationRoutes(
		agentAccessAuthorizer, aapConversations,
	)
	if err != nil {
		return nil, err
	}
	if err := aapConversationRoutes.ConfigureCommandQuota(aapCommandQuota); err != nil {
		return nil, err
	}
	toolResumeExecutor, err := execution.NewToolConfirmationResumeExecutor(chatInvoker)
	if err != nil {
		return nil, err
	}
	workflowResumeExecutor, err := workflowruntime.NewConfirmationResumeExecutor(publishedWorkflowRunner)
	if err != nil {
		return nil, err
	}
	resumeRegistry, err := execution.NewConfirmationResumeRegistry(toolResumeExecutor, workflowResumeExecutor)
	if err != nil {
		return nil, err
	}
	resumeRepository, err := execution.NewConfirmationResumeRepository(db)
	if err != nil {
		return nil, err
	}
	resumeService, err := execution.NewConfirmationResumeService(resumeRepository,
		executionConfirmations, runRepository, resumeRegistry)
	if err != nil {
		return nil, err
	}
	// Production Approval HITL: prepare durable confirmation + resume checkpoint.
	if err := productionExecute.ConfigureConfirmationResume(resumeService); err != nil {
		return nil, err
	}
	chatConfirmations, err := chat.NewConfirmationService(chatRepository,
		executionConfirmations, resumeService, chat.WithConfirmationAuditSink(auditRecorder))
	if err != nil {
		return nil, err
	}
	liveEvents := protocolevent.NewInProcessLiveNotifier()
	protocolUnit, err := protocolevent.NewProtocolUnitOfWork(db, liveEvents)
	if err != nil {
		return nil, err
	}
	protocolRunLifecycle, err := execution.NewProtocolRunLifecycleService(runRepository, protocolUnit)
	if err != nil {
		return nil, err
	}
	protocolRunItems, err := protocolevent.NewRunItemRepository(db)
	if err != nil {
		return nil, err
	}
	interactionDecisions, err := execution.NewInteractionDecisionService(
		executionConfirmations, resumeService,
	)
	if err != nil {
		return nil, err
	}
	if err := workflowRoutes.ConfigureExecutionConfirmations(interactionDecisions); err != nil {
		return nil, err
	}
	protocolInteractionDecisions, err := execution.NewProtocolInteractionDecisionService(
		interactionDecisions, protocolUnit, execution.NewInteractionProtocolMapper(),
	)
	if err != nil {
		return nil, err
	}
	aapInteractionDecisions, err := aap.NewInteractionDecisionService(
		runRepository, protocolRunItems, protocolInteractionDecisions,
	)
	if err != nil {
		return nil, err
	}
	if err := aapInteractionDecisions.ConfigureCommandReceipts(aapCommandReceipts); err != nil {
		return nil, err
	}
	runtimeProtocol, err := chatruntime.NewNativeProtocolRecorder(
		runRepository, invocationRepository, protocolRunItems, protocolUnit,
		protocolRunLifecycle, chatObjects,
	)
	if err != nil {
		return nil, err
	}
	protocolEvents, err := protocolevent.NewEventReader(db)
	if err != nil {
		return nil, err
	}
	eventFollower, err := ssetransport.NewCatchUpFollow(
		protocolEvents, liveEvents, ssetransport.DefaultFollowPolicy(),
	)
	if err != nil {
		return nil, err
	}
	streamPolicy := ssetransport.DefaultBackpressurePolicy()
	streamLimiter, err := ssetransport.NewInMemoryConnectionLimiter(streamPolicy)
	if err != nil {
		return nil, err
	}
	streamAgentAccessAuthorizer, err := agentaccessauth.NewCachedStreamAuthorizer(
		agentAccessStreamAuthorizationStateStore{repository: agentAccessRepository},
		securityVersionCache,
	)
	if err != nil {
		return nil, err
	}
	streamRevalidator, err := agentaccessauth.NewStreamRevalidator(
		streamAuthorizerRouter{
			agentAccess: streamAgentAccessAuthorizer,
			userAPI:     agentaccessauth.NewControlledStreamAuthorizer(),
		}, securityChanges,
		agentaccessauth.DefaultRevalidationPolicy(),
	)
	if err != nil {
		return nil, err
	}
	aapEventAttacher, err := httptransport.NewAAPEventCatchUp(protocolEvents, eventFollower)
	if err != nil {
		return nil, err
	}
	if err := aapEventAttacher.ConfigureBackpressure(streamPolicy, streamLimiter); err != nil {
		return nil, err
	}
	if err := aapEventAttacher.ConfigureRevalidator(streamRevalidator); err != nil {
		return nil, err
	}
	// Production agent path is eino-only (chatruntimebridge). Bridge
	// construction failures fail bootstrap closed.
	runtimeCfg := config.Runtime.Normalized()
	checkpointStore, storeErr := einoruntime.NewPostgresCheckPointStore(db)
	if storeErr != nil {
		return nil, fmt.Errorf("eino checkpoint store required after PR16: %w", storeErr)
	}
	einoCheckpoints := einoCheckpointDeleter(checkpointStore)
	einoEngine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: checkpointStore})
	// modelHTTP is constructed earlier for smart-dag PlatformChatGraphModel (shared).
	// D14: ProtocolMessageTextSink path for true Stream → item.delta.
	messageProjector, projectorErr := chat.NewProtocolMessageProjector(
		protocolUnit, chat.NewProtocolMessageMapper(chatObjects),
	)
	var textSinkFactory chatruntimebridge.TextSinkFactory
	if projectorErr == nil && messageProjector != nil {
		items := protocolRunItems
		textSinkFactory = func(ctx context.Context, args chatruntimebridge.TextSinkArgs) (chatruntime.TextDeltaSink, error) {
			msgCtx := chat.ProtocolMessageContext{
				Scope: protocolevent.RunScope{
					WorkspaceID: args.Run.WorkspaceID, AgentID: args.Run.AgentID,
					ConversationID: args.Run.SessionID, RunID: args.Run.ID,
				},
				EventStreamID: args.Run.ID,
				TraceID:       args.Run.TraceID,
			}
			ordinal := 1
			if items != nil {
				if existing, listErr := items.ListForRun(
					ctx, args.Run.WorkspaceID, args.Run.AgentID, args.Run.ID,
				); listErr == nil {
					for _, item := range existing {
						if item.Ordinal >= ordinal {
							ordinal = item.Ordinal + 1
						}
					}
				}
			}
			if _, startErr := messageProjector.ProjectStarted(ctx, chat.ProjectStartedMessageInput{
				Context: msgCtx, MessageID: args.MessageID, Role: "ASSISTANT",
				Ordinal: ordinal, StartedAt: time.Now().UTC(),
			}); startErr != nil {
				// Do not attach ProtocolMessageTextSink without a started
				// item — ProjectDelta would fail the model drive. Keep Sink
				// non-nil (nop) so StreamDeltaRecorder path stays wired;
				// permanent assistant content still lands via completeRun.
				return chatruntimebridge.NopTextDeltaSink{}, nil
			}
			return chatruntime.NewProtocolMessageTextSink(
				messageProjector,
				chatruntimebridge.NopTextStreamFinalizer{},
				nil,
				msgCtx,
				args.MessageID,
			)
		}
	}
	modelTurnContent, modelTurnErr := agent.NewModelTurnContentService(secureObjects, runRepository)
	if modelTurnErr != nil {
		return nil, fmt.Errorf("model turn content service: %w", modelTurnErr)
	}
	bridge, bridgeErr := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: chatRepository, Results: chatService, Content: chatObjects,
		Agents: agentRepository, Models: modelRepository, Runs: runRepository,
		Events: runtimeProtocol, Steps: runRepository,
		ModelTurns:         &chatModelTurnRecorder{inner: modelTurnContent},
		ToolInvoker:        chatInvoker,
		Confirmations:      chatConfirmations,
		Engine:             einoEngine,
		CheckpointTTL:      checkpointStore,
		TextSinkFactory:    textSinkFactory,
		MaxIterations:      runtimeCfg.Eino.MaxIterations,
		MaxToolInvocations: runtimeCfg.Eino.MaxToolInvocations,
		AgentAuditDebug:    config.AgentAuditDebug,
		BuildChatModel: func(ctx context.Context, cfg modelconfig.Config) (model.BaseChatModel, error) {
			return modelapi.NewEinoOpenAIChatModel(ctx, modelHTTP, secretService, cfg)
		},
	})
	if bridgeErr != nil {
		return nil, fmt.Errorf("eino chat runtime bridge required after PR16: %w", bridgeErr)
	}
	einoRuntime := agentrun.Runtime(bridge)
	agentRuntime, err := agentrun.NewFactory(runtimeCfg.Agent, einoRuntime)
	if err != nil {
		return nil, err
	}

	continuationRecovery, err := execution.NewContinuationRecoveryService(
		executionConfirmations, resumeService, interactionDecisions,
	)
	if err != nil {
		return nil, err
	}
	// Approval path and Recovery Worker share ContinueApprovedInteraction, which
	// acquires the durable runtime continue lease before EnqueueContinue.
	// ContinueDispatcher (PR16): einoChatResume only; chatLoop-only → invalid.
	interactionContinuation := &aapInteractionContinuation{
		runs: runRepository, protocol: runtimeProtocol,
		eino: einoRuntime, recovery: continuationRecovery,
	}
	if err := aapInteractionDecisions.ConfigureContinuation(interactionContinuation); err != nil {
		return nil, err
	}
	protocolLifecycleRepair, err := execution.NewProtocolLifecycleRepair(
		runRepository, protocolRunLifecycle, protocolEvents,
	)
	if err != nil {
		return nil, err
	}
	recoveryWorker, err := execution.NewRecoveryWorker(
		continuationRecovery, protocolLifecycleRepair, interactionContinuation,
		execution.DefaultRecoveryWorkerConfig(), nil,
	)
	if err != nil {
		return nil, err
	}
	// T2=A: optional instance/boot affinity. Empty instance id keeps multi-replica
	// pure-Broker recovery unchanged; passthrough roots require deploy config.
	outboundRuntime, err := startOutboundRuntimeLifecycle(
		ctx, db,
		config.OutboundRuntimeInstanceID, config.OutboundRuntimeInternalAddress,
		continuationRecovery, recoveryWorker, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("outbound runtime lifecycle: %w", err)
	}
	aapRuns, err := aap.NewRunService(
		chatService, runRepository, protocolRunLifecycle, protocolEvents,
		aapRunDispatcher{runtime: agentRuntime},
	)
	if err != nil {
		return nil, err
	}
	if err := aapRuns.ConfigureCommandReceipts(aapCommandReceipts); err != nil {
		return nil, err
	}
	// REQUEST_PASSTHROUGH: attach under RootScopeAgentRun at createRun (same vault
	// as tool test / direct invoke / trial). Inject at tool invoke after confirmation.
	if vaultErr == nil && outboundVault != nil {
		if runAttacher, attErr := outboundidentity.NewBindingAttacher(outboundVault, nil); attErr == nil {
			if agentOutbound, loadErr := aap.NewDBAgentOutboundLoader(db); loadErr == nil {
				_ = aapRuns.ConfigureOutbound(runAttacher, agentOutbound, outboundVault.BootID())
			}
		}
	}
	aapRunCancellations, err := aap.NewRunCancellationService(
		runRepository, protocolRunLifecycle, agentRuntime,
	)
	if err != nil {
		return nil, err
	}
	if err := aapRunCancellations.ConfigureCommandReceipts(aapCommandReceipts); err != nil {
		return nil, err
	}
	aapRunRoutes, err := httptransport.NewAAPRunRoutes(
		agentAccessAuthorizer, aapConversations, aapRuns, runRepository,
		protocolRunItems, aapEventAttacher, aapRunCancellations,
	)
	if err != nil {
		return nil, err
	}
	// Fail closed at transport when outboundCredentials present only if RunService
	// was configured for vault attach.
	if vaultErr == nil && outboundVault != nil {
		aapRunRoutes.ConfigureOutboundAttach()
	}
	if err := aapRunRoutes.ConfigureInteractionDecisions(aapInteractionDecisions); err != nil {
		return nil, err
	}
	if err := aapRunRoutes.ConfigureCommandQuota(aapCommandQuota); err != nil {
		return nil, err
	}
	chatMessenger, err := chatruntime.NewMessenger(chatService, agentRuntime)
	if err != nil {
		return nil, err
	}
	// Chat Confirm shares the same Claim/Renew/Complete lease as AAP approval
	// and Recovery Worker so multi-replica cannot double-continue (PR16: eino).
	chatConfirmationAPI := &chatConfirmationContinue{
		inner: chatConfirmations, eino: einoRuntime,
		recovery: continuationRecovery, checkpoints: einoCheckpoints,
	}
	// 运行调试台 one-shot attach store (checklist #11).
	var debugAttach *outboundidentity.DebugAttachmentStore
	var debugVault outboundidentity.CredentialVault
	debugBoot := ""
	if vaultErr == nil && outboundVault != nil {
		hmacKey := sha256.Sum256([]byte("outbound-debug-attach:" + config.JWTSecret))
		if store, storeErr := outboundidentity.NewDebugAttachmentStore(
			outboundVault, hmacKey[:], nil,
		); storeErr == nil {
			debugAttach = store
			debugVault = outboundVault
			debugBoot = outboundVault.BootID()
		}
	}
	chatRoutes, err := httptransport.NewChatExecutionRoutes(httptransport.ChatExecutionDependencies{
		Authorizer: authorizer, Chats: chatRepository, Messages: chatMessenger,
		Content: chatObjects, Runs: runRepository, ProtocolEvents: protocolEvents,
		EventFollower: eventFollower, StreamPolicy: &streamPolicy,
		StreamLimiter: streamLimiter, StreamRevalidator: streamRevalidator,
		Confirmations:    chatConfirmationAPI,
		DebugAttachments: debugAttach,
		Vault:            debugVault,
		BootID:           debugBoot,
	})
	if err != nil {
		return nil, err
	}

	auditQueries, err := audit.NewQueryService(db, secureObjects)
	if err != nil {
		return nil, err
	}
	auditExports, err := audit.NewExportService(db, auditQueries, auditRecorder, secureObjects)
	if err != nil {
		return nil, err
	}
	auditRoutes, err := httptransport.NewAuditRoutes(authorizer, auditQueries, auditExports)
	if err != nil {
		return nil, err
	}
	agentAuditService, err := agentaudit.NewService(db, config.AgentAuditDebug)
	if err != nil {
		return nil, err
	}
	agentAuditRoutes, err := httptransport.NewAgentAuditRoutes(agentAuditService)
	if err != nil {
		return nil, err
	}
	overviewMetrics, err := workspaceoverview.NewService(db)
	if err != nil {
		return nil, err
	}
	overviewRoutes, err := httptransport.NewWorkspaceOverviewRoutes(overviewMetrics)
	if err != nil {
		return nil, err
	}

	// Exact CORS backed by per-Client allowedCorsOrigins (no global Origin union).
	// Authenticated responses reflect Origin only for the token azp; workspace
	// preflight is scoped to that workspace's ACTIVE clients.
	corsMatcher, err := agentaccessauth.NewCachedExactOriginMatcher(
		agentAccessRepository, 30*time.Second,
	)
	if err != nil {
		return nil, err
	}
	aapCORS := agentaccessauth.CORSPolicy{
		Mode: agentaccessauth.CORSModeExact, Matcher: corsMatcher, ClientMatcher: corsMatcher,
	}

	// Outbound Subject Assertion JWKS + issuer (T1=A). Optional so existing tests
	// without outbound keys keep working; production main always supplies keys.
	var outboundJWKS *httptransport.OutboundIdentityJWKSRoutes
	if config.OutboundIdentitySigningKeys != nil {
		outboundJWKS, err = httptransport.NewOutboundIdentityJWKSRoutes(config.OutboundIdentitySigningKeys)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(config.OutboundIdentityIssuer) != "" {
			if _, err := outboundidentity.NewAssertionIssuer(
				config.OutboundIdentitySigningKeys, config.OutboundIdentityIssuer, nil,
			); err != nil {
				return nil, fmt.Errorf("outbound assertion issuer: %w", err)
			}
		}
	}

	feature := config.AgentAccessFeature.Normalized()
	handler, err := httptransport.NewRouter(httptransport.Config{
		Authenticator: authenticator, AgentAccessAuthenticator: agentAccessTokenVerifier,
		Registrars: []httptransport.V1RouteRegistrar{
			authRoutes, workspaceRoutes, agentAccessRoutes, configurationRoutes, agentRoutes,
			toolRoutes, workflowRoutes, generateSessionRoutes, chatRoutes, auditRoutes, agentAuditRoutes,
			overviewRoutes,
		},
		AgentAccessRegistrars: []httptransport.AgentAccessV1RouteRegistrar{
			agentAccessJWKSRoutes, agentAccessTokenRoutes, aapAgentProfileRoutes,
			aapConversationRoutes, aapRunRoutes,
		},
		OutboundIdentityJWKS: outboundJWKS,
		AAPCORS:              aapCORS,
		MetricsBearerToken:   config.MetricsBearerToken,
		AAPFeature:           &feature,
	})
	if err != nil {
		return nil, err
	}
	purgeConfig := config.PreviewPurge
	if purgeConfig.Interval <= 0 || purgeConfig.BatchLimit <= 0 || purgeConfig.ClaimLease <= 0 {
		purgeConfig = agent.DefaultPreviewPurgeConfig()
	}
	previewPurgeWorker, err := agent.NewPreviewPurgeWorker(db, objectStore, purgeConfig, nil)
	if err != nil {
		return nil, err
	}

	// Start after full wiring succeeds so partial Open failures do not leak loops.
	recoveryWorker.Start(context.WithoutCancel(ctx))
	previewPurgeWorker.Start(context.WithoutCancel(ctx))
	return &Application{
		db: db, handler: handler, eventNotifier: liveEvents,
		securityChanges: securityChanges, clientSecretAuthenticator: clientSecretAuthenticator,
		privateKeyJWTAuthenticator: privateKeyJWTAuthenticator,
		agentAccessAuthorizer:      agentAccessAuthorizer,
		securityVersionCache:       securityVersionCache,
		recoveryWorker:             recoveryWorker,
		previewPurgeWorker:         previewPurgeWorker,
		outboundRuntime:            outboundRuntime,
	}, nil
}
