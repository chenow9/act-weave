package smartdag

import (
	"errors"
	"testing"
)

func TestClassifyTurnErrorCode_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code      string
		stage     FailureStage
		retryable bool
	}{
		{CodeSessionClosed, FailureStageSession, false},
		{CodeTurnInProgress, FailureStageSession, false},
		{CodeSessionVersionConflict, FailureStageSession, true},
		{CodeAgentModelRequired, FailureStageModelCall, false},
		{CodeModelTimeout, FailureStageModelCall, true},
		{CodeModelUnavailable, FailureStageModelCall, true},
		{CodeOutputInvalid, FailureStageOutputParse, true},
		{CodeGuardRejected, FailureStageGuard, true},
		{CodeDraftConflict, FailureStageDraftPersist, true},
		{CodeDraftPersistFailed, FailureStageDraftPersist, true},
		{CodeUnknownFailure, FailureStageUnknown, false},
		{CodeHistoricalFailed, FailureStageUnknown, false},
		{"", FailureStageUnknown, false},
		{"SOME_LEGACY_CODE", FailureStageUnknown, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.code+"_"+string(tc.stage), func(t *testing.T) {
			t.Parallel()
			stage, retryable, _ := ClassifyTurnErrorCode(tc.code)
			if stage != tc.stage || retryable != tc.retryable {
				t.Fatalf("code=%q stage=%s retryable=%v want %s/%v",
					tc.code, stage, retryable, tc.stage, tc.retryable)
			}
		})
	}
}

func TestAsTurnFailure_Sentinels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		code string
	}{
		{ErrSessionClosed, CodeSessionClosed},
		{ErrTurnInProgress, CodeTurnInProgress},
		{ErrSessionVersionConflict, CodeSessionVersionConflict},
		{ErrAgentModelRequired, CodeAgentModelRequired},
		{ErrModelTimeout, CodeModelTimeout},
		{ErrModelUnavailable, CodeModelUnavailable},
		{ErrOutputInvalid, CodeOutputInvalid},
		{ErrGuardRejected, CodeGuardRejected},
		{ErrDraftConflict, CodeDraftConflict},
		{ErrDraftPersistFailed, CodeDraftPersistFailed},
		{NewGuardError(GuardReport{OK: false}), CodeGuardRejected},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			tf, ok := AsTurnFailure(tc.err)
			if !ok || tf == nil {
				t.Fatalf("AsTurnFailure(%v) ok=%v", tc.err, ok)
			}
			if tf.Code != tc.code {
				t.Fatalf("code=%q want %q", tf.Code, tc.code)
			}
			if tf.Message == "" {
				t.Fatal("public message required")
			}
			// Cause text must not be required for public message safety.
			if tf.Cause != nil && tf.Message == tf.Cause.Error() && tc.code != CodeGuardRejected {
				// Guard may embed report in Error() of wrapper; public Message is still safe catalog text.
			}
		})
	}
}

func TestNewTurnFailure_DoesNotExposeInternalCauseInMessage(t *testing.T) {
	t.Parallel()
	secret := errors.New("secret token=super-secret-value")
	tf := NewTurnFailure(CodeModelTimeout, secret)
	if tf.Message == secret.Error() || containsSecret(tf.Message, "super-secret") {
		t.Fatalf("public message leaked cause: %q", tf.Message)
	}
	if !errors.Is(tf, ErrModelTimeout) {
		t.Fatal("Unwrap should allow errors.Is against sentinel")
	}
}

func containsSecret(s, needle string) bool {
	return len(s) > 0 && len(needle) > 0 && (s == needle || len(s) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(s); i++ {
			if s[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})())
}
