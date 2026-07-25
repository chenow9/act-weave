package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"actweave/backend/internal/protocolschema"

	"github.com/gin-gonic/gin"
)

const AAPProtocolVersionHeader = "ActWeave-Protocol-Version"

var ErrAAPProtocolVersionUnsupported = errors.New("AAP protocol version is not supported")

type AAPProtocolContext struct {
	MajorVersion    string
	ProtocolVersion string
}

type aapProtocolContextKey struct{}

func AAPProtocolContextFrom(ctx context.Context) (AAPProtocolContext, bool) {
	value, exists := ctx.Value(aapProtocolContextKey{}).(AAPProtocolContext)
	return value, exists
}

// aapProtocolMiddleware negotiates the date-versioned AAP contract. Omitting
// the header selects the current v1 snapshot; sending a value makes support
// explicit and fail-closed. Every response reports the snapshot actually used.
func aapProtocolMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header(AAPProtocolVersionHeader, protocolschema.ProtocolVersion)
		appendVary(c.Writer.Header(), AAPProtocolVersionHeader)
		values := c.Request.Header.Values(AAPProtocolVersionHeader)
		if len(values) > 1 || (len(values) == 1 &&
			strings.TrimSpace(values[0]) != protocolschema.ProtocolVersion) {
			RespondError(c, ErrAAPProtocolVersionUnsupported)
			return
		}
		protocolContext := AAPProtocolContext{
			MajorVersion: "v1", ProtocolVersion: protocolschema.ProtocolVersion,
		}
		ctx := context.WithValue(c.Request.Context(), aapProtocolContextKey{}, protocolContext)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func appendVary(header http.Header, name string) {
	for _, value := range header.Values("Vary") {
		for _, entry := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(entry), name) {
				return
			}
		}
	}
	header.Add("Vary", name)
}

func isAAPRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	if _, ok := AAPProtocolContextFrom(c.Request.Context()); ok {
		return true
	}
	return strings.HasPrefix(c.Request.URL.Path, "/api/agent-access/")
}
