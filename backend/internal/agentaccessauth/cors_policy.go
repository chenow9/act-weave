package agentaccessauth

import (
	"errors"
	"net/url"
	"strings"
)

// CORSMode controls whether the AAP data plane emits CORS headers.
type CORSMode string

const (
	// CORSModeDisabled is the BFF default: browsers talk only to the BFF, so
	// AAP itself must not open browser CORS.
	CORSModeDisabled CORSMode = "disabled"
	// CORSModeExact allows only client-registered exact HTTPS origins.
	CORSModeExact CORSMode = "exact"
)

var (
	ErrCORSPolicyInvalid = errors.New("AAP CORS policy is invalid")
	ErrCORSOriginDenied  = errors.New("AAP CORS origin is not allowed")
)

// Default AAP preflight surface — keep tight; browsers must not receive *
// methods or headers.
var (
	AAPCORSAllowedMethods = []string{"GET", "POST", "OPTIONS"}
	AAPCORSAllowedHeaders = []string{
		"Accept",
		"Authorization",
		"Content-Type",
		"Idempotency-Key",
		"If-Match",
		"If-None-Match",
		"Last-Event-ID",
		// Must match AAPProtocolVersionHeader (ActWeave-Protocol-Version).
		"ActWeave-Protocol-Version",
	}
	AAPCORSExposedHeaders = []string{
		"ETag",
		"Location",
		"RateLimit-Limit",
		"RateLimit-Remaining",
		"RateLimit-Reset",
		"Retry-After",
		// Production protocol header (not the legacy X-AAP-Protocol-Version name).
		"ActWeave-Protocol-Version",
	}
)

// ExactOriginMatcher evaluates browser Origins under CORSModeExact when the
// static Origins slice is not the sole source of truth (for example, ACTIVE
// Agent Access Client allowedCorsOrigins loaded from PostgreSQL).
type ExactOriginMatcher interface {
	AllowsExactOrigin(origin string) bool
}

// ClientScopedOriginMatcher isolates CORS reflection to one Client's registered
// origins (public client_id / azp). The production cached matcher implements this.
type ClientScopedOriginMatcher interface {
	AllowsOriginForClient(origin, publicClientID string) bool
	AllowsOriginForWorkspace(origin, workspaceID string) bool
}

// CORSPolicy is the production CORS gate for /api/agent-access/v1.
// Origins are exact string matches after ValidateExactOrigins. When Matcher is
// non-nil under exact mode, it is consulted instead of the static Origins list.
// Client/Workspace isolation uses ClientMatcher when present — never a global
// Origin union for authenticated responses.
type CORSPolicy struct {
	Mode    CORSMode
	Origins []string
	Matcher ExactOriginMatcher
	// ClientMatcher is optional; when set, ReflectOriginForClient/Workspace use it.
	ClientMatcher ClientScopedOriginMatcher
}

// NewDisabledCORSPolicy returns the BFF-safe policy (no Access-Control-* headers).
func NewDisabledCORSPolicy() CORSPolicy {
	return CORSPolicy{Mode: CORSModeDisabled}
}

// NewExactCORSPolicy validates and freezes an exact-origin allowlist.
func NewExactCORSPolicy(origins []string) (CORSPolicy, error) {
	normalized, err := ValidateExactOrigins(origins)
	if err != nil {
		return CORSPolicy{}, err
	}
	return CORSPolicy{Mode: CORSModeExact, Origins: normalized}, nil
}

