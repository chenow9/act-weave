package sse_test

import (
	"bytes"
	"strings"
	"testing"

	"actweave/backend/internal/transport/sse"
)

func BenchmarkAAPSSERecoveryEncode(b *testing.B) {
	event := persistedSSEEvent(b, strings.Repeat("x", 256))
	encoder := sse.NewEncoder()
	var output bytes.Buffer
	b.ReportAllocs()
	b.SetBytes(int64(len(event.Payload)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		output.Reset()
		if err := encoder.Encode(&output, event); err != nil {
			b.Fatal(err)
		}
	}
}
