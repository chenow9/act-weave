package httptransport

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestAAPSSERecoveryAcceptanceHTTP verifies the public Last-Event-ID boundary
// for every possible disconnect point in one finite persisted trace.
func TestAAPSSERecoveryAcceptanceHTTP(t *testing.T) {
	const eventCount = 8
	for cursor := 0; cursor <= eventCount; cursor++ {
		t.Run("after_sequence_"+strconv.Itoa(cursor), func(t *testing.T) {
			reader := newCatchUpReader(t, eventCount)
			response := requestCatchUp(t, reader, catchUpScope(), strconv.Itoa(cursor), "2")
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for sequence := 1; sequence <= eventCount; sequence++ {
				count := strings.Count(body, "id: "+strconv.Itoa(sequence)+"\n")
				want := 1
				if sequence <= cursor {
					want = 0
				}
				if count != want {
					t.Fatalf("cursor=%d sequence=%d count=%d want=%d body=%s",
						cursor, sequence, count, want, body)
				}
			}
			if strings.Count(body, "id: ") != eventCount-cursor {
				t.Fatalf("cursor=%d replay count=%d want=%d",
					cursor, strings.Count(body, "id: "), eventCount-cursor)
			}
		})
	}
}
