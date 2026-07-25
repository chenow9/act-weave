package audit_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/audit"
)

func TestRedactionBuildsFieldDiffAllowlistedHeadersAndOverflowReference(t *testing.T) {
	builder, err := audit.NewBuilder(2048, "database-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	event, err := builder.Build(audit.BuildInput{
		ID: auditMigrationEventID, WorkspaceID: auditMigrationWorkspaceID,
		ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Audit Owner",
		Action: "tool.release.published", ResourceType: "TOOL",
		ResourceID: "a38f1f2e-7b5a-7c3d-8e9f-123456789099", Result: "SUCCESS",
		RequestID: "request-redaction-1", TraceID: "trace-redaction-1", SourceIP: "203.0.113.20",
		UserAgent: "safe-agent",
		Headers: map[string][]string{
			"Content-Type":  {"application/json"},
			"X-Request-ID":  {"request-redaction-1"},
			"Authorization": {"Bearer header-secret"},
			"Cookie":        {"session=cookie-secret"},
		},
		Before: map[string]any{
			"name": "Orders", "password": "old-password", "prompt": "raw prompt before",
			"settings": map[string]any{"timeout": 10, "token": "old-token"},
		},
		After: map[string]any{
			"name": "Orders", "password": "database-secret-value", "prompt": "raw prompt after",
			"settings": map[string]any{"timeout": 20, "token": "new-token"},
		},
		Metadata: map[string]any{
			"errorCode":  "UPSTREAM_FAILED",
			"diagnostic": "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJlMTIzNDU2",
			"request":    map[string]any{"orderId": "private-order"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(event)
	for _, forbidden := range []string{
		"old-password", "database-secret-value", "raw prompt before", "raw prompt after",
		"old-token", "new-token", "header-secret", "cookie-secret", "private-order",
		"eyJhbGciOiJIUzI1NiJ9", "authorization", "cookie",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("audit event leaked %q: %s", forbidden, encoded)
		}
	}
	if strings.Contains(string(event.Changes), `"name"`) ||
		!strings.Contains(string(event.Changes), "[REDACTED]") ||
		!strings.Contains(string(event.Metadata), "content-type") ||
		!strings.Contains(string(event.Metadata), "x-request-id") {
		t.Fatalf("unexpected redacted audit event: changes=%s metadata=%s", event.Changes, event.Metadata)
	}

	smallBuilder, _ := audit.NewBuilder(256)
	overflow := audit.BuildInput{
		ID: "a38f1f2e-7b5a-7c3d-8e9f-123456789088", ActorType: "SYSTEM",
		ActorDisplay: "System", Action: "audit.detail.recorded", ResourceType: "AUDIT_EVENT",
		Result: "SUCCESS", Metadata: map[string]any{"description": strings.Repeat("safe-detail-", 200)},
	}
	if _, err := smallBuilder.Build(overflow); !errors.Is(err, audit.ErrPayloadRequired) {
		t.Fatalf("oversized detail without object error = %v", err)
	}
	overflow.WorkspaceID = auditMigrationWorkspaceID
	overflow.PayloadObjectID = "a38f1f2e-7b5a-7c3d-8e9f-123456789087"
	withObject, err := smallBuilder.Build(overflow)
	if err != nil || !strings.Contains(string(withObject.Changes), `"overflow":true`) ||
		!strings.Contains(string(withObject.Changes), overflow.PayloadObjectID) ||
		strings.Contains(string(withObject.Metadata), "safe-detail") {
		t.Fatalf("overflow reference event: %+v err=%v", withObject, err)
	}

	invalid := overflow
	invalid.ID = "not-a-uuid"
	if _, err := builder.Build(invalid); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("invalid audit build error = %v", err)
	}
}