// ValidateExactOrigins rejects "*", wildcards, non-https (except loopback http
// for local demos), credentials-in-origin, and empty values. Production must
// only store the returned slice.
func ValidateExactOrigins(origins []string) ([]string, error) {
	if len(origins) == 0 {
		return nil, ErrCORSPolicyInvalid
	}
	seen := make(map[string]struct{}, len(origins))
	out := make([]string, 0, len(origins))
	for _, raw := range origins {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			return nil, ErrCORSPolicyInvalid
		}
		lower := strings.ToLower(origin)
		if origin == "*" || strings.Contains(origin, "*") ||
			strings.Contains(origin, "?") || strings.Contains(origin, " ") {
			return nil, ErrCORSPolicyInvalid
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
			parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" ||
			parsed.Fragment != "" || parsed.User != nil {
			return nil, ErrCORSPolicyInvalid
		}
		// Strip trailing slash for exact compare consistency.
		if parsed.Path == "/" {
			parsed.Path = ""
		}
		// Recompose without path noise.
		canonical := parsed.Scheme + "://" + parsed.Host
		if parsed.Scheme != "https" {
			host := strings.ToLower(parsed.Hostname())
			if parsed.Scheme != "http" || (host != "localhost" && host != "127.0.0.1") {
				return nil, ErrCORSPolicyInvalid
			}
		}
		// Reject "null" origin token and scheme-relative forms.
		if lower == "null" || strings.HasPrefix(origin, "//") {
			return nil, ErrCORSPolicyInvalid
		}
		if _, dup := seen[canonical]; dup {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	if len(out) == 0 {
		return nil, ErrCORSPolicyInvalid
	}
	return out, nil
}

// Allows reports whether requestOrigin may receive CORS headers.
// Empty origin (non-browser / same-origin BFF server call) is always allowed
// for the request itself; callers decide whether to emit headers.
func (policy CORSPolicy) Allows(requestOrigin string) bool {
	requestOrigin = strings.TrimSpace(requestOrigin)
	if policy.Mode == CORSModeDisabled {
		return false
	}
	if policy.Mode != CORSModeExact || requestOrigin == "" {
		return false
	}
	if policy.Matcher != nil {
		return policy.Matcher.AllowsExactOrigin(requestOrigin)
	}
	for _, allowed := range policy.Origins {
		if requestOrigin == allowed {
			return true
		}
	}
	return false
}

// ReflectOrigin returns the exact allowed origin string or empty when denied.
// Never returns the request Origin when it is not on the allowlist (no echo).
// Prefer ReflectOriginForClient for authenticated data-plane responses so
// Client A's registered Origin cannot authorize Client B's browser session.
func (policy CORSPolicy) ReflectOrigin(requestOrigin string) string {
	requestOrigin = strings.TrimSpace(requestOrigin)
	if !policy.Allows(requestOrigin) {
		return ""
	}
	return requestOrigin
}

// ReflectOriginForClient returns origin only when the authenticated public
// client_id registered it. Falls back to ReflectOrigin when no ClientMatcher
// is configured (static test policies).
func (policy CORSPolicy) ReflectOriginForClient(requestOrigin, publicClientID string) string {
	requestOrigin = strings.TrimSpace(requestOrigin)
	publicClientID = strings.TrimSpace(publicClientID)
	if policy.Mode == CORSModeDisabled || requestOrigin == "" {
		return ""
	}
	if policy.ClientMatcher != nil {
		if publicClientID == "" || !policy.ClientMatcher.AllowsOriginForClient(requestOrigin, publicClientID) {
			return ""
		}
		return requestOrigin
	}
	return policy.ReflectOrigin(requestOrigin)
}

// ReflectOriginForWorkspace returns origin only when some ACTIVE client in the
// workspace registered it. Used for preflight on workspace-scoped paths.
func (policy CORSPolicy) ReflectOriginForWorkspace(requestOrigin, workspaceID string) string {
	requestOrigin = strings.TrimSpace(requestOrigin)
	workspaceID = strings.TrimSpace(workspaceID)
	if policy.Mode == CORSModeDisabled || requestOrigin == "" {
		return ""
	}
	if policy.ClientMatcher != nil && workspaceID != "" {
		if !policy.ClientMatcher.AllowsOriginForWorkspace(requestOrigin, workspaceID) {
			return ""
		}
		return requestOrigin
	}
	return policy.ReflectOrigin(requestOrigin)
}

// IsPreflight reports a CORS preflight OPTIONS request with Access-Control-Request-Method.
func IsCORSPreflight(method, accessControlRequestMethod string) bool {
	return strings.EqualFold(method, "OPTIONS") && strings.TrimSpace(accessControlRequestMethod) != ""
}

// AllowPreflightMethod checks the requested method against the tight AAP list.
func AllowPreflightMethod(method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	for _, allowed := range AAPCORSAllowedMethods {
		if method == allowed && method != "OPTIONS" {
			return true
		}
	}
	return false
}

// AllowPreflightHeaders validates a comma-separated Access-Control-Request-Headers list.
func AllowPreflightHeaders(headerList string) bool {
	if strings.TrimSpace(headerList) == "" {
		return true
	}
	allowed := make(map[string]struct{}, len(AAPCORSAllowedHeaders))
	for _, name := range AAPCORSAllowedHeaders {
		allowed[strings.ToLower(name)] = struct{}{}
	}
	for _, part := range strings.Split(headerList, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			return false
		}
	}
	return true
}
