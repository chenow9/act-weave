package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"actweave/backend/internal/metrics"
)

func TestAAPMetricsRouteExposesPrometheusText(t *testing.T) {
	metrics.Default().ObserveTokenIssue(false, map[string]string{"reason": "test"})
	// Empty token = loopback-only scrape surface.
	router, err := NewRouter(Config{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "aap_token_issue_failure_total") {
		t.Fatalf("missing token failure metric: %s", body)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
	}

	// Non-loopback without bearer must not expose metrics.
	remote := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	remote.RemoteAddr = "203.0.113.10:9999"
	remoteRec := httptest.NewRecorder()
	router.ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusNotFound {
		t.Fatalf("non-loopback status=%d want 404", remoteRec.Code)
	}
}

func TestAAPMetricsRouteRequiresBearerWhenConfigured(t *testing.T) {
	router, err := NewRouter(Config{MetricsBearerToken: "metrics-secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	denied := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	denied.RemoteAddr = "203.0.113.10:9999"
	deniedRec := httptest.NewRecorder()
	router.ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", deniedRec.Code)
	}
	ok := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	ok.RemoteAddr = "203.0.113.10:9999"
	ok.Header.Set("Authorization", "Bearer metrics-secret-token")
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, ok)
	if okRec.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d", okRec.Code)
	}
}
