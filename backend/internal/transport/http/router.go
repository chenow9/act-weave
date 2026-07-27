package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/config"
	"actweave/backend/internal/protocolschema"

	"github.com/gin-gonic/gin"
)

type TokenAuthenticator interface {
	AuthenticateAccessToken(context.Context, string) (Principal, error)
}

type AAPTokenAuthenticator interface {
	VerifyAccessToken(context.Context, string) (agentaccessauth.AAPAccessTokenPrincipal, error)
}

func decodeJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

// AccessTokenAuthenticator adapts authn.Service into the Console HTTP
// TokenAuthenticator. Every protected request revalidates session/user/credential
// state through the service; JWT claim username/role are never used for authorization.
type AccessTokenAuthenticator struct {
	service *authn.Service
}

func NewAccessTokenAuthenticator(service *authn.Service) (*AccessTokenAuthenticator, error) {
	if service == nil {
		return nil, errors.New("authentication service is required")
	}
	return &AccessTokenAuthenticator{service: service}, nil
}

func (authenticator *AccessTokenAuthenticator) AuthenticateAccessToken(
	ctx context.Context,
	value string,
) (Principal, error) {
	identity, err := authenticator.service.AuthenticateAccessToken(ctx, value)
	if err != nil {
		return Principal{}, err
	}
	if identity.UserID == "" || identity.SessionID == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{
		UserID:             identity.UserID,
		SessionID:          identity.SessionID,
		Username:           identity.Username,
		PlatformRole:       string(identity.PlatformRole),
		MustChangePassword: identity.MustChangePassword,
		TokenExpiresAt:     identity.TokenExpiresAt.UTC(),
	}, nil
}

type V1Routes struct {
	Public    *gin.RouterGroup
	Protected *gin.RouterGroup
}

type V1RouteRegistrar interface {
	RegisterV1(V1Routes)
}

type AgentAccessV1Routes struct {
	Public    *gin.RouterGroup
	Protected *gin.RouterGroup
}

type AgentAccessV1RouteRegistrar interface {
	RegisterAgentAccessV1(AgentAccessV1Routes)
}

type Config struct {
	Authenticator            TokenAuthenticator
	AgentAccessAuthenticator AAPTokenAuthenticator
	Registrars               []V1RouteRegistrar
	AgentAccessRegistrars    []AgentAccessV1RouteRegistrar
	// OutboundIdentityJWKS registers the public Subject Assertion JWKS at
	// GET /api/outbound-identity/v1/.well-known/jwks.json (separate from AAP).
	// Nil skips registration (tests that do not exercise Broker/OBO).
	OutboundIdentityJWKS *OutboundIdentityJWKSRoutes
	// AAPCORS controls browser access to the data plane. Default is disabled
	// (BFF-safe). Use agentaccessauth.NewExactCORSPolicy for direct short-token.
	AAPCORS agentaccessauth.CORSPolicy
	// MetricsBearerToken protects GET /metrics. Empty = loopback-only access.
	// Non-empty requires Authorization: Bearer <token>.
	MetricsBearerToken string
	// AAPFeature gates the public AAP surface (M10-T8).
	// nil = unconstrained (existing unit tests / fully open wiring).
	// Non-nil with Enabled=false closes /api/agent-access/v1 (no public surface).
	AAPFeature *config.AAPFeatureRollout
	Logger     *slog.Logger
}

