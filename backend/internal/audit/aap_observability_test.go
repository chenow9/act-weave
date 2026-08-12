package audit_test

import (
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/audit"
)

// TestAAPObservability is the M10-T4 gate for AAP ops audit surfaces:
// authorization/auth failures and credential lifecycle events retain only
// index IDs/codes for dashboard join (Client/Agent/Run) and never embed
// secrets, tokens, message bodies, or tool payloads.
func TestAAPObservability(t *testing.T) {
	t.Run("AuthDenialAndTokenFailureIndexOnly", testAAPAuditAuthIndexOnly)
	t.Run("CredentialLifecycleNoSecretMaterial", testAAPAuditCredentialLifecycle)
	t.Run("SequenceConflictAndWaitingApprovalMetadata", testAAPAuditOpsSignals)
	t.Run("DashboardCanLocateClientAgentRun", testAAPAuditDashboardJoin)
}

func testAAPAuditAuthIndexOnly(t *testing.T) {
	builder, err := audit.NewBuilder(4096)
	if err != nil {
		t.Fatal(err)
	}

	// Authorization denied — reason/scope codes only.
	denied, err := builder.Build(audit.BuildInput{
		ID:           "a38f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		WorkspaceID:  "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1",
		ActorType:    "SERVICE_PRINCIPAL",
		ActorID:      "a38f1f2e-7b5a-7c3d-8e9f-1234567890c1",
		ActorDisplay: "AAP Client",
		Action:       "agentaccess.authorization.denied",
		ResourceType: "AGENT_RUN",
		ResourceID:   "a38f1f2e-7b5a-7c3d-8e9f-1234567890d1",
		Result:       "DENIED",
		RequestID:    "req-obs-auth-1",
		TraceID:      "tr-obs-auth-1",
		Metadata: map[string]any{
			"clientId":           "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
			"agentId":            "a38f1f2e-7b5a-7c3d-8e9f-1234567890f1",
			"servicePrincipalId": "a38f1f2e-7b5a-7c3d-8e9f-1234567890c1",
			"runId":              "a38f1f2e-7b5a-7c3d-8e9f-1234567890d1",
			"requiredScope":      "run:create",
			"reason":             "SCOPE_MISSING",
			"errorCode":          "AUTHORIZATION_DENIED",
			// Attempted smuggling — must be redacted / dropped.
			"accessToken":   "eyJhbGciOiJIUzI1NiJ9.payload.signature123456",
			"clientSecret":  "awsk_should_never_persist",
			"authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature123456",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitive(t, denied)
	assertMetadataContains(t, denied, "SCOPE_MISSING", "run:create", "AUTHORIZATION_DENIED")

	// Authentication failed — error code only, no raw credential.
	authFail, err := builder.Build(audit.BuildInput{
		ID:           "a38f1f2e-7b5a-7c3d-8e9f-1234567890a2",
		WorkspaceID:  "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1",
		ActorType:    "SYSTEM",
		ActorDisplay: "AAP Token Endpoint",
		Action:       "agentaccess.authentication.failed",
		ResourceType: "AGENT_ACCESS_CLIENT",
		ResourceID:   "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
		Result:       "FAILURE",
		RequestID:    "req-obs-token-1",
		Metadata: map[string]any{
			"clientId":   "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
			"authMethod": "client_secret_basic",
			"errorCode":  "CREDENTIAL_REJECTED",
			"password":   "plain-password-attempt",
			"token":      "raw-token-attempt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitive(t, authFail)
	assertMetadataContains(t, authFail, "CREDENTIAL_REJECTED")
}

func testAAPAuditCredentialLifecycle(t *testing.T) {
	builder, err := audit.NewBuilder(4096)
	if err != nil {
		t.Fatal(err)
	}
	event, err := builder.Build(audit.BuildInput{
		ID:           "a38f1f2e-7b5a-7c3d-8e9f-1234567890a3",
		WorkspaceID:  "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1",
		ActorType:    "USER",
		ActorID:      "a38f1f2e-7b5a-7c3d-8e9f-1234567890c2",
		ActorDisplay: "Workspace Admin",
		Action:       "agentaccess.credential.rotated",
		ResourceType: "AGENT_ACCESS_CLIENT",
		ResourceID:   "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
		Result:       "SUCCESS",
		RequestID:    "req-obs-cred-1",
		Before: map[string]any{
			"credentialId": "a38f1f2e-7b5a-7c3d-8e9f-1234567890c3",
			"status":       "active",
			"secret":       "awsk_old_secret_value",
		},
		After: map[string]any{
			"credentialId": "a38f1f2e-7b5a-7c3d-8e9f-1234567890c4",
			"status":       "active",
			"secret":       "awsk_new_secret_value",
		},
		Metadata: map[string]any{
			"clientId":    "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
			"operation":   "rotate",
			"errorCode":   "NONE",
			"lastUsedAge": "72h", // age signal for credential expiry backlog alerts
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitive(t, event)
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "awsk_") {
		t.Fatalf("credential secret leaked into audit: %s", encoded)
	}
	if !strings.Contains(strings.ToLower(string(event.Changes)), "redacted") &&
		!strings.Contains(string(event.Changes), "[REDACTED]") {
		// secret keys should be redacted in before/after diff
		t.Fatalf("expected secret fields redacted in changes: %s", event.Changes)
	}
}

func testAAPAuditOpsSignals(t *testing.T) {
	builder, err := audit.NewBuilder(4096)
	if err != nil {
		t.Fatal(err)
	}

	// Sequence conflict — ops alert signal with Run join key only.
	seq, err := builder.Build(audit.BuildInput{
		ID:           "a38f1f2e-7b5a-7c3d-8e9f-1234567890a4",
		WorkspaceID:  "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1",
		ActorType:    "SYSTEM",
		ActorDisplay: "Event Kernel",
		Action:       "aap.protocol_event.sequence_conflict",
		ResourceType: "AGENT_RUN",
		ResourceID:   "a38f1f2e-7b5a-7c3d-8e9f-1234567890d2",
		Result:       "FAILURE",
		RequestID:    "req-obs-seq-1",
		Metadata: map[string]any{
			"runId":            "a38f1f2e-7b5a-7c3d-8e9f-1234567890d2",
			"clientId":         "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
			"agentId":          "a38f1f2e-7b5a-7c3d-8e9f-1234567890f1",
			"errorCode":        "SEQUENCE_CONFLICT",
			"expectedSequence": 12,
			"observedSequence": 11,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitive(t, seq)
	assertMetadataContains(t, seq, "SEQUENCE_CONFLICT")

	// Waiting-approval backlog age signal (index IDs + age code).
	wait, err := builder.Build(audit.BuildInput{
		ID:           "a38f1f2e-7b5a-7c3d-8e9f-1234567890a5",
		WorkspaceID:  "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1",
		ActorType:    "SYSTEM",
		ActorDisplay: "Ops Sampler",
		Action:       "aap.run.waiting_approval.age",
		ResourceType: "AGENT_RUN",
		ResourceID:   "a38f1f2e-7b5a-7c3d-8e9f-1234567890d3",
		Result:       "SUCCESS",
		RequestID:    "req-obs-wait-1",
		Metadata: map[string]any{
			"runId":         "a38f1f2e-7b5a-7c3d-8e9f-1234567890d3",
			"clientId":      "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1",
			"agentId":       "a38f1f2e-7b5a-7c3d-8e9f-1234567890f1",
			"interactionId": "a38f1f2e-7b5a-7c3d-8e9f-1234567890c2",
			"waitingAgeMs":  3_600_000,
			"errorCode":     "NONE",
			// Free-text content keys must be redacted (rawContentKey).
			"body":   "approve purchase of $999",
			"prompt": "chain of thought secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitive(t, wait)
	// Age + join keys retained; free-text redacted.
	assertMetadataContains(t, wait, "3600000", "[REDACTED]")
}

func testAAPAuditDashboardJoin(t *testing.T) {
	builder, err := audit.NewBuilder(4096)
	if err != nil {
		t.Fatal(err)
	}
	const (
		ws  = "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1"
		cl  = "a38f1f2e-7b5a-7c3d-8e9f-1234567890e1"
		ag  = "a38f1f2e-7b5a-7c3d-8e9f-1234567890f1"
		run = "a38f1f2e-7b5a-7c3d-8e9f-1234567890d1"
	)
	event, err := builder.Build(audit.BuildInput{
		ID:           "a38f1f2e-7b5a-7c3d-8e9f-1234567890a6",
		WorkspaceID:  ws,
		ActorType:    "SERVICE_PRINCIPAL",
		ActorID:      "a38f1f2e-7b5a-7c3d-8e9f-1234567890c1",
		ActorDisplay: "AAP Client",
		Action:       "aap.sse.slow_consumer.disconnect",
		ResourceType: "AGENT_RUN",
		ResourceID:   run,
		Result:       "SUCCESS",
		RequestID:    "req-obs-sse-1",
		TraceID:      "tr-obs-sse-1",
		Metadata: map[string]any{
			"clientId":  cl,
			"agentId":   ag,
			"runId":     run,
			"errorCode": "SLOW_CONSUMER",
			"reason":    "WRITE_TIMEOUT",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.WorkspaceID != ws || event.ResourceID != run {
		t.Fatalf("workspace/run join keys missing: ws=%s resource=%s", event.WorkspaceID, event.ResourceID)
	}
	meta := string(event.Metadata)
	for _, id := range []string{cl, ag, run} {
		if !strings.Contains(meta, id) {
			t.Fatalf("dashboard cannot locate id %s in metadata: %s", id, meta)
		}
	}
	// Top-level request/trace for log correlation.
	if event.RequestID == "" || event.TraceID == "" {
		t.Fatalf("request/trace missing for cross-signal join")
	}
}

func assertNoSensitive(t *testing.T, event audit.Event) {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"eyjhbGciOi", "awsk_", "plain-password", "raw-token-attempt",
		"super-secret", "bearer eyj", "chain of thought",
		"approve purchase",
	} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Fatalf("audit event leaked %q: %s", forbidden, encoded)
		}
	}
}

func assertMetadataContains(t *testing.T, event audit.Event, needles ...string) {
	t.Helper()
	meta := string(event.Metadata)
	for _, needle := range needles {
		if !strings.Contains(meta, needle) {
			t.Fatalf("metadata missing %q: %s", needle, meta)
		}
	}
}
