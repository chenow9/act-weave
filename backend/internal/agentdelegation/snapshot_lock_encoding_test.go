package agentdelegation_test

import (
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
)

// Parser-level companion to the Bridge-level exotic/duplicate lock rows.
//
// snapshot_lock_matrix_test.go owns missing / null / string / zero / negative
// and the equality matrix. This file adds the two shapes it does not reach:
// JSON encodings that are valid numbers-ish but must not be read as a lock
// version, and duplicate raw keys on each of the three lock fields.
//
// Every row re-reads the mutated bytes to prove the mutation landed on the
// intended producer field, so a future rename fails loudly here instead of
// silently downgrading these rows into unknown-field tests.

type lockEncTarget struct {
	label string
	nest  string // "" means the node object itself
	field string
}

var lockEncTargets = []lockEncTarget{
	{"node_modelConfigLockVersion", "", "modelConfigLockVersion"},
	{"modelSnapshot_lockVersion", "modelSnapshot", "lockVersion"},
	{"agentSnapshot_modelConfigLockVer", "agentSnapshot", "modelConfigLockVer"},
}

func lockEncDecode(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func lockEncWrite(t *testing.T, graph json.RawMessage, tgt lockEncTarget, token string) json.RawMessage {
	t.Helper()
	m := lockEncDecode(t, graph)
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatalf("decode nodes: %v", err)
	}
	if tgt.nest == "" {
		nodes[0][tgt.field] = json.RawMessage(token)
	} else {
		nest := lockEncDecode(t, nodes[0][tgt.nest])
		nest[tgt.field] = json.RawMessage(token)
		nb, err := json.Marshal(nest)
		if err != nil {
			t.Fatal(err)
		}
		nodes[0][tgt.nest] = nb
	}
	nb, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = nb
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func lockEncRead(t *testing.T, graph json.RawMessage, tgt lockEncTarget) (string, bool) {
	t.Helper()
	m := lockEncDecode(t, graph)
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatalf("decode nodes: %v", err)
	}
	obj := nodes[0]
	if tgt.nest != "" {
		obj = lockEncDecode(t, nodes[0][tgt.nest])
	}
	v, ok := obj[tgt.field]
	return string(v), ok
}

// TestParseSnapshot_LockExoticEncodingsRejected covers JSON tokens that a
// lenient reader could coerce into a plausible lock version.
func TestParseSnapshot_LockExoticEncodingsRejected(t *testing.T) {
	base := validEmptyGraph(t)
	if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, base); err != nil {
		t.Fatalf("baseline must be valid: %v", err)
	}

	for _, tgt := range lockEncTargets {
		tgt := tgt
		current, ok := lockEncRead(t, base, tgt)
		if !ok {
			t.Fatalf("producer no longer emits %s.%s", tgt.nest, tgt.field)
		}
		// The first six tokens are built from the field's own value, so a lenient
		// reader would coerce each one to the number the other two lock fields
		// already hold. Without that the three-way equality check would reject
		// them and the row would prove nothing about encoding strictness.
		tokens := []struct{ name, token string }{
			{"decimal_same_value", current + ".0"},
			{"exponent_same_value", current + "e0"},
			{"quoted_same_value", `"` + current + `"`},
			{"quoted_padded_same_value", `" ` + current + `"`},
			{"quoted_plus_sign_same_value", `"+` + current + `"`},
			{"array_wrapped_same_value", "[" + current + "]"},
			{"boolean_true", "true"},
			{"empty_array", "[]"},
			{"empty_object", "{}"},
			{"fraction", "0.5"},
			{"negative_zero", "-0"},
			{"int64_overflow", "9223372036854775808"},
		}
		for _, tok := range tokens {
			tok := tok
			t.Run(tgt.label+"_"+tok.name, func(t *testing.T) {
				raw := lockEncWrite(t, base, tgt, tok.token)
				got, ok := lockEncRead(t, raw, tgt)
				if !ok || got != tok.token {
					t.Fatalf("mutation read-back = %q (present=%v), want %q", got, ok, tok.token)
				}
				if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, raw); err == nil {
					t.Fatalf("%s encoded as %s must fail closed", tgt.field, tok.token)
				}
			})
		}
	}
}

// TestParseSnapshot_LockDuplicateKeysRejected forges a second copy of each lock
// key. Whichever side a decoder prefers, the document must be rejected rather
// than resolved.
func TestParseSnapshot_LockDuplicateKeysRejected(t *testing.T) {
	base := validEmptyGraph(t)

	for _, tgt := range lockEncTargets {
		tgt := tgt
		current, ok := lockEncRead(t, base, tgt)
		if !ok {
			t.Fatalf("producer no longer emits %s.%s", tgt.nest, tgt.field)
		}
		needle := `"` + tgt.field + `":` + current
		if n := strings.Count(string(base), needle); n != 1 {
			t.Fatalf("expected exactly one %s in the producer document, found %d", needle, n)
		}
		for _, order := range []struct {
			name        string
			forgedFirst bool
		}{{"forged_first", true}, {"legit_first", false}} {
			order := order
			t.Run(tgt.label+"_dup_"+order.name, func(t *testing.T) {
				replacement := needle + `,"` + tgt.field + `":9`
				if order.forgedFirst {
					replacement = `"` + tgt.field + `":9,` + needle
				}
				raw := strings.Replace(string(base), needle, replacement, 1)
				if raw == string(base) {
					t.Fatal("duplicate-key mutation did not change the document")
				}
				if n := strings.Count(raw, `"`+tgt.field+`":`); n != 2 {
					t.Fatalf("expected 2 copies of %q, found %d", tgt.field, n)
				}
				if _, err := agentdelegation.ParseSnapshot(
					testFreezeWorkspaceID, json.RawMessage(raw),
				); err == nil {
					t.Fatalf("duplicate %s key must fail closed", tgt.field)
				}
			})
		}
	}
}

// TestParseSnapshot_LockEncodingPositiveControl proves the mutator itself is
// harmless: rewriting each lock with a plain integer keeps the graph valid.
func TestParseSnapshot_LockEncodingPositiveControl(t *testing.T) {
	base := validEmptyGraph(t)
	raw := base
	for _, tgt := range lockEncTargets {
		raw = lockEncWrite(t, raw, tgt, "4")
	}
	for _, tgt := range lockEncTargets {
		if got, ok := lockEncRead(t, raw, tgt); !ok || got != "4" {
			t.Fatalf("%s read-back %q present=%v", tgt.field, got, ok)
		}
	}
	if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, raw); err != nil {
		t.Fatalf("rewritten matching triple must still parse: %v", err)
	}
}
