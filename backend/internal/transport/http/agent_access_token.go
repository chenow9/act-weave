package httptransport

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"runtime"
	"strings"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/metrics"

	"github.com/gin-gonic/gin"
)

const (
	clientAssertionTypeJWTBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	maximumOAuthTokenFormBytes   = 32 * 1024
)

type clientSecretTokenAuthenticator interface {
	AuthenticateBasic(
		context.Context,
		agentaccessauth.ClientSecretAuthenticationRequest,
	) (agentaccessauth.AuthenticatedClient, error)
}

type privateKeyJWTTokenAuthenticator interface {
	Authenticate(
		context.Context,
		agentaccessauth.PrivateKeyJWTAuthenticationRequest,
	) (agentaccessauth.AuthenticatedClient, error)
}

type clientCredentialsTokenIssuer interface {
	IssueClientCredentialsToken(
		context.Context,
		agentaccessauth.ClientCredentialsTokenRequest,
	) (agentaccessauth.ClientCredentialsToken, error)
}

type tokenExchangeIssuer interface {
	IssueTokenExchange(
		context.Context,
		agentaccessauth.TokenExchangeRequest,
	) (agentaccessauth.TokenExchangeToken, error)
}

type AgentAccessTokenRoutes struct {
	clientSecrets  clientSecretTokenAuthenticator
	privateKeys    privateKeyJWTTokenAuthenticator
	tokens         clientCredentialsTokenIssuer
	tokenExchange  tokenExchangeIssuer
	issueLimiter   agentaccessauth.TokenEndpointLimiter
}

func NewAgentAccessTokenRoutes(
	clientSecrets clientSecretTokenAuthenticator,
	privateKeys privateKeyJWTTokenAuthenticator,
	tokens clientCredentialsTokenIssuer,
	tokenExchange tokenExchangeIssuer,
) (*AgentAccessTokenRoutes, error) {
	if clientSecrets == nil || privateKeys == nil || tokens == nil || tokenExchange == nil {
		return nil, errors.New("Agent Access Client authenticators and Token Services are required")
	}
	return &AgentAccessTokenRoutes{
		clientSecrets: clientSecrets, privateKeys: privateKeys, tokens: tokens,
		tokenExchange: tokenExchange,
	}, nil
}

// ConfigureTokenIssueLimiter attaches multi-dimensional token endpoint rate limits.
// Optional: nil keeps unlimited issuance (tests may inject a tight limiter).
func (routes *AgentAccessTokenRoutes) ConfigureTokenIssueLimiter(limiter agentaccessauth.TokenEndpointLimiter) error {
	if routes == nil {
		return errors.New("Agent Access Token routes are required")
	}
	routes.issueLimiter = limiter
	return nil
}

func (routes *AgentAccessTokenRoutes) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	v1.Public.POST("/oauth/token", routes.issueToken)
}

type oauthTokenSuccess struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type,omitempty"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
	Scope           string `json:"scope"`
}

