package httptransport

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/storedobject"
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

func TestMapErrorObjectStorageUnavailable(t *testing.T) {
	mapped := mapError(fmt.Errorf("put permanent prompt object: %w", storedobject.ErrObjectStorage))
	if mapped.status != http.StatusServiceUnavailable || mapped.code != "OBJECT_STORAGE_UNAVAILABLE" {
		t.Fatalf("object storage: %+v", mapped)
	}
}
