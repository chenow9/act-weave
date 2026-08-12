package modelconfig

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The verified-capability digest is computed twice on two different byte shapes
// of the same document: once on the live row (jsonb text, `{"a": 1}`) and once
// on the frozen run snapshot, where encoding/json compacted the embedded
// RawMessage (`{"a":1}`). If those disagree, every real Agentic run fails its
// staleness check, so the digest must be byte-shape independent.
func TestWireConfigDigestIsStableAcrossJSONByteShape(t *testing.T) {
	spaced := json.RawMessage(`{"schemaVersion": "model-runtime.v1", "contextWindowTokens": 128000}`)
	compact := json.RawMessage(`{"schemaVersion":"model-runtime.v1","contextWindowTokens":128000}`)
	indented := json.RawMessage("{\n  \"schemaVersion\": \"model-runtime.v1\",\n  \"contextWindowTokens\": 128000\n}")

	base := Config{
		Provider: "openai", APIBase: "https://api.example.com/v1", ModelName: "gpt-test",
		Options: json.RawMessage(`{}`),
	}
	want := ""
	for name, raw := range map[string]json.RawMessage{
		"spaced": spaced, "compact": compact, "indented": indented,
	} {
		cfg := base
		cfg.RuntimeCapabilities = raw
		got := WireConfigDigest(cfg)
		if want == "" {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("%s byte shape changed the digest: %s vs %s", name, got, want)
		}
	}

	// Options must be covered by the same normalization.
	optSpaced, optCompact := base, base
	optSpaced.RuntimeCapabilities, optCompact.RuntimeCapabilities = compact, compact
	optSpaced.Options = json.RawMessage(`{"temperature": 0.5}`)
	optCompact.Options = json.RawMessage(`{"temperature":0.5}`)
	if WireConfigDigest(optSpaced) != WireConfigDigest(optCompact) {
		t.Fatal("options byte shape changed the digest")
	}

	// Positive control: normalization must not erase real differences.
	changed := base
	changed.RuntimeCapabilities = json.RawMessage(`{"schemaVersion":"model-runtime.v1","contextWindowTokens":64000}`)
	if WireConfigDigest(changed) == want {
		t.Fatal("different runtime capabilities produced the same digest")
	}
	changedModel := base
	changedModel.RuntimeCapabilities = compact
	changedModel.ModelName = "gpt-other"
	if WireConfigDigest(changedModel) == want {
		t.Fatal("different model name produced the same digest")
	}
	// Malformed JSON must not collapse onto the empty-object digest.
	broken := base
	broken.RuntimeCapabilities = json.RawMessage(`{not json`)
	empty := base
	empty.RuntimeCapabilities = nil
	if WireConfigDigest(broken) == WireConfigDigest(empty) {
		t.Fatal("malformed runtime capabilities collided with empty")
	}
}

// AgenticCapabilityLockMatches is the single definition of "this evidence
// belongs to this config". Verification CAS stamps the pre-CAS lock and then
// bumps lock_version, so the only live pair is (N, N+1).
func TestAgenticCapabilityLockMatchesFollowsVerificationCAS(t *testing.T) {
	doc, err := CanonicalAgenticCapabilities(
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), 4, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !AgenticCapabilityLockMatches(doc, 5) {
		t.Fatal("verifiedLockVersion=4 must match a config at lock 5")
	}
	for _, lock := range []int64{0, 1, 3, 4, 6, 100} {
		if AgenticCapabilityLockMatches(doc, lock) {
			t.Fatalf("verifiedLockVersion=4 must not match a config at lock %d", lock)
		}
	}
	// A config that has never been verified can never satisfy the relation.
	if AgenticCapabilityLockMatches(AgenticCapabilities{}, 2) {
		t.Fatal("empty capability document must not match")
	}
	// Lock 1 is unreachable for VERIFIED rows: create writes 1, verify bumps to 2.
	one, err := CanonicalAgenticCapabilities(
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), 1, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if AgenticCapabilityLockMatches(one, 1) {
		t.Fatal("lock 1 must not be treated as a verified pair")
	}
	if !AgenticCapabilityLockMatches(one, 2) {
		t.Fatal("the lowest live pair (1,2) must match")
	}
}