func NewRouter(config Config) (http.Handler, error) {
	for _, registrar := range config.Registrars {
		if registrar == nil {
			return nil, errors.New("v1 route registrar is required")
		}
	}
	for _, registrar := range config.AgentAccessRegistrars {
		if registrar == nil {
			return nil, errors.New("Agent Access v1 route registrar is required")
		}
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	router.Use(requestContextMiddleware(), requestLoggingMiddleware(logger), recoveryMiddleware(logger))
	// Process-local Prometheus text exposition. Protected by bearer token when
	// configured; otherwise restricted to loopback (no open internet scrape).
	router.GET("/metrics", aapMetricsAuthMiddleware(config.MetricsBearerToken), aapMetricsHandler)
	// Outbound Subject Assertion JWKS — public, fixed path, separate from AAP JWKS.
	if config.OutboundIdentityJWKS != nil {
		config.OutboundIdentityJWKS.Register(router)
	}
	v1 := router.Group("/api/v1")
	v1.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	protected := v1.Group("")
	protected.Use(
		authenticationMiddleware(config.Authenticator),
		mustChangePasswordMiddleware(),
	)
	routes := V1Routes{Public: v1, Protected: protected}
	for _, registrar := range config.Registrars {
		registrar.RegisterV1(routes)
	}
	// Default CORS is disabled (BFF). Zero-value Mode is also treated as disabled.
	// Applied at the engine level so OPTIONS preflight hits CORS before route match.
	corsPolicy := config.AAPCORS
	if corsPolicy.Mode == "" {
		corsPolicy = agentaccessauth.NewDisabledCORSPolicy()
	}
	router.Use(aapCORSPathMiddleware(corsPolicy))
	// Feature gate: when AAPFeature is non-nil and disabled, do not register
	// public AAP routes (NoRoute returns RESOURCE_NOT_FOUND for /api/agent-access/*).
	if config.AAPFeature == nil || config.AAPFeature.PublicSurfaceOpen() {
		feature := config.AAPFeature
		if feature != nil {
			normalized := feature.Normalized()
			feature = &normalized
		}
		agentAccessPublic := router.Group("/api/agent-access/v1")
		agentAccessPublic.Use(aapFeatureSurfaceMiddleware(feature))
		agentAccessProtected := router.Group("/api/agent-access/v1")
		agentAccessProtected.Use(
			aapFeatureSurfaceMiddleware(feature),
			aapProtocolMiddleware(),
			aapAuthenticationMiddleware(config.AgentAccessAuthenticator),
			aapFeaturePrincipalMiddleware(feature),
			// Client-isolated CORS after auth, before handlers write the body.
			aapClientCORSMiddleware(corsPolicy),
		)
		agentAccessV1 := AgentAccessV1Routes{
			Public: agentAccessPublic, Protected: agentAccessProtected,
		}
		for _, registrar := range config.AgentAccessRegistrars {
			registrar.RegisterAgentAccessV1(agentAccessV1)
		}
	}
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/agent-access/") {
			c.Header(AAPProtocolVersionHeader, protocolschema.ProtocolVersion)
			appendVary(c.Writer.Header(), AAPProtocolVersionHeader)
			if !strings.HasPrefix(c.Request.URL.Path, "/api/agent-access/v1/") &&
				c.Request.URL.Path != "/api/agent-access/v1" {
				RespondError(c, ErrAAPProtocolVersionUnsupported)
				return
			}
			RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
			return
		}
		c.Status(http.StatusNotFound)
	})
	return colonCommandAdapter(router), nil
}

func colonCommandAdapter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		lastSlash := strings.LastIndex(path, "/")
		if colon := strings.LastIndex(path[lastSlash+1:], ":"); colon > 0 {
			colon += lastSlash + 1
			resource, command := path[:colon], path[colon+1:]
			if stableCommand(command) {
				clone := request.Clone(request.Context())
				urlCopy := *request.URL
				urlCopy.Path, urlCopy.RawPath = resource+"/__command/"+command, ""
				clone.URL = &urlCopy
				request = clone
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func stableCommand(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && character != '-' {
			return false
		}
	}
	return true
}

func requestLoggingMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		c.Next()

		request, _ := RequestContextFrom(c.Request.Context())
		attrs := []any{
			"event", "http.request.completed",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"response_bytes", c.Writer.Size(),
			"request_id", request.RequestID,
			"trace_id", request.TraceID,
		}
		if request.SourceIP != "" {
			attrs = append(attrs, "client_ip", request.SourceIP)
		}
		if principal, ok := PrincipalFrom(c.Request.Context()); ok {
			attrs = append(attrs, "user_id", principal.UserID)
		}
		if principal, ok := AAPPrincipalFrom(c.Request.Context()); ok {
			attrs = append(attrs,
				"service_principal_id", principal.ServicePrincipalID,
				"authorized_party", principal.AuthorizedParty,
				"workspace_id", principal.WorkspaceID,
				"agent_id", principal.AgentID,
			)
		}
		if value, exists := c.Get(requestFailureKey); exists {
			if failure, ok := value.(requestFailure); ok {
				// HIGH-02 (ZKL-63): never log raw err.Error(); only stable fields.
				attrs = append(attrs,
					"error_code", failure.mapped.code,
					"error_type", failure.errorType,
					"error_source", fmt.Sprintf("%s:%d", failure.file, failure.line),
				)
			}
		}

		status := c.Writer.Status()
		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("HTTP request failed", attrs...)
		case status >= http.StatusBadRequest:
			logger.Warn("HTTP request rejected", attrs...)
		default:
			logger.Info("HTTP request completed", attrs...)
		}
	}
}

func recoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				request, _ := RequestContextFrom(c.Request.Context())
				logger.Error("HTTP handler panic recovered",
					"event", "http.request.panic",
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"route", c.FullPath(),
					"request_id", request.RequestID,
					"trace_id", request.TraceID,
					"panic", fmt.Sprint(recovered),
					"stack", string(debug.Stack()),
				)
				RespondError(c, errors.New("HTTP handler panic"))
			}
		}()
		c.Next()
	}
}

