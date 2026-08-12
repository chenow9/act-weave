package execution

import (
	"encoding/json"
	"testing"
)

// The assembly digest hashes included_segments as text, and the column is JSONB:
// Postgres re-spaces the document and reorders object keys by (length, bytes)
// while encoding/json writes them alphabetically and compact. Before
// canonicalization the digest recomputed in GetByRun never matched the digest
// written milliseconds earlier, so every Agentic initial run — where the
// manifest write is mandatory — failed against a real database.
func TestAssemblyDigestSurvivesJSONBRoundTrip(t *testing.T) {
	base := ContextAssemblyRecord{
		WorkspaceID: "b18f1f2e-7b5a-7c3d-8e9f-123456789002",
		RunID:       "b18f1f2e-7b5a-7c3d-8e9f-123456789005",
		SessionID:   "b18f1f2e-7b5a-7c3d-8e9f-123456789007",
		Mode:        "token_window",
	}
	// As encoding/json writes it (alphabetical, compact).
	written := base
	written.IncludedSegments = json.RawMessage(
		`[{"contentHash":"ab","messageId":"m1","role":"USER"}]`)
	// As jsonb hands it back (length-then-byte key order, spaced).
	readBack := base
	readBack.IncludedSegments = json.RawMessage(
		`[{"role": "USER", "messageId": "m1", "contentHash": "ab"}]`)

	writtenDigest := ComputeAssemblyDigest(normalizeAssemblyRecordForRead(written))
	readDigest := ComputeAssemblyDigest(normalizeAssemblyRecordForRead(readBack))
	if writtenDigest != readDigest {
		t.Fatalf("jsonb round trip changed the assembly digest:\nwritten=%s\n   read=%s",
			writtenDigest, readDigest)
	}

	// Positive control: content differences must still change the digest.
	other := base
	other.IncludedSegments = json.RawMessage(
		`[{"contentHash":"ab","messageId":"m2","role":"USER"}]`)
	if ComputeAssemblyDigest(normalizeAssemblyRecordForRead(other)) == writtenDigest {
		t.Fatal("a different messageId produced the same digest")
	}
	extra := base
	extra.IncludedSegments = json.RawMessage(
		`[{"contentHash":"ab","messageId":"m1","role":"USER"},` +
			`{"contentHash":"cd","messageId":"m2","role":"ASSISTANT"}]`)
	if ComputeAssemblyDigest(normalizeAssemblyRecordForRead(extra)) == writtenDigest {
		t.Fatal("an extra segment produced the same digest")
	}
	// Ordering of the array itself is meaningful and must not be normalized away.
	reordered := base
	reordered.IncludedSegments = json.RawMessage(
		`[{"contentHash":"cd","messageId":"m2","role":"ASSISTANT"},` +
			`{"contentHash":"ab","messageId":"m1","role":"USER"}]`)
	if ComputeAssemblyDigest(normalizeAssemblyRecordForRead(reordered)) ==
		ComputeAssemblyDigest(normalizeAssemblyRecordForRead(extra)) {
		t.Fatal("segment array order was normalized away")
	}
	// Numeric literals must survive verbatim (no float64 rewrite).
	big := base
	big.SummaryCoverage = json.RawMessage(`{"tokens": 9007199254740993}`)
	if got := string(normalizeAssemblyRecordForRead(big).SummaryCoverage); got !=
		`{"tokens":9007199254740993}` {
		t.Fatalf("numeric literal was rewritten: %s", got)
	}
	// Malformed JSON is left alone rather than normalized into a valid shape.
	bad := base
	bad.IncludedSegments = json.RawMessage(`[{"role":`)
	if got := string(normalizeAssemblyRecordForRead(bad).IncludedSegments); got != `[{"role":` {
		t.Fatalf("malformed segments were rewritten: %s", got)
	}
}
