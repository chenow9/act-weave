package httptransport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReadOutboundCredentialsBodyStripsToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"input":[{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}],"stream":false,"outboundCredentials":{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"c1","credentialType":"ACCESS_TOKEN","value":"CANARY-AAP-BODY","expiresAt":"2099-01-01T00:00:00Z"}]}}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	split, err := ReadOutboundCredentialsBody(c)
	if err != nil {
		t.Fatal(err)
	}
	defer split.Zero()
	if !strings.Contains(string(split.CredentialsRaw), "CANARY-AAP-BODY") {
		t.Fatalf("creds: %s", split.CredentialsRaw)
	}
	if strings.Contains(string(split.BusinessJSON), "CANARY") ||
		strings.Contains(string(split.BusinessJSON), "outboundCredentials") {
		t.Fatalf("business still has secrets: %s", split.BusinessJSON)
	}
	var request AAPCreateRunRequest
	if err := DecodeBusinessJSON(split.BusinessJSON, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Input) != 1 {
		t.Fatalf("input: %+v", request.Input)
	}
}

func TestRejectOutboundCredentialsInProductionBody(t *testing.T) {
	body := []byte(`{"input":{},"trigger":"MANUAL","outboundCredentials":{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"CANARY","expiresAt":"2099-01-01T00:00:00Z"}]}}`)
	if err := RejectOutboundCredentialsInProductionBody(body); err == nil {
		t.Fatal("expected reject")
	}
	if err := RejectOutboundCredentialsInProductionBody([]byte(`{"input":{},"trigger":"MANUAL"}`)); err != nil {
		t.Fatal(err)
	}
}