type oauthTokenFailure struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (routes *AgentAccessTokenRoutes) issueToken(c *gin.Context) {
	setOAuthNoStoreHeaders(c)
	form, err := parseOAuthTokenForm(c)
	if err != nil {
		metrics.Default().ObserveTokenIssue(false, map[string]string{"reason": "invalid_request"})
		respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_request", "The token request is not valid.", false)
		return
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	client, err := routes.authenticateClient(c, form, requestContext)
	if err != nil {
		if errors.Is(err, agentaccessauth.ErrClientAuthenticationUnavailable) {
			metrics.Default().ObserveTokenIssue(false, map[string]string{"reason": "temporarily_unavailable"})
			respondOAuthTokenError(c, err, http.StatusServiceUnavailable, "temporarily_unavailable", "The authorization server is temporarily unavailable.", false)
			return
		}
		metrics.Default().ObserveTokenIssue(false, map[string]string{"reason": "invalid_client"})
		respondOAuthTokenError(c, agentaccessauth.ErrInvalidClient, http.StatusUnauthorized, "invalid_client", "Client authentication failed.", basicAuthorizationPresented(c.GetHeader("Authorization")))
		return
	}
	grantType, ok := oneOAuthFormValue(form, "grant_type")
	if !ok || grantType == "" {
		metrics.Default().ObserveTokenIssue(false, map[string]string{
			"client_id": client.PublicClientID, "reason": "invalid_request",
		})
		respondOAuthTokenError(c, agentaccessauth.ErrClientCredentialsRequestInvalid, http.StatusBadRequest, "invalid_request", "The grant_type parameter is required.", false)
		return
	}
	if !routes.allowTokenIssue(c, client, grantType) {
		metrics.Default().ObserveTokenIssue(false, map[string]string{
			"client_id": client.PublicClientID, "reason": "rate_limited",
		})
		return
	}
	switch grantType {
	case "client_credentials":
		routes.issueClientCredentials(c, form, client)
	case agentaccessauth.TokenExchangeGrantType:
		routes.issueTokenExchange(c, form, client)
	default:
		metrics.Default().ObserveTokenIssue(false, map[string]string{
			"client_id": client.PublicClientID, "reason": "unsupported_grant_type",
		})
		respondOAuthTokenError(c, agentaccessauth.ErrClientCredentialsRequestInvalid, http.StatusBadRequest, "unsupported_grant_type", "The grant type is not supported.", false)
	}
}

func (routes *AgentAccessTokenRoutes) allowTokenIssue(
	c *gin.Context,
	client agentaccessauth.AuthenticatedClient,
	grantType string,
) bool {
	if routes.issueLimiter == nil {
		return true
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	decision, err := routes.issueLimiter.AllowTokenIssue(c.Request.Context(), agentaccessauth.TokenIssueAttempt{
		PublicClientID: client.PublicClientID,
		RemoteIP:       requestContext.SourceIP,
		GrantType:      grantType,
	})
	writeTokenIssueRateLimitHeaders(c, decision)
	if err != nil {
		// Rate limit must not leak resource identifiers in the OAuth error body.
		respondOAuthTokenError(c, err, http.StatusTooManyRequests, "temporarily_unavailable", "The request rate limit was exceeded.", false)
		return false
	}
	return true
}

func (routes *AgentAccessTokenRoutes) issueClientCredentials(
	c *gin.Context,
	form map[string][]string,
	client agentaccessauth.AuthenticatedClient,
) {
	if _, hasSubjectToken := form["subject_token"]; hasSubjectToken {
		respondOAuthTokenError(c, agentaccessauth.ErrClientCredentialsRequestInvalid, http.StatusBadRequest, "invalid_request", "subject_token is not valid for client_credentials.", false)
		return
	}
	agentID, ok := oneOAuthFormValue(form, "agent_id")
	if !ok || agentID == "" {
		respondOAuthTokenError(c, agentaccessauth.ErrClientCredentialsRequestInvalid, http.StatusBadRequest, "invalid_request", "The agent_id parameter is required.", false)
		return
	}
	scope, ok := oneOAuthFormValue(form, "scope")
	requestedScopes, scopeOK := parseOAuthScope(scope)
	if !ok || !scopeOK {
		respondOAuthTokenError(c, agentaccessauth.ErrClientCredentialsScopeInvalid, http.StatusBadRequest, "invalid_scope", "The requested scope is invalid.", false)
		return
	}
	issued, err := routes.tokens.IssueClientCredentialsToken(c.Request.Context(), agentaccessauth.ClientCredentialsTokenRequest{
		Client: client, AgentID: agentID, RequestedScopes: requestedScopes,
	})
	if err != nil {
		reason := "temporarily_unavailable"
		switch {
		case errors.Is(err, agentaccessauth.ErrClientCredentialsScopeInvalid):
			reason = "invalid_scope"
			respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_scope", "The requested scope is invalid.", false)
		case errors.Is(err, agentaccessauth.ErrClientCredentialsTargetInvalid),
			errors.Is(err, agentaccessauth.ErrClientCredentialsRequestInvalid):
			reason = "invalid_target"
			respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_target", "The requested Agent is not available to this Client.", false)
		default:
			respondOAuthTokenError(c, err, http.StatusServiceUnavailable, "temporarily_unavailable", "The authorization server is temporarily unavailable.", false)
		}
		metrics.Default().ObserveTokenIssue(false, map[string]string{
			"client_id": client.PublicClientID, "reason": reason,
		})
		return
	}
	metrics.Default().ObserveTokenIssue(true, map[string]string{
		"client_id": client.PublicClientID, "operation": "client_credentials",
	})
	c.JSON(http.StatusOK, oauthTokenSuccess{
		AccessToken: issued.AccessToken, TokenType: issued.TokenType,
		ExpiresIn: issued.ExpiresIn, Scope: issued.Scope,
	})
}

func (routes *AgentAccessTokenRoutes) issueTokenExchange(
	c *gin.Context,
	form map[string][]string,
	client agentaccessauth.AuthenticatedClient,
) {
	agentID, ok := oneOAuthFormValue(form, "agent_id")
	if !ok || agentID == "" {
		respondOAuthTokenError(c, agentaccessauth.ErrTokenExchangeRequestInvalid, http.StatusBadRequest, "invalid_request", "The agent_id parameter is required.", false)
		return
	}
	subjectToken, ok := oneOAuthFormValue(form, "subject_token")
	if !ok || subjectToken == "" {
		respondOAuthTokenError(c, agentaccessauth.ErrTokenExchangeRequestInvalid, http.StatusBadRequest, "invalid_request", "The subject_token parameter is required.", false)
		return
	}
	subjectTokenType, ok := oneOAuthFormValue(form, "subject_token_type")
	if !ok || subjectTokenType == "" {
		respondOAuthTokenError(c, agentaccessauth.ErrTokenExchangeRequestInvalid, http.StatusBadRequest, "invalid_request", "The subject_token_type parameter is required.", false)
		return
	}
	requestedTokenType, _ := oneOAuthFormValue(form, "requested_token_type")
	scope, ok := oneOAuthFormValue(form, "scope")
	requestedScopes, scopeOK := parseOAuthScope(scope)
	if !ok || !scopeOK {
		respondOAuthTokenError(c, agentaccessauth.ErrTokenExchangeScopeInvalid, http.StatusBadRequest, "invalid_scope", "The requested scope is invalid.", false)
		return
	}
	issued, err := routes.tokenExchange.IssueTokenExchange(c.Request.Context(), agentaccessauth.TokenExchangeRequest{
		Client: client, AgentID: agentID, RequestedScopes: requestedScopes,
		SubjectToken: subjectToken, SubjectTokenType: subjectTokenType,
		RequestedTokenType: requestedTokenType,
	})
	if err != nil {
		reason := "temporarily_unavailable"
		switch {
		case errors.Is(err, agentaccessauth.ErrTokenExchangeScopeInvalid):
			reason = "invalid_scope"
			respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_scope", "The requested scope is invalid.", false)
		case errors.Is(err, agentaccessauth.ErrTokenExchangeSubjectInvalid),
			errors.Is(err, agentaccessauth.ErrTokenExchangeReplay):
			reason = "invalid_grant"
			respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_grant", "The subject token is invalid.", false)
		case errors.Is(err, agentaccessauth.ErrTokenExchangeSubjectDenied),
			errors.Is(err, agentaccessauth.ErrTokenExchangeTrustMissing):
			reason = "invalid_grant"
			respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_grant", "The subject token is not accepted for this Client.", false)
		case errors.Is(err, agentaccessauth.ErrTokenExchangeTargetInvalid),
			errors.Is(err, agentaccessauth.ErrTokenExchangeRequestInvalid):
			reason = "invalid_request"
			respondOAuthTokenError(c, err, http.StatusBadRequest, "invalid_request", "The token exchange request is not valid.", false)
		default:
			respondOAuthTokenError(c, err, http.StatusServiceUnavailable, "temporarily_unavailable", "The authorization server is temporarily unavailable.", false)
		}
		metrics.Default().ObserveTokenIssue(false, map[string]string{
			"client_id": client.PublicClientID, "reason": reason,
		})
		return
	}
	metrics.Default().ObserveTokenIssue(true, map[string]string{
		"client_id": client.PublicClientID, "operation": "token_exchange",
	})
	c.JSON(http.StatusOK, oauthTokenSuccess{
		AccessToken: issued.AccessToken, IssuedTokenType: issued.IssuedTokenType,
		TokenType: issued.TokenType, ExpiresIn: issued.ExpiresIn, Scope: issued.Scope,
	})
}

func (routes *AgentAccessTokenRoutes) authenticateClient(
	c *gin.Context,
	form map[string][]string,
	request RequestContext,
) (agentaccessauth.AuthenticatedClient, error) {
	authorizationValues := c.Request.Header.Values("Authorization")
	if len(authorizationValues) > 1 {
		return agentaccessauth.AuthenticatedClient{}, agentaccessauth.ErrInvalidClient
	}
	authorization := ""
	if len(authorizationValues) == 1 {
		authorization = strings.TrimSpace(authorizationValues[0])
	}
	_, assertionPresent := form["client_assertion"]
	_, assertionTypePresent := form["client_assertion_type"]
	if authorization != "" {
		if assertionPresent || assertionTypePresent {
			return agentaccessauth.AuthenticatedClient{}, agentaccessauth.ErrInvalidClient
		}
		return routes.clientSecrets.AuthenticateBasic(c.Request.Context(), agentaccessauth.ClientSecretAuthenticationRequest{
			Authorization: authorization, SourceIP: request.SourceIP, UserAgent: request.UserAgent,
		})
	}
	assertion, assertionOK := oneOAuthFormValue(form, "client_assertion")
	assertionType, assertionTypeOK := oneOAuthFormValue(form, "client_assertion_type")
	if !assertionOK || !assertionTypeOK || assertion == "" || assertionType != clientAssertionTypeJWTBearer {
		return agentaccessauth.AuthenticatedClient{}, agentaccessauth.ErrInvalidClient
	}
	return routes.privateKeys.Authenticate(c.Request.Context(), agentaccessauth.PrivateKeyJWTAuthenticationRequest{
		ClientAssertion: assertion, SourceIP: request.SourceIP, UserAgent: request.UserAgent,
	})
}

func parseOAuthTokenForm(c *gin.Context) (map[string][]string, error) {
	if c.Request.URL.RawQuery != "" {
		return nil, agentaccessauth.ErrClientCredentialsRequestInvalid
	}
	mediaType, parameters, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" ||
		(len(parameters) != 0 && !(len(parameters) == 1 && strings.EqualFold(parameters["charset"], "UTF-8"))) {
		return nil, agentaccessauth.ErrClientCredentialsRequestInvalid
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maximumOAuthTokenFormBytes)
	if err := c.Request.ParseForm(); err != nil {
		return nil, agentaccessauth.ErrClientCredentialsRequestInvalid
	}
	allowed := map[string]struct{}{
		"grant_type": {}, "agent_id": {}, "scope": {},
		"client_assertion_type": {}, "client_assertion": {},
		"subject_token": {}, "subject_token_type": {}, "requested_token_type": {},
	}
	form := make(map[string][]string, len(c.Request.PostForm))
	for key, values := range c.Request.PostForm {
		if _, ok := allowed[key]; !ok || len(values) != 1 {
			return nil, agentaccessauth.ErrClientCredentialsRequestInvalid
		}
		form[key] = append([]string(nil), values...)
	}
	return form, nil
}

func oneOAuthFormValue(form map[string][]string, key string) (string, bool) {
	values, exists := form[key]
	if !exists || len(values) != 1 {
		return "", false
	}
	return values[0], true
}

func parseOAuthScope(value string) ([]string, bool) {
	if value == "" || len(value) > 1024 || strings.TrimSpace(value) != value ||
		strings.ContainsAny(value, "\t\r\n") || strings.Contains(value, "  ") {
		return nil, false
	}
	values := strings.Split(value, " ")
	for _, scope := range values {
		if scope == "" {
			return nil, false
		}
	}
	return values, true
}

func setOAuthNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

func basicAuthorizationPresented(value string) bool {
	parts := strings.Fields(strings.TrimSpace(value))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Basic")
}

func respondOAuthTokenError(
	c *gin.Context,
	err error,
	status int,
	code, description string,
	basicChallenge bool,
) {
	if basicChallenge {
		c.Header("WWW-Authenticate", `Basic realm="ActWeave Agent Access Token Endpoint"`)
	}
	_, file, line, _ := runtime.Caller(1)
	c.Set(requestFailureKey, requestFailure{
		err: err, mapped: mappedError{status: status, code: code, message: description},
		file: file, line: line,
	})
	c.AbortWithStatusJSON(status, oauthTokenFailure{Error: code, ErrorDescription: description})
}
