package modelconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestVerifyModelConfigCallsUpstreamWithoutDatabaseTransaction(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Verify Model")
	service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, config Config) (AgenticCapabilities, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		_, probeErr := db.ExecContext(probeCtx, `
			UPDATE model_configs SET updated_at = updated_at
			WHERE workspace_id = $1 AND id = $2
		`, config.WorkspaceID, config.ID)
		return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, probeErr
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("verify model config: %v", err)
	}
	if verified.Status != StatusVerified || verified.LastVerifiedAt == nil ||
		verified.LastLatencyMS == nil || verified.LastErrorCode != nil || verified.LockVersion != created.LockVersion+1 {
		t.Fatalf("unexpected verified model config: %+v", verified)
	}
	doc, _, err := ParseAgenticCapabilities(verified.AgenticCapabilities)
	if err != nil {
		t.Fatalf("parse agentic caps: %v", err)
	}
	if doc.SchemaVersion != AgenticCapabilitiesSchemaV1 || doc.Protocol != AgenticProtocolOpenAIResponsesV1 ||
		doc.VerifiedLockVersion != created.LockVersion || doc.VerifiedAdapter != VerifiedAdapterAgenticOpenAIV022 {
		t.Fatalf("unexpected agentic caps: %+v", doc)
	}
	if !doc.Streaming || !doc.Usage || len(doc.ToolSearchModes) != 1 || doc.ToolSearchModes[0] != AgenticToolSearchModeClient {
		t.Fatalf("unexpected capability flags: %+v", doc)
	}
	if !IsUnsetToolDisclosurePolicy(verified.ToolDisclosurePolicy) {
		t.Fatalf("native verify must persist empty policy, got %s", verified.ToolDisclosurePolicy)
	}
	// Evidence relationship: capability VerifiedAt equals LastVerifiedAt at UTC second.
	if verified.LastVerifiedAt == nil ||
		!doc.VerifiedAt.UTC().Truncate(time.Second).Equal(verified.LastVerifiedAt.UTC().Truncate(time.Second)) {
		t.Fatalf("verifiedAt=%v lastVerifiedAt=%v must match at UTC second", doc.VerifiedAt, verified.LastVerifiedAt)
	}
	// Re-read via Get must succeed with strict evidence invariant.
	again, err := repository.Get(context.Background(), created.WorkspaceID, created.ID)
	if err != nil {
		t.Fatalf("Get after verify: %v", err)
	}
	if again.Status != StatusVerified || again.LastErrorCode != nil || again.LastLatencyMS == nil || *again.LastLatencyMS < 0 {
		t.Fatalf("re-read evidence: %+v", again)
	}
}

// TestVerifyAppliesConfiguredOuterBudgetToUpstreamContext proves the timeout
// passed to NewVerificationService is the deadline the verifier actually sees on
// its context (R11-1). Before the fix application.Open hardcoded 20s here, which
// silently capped the 30s Responses-stream, 45s client tool_search, and 30s
// function-calling probe budgets nested inside it; with the configured 120s
// default those inner budgets are reachable, and a small configured budget is
// still honoured exactly.
func TestVerifyAppliesConfiguredOuterBudgetToUpstreamContext(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Outer Budget Model")
	for _, budget := range []time.Duration{
		750 * time.Millisecond,
		75 * time.Second,  // smaller configured budget still honoured
		120 * time.Second, // config.DefaultModelVerificationTimeoutSeconds
		600 * time.Second, // config.MaxModelVerificationTimeoutSeconds
	} {
		t.Run(budget.String(), func(t *testing.T) {
			var remaining time.Duration
			var hasDeadline bool
			service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, _ Config) (AgenticCapabilities, error) {
				deadline, ok := ctx.Deadline()
				hasDeadline = ok
				if ok {
					remaining = time.Until(deadline)
				}
				return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, nil
			}), budget)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID); err != nil {
				t.Fatalf("verify: %v", err)
			}
			if !hasDeadline {
				t.Fatal("upstream verification context must carry the configured deadline")
			}
			// Only scheduling slack may be missing; never a different budget.
			if remaining > budget || remaining < budget-500*time.Millisecond {
				t.Fatalf("upstream deadline %v is not the configured budget %v", remaining, budget)
			}
		})
	}
}

