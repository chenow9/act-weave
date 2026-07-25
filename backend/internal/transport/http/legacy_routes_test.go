package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyRoutesRemoved(t *testing.T) {
	router, err := NewRouter(Config{})
	if err != nil {
		t.Fatalf("create final router: %v", err)
	}

	for _, path := range []string{
		"/health",
		"/api/health",
		"/api/auth/login",
		"/api/workspaces",
		"/api/tools",
		"/api/chat/sessions",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("legacy route %q returned %d, want 404", path, response.Code)
		}
	}

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("final health route returned %d, want 200", health.Code)
	}
}