func authenticationMiddleware(authenticator TokenAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := c.Request.Header.Values("Authorization")
		value := ""
		if len(values) == 1 {
			value = strings.TrimSpace(values[0])
		}
		parts := strings.Fields(value)
		if authenticator == nil || len(values) != 1 || len(parts) != 2 ||
			!strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			RespondError(c, ErrUnauthenticated)
			return
		}
		principal, err := authenticator.AuthenticateAccessToken(c.Request.Context(), parts[1])
		if err != nil {
			// D2=A: infrastructure failures are 503; all other auth failures stay 401
			// without exposing revoke/disable/lock/missing distinctions.
			if errors.Is(err, authn.ErrAuthenticationUnavailable) {
				RespondError(c, err)
				return
			}
			RespondError(c, ErrUnauthenticated)
			return
		}
		if principal.UserID == "" || principal.SessionID == "" {
			RespondError(c, ErrUnauthenticated)
			return
		}
		ctx := context.WithValue(c.Request.Context(), principalContextKey{}, principal)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// mustChangePasswordMiddleware enforces HIGH-03: when MustChangePassword is true,
// only an exact method+registered-template allowlist may proceed. Matching uses
// Gin FullPath() (registered template), never path prefixes or substrings.
func mustChangePasswordMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := PrincipalFrom(c.Request.Context())
		if !ok || !principal.MustChangePassword {
			c.Next()
			return
		}
		if mustChangePasswordAllowed(c.Request.Method, c.FullPath()) {
			c.Next()
			return
		}
		RespondError(c, ErrPasswordChangeRequired)
	}
}

func mustChangePasswordAllowed(method, fullPath string) bool {
	switch {
	case method == http.MethodPost && fullPath == "/api/v1/users/me/__command/change-password":
		return true
	case method == http.MethodPost && fullPath == "/api/v1/auth/logout":
		return true
	case method == http.MethodGet && fullPath == "/api/v1/users/me":
		return true
	default:
		return false
	}
}

// aapFeatureSurfaceMiddleware enforces the global AAP feature master switch.
// nil feature means unconstrained (tests / open deployments).
func aapFeatureSurfaceMiddleware(feature *config.AAPFeatureRollout) gin.HandlerFunc {
	return func(c *gin.Context) {
		if feature != nil && !feature.PublicSurfaceOpen() {
			RespondError(c, ErrAAPFeatureDisabled)
			return
		}
		c.Next()
	}
}

// aapFeaturePrincipalMiddleware enforces Workspace/Client allowlists after auth.
func aapFeaturePrincipalMiddleware(feature *config.AAPFeatureRollout) gin.HandlerFunc {
	return func(c *gin.Context) {
		if feature == nil || !feature.PublicSurfaceOpen() {
			c.Next()
			return
		}
		principal, ok := AAPPrincipalFrom(c.Request.Context())
		if !ok {
			RespondError(c, ErrUnauthenticated)
			return
		}
		if !feature.AllowsClient(principal.AuthorizedParty) {
			RespondError(c, ErrAAPFeatureNotEnabledForClient)
			return
		}
		if !feature.AllowsWorkspace(principal.WorkspaceID) {
			RespondError(c, ErrAAPFeatureNotEnabledForWorkspace)
			return
		}
		// Path workspace must match allowlist (and usually the token workspace).
		if wid := strings.TrimSpace(c.Param("wid")); wid != "" && !feature.AllowsWorkspace(wid) {
			RespondError(c, ErrAAPFeatureNotEnabledForWorkspace)
			return
		}
		c.Next()
	}
}

func aapAuthenticationMiddleware(authenticator AAPTokenAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := c.Request.Header.Values("Authorization")
		value := ""
		if len(values) == 1 {
			value = strings.TrimSpace(values[0])
		}
		parts := strings.Fields(value)
		if authenticator == nil || len(values) != 1 || len(parts) != 2 ||
			!strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			RespondError(c, ErrUnauthenticated)
			return
		}
		principal, err := authenticator.VerifyAccessToken(c.Request.Context(), parts[1])
		if err != nil {
			if errors.Is(err, agentaccessauth.ErrTokenExpired) {
				RespondError(c, agentaccessauth.ErrTokenExpired)
				return
			}
			RespondError(c, ErrUnauthenticated)
			return
		}
		if principal.PrincipalID == "" || principal.ServicePrincipalID == "" ||
			principal.AuthorizedParty == "" || principal.WorkspaceID == "" ||
			principal.AgentID == "" || principal.SecurityVersion < 1 ||
			principal.TokenID == "" || principal.ExpiresAt.IsZero() || len(principal.Scopes) == 0 {
			RespondError(c, ErrUnauthenticated)
			return
		}
		ctx := context.WithValue(c.Request.Context(), aapPrincipalContextKey{}, principal)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