func TestVerifiedReadEvidenceCorruptRowsFailClosed(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Corrupt Evidence Model")
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt LastLatencyMS to NULL via SQL — Get must fail.
	if _, err := db.Exec(`UPDATE model_configs SET last_latency_ms = NULL WHERE id=$1`, verified.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for null latency, got %v", err)
	}
	// Restore latency; corrupt LastErrorCode non-null on VERIFIED.
	if _, err := db.Exec(`UPDATE model_configs SET last_latency_ms = 1, last_error_code = 'MODEL_CONFIG_UPSTREAM_ERROR' WHERE id=$1`, verified.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for error code on VERIFIED, got %v", err)
	}
	// Restore error code; drift capability verifiedAt vs last_verified_at by rewriting
	// the JSONB verifiedAt one hour earlier (DB constraint forbids last_verified_at < created_at).
	if _, err := db.Exec(`
		UPDATE model_configs
		SET last_error_code = NULL,
		    last_latency_ms = 1,
		    agentic_capabilities = jsonb_set(
		      agentic_capabilities,
		      '{verifiedAt}',
		      to_jsonb(to_char(last_verified_at - interval '1 hour', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
		    )
		WHERE id=$1
	`, verified.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for verifiedAt drift, got %v", err)
	}
	// List must also fail closed on corrupt VERIFIED evidence.
	if _, err := repository.List(context.Background(), created.WorkspaceID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want List ErrInvalid, got %v", err)
	}
}

func TestVerifyModelConfigPersistsStableRedactedErrors(t *testing.T) {
	tests := []struct {
		name  string
		error func(context.Context) error
		code  string
	}{
		{name: "authentication", error: func(context.Context) error {
			return fmt.Errorf("Authorization Bearer raw-model-secret: %w", ErrUpstreamAuthentication)
		}, code: ErrorCodeAuthentication},
		{name: "network", error: func(context.Context) error {
			return testNetworkError{message: "dial included raw-model-secret"}
		}, code: ErrorCodeNetwork},
		{name: "timeout", error: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, code: ErrorCodeVerificationTimeout},
		{name: "responses unsupported", error: func(context.Context) error {
			return fmt.Errorf("endpoint missing responses: %w", ErrResponsesUnsupported)
		}, code: ErrorCodeResponsesUnsupported},
		{name: "tool search unsupported", error: func(context.Context) error {
			return fmt.Errorf("hosted only: %w", ErrToolSearchUnsupported)
		}, code: ErrorCodeToolSearchUnsupported},
		{name: "stream invalid", error: func(context.Context) error {
			return fmt.Errorf("malformed: %w", ErrAgenticStreamInvalid)
		}, code: ErrorCodeAgenticStreamInvalid},
		{name: "usage invalid", error: func(context.Context) error {
			return fmt.Errorf("bad usage: %w", ErrAgenticUsageInvalid)
		}, code: ErrorCodeAgenticUsageInvalid},
		{name: "upstream", error: func(context.Context) error {
			return errors.New("raw-model-secret in response body")
		}, code: ErrorCodeUpstream},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, db := newModelConfigRepositoryTest(t, nil)
			created := createRepositoryConfig(t, repository, repositoryConfigID, "Verify Error Model")
			service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, _ Config) (AgenticCapabilities, error) {
				return AgenticCapabilities{}, test.error(ctx)
			}), 5*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
			if err != nil {
				t.Fatalf("verify model config failure result: %v", err)
			}
			if verified.Status != StatusError || verified.LastErrorCode == nil || *verified.LastErrorCode != test.code {
				t.Fatalf("unexpected verification error state: %+v", verified)
			}
			if !IsUnverifiedAgenticCapabilities(verified.AgenticCapabilities) {
				t.Fatalf("failure must clear agentic capabilities, got %s", verified.AgenticCapabilities)
			}
			var storedCode string
			var storedCaps []byte
			if err := db.QueryRow(`SELECT last_error_code, agentic_capabilities FROM model_configs WHERE id=$1`, created.ID).Scan(&storedCode, &storedCaps); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(storedCode, "raw-model-secret") || storedCode != test.code {
				t.Fatalf("unsafe or unstable stored error code %q", storedCode)
			}
			if strings.Contains(string(storedCaps), "raw-model-secret") || !json.Valid(storedCaps) {
				t.Fatalf("unsafe stored capabilities %s", storedCaps)
			}
		})
	}
}

