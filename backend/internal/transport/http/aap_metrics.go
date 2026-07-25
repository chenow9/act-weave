package httptransport

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"actweave/backend/internal/metrics"

	"github.com/gin-gonic/gin"
)

// aapMetricsAuthMiddleware protects GET /metrics:
//   - when bearerToken is non-empty: require Authorization: Bearer <token>
//   - when empty: allow only loopback RemoteAddr (local scrape / unit tests)
func aapMetricsAuthMiddleware(bearerToken string) gin.HandlerFunc {
	expected := strings.TrimSpace(bearerToken)
	return func(c *gin.Context) {
		if expected != "" {
			got := bearerTokenFromAuthorization(c.GetHeader("Authorization"))
			if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			c.Next()
			return
		}
		if !isLoopbackRemoteAddr(c.Request.RemoteAddr) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func bearerTokenFromAuthorization(header string) string {
	parts := strings.Fields(strings.TrimSpace(header))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		// httptest and some local servers leave RemoteAddr empty.
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host == "localhost"
	}
	return ip.IsLoopback()
}

// aapMetricsHandler exposes process-local AAP + smart-dag counters as Prometheus text.
func aapMetricsHandler(c *gin.Context) {
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, metrics.Default().PrometheusText()+metrics.SmartDag().PrometheusText())
}
