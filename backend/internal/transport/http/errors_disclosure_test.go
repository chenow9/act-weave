package httptransport

import (
	"errors"
	"net/http"
	"testing"

	"actweave/backend/internal/modelconfig"
)

func TestMapErrorToolDisclosureRuntimeCodes(t *testing.T) {
	pending := mapError(modelconfig.ErrToolDisclosureRuntimePending)
	if pending.status != http.StatusUnprocessableEntity || pending.code != modelconfig.ErrorCodeToolDisclosureRuntimePending {
		t.Fatalf("runtime pending: %+v", pending)
	}
	wrapped := mapError(errors.Join(errors.New("wrap"), modelconfig.ErrAgentModelToolsUnsupported))
	if wrapped.status != http.StatusUnprocessableEntity || wrapped.code != modelconfig.ErrorCodeAgentModelToolsUnsupported {
		t.Fatalf("tools unsupported: %+v", wrapped)
	}
}
