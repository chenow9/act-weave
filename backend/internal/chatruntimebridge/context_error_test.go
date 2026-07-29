package chatruntimebridge

import (
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/execution"
)

func TestUserSafeBridgeErrorContextCodes(t *testing.T) {
	err := execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
	msg := userSafeBridgeError(err)
	if !strings.Contains(msg, "缩短输入") {
		t.Fatalf("unexpected message: %s", msg)
	}
	if strings.Contains(strings.ToLower(msg), "provider") {
		t.Fatalf("leaky: %s", msg)
	}
	code := executionErrorCode(err)
	if code != execution.ErrCodeContextRequiredInputTooLarge {
		t.Fatalf("code: %s", code)
	}
}

func TestUserSafeBridgeErrorWrappedContextCode(t *testing.T) {
	cause := errors.New("CONTEXT_SNAPSHOT_UNSUPPORTED: bad version")
	if executionErrorCode(cause) != execution.ErrCodeContextSnapshotUnsupported {
		t.Fatalf("code: %s", executionErrorCode(cause))
	}
}
