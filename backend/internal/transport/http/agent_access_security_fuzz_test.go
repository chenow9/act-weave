package httptransport

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func FuzzOAuthTokenFormParser(f *testing.F) {
	for _, seed := range []struct{ body, contentType, query string }{
		{"grant_type=client_credentials&agent_id=a68f1f2e-7b5a-7c3d-8e9f-123456789001&scope=run%3Acreate", "application/x-www-form-urlencoded", ""},
		{"grant_type=client_credentials&grant_type=password", "application/x-www-form-urlencoded", ""},
		{"client_secret=must-not-be-accepted", "application/x-www-form-urlencoded", ""},
		{"grant_type=%zz", "application/x-www-form-urlencoded", ""},
		{"{}", "application/json", ""},
		{"grant_type=client_credentials", "application/x-www-form-urlencoded; charset=UTF-8", "access_token=forbidden"},
		{strings.Repeat("a", maximumOAuthTokenFormBytes+1), "application/x-www-form-urlencoded", ""},
	} {
		f.Add(seed.body, seed.contentType, seed.query)
	}
	f.Fuzz(func(t *testing.T, body, contentType, rawQuery string) {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		request := httptest.NewRequest("POST", "/api/agent-access/v1/oauth/token", strings.NewReader(body))
		request.Header.Set("Content-Type", contentType)
		request.URL.RawQuery = rawQuery
		c.Request = request
		form, err := parseOAuthTokenForm(c)
		if err == nil {
			allowed := map[string]bool{
				"grant_type": true, "agent_id": true, "scope": true,
				"client_assertion_type": true, "client_assertion": true,
			}
			for key, values := range form {
				if !allowed[key] || len(values) != 1 {
					t.Fatalf("parser accepted invalid form key=%q values=%q", key, values)
				}
			}
		}
		_, _ = io.Copy(io.Discard, c.Request.Body)
	})
}
