package connection

import (
	"errors"
	"fmt"
	"testing"
)

func TestSafeVerificationDetailHTTPStatus(t *testing.T) {
	detail := safeVerificationDetail(fmt.Errorf("connection verification returned HTTP_STATUS_502"))
	if detail != "HTTP_STATUS_502" {
		t.Fatalf("detail=%q", detail)
	}
}

func TestSafeVerificationDetailRedactsSecrets(t *testing.T) {
	detail := safeVerificationDetail(errors.New("Authorization Bearer raw-connection-secret"))
	if detail != "upstream_error" {
		t.Fatalf("detail=%q want upstream_error", detail)
	}
}
