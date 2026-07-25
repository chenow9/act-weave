package httptransport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"actweave/backend/internal/outboundidentity"

	"github.com/gin-gonic/gin"
)

// OutboundIdentityJWKSRoutes serves the public outbound Subject Assertion JWKS.
// Completely separate from /api/agent-access/v1/.well-known/jwks.json.
type OutboundIdentityJWKSRoutes struct {
	keys outboundidentity.SigningKeyProvider
	now  func() time.Time
}

// NewOutboundIdentityJWKSRoutes builds the fixed outbound JWKS registrar.
func NewOutboundIdentityJWKSRoutes(keys outboundidentity.SigningKeyProvider) (*OutboundIdentityJWKSRoutes, error) {
	if keys == nil {
		return nil, errors.New("outbound identity signing key provider is required")
	}
	return &OutboundIdentityJWKSRoutes{
		keys: keys,
		now:  func() time.Time { return time.Now().UTC() },
	}, nil
}

// RegisterV1 registers GET /api/outbound-identity/v1/.well-known/jwks.json on the
// public engine (not under /api/v1) via a dedicated method used by application wiring.
// Implements a small registrar interface used only for outbound identity.
func (routes *OutboundIdentityJWKSRoutes) Register(router gin.IRoutes) {
	if routes == nil || router == nil {
		return
	}
	router.GET("/api/outbound-identity/v1/.well-known/jwks.json", routes.jwks)
}

func (routes *OutboundIdentityJWKSRoutes) jwks(c *gin.Context) {
	set, err := routes.keys.PublicJWKS(routes.now())
	if err != nil {
		RespondError(c, err)
		return
	}
	payload, err := json.Marshal(set)
	if err != nil {
		RespondError(c, err)
		return
	}
	// Defense: never emit private material keys even if a future field is added.
	if containsPrivateJWKFields(payload) {
		RespondError(c, errors.New("outbound JWKS rejected private material"))
		return
	}
	digest := sha256.Sum256(payload)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	c.Header("Cache-Control", "public, max-age=60, must-revalidate")
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/jwk-set+json", payload)
}

func containsPrivateJWKFields(payload []byte) bool {
	// Cheap scan for common private JWK fields.
	s := string(payload)
	return containsJSONKey(s, "d") || containsJSONKey(s, "p") ||
		containsJSONKey(s, "q") || containsJSONKey(s, "dp") ||
		containsJSONKey(s, "dq") || containsJSONKey(s, "qi") ||
		containsJSONKey(s, "oth") || containsJSONKey(s, "k")
}

func containsJSONKey(payload, key string) bool {
	// Match "key":
	needle := `"` + key + `"`
	idx := 0
	for {
		i := indexFrom(payload, needle, idx)
		if i < 0 {
			return false
		}
		// Ensure it's a key: after whitespace comes ':'
		j := i + len(needle)
		for j < len(payload) && (payload[j] == ' ' || payload[j] == '\t' || payload[j] == '\n' || payload[j] == '\r') {
			j++
		}
		if j < len(payload) && payload[j] == ':' {
			return true
		}
		idx = i + 1
	}
}

func indexFrom(s, sub string, from int) int {
	if from >= len(s) {
		return -1
	}
	i := 0
	// strings.Index on slice
	rest := s[from:]
	i = len(rest)
	_ = i
	for pos := 0; pos+len(sub) <= len(rest); pos++ {
		if rest[pos:pos+len(sub)] == sub {
			return from + pos
		}
	}
	return -1
}
