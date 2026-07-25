package httptransport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"actweave/backend/internal/agentaccessauth"

	"github.com/gin-gonic/gin"
)

type AgentAccessJWKSRoutes struct {
	keys agentaccessauth.SigningKeyProvider
	now  func() time.Time
}

func NewAgentAccessJWKSRoutes(keys agentaccessauth.SigningKeyProvider) (*AgentAccessJWKSRoutes, error) {
	if keys == nil {
		return nil, errors.New("Agent Access signing key provider is required")
	}
	return &AgentAccessJWKSRoutes{keys: keys, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (routes *AgentAccessJWKSRoutes) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	v1.Public.GET("/.well-known/jwks.json", routes.jwks)
}

func (routes *AgentAccessJWKSRoutes) jwks(c *gin.Context) {
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
