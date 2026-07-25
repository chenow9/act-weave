package httptransport

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"actweave/backend/internal/outboundidentity"
)

func TestMapErrorOutboundIdentityStableCodes(t *testing.T) {
	t.Parallel()

	type expect struct {
		status    int
		retryable bool
	}
	// Unique HTTP mapping from technical design §10.
	want := map[string]expect{
		outboundidentity.CodeIdentityPolicyInvalid:              {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeIdentityModeUnsupported:            {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeIdentityMigrationRequired:          {http.StatusConflict, false},
		outboundidentity.CodeIdentityConnectionNotReady:         {http.StatusConflict, false},
		outboundidentity.CodeIdentityPolicyChanged:              {http.StatusConflict, false},
		outboundidentity.CodeIdentityScopeNotAllowed:            {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeIdentityChangeConfirmationRequired: {http.StatusConflict, false},
		outboundidentity.CodeIdentityChangeConfirmationStale:    {http.StatusConflict, false},
		outboundidentity.CodeIdentityExecutorUnsupported:        {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeSubjectRequired:                    {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeCredentialRequired:                 {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeCredentialInvalid:                  {http.StatusBadRequest, false},
		outboundidentity.CodeCredentialTargetMismatch:           {http.StatusUnprocessableEntity, false},
		outboundidentity.CodeCredentialExpired:                  {http.StatusConflict, true},
		outboundidentity.CodeCredentialCapacityExceeded:         {http.StatusTooManyRequests, true},
		outboundidentity.CodeBrokerDenied:                       {http.StatusForbidden, false},
		outboundidentity.CodeBrokerUnavailable:                  {http.StatusServiceUnavailable, true},
		outboundidentity.CodeBusinessAuthorizationDenied:        {http.StatusForbidden, false},
		outboundidentity.CodeTargetRejected:                     {http.StatusUnprocessableEntity, false},
	}

	codes := outboundidentity.AllStableCodes()
	if len(codes) != len(want) {
		t.Fatalf("catalog size %d != expected mapping size %d", len(codes), len(want))
	}

	seenStatusCode := map[string]struct{}{}
	for _, code := range codes {
		exp, ok := want[code]
		if !ok {
			t.Fatalf("missing expected mapping for %s", code)
		}
		sentinel := outboundidentity.SentinelByCode(code)
		if sentinel == nil {
			t.Fatalf("missing sentinel for %s", code)
		}
		// Management surface.
		mapped := mapError(sentinel)
		if mapped.status != exp.status || mapped.code != code {
			t.Fatalf("mapError(%s)=%+v want status=%d", code, mapped, exp.status)
		}
		if mappedRetryable(mapped) != exp.retryable {
			t.Fatalf("mapError(%s) retryable=%v want %v", code, mappedRetryable(mapped), exp.retryable)
		}
		if strings.TrimSpace(mapped.message) == "" {
			t.Fatalf("empty message for %s", code)
		}
		if strings.Contains(mapped.message, "access_token") ||
			strings.Contains(mapped.message, "secret-") ||
			strings.Contains(mapped.message, "vault") {
			t.Fatalf("unsafe message for %s: %s", code, mapped.message)
		}
		// AAP surface must share the same stable outbound vocabulary.
		aapMapped := mapAAPError(sentinel)
		if aapMapped.status != mapped.status || aapMapped.code != mapped.code {
			t.Fatalf("AAP mapping drift for %s: mgmt=%+v aap=%+v", code, mapped, aapMapped)
		}
		// Wrapped errors still map.
		wrapped := mapError(errors.Join(errors.New("wrap"), sentinel))
		if wrapped.code != code {
			t.Fatalf("wrapped %s mapped to %s", code, wrapped.code)
		}
		key := code
		if _, dup := seenStatusCode[key]; dup {
			t.Fatalf("duplicate code entry %s", key)
		}
		seenStatusCode[key] = struct{}{}
	}

	// 409 credential expired is retryable even though generic 409 is not.
	expired := mapError(outboundidentity.ErrCredentialExpired)
	if expired.status != http.StatusConflict || mappedRetryable(expired) != true {
		t.Fatalf("EXPIRED must be 409 retryable, got %+v retryable=%v", expired, mappedRetryable(expired))
	}
}
