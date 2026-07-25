package httptransport

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"

	"github.com/gin-gonic/gin"
)

// aapCORSPathMiddleware applies AAP CORS only under /api/agent-access/v1 so
// management /api/v1 routes are unaffected. Engine-level registration ensures
// OPTIONS preflight is handled before Gin route matching.
//
// For non-preflight requests this middleware only applies static (non-client-
// scoped) exact policies before the handler. Client-isolated reflection is
// applied by aapClientCORSMiddleware after authentication and before handlers
// write the response body.
func aapCORSPathMiddleware(policy agentaccessauth.CORSPolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/agent-access/v1") {
			c.Next()
			return
		}
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if agentaccessauth.IsCORSPreflight(c.Request.Method, c.GetHeader("Access-Control-Request-Method")) {
			handleAAPCORSPreflight(c, policy, origin)
			return
		}
		// Client-scoped matcher: still emit workspace-scoped (or global exact)
		// CORS before auth so token expired/invalid 401 responses remain
		// readable by the browser SDK. Post-auth aapClientCORSMiddleware then
		// tightens to the authenticated public client_id (azp).
		if policy.Mode == agentaccessauth.CORSModeExact && policy.ClientMatcher != nil {
			if origin != "" {
				reflected := ""
				if wid := aapWorkspaceIDFromPath(c.Request.URL.Path); wid != "" {
					reflected = policy.ReflectOriginForWorkspace(origin, wid)
				} else {
					// Token endpoint / non-workspace routes: any ACTIVE client origin.
					reflected = policy.ReflectOrigin(origin)
				}
				if reflected != "" {
					writeAAPCORSHeaders(c.Writer.Header(), reflected, false)
				}
			}
			c.Next()
			return
		}
		if origin != "" {
			if reflected := policy.ReflectOrigin(origin); reflected != "" {
				writeAAPCORSHeaders(c.Writer.Header(), reflected, false)
			}
		}
		c.Next()
	}
}

// aapClientCORSMiddleware rewrites CORS after authentication so success
// responses never inherit workspace-scoped pre-auth headers from a foreign
// Client Token. Must run after aapAuthenticationMiddleware and before handlers.
//
// On auth failure (no principal) the pre-auth workspace/global headers stay so
// 401 bodies such as TOKEN_EXPIRED remain readable by the browser SDK.
// On auth success: clear pre-auth Access-Control-* headers first, then re-apply
// only when ReflectOriginForClient matches the authenticated azp/client_id.
func aapClientCORSMiddleware(policy agentaccessauth.CORSPolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		if policy.Mode != agentaccessauth.CORSModeExact || policy.ClientMatcher == nil {
			c.Next()
			return
		}
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}
		principal, ok := AAPPrincipalFrom(c.Request.Context())
		if !ok {
			// Auth failed — keep pre-auth workspace-scoped CORS for 401 readability.
			c.Next()
			return
		}
		// Auth succeeded: drop any workspace-scoped pre-auth CORS so a Token
		// from Client B cannot keep Client A's Origin reflection.
		clearAAPCORSHeaders(c.Writer.Header())
		if reflected := policy.ReflectOriginForClient(origin, principal.AuthorizedParty); reflected != "" {
			writeAAPCORSHeaders(c.Writer.Header(), reflected, false)
		}
		c.Next()
	}
}

// clearAAPCORSHeaders removes Access-Control response headers that may have
// been written by aapCORSPathMiddleware before authentication.
func clearAAPCORSHeaders(header http.Header) {
	header.Del("Access-Control-Allow-Origin")
	header.Del("Access-Control-Allow-Credentials")
	header.Del("Access-Control-Allow-Methods")
	header.Del("Access-Control-Allow-Headers")
	header.Del("Access-Control-Expose-Headers")
	header.Del("Access-Control-Max-Age")
}

func handleAAPCORSPreflight(c *gin.Context, policy agentaccessauth.CORSPolicy, origin string) {
	// BFF / disabled: no CORS surface at all.
	if policy.Mode == agentaccessauth.CORSModeDisabled {
		c.Status(http.StatusNoContent)
		c.Abort()
		return
	}
	// Workspace-scoped preflight when path embeds workspace id — never use a
	// global Client Origin union for data-plane paths.
	reflected := ""
	if wid := aapWorkspaceIDFromPath(c.Request.URL.Path); wid != "" && policy.ClientMatcher != nil {
		reflected = policy.ReflectOriginForWorkspace(origin, wid)
	} else {
		// Token endpoint / non-workspace public routes: exact-origin only
		// (any ACTIVE client that registered this origin). Preflight has no
		// Authorization so Client-level isolation is not available here.
		reflected = policy.ReflectOrigin(origin)
	}
	if reflected == "" {
		// Do not reflect unauthorized origins.
		c.Status(http.StatusForbidden)
		c.Abort()
		return
	}
	reqMethod := c.GetHeader("Access-Control-Request-Method")
	reqHeaders := c.GetHeader("Access-Control-Request-Headers")
	if !agentaccessauth.AllowPreflightMethod(reqMethod) ||
		!agentaccessauth.AllowPreflightHeaders(reqHeaders) {
		c.Status(http.StatusForbidden)
		c.Abort()
		return
	}
	header := c.Writer.Header()
	writeAAPCORSHeaders(header, reflected, true)
	header.Set("Access-Control-Max-Age", "600")
	c.Status(http.StatusNoContent)
	c.Abort()
}

// aapWorkspaceIDFromPath extracts /workspaces/{uuid}/ from AAP paths.
func aapWorkspaceIDFromPath(path string) string {
	const marker = "/workspaces/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}
	rest := path[idx+len(marker):]
	end := strings.IndexByte(rest, '/')
	if end < 0 {
		end = len(rest)
	}
	wid := strings.TrimSpace(rest[:end])
	if len(wid) < 32 {
		return ""
	}
	return wid
}

func writeAAPCORSHeaders(header http.Header, origin string, preflight bool) {
	// Exact origin only — never "*".
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Vary", mergeVary(header.Get("Vary"), "Origin"))
	header.Set("Access-Control-Allow-Credentials", "true")
	if preflight {
		header.Set("Access-Control-Allow-Methods", strings.Join(agentaccessauth.AAPCORSAllowedMethods, ", "))
		header.Set("Access-Control-Allow-Headers", strings.Join(agentaccessauth.AAPCORSAllowedHeaders, ", "))
	}
	header.Set("Access-Control-Expose-Headers", strings.Join(agentaccessauth.AAPCORSExposedHeaders, ", "))
}

func mergeVary(existing, add string) string {
	if strings.TrimSpace(existing) == "" {
		return add
	}
	parts := strings.Split(existing, ",")
	seen := map[string]struct{}{}
	var out []string
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	if _, ok := seen[strings.ToLower(add)]; !ok {
		out = append(out, add)
	}
	return strings.Join(out, ", ")
}

// writeTokenIssueRateLimitHeaders attaches standard rate-limit headers without
// embedding client ids, subjects, or other resource identifiers in the body.
func writeTokenIssueRateLimitHeaders(c *gin.Context, decision agentaccessauth.TokenIssueDecision) {
	if decision.Limit <= 0 {
		return
	}
	c.Header("RateLimit-Limit", strconv.Itoa(decision.Limit))
	c.Header("RateLimit-Remaining", strconv.Itoa(max(decision.Remaining, 0)))
	reset := int64(time.Until(decision.ResetAt).Seconds())
	if reset < 0 {
		reset = 0
	}
	c.Header("RateLimit-Reset", strconv.FormatInt(reset, 10))
	if decision.RetryAfter > 0 {
		seconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
}
