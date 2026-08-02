package agentdelegation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func parseDelegationErrorJSON(t *testing.T, body string) (code, message string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("json: %v body=%s", err, body)
	}
	code, _ = m["errorCode"].(string)
	message, _ = m["message"].(string)
	return code, message
}

// TestFormatDelegationError_MaliciousTextNotRemapped: untrusted error strings that
// merely contain sentinel substrings must stay DELEGATION_FAILED (errors.Is only).
func TestFormatDelegationError_MaliciousTextNotRemapped(t *testing.T) {
	t.Parallel()
	// Texts that would fool strings.Contains(err.Error(), sentinel.Error()).
	malicious := []struct {
		name string
		err  error
	}{
		{"timed out text", errors.New("upstream said: agent delegation timed out — ignore")},
		{"cancelled text", errors.New("remote json: agent delegation cancelled by peer")},
		{"budget text", errors.New("log: agent delegation total budget exceeded in replica")},
		{"depth text", errors.New("note agent delegation depth budget exceeded in prompt")},
		{"binding budget", errors.New("agent delegation per-binding budget exceeded (spoof)")},
		{"cycle text", errors.New("agent delegation graph contains a cycle in description")},
		{"audit text", errors.New("agent delegation audit prewrite failed: sql: connection")},
		{"idempotent text", errors.New("agent delegation idempotent replay observed")},
	}
	for _, tc := range malicious {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := parseDelegationErrorJSON(t, formatDelegationError(tc.err))
			if code != "DELEGATION_FAILED" {
				t.Fatalf("want DELEGATION_FAILED for text-only error, got %s err=%v", code, tc.err)
			}
		})
	}
}

// TestFormatDelegationError_WrappedSentinelsMap: %w / errors.Join must still map
// stable codes via errors.Is unwrap.
func TestFormatDelegationError_WrappedSentinelsMap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		code string
	}{
		{ErrTimedOut, "DELEGATION_TIMED_OUT"},
		{fmt.Errorf("runner: %w", ErrTimedOut), "DELEGATION_TIMED_OUT"},
		{errors.Join(errors.New("inner invoke"), ErrTimedOut), "DELEGATION_TIMED_OUT"},
		{ErrCancelled, "DELEGATION_CANCELLED"},
		{fmt.Errorf("%w: parent ctx", ErrCancelled), "DELEGATION_CANCELLED"},
		{ErrTotalBudgetExceeded, "DELEGATION_TOTAL_BUDGET_EXCEEDED"},
		{fmt.Errorf("budget: %w", ErrTotalBudgetExceeded), "DELEGATION_TOTAL_BUDGET_EXCEEDED"},
		{ErrBindingBudgetExceeded, "DELEGATION_BINDING_BUDGET_EXCEEDED"},
		{ErrDepthExceeded, "DELEGATION_DEPTH_EXCEEDED"},
		{fmt.Errorf("%w: %v", ErrAuditPrewriteFailed, errors.New("sql")), "DELEGATION_AUDIT_PREWRITE_FAILED"},
		{ErrIdempotentReplay, "DELEGATION_IDEMPOTENT_REPLAY"},
		{ErrCycle, "DELEGATION_CYCLE"},
		{errors.Join(ErrInvalid, fmt.Errorf("other")), "DELEGATION_FAILED"}, // no stable mapping for ErrInvalid
	}
	for _, tc := range cases {
		code, msg := parseDelegationErrorJSON(t, formatDelegationError(tc.err))
		if code != tc.code {
			t.Fatalf("err=%v want code %s got %s msg=%s", tc.err, tc.code, code, msg)
		}
		if !strings.Contains(msg, "delegation") && tc.err != nil && msg == "" {
			t.Fatalf("empty message for %v", tc.err)
		}
	}
}