func TestVerifyPersistsTieredCapabilitiesAndPolicy(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	cases := []struct {
		name        string
		probe       AgenticCapabilities
		wantVersion string
		wantCalling string
		wantPolicy  string
	}{
		{
			name:        "native",
			probe:       AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch},
			wantVersion: AgenticCapabilitiesSchemaV1,
			wantCalling: ToolCallingNativeClientSearch,
			wantPolicy:  "",
		},
		{
			name:        "function_calling",
			probe:       AgenticCapabilities{ToolCalling: ToolCallingFunctionCalling},
			wantVersion: AgenticCapabilitiesSchemaV2,
			wantCalling: ToolCallingFunctionCalling,
			wantPolicy:  DisclosureModePlatformOnDemand,
		},
		{
			name:        "none",
			probe:       AgenticCapabilities{ToolCalling: ToolCallingNone},
			wantVersion: AgenticCapabilitiesSchemaV2,
			wantCalling: ToolCallingNone,
			wantPolicy:  "",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("018f1f2e-7b5a-7c3d-8e9f-e23456789%03d", i+40)
			created := createRepositoryConfig(t, repository, id, "Tier "+tc.name)
			service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
				return tc.probe, nil
			}), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if verified.Status != StatusVerified || verified.LastErrorCode != nil {
				t.Fatalf("verified row: %+v", verified)
			}
			doc, _, err := ParseAgenticCapabilities(verified.AgenticCapabilities)
			if err != nil {
				t.Fatal(err)
			}
			if doc.SchemaVersion != tc.wantVersion || doc.ToolCalling != tc.wantCalling {
				t.Fatalf("caps version=%s calling=%s want %s/%s", doc.SchemaVersion, doc.ToolCalling, tc.wantVersion, tc.wantCalling)
			}
			if tc.wantVersion == AgenticCapabilitiesSchemaV1 && strings.Contains(string(verified.AgenticCapabilities), "toolCalling") {
				t.Fatalf("native persist must stay v1 bytes: %s", verified.AgenticCapabilities)
			}
			if tc.wantVersion == AgenticCapabilitiesSchemaV2 && strings.Contains(string(verified.AgenticCapabilities), "toolSearchModes") {
				t.Fatalf("v2 persist must omit toolSearchModes: %s", verified.AgenticCapabilities)
			}
			policy, _, err := ParseToolDisclosurePolicy(verified.ToolDisclosurePolicy)
			if err != nil {
				t.Fatal(err)
			}
			if policy.Mode != tc.wantPolicy {
				t.Fatalf("policy mode=%q want %q raw=%s", policy.Mode, tc.wantPolicy, verified.ToolDisclosurePolicy)
			}
		})
	}
}

func TestVerifyFunctionCallingKeepsLegalCarryAllPolicy(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Keep Carry All")
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{ToolCalling: ToolCallingFunctionCalling}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	carry, err := CanonicalToolDisclosurePolicy(DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.UpdateDisclosurePolicy(context.Background(), DisclosurePolicyUpdate{
		WorkspaceID: first.WorkspaceID, ConfigID: first.ID, Policy: carry,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: first.LockVersion,
	})
	if err != nil {
		t.Fatalf("set carry_all: %v", err)
	}
	again, err := service.Verify(context.Background(), updated.WorkspaceID, updated.ID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("re-verify: %v", err)
	}
	if again.Status != StatusVerified || again.LastErrorCode != nil {
		t.Fatalf("re-verify must stay VERIFIED with nil error: %+v", again)
	}
	if again.LockVersion != updated.LockVersion+1 {
		t.Fatalf("re-verify must bump lock once, got %d want %d", again.LockVersion, updated.LockVersion+1)
	}
	policy, _, err := ParseToolDisclosurePolicy(again.ToolDisclosurePolicy)
	if err != nil || policy.Mode != DisclosureModeCarryAll {
		t.Fatalf("legal carry_all must be kept, got %+v err=%v raw=%s", policy, err, again.ToolDisclosurePolicy)
	}
	doc, _, err := ParseAgenticCapabilities(again.AgenticCapabilities)
	if err != nil || doc.ToolCalling != ToolCallingFunctionCalling {
		t.Fatalf("caps: %+v err=%v", doc, err)
	}
}

func TestVerifyNoneDoesNotLeaveToolSearchUnsupported(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "None No Stale Code")
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{ToolCalling: ToolCallingNone}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.LastErrorCode != nil {
		t.Fatalf("none VERIFIED must have nil last_error_code, got %v", *verified.LastErrorCode)
	}
	if !IsUnsetToolDisclosurePolicy(verified.ToolDisclosurePolicy) {
		t.Fatalf("none policy must be empty, got %s", verified.ToolDisclosurePolicy)
	}
}

