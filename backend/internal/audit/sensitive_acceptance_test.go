package audit_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/audit"
)

// TestAAPSensitiveDataAcceptance is the M10-T2 gate for Audit Event surfaces:
// redaction of secrets/JWT/cookies, irreversible overflow digests, and no
// echo of sensitive inputs in errors or stored inline detail.
func TestAAPSensitiveDataAcceptance(t *testing.T) {
	t.Run("BuilderRedactsSecretsJWTCookiesAndPrompts", testAuditSensitiveRedaction)
	t.Run("OverflowUsesIrreversibleSHA256WithoutRawDetail", testAuditSensitiveOverflowHash)
	t.Run("InvalidInputDoesNotEchoSecretsInError", testAuditSensitiveErrorNoEcho)
}

func testAuditSensitiveRedaction(t *testing.T) {
	builder, err := audit.NewBuilder(4096, "database-secret-value", "header-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	event, err := builder.Build(audit.BuildInput{
		ID: auditMigrationEventID, WorkspaceID: auditMigrationWorkspaceID,
		ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Audit Owner",
		Action: "aap.client.credential.rotated", ResourceType: "AGENT_ACCESS_CLIENT",
		ResourceID: "a38f1f2e-7b5a-7c3d-8e9f-123456789099", Result: "SUCCESS",
		RequestID: "request-aap-sensitive-1", TraceID: "trace-aap-sensitive-1",
		SourceIP: "203.0.113.50", UserAgent: "aap-sensitive-test",
		Headers: map[string][]string{
			"Content-Type":  {"application/json"},
			"X-Request-ID":  {"request-aap-sensitive-1"},
			"Authorization": {"Bearer header-secret-value"},
			"Cookie":        {"session=cookie-secret-value"},
		},
		Before: map[string]any{
			"status": "active", "password": "old-password-value",
			"settings": map[string]any{"token": "old-token-value"},
		},
		After: map[string]any{
			"status": "active", "password": "database-secret-value",
			"settings": map[string]any{"token": "new-token-value"},
			"prompt":   "raw chain of thought prompt text",
		},
		Metadata: map[string]any{
			"errorCode":  "NONE",
			"diagnostic": "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJlMTIzNDU2Nzg",
			"resumeToken": "resume-token-should-redact",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(event)
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"old-password-value", "database-secret-value", "old-token-value", "new-token-value",
		"header-secret-value", "cookie-secret-value", "raw chain of thought",
		"eyJhbGciOiJIUzI1NiJ9", "resume-token-should-redact", "authorization", "cookie",
	} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Fatalf("audit event leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(string(event.Changes), "[REDACTED]") {
		t.Fatalf("expected redacted marker in changes: %s", event.Changes)
	}
	// Allowlisted operational headers remain.
	if !strings.Contains(strings.ToLower(string(event.Metadata)), "content-type") ||
		!strings.Contains(strings.ToLower(string(event.Metadata)), "x-request-id") {
		t.Fatalf("allowlisted headers missing: %s", event.Metadata)
	}
}

func testAuditSensitiveOverflowHash(t *testing.T) {
	builder, err := audit.NewBuilder(256)
	if err != nil {
		t.Fatal(err)
	}
	detail := strings.Repeat("safe-public-detail-", 200)
	input := audit.BuildInput{
		ID: "a38f1f2e-7b5a-7c3d-8e9f-123456789088", WorkspaceID: auditMigrationWorkspaceID,
		ActorType: "SYSTEM", ActorDisplay: "System", Action: "audit.detail.recorded",
		ResourceType: "AUDIT_EVENT", Result: "SUCCESS",
		Metadata:        map[string]any{"description": detail},
		PayloadObjectID: "a38f1f2e-7b5a-7c3d-8e9f-123456789087",
	}
	event, err := builder.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(event.Changes), "safe-public-detail") ||
		strings.Contains(string(event.Metadata), "safe-public-detail") {
		t.Fatalf("overflow still embeds raw detail: changes=%s metadata=%s", event.Changes, event.Metadata)
	}
	if !strings.Contains(string(event.Changes), `"overflow":true`) ||
		!strings.Contains(string(event.Changes), `"sha256"`) {
		t.Fatalf("overflow missing irreversible digest: %s", event.Changes)
	}
	// Digest is SHA-256 hex (64 chars) — irreversible relative to the detail.
	var payload map[string]any
	if err := json.Unmarshal(event.Changes, &payload); err != nil {
		t.Fatal(err)
	}
	digest, _ := payload["sha256"].(string)
	if len(digest) != 64 {
		t.Fatalf("sha256 digest length=%d value=%q", len(digest), digest)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		t.Fatalf("sha256 not hex: %v", err)
	}
	// Confirm it is not a reversible encoding of the detail.
	if digest == detail || strings.Contains(detail, digest) {
		t.Fatal("digest appears reversible/embedded in detail")
	}
	// Known SHA-256 length for comparison of algorithm family (not preimage recovery).
	sum := sha256.Sum256([]byte("probe"))
	if len(hex.EncodeToString(sum[:])) != len(digest) {
		t.Fatal("digest length mismatch vs sha256")
	}
}

func testAuditSensitiveErrorNoEcho(t *testing.T) {
	builder, err := audit.NewBuilder(2048)
	if err != nil {
		t.Fatal(err)
	}
	// Invalid actor/action should fail closed without embedding secrets from input maps.
	_, err = builder.Build(audit.BuildInput{
		ID: "not-a-uuid", WorkspaceID: auditMigrationWorkspaceID,
		ActorType: "USER", ActorID: auditMigrationUserID,
		Action: "INVALID ACTION", ResourceType: "TOOL", Result: "FAILURE",
		Metadata: map[string]any{"password": "should-not-appear-in-error"},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !errors.Is(err, audit.ErrInvalid) && !strings.Contains(strings.ToLower(err.Error()), "invalid") {
		// ErrInvalid is the stable sentinel; message must not carry secrets either way.
		t.Fatalf("unexpected error type: %v", err)
	}
	if strings.Contains(err.Error(), "should-not-appear-in-error") {
		t.Fatalf("error echoed secret: %v", err)
	}
}
