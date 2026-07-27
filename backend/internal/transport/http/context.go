package httptransport

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/audit"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	traceIDPattern   = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)
)

type RequestContext struct {
	RequestID string
	TraceID   string
	SourceIP  string
	UserAgent string
}

type Principal struct {
	UserID             string
	SessionID          string
	Username           string
	PlatformRole       string
	MustChangePassword bool
	TokenExpiresAt     time.Time
}

type requestContextKey struct{}
type principalContextKey struct{}
type aapPrincipalContextKey struct{}

func RequestContextFrom(ctx context.Context) (RequestContext, bool) {
	value, exists := ctx.Value(requestContextKey{}).(RequestContext)
	return value, exists
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	value, exists := ctx.Value(principalContextKey{}).(Principal)
	return value, exists
}

func AAPPrincipalFrom(ctx context.Context) (agentaccessauth.AAPAccessTokenPrincipal, bool) {
	value, exists := ctx.Value(aapPrincipalContextKey{}).(agentaccessauth.AAPAccessTokenPrincipal)
	if exists {
		value.Scopes = append([]string(nil), value.Scopes...)
	}
	return value, exists
}

func requestContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		value := RequestContext{
			RequestID: requestID(c.Request.Header.Get("X-Request-ID")),
			TraceID:   traceID(c.Request),
			SourceIP:  sourceIP(c.Request.RemoteAddr),
			UserAgent: strings.TrimSpace(c.Request.UserAgent()),
		}
		if value.TraceID == "" {
			value.TraceID = value.RequestID
		}
		ctx := context.WithValue(c.Request.Context(), requestContextKey{}, value)
		ctx = audit.WithRequestContext(ctx, audit.RequestContext{
			RequestID: value.RequestID, TraceID: value.TraceID,
			SourceIP: value.SourceIP, UserAgent: value.UserAgent,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Request-ID", value.RequestID)
		c.Header("X-Trace-ID", value.TraceID)
		c.Next()
	}
}

func requestID(value string) string {
	value = strings.TrimSpace(value)
	if requestIDPattern.MatchString(value) {
		return value
	}
	generated, err := uuid.NewV7()
	if err == nil {
		return generated.String()
	}
	return uuid.NewString()
}

func traceID(request *http.Request) string {
	if value := strings.TrimSpace(request.Header.Get("X-Trace-ID")); requestIDPattern.MatchString(value) {
		return value
	}
	parts := strings.Split(strings.TrimSpace(request.Header.Get("traceparent")), "-")
	if len(parts) == 4 && parts[0] == "00" && traceIDPattern.MatchString(parts[1]) &&
		parts[1] != strings.Repeat("0", 32) {
		return strings.ToLower(parts[1])
	}
	return ""
}

func sourceIP(remoteAddress string) string {
	remoteAddress = strings.TrimSpace(remoteAddress)
	if host, _, err := net.SplitHostPort(remoteAddress); err == nil {
		remoteAddress = host
	}
	ip := net.ParseIP(remoteAddress)
	if ip == nil {
		return ""
	}
	return ip.String()
}