func TestVerifyEmptyToolCallingFailsClosed(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Empty Probe Caps")
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Status != StatusError || got.LastErrorCode == nil || *got.LastErrorCode != ErrorCodeUpstream {
		t.Fatalf("empty ToolCalling must not stamp native, got %+v", got)
	}
	if !IsUnverifiedAgenticCapabilities(got.AgenticCapabilities) {
		t.Fatalf("empty ToolCalling must persist {{}}, got %s", got.AgenticCapabilities)
	}
}

func TestVerifyErrorLeavesExistingDisclosurePolicy(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Keep Policy On Error")
	ok, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{ToolCalling: ToolCallingFunctionCalling}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ok.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	carry, err := CanonicalToolDisclosurePolicy(DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.UpdateDisclosurePolicy(context.Background(), DisclosurePolicyUpdate{
		WorkspaceID: first.WorkspaceID, ConfigID: first.ID, Policy: carry,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: first.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	fail, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{}, ErrVerificationUpstream
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	errored, err := fail.Verify(context.Background(), updated.WorkspaceID, updated.ID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("error verify: %v", err)
	}
	if errored.Status != StatusError || errored.LastErrorCode == nil {
		t.Fatalf("want ERROR, got %+v", errored)
	}
	if !IsUnverifiedAgenticCapabilities(errored.AgenticCapabilities) {
		t.Fatalf("ERROR must clear caps, got %s", errored.AgenticCapabilities)
	}
	policy, _, err := ParseToolDisclosurePolicy(errored.ToolDisclosurePolicy)
	if err != nil || policy.Mode != DisclosureModeCarryAll {
		t.Fatalf("ERROR must keep carry_all, got %+v err=%v raw=%s", policy, err, errored.ToolDisclosurePolicy)
	}
	if errored.LockVersion != updated.LockVersion+1 {
		t.Fatalf("ERROR must still bump lock once, got %d want %d", errored.LockVersion, updated.LockVersion+1)
	}
}

func TestVerifyModelConfigRejectsStaleResult(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Verify Stale Model")
	service, err := NewVerificationService(repository, VerifierFunc(func(ctx context.Context, config Config) (AgenticCapabilities, error) {
		_, updateErr := repository.Update(ctx, config.WorkspaceID, config.ID, UpdateConfig{
			Name: "Changed During Verification", Provider: config.Provider, APIBase: config.APIBase,
			ModelName: config.ModelName, CredentialSecretID: config.CredentialSecretID,
			Options: config.Options, Status: StatusUnverified, UpdatedBy: repositoryOwnerID,
			ExpectedLockVersion: config.LockVersion,
		})
		return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, updateErr
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale verification conflict, got %v", err)
	}
	// Concurrent edit must leave no stale verified capability.
	after, err := repository.Get(context.Background(), created.WorkspaceID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != StatusUnverified || !IsUnverifiedAgenticCapabilities(after.AgenticCapabilities) {
		t.Fatalf("stale capability survived concurrent edit: status=%s caps=%s", after.Status, after.AgenticCapabilities)
	}
}

func TestUpdateClearsAgenticCapabilities(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Clear Caps Model")
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if IsUnverifiedAgenticCapabilities(verified.AgenticCapabilities) {
		t.Fatal("expected non-empty agentic capabilities after verify")
	}
	if verified.LastVerifiedAt == nil || verified.LastLatencyMS == nil {
		t.Fatal("expected verification evidence after verify")
	}
	updated, err := repository.Update(context.Background(), created.WorkspaceID, created.ID, UpdateConfig{
		Name: verified.Name, Provider: verified.Provider, APIBase: verified.APIBase,
		ModelName: verified.ModelName, CredentialSecretID: verified.CredentialSecretID,
		Options: verified.Options, RuntimeCapabilities: verified.RuntimeCapabilities,
		Status: StatusUnverified, UpdatedBy: repositoryOwnerID, ExpectedLockVersion: verified.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !IsUnverifiedAgenticCapabilities(updated.AgenticCapabilities) {
		t.Fatalf("update must clear agentic capabilities, got %s", updated.AgenticCapabilities)
	}
	if updated.Status != StatusUnverified {
		t.Fatalf("update must force UNVERIFIED, got %s", updated.Status)
	}
	if updated.LastVerifiedAt != nil || updated.LastLatencyMS != nil || updated.LastErrorCode != nil {
		t.Fatalf("update must clear all verification evidence, got verifiedAt=%v latency=%v err=%v",
			updated.LastVerifiedAt, updated.LastLatencyMS, updated.LastErrorCode)
	}
	// DB-level proof: all evidence columns null / empty in same CAS row.
	var caps []byte
	var lastAt sql.NullTime
	var lastLat sql.NullInt64
	var lastErr sql.NullString
	var status string
	if err := db.QueryRow(`
		SELECT agentic_capabilities, last_verified_at, last_latency_ms, last_error_code, status
		FROM model_configs WHERE id=$1
	`, created.ID).Scan(&caps, &lastAt, &lastLat, &lastErr, &status); err != nil {
		t.Fatal(err)
	}
	if string(caps) != "{}" || lastAt.Valid || lastLat.Valid || lastErr.Valid || status != string(StatusUnverified) {
		t.Fatalf("stale evidence survived update: caps=%s at=%v lat=%v err=%v status=%s",
			caps, lastAt, lastLat, lastErr, status)
	}
	if _, err := repository.Update(context.Background(), created.WorkspaceID, created.ID, UpdateConfig{
		Name: updated.Name, Provider: updated.Provider, APIBase: updated.APIBase,
		ModelName: updated.ModelName, Options: updated.Options,
		Status: StatusVerified, UpdatedBy: repositoryOwnerID, ExpectedLockVersion: updated.LockVersion,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected reject StatusVerified on Update, got %v", err)
	}
	if _, err := repository.Update(context.Background(), created.WorkspaceID, created.ID, UpdateConfig{
		Name: updated.Name, Provider: updated.Provider, APIBase: updated.APIBase,
		ModelName: updated.ModelName, Options: updated.Options,
		Status: StatusError, UpdatedBy: repositoryOwnerID, ExpectedLockVersion: updated.LockVersion,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected reject StatusError on Update, got %v", err)
	}
}

type testNetworkError struct{ message string }

func (e testNetworkError) Error() string { return e.message }
func (testNetworkError) Timeout() bool   { return false }
func (testNetworkError) Temporary() bool { return true }

// TestStateEvidenceCrossInvariants_DirectSQLCorruptFailClosed proves Get/List
// fail closed for every corrupt status×evidence combination required by Task3 fix4.
func TestStateEvidenceCrossInvariants_DirectSQLCorruptFailClosed(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Cross Invariant Model")

	// Fresh create is UNVERIFIED with empty evidence.
	got, err := repository.Get(context.Background(), created.WorkspaceID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnverified || got.LastVerifiedAt != nil || got.LastLatencyMS != nil || got.LastErrorCode != nil {
		t.Fatalf("create evidence: %+v", got)
	}
	if !IsUnverifiedAgenticCapabilities(got.AgenticCapabilities) {
		t.Fatalf("create caps: %s", got.AgenticCapabilities)
	}

	// --- UNVERIFIED corrupt combinations ---
	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"UNVERIFIED with last_verified_at", `UPDATE model_configs SET last_verified_at=clock_timestamp() WHERE id=$1`, []any{created.ID}},
		{"UNVERIFIED with last_latency_ms", `UPDATE model_configs SET last_latency_ms=1 WHERE id=$1`, []any{created.ID}},
		{"UNVERIFIED with last_error_code", `UPDATE model_configs SET last_error_code=$2 WHERE id=$1`, []any{created.ID, ErrorCodeUpstream}},
		{"UNVERIFIED with non-empty caps", `UPDATE model_configs SET agentic_capabilities=$2::jsonb, lock_version=2 WHERE id=$1`, []any{created.ID, mustVerifiedCapsJSON(t, created)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reset to clean UNVERIFIED first.
			if _, err := db.Exec(`
				UPDATE model_configs SET status='UNVERIFIED', agentic_capabilities='{}'::jsonb,
				  last_verified_at=NULL, last_latency_ms=NULL, last_error_code=NULL, lock_version=1
				WHERE id=$1`, created.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.sql, tc.args...); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Get want ErrInvalid, got %v", err)
			}
			if _, err := repository.List(context.Background(), created.WorkspaceID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("List want ErrInvalid, got %v", err)
			}
		})
	}

	// --- DISABLED corrupt combinations ---
	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"DISABLED with last_verified_at", `UPDATE model_configs SET status='DISABLED', last_verified_at=clock_timestamp() WHERE id=$1`, []any{created.ID}},
		{"DISABLED with last_latency_ms", `UPDATE model_configs SET status='DISABLED', last_latency_ms=0 WHERE id=$1`, []any{created.ID}},
		{"DISABLED with last_error_code", `UPDATE model_configs SET status='DISABLED', last_error_code=$2 WHERE id=$1`, []any{created.ID, ErrorCodeNetwork}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`
				UPDATE model_configs SET status='DISABLED', agentic_capabilities='{}'::jsonb,
				  last_verified_at=NULL, last_latency_ms=NULL, last_error_code=NULL, lock_version=1
				WHERE id=$1`, created.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.sql, tc.args...); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Get want ErrInvalid, got %v", err)
			}
		})
	}
	// Clean DISABLED is readable.
	if _, err := db.Exec(`
		UPDATE model_configs SET status='DISABLED', agentic_capabilities='{}'::jsonb,
		  last_verified_at=NULL, last_latency_ms=NULL, last_error_code=NULL, lock_version=1
		WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if dis, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); err != nil || dis.Status != StatusDisabled {
		t.Fatalf("clean DISABLED must read: %+v err=%v", dis, err)
	}

	// --- ERROR corrupt combinations ---
	// First write a valid ERROR row via production CAS.
	service, err := NewVerificationService(repository, VerifierFunc(func(context.Context, Config) (AgenticCapabilities, error) {
		return AgenticCapabilities{}, ErrUpstreamAuthentication
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Reset to UNVERIFIED for verify failure path.
	if _, err := db.Exec(`
		UPDATE model_configs SET status='UNVERIFIED', agentic_capabilities='{}'::jsonb,
		  last_verified_at=NULL, last_latency_ms=NULL, last_error_code=NULL, lock_version=1
		WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	errored, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if errored.Status != StatusError || errored.LastErrorCode == nil || *errored.LastErrorCode != ErrorCodeAuthentication {
		t.Fatalf("production ERROR write: %+v", errored)
	}
	if errored.LastVerifiedAt == nil || errored.LastLatencyMS == nil || *errored.LastLatencyMS < 0 {
		t.Fatalf("ERROR evidence incomplete: %+v", errored)
	}
	if !IsUnverifiedAgenticCapabilities(errored.AgenticCapabilities) {
		t.Fatalf("ERROR caps: %s", errored.AgenticCapabilities)
	}

	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"ERROR null last_verified_at", `UPDATE model_configs SET last_verified_at=NULL WHERE id=$1`},
		{"ERROR null last_latency_ms", `UPDATE model_configs SET last_latency_ms=NULL WHERE id=$1`},
		{"ERROR null last_error_code", `UPDATE model_configs SET last_error_code=NULL WHERE id=$1`},
		{"ERROR arbitrary uppercase code", `UPDATE model_configs SET last_error_code='SOMETHING_ELSE' WHERE id=$1`},
		{"ERROR free-text code", `UPDATE model_configs SET last_error_code='AUTH_FAILED' WHERE id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Restore valid ERROR base from production fields.
			if _, err := db.Exec(`
				UPDATE model_configs SET status='ERROR', agentic_capabilities='{}'::jsonb,
				  last_verified_at=GREATEST(created_at, clock_timestamp()), last_latency_ms=1,
				  last_error_code=$2 WHERE id=$1`, created.ID, ErrorCodeAuthentication); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.sql, created.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.Get(context.Background(), created.WorkspaceID, created.ID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Get want ErrInvalid, got %v", err)
			}
			if _, err := repository.List(context.Background(), created.WorkspaceID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("List want ErrInvalid, got %v", err)
			}
		})
	}

	// RecordVerification rejects non-allowlist error codes atomically.
	if _, err := repository.RecordVerification(context.Background(), VerificationUpdate{
		WorkspaceID: created.WorkspaceID, ConfigID: created.ID,
		Status: StatusError, LatencyMS: 1, ErrorCode: strPtr("NOT_A_REAL_CODE"),
		AgenticCapabilities: json.RawMessage(`{}`), VerifiedBy: repositoryOwnerID,
		ExpectedLockVersion: 99, // will fail invalid before CAS
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("RecordVerification must reject non-allowlist code, got %v", err)
	}
}

func strPtr(s string) *string { return &s }

func mustVerifiedCapsJSON(t *testing.T, cfg Config) string {
	t.Helper()
	// Build a non-empty capability document shape (may not match lock; used only as corrupt payload).
	at := time.Now().UTC().Truncate(time.Second)
	doc, err := CanonicalAgenticCapabilities(at, 1, WireConfigDigest(cfg))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
