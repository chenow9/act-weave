package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/chatruntimebridge"
)

// Lock-field mutation kit with read-back self-checks, plus the Bridge-level lock
// coverage that the parser-level matrix already had.
//
// Why the read-back matters: every row below builds hostile JSON and asserts a
// rejection. Without proving the mutation landed on the intended producer field,
// renaming that field (say modelConfigLockVer -> modelConfigLockVersion) would
// leave every row passing, because the stale key is now an *unknown* field and
// unknown fields are also rejected. The suite would stay green while the lock
// check it claims to guard had stopped running. lkMutate closes that hole by
// reading the mutated bytes back and asserting both that the target changed to
// the intended token and that nothing else moved.

// lkAbsent is the pseudo-token meaning "delete this key".
const lkAbsent = "\x00ABSENT\x00"

// lkTarget names one of the three producer lock fields. They deliberately live
// at different nesting levels and under different names, which is exactly why a
// rename is plausible and why the read-back is worth its weight.
type lkTarget struct {
	label string
	nest  string // "" for the node object itself
	field string
}

var (
	lkNode  = lkTarget{label: "node", nest: "", field: "modelConfigLockVersion"}
	lkModel = lkTarget{label: "model", nest: "modelSnapshot", field: "lockVersion"}
	lkAgent = lkTarget{label: "agent", nest: "agentSnapshot", field: "modelConfigLockVer"}
)

var lkAllTargets = []lkTarget{lkNode, lkModel, lkAgent}

func lkObj(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return m
}

func lkRootNode(t *testing.T, graph string) (map[string]json.RawMessage, []map[string]json.RawMessage) {
	t.Helper()
	m := lkObj(t, json.RawMessage(graph))
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil {
		t.Fatalf("decode nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("graph fixture has no nodes to mutate")
	}
	return m, nodes
}

func lkPack(t *testing.T, m map[string]json.RawMessage, nodes []map[string]json.RawMessage) string {
	t.Helper()
	nb, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = nb
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// lkWrite puts one raw JSON token into one lock field of the root node.
func lkWrite(t *testing.T, graph string, tgt lkTarget, token string) string {
	t.Helper()
	m, nodes := lkRootNode(t, graph)
	apply := func(obj map[string]json.RawMessage) {
		if token == lkAbsent {
			delete(obj, tgt.field)
			return
		}
		obj[tgt.field] = json.RawMessage(token)
	}
	if tgt.nest == "" {
		apply(nodes[0])
	} else {
		nest := lkObj(t, nodes[0][tgt.nest])
		apply(nest)
		nb, err := json.Marshal(nest)
		if err != nil {
			t.Fatal(err)
		}
		nodes[0][tgt.nest] = nb
	}
	return lkPack(t, m, nodes)
}

// lkRead reports the raw token currently stored in a lock field, reading the
// very bytes that will be handed to Bridge.Execute rather than a copy the
// mutator happened to touch. Returns lkAbsent when the key is gone.
func lkRead(t *testing.T, graph string, tgt lkTarget) string {
	t.Helper()
	_, nodes := lkRootNode(t, graph)
	obj := nodes[0]
	if tgt.nest != "" {
		obj = lkObj(t, nodes[0][tgt.nest])
	}
	v, ok := obj[tgt.field]
	if !ok {
		return lkAbsent
	}
	return string(v)
}

// lkFingerprint captures every lock token plus the identity fields that no lock
// mutation is allowed to disturb. Comparing fingerprints before and after a
// mutation is how collateral damage gets caught.
func lkFingerprint(t *testing.T, graph string) map[string]string {
	t.Helper()
	m, nodes := lkRootNode(t, graph)
	fp := map[string]string{}
	for _, tgt := range lkAllTargets {
		fp["lock:"+tgt.label] = lkRead(t, graph, tgt)
	}
	for _, k := range []string{"schemaVersion", "rootAgentId", "maxDepth", "maxTotalDelegations",
		"maxPerBinding", "builtAt", "edges", "remotesFrozen", "frozenRemotesByCaller"} {
		fp["root:"+k] = string(m[k])
	}
	fp["nodes:count"] = itoa(int64(len(nodes)))
	for _, k := range []string{"agentId", "modelConfigId", "depth", "capabilitySnapshot"} {
		fp["node:"+k] = string(nodes[0][k])
	}
	modelSnap := lkObj(t, nodes[0]["modelSnapshot"])
	for _, k := range []string{"id", "provider", "apiBase", "modelName"} {
		fp["modelSnapshot:"+k] = string(modelSnap[k])
	}
	agentSnap := lkObj(t, nodes[0]["agentSnapshot"])
	for _, k := range []string{"schemaVersion", "agentId", "modelConfigId"} {
		fp["agentSnapshot:"+k] = string(agentSnap[k])
	}
	return fp
}

// lkMutate is the guarded mutator every row in this file goes through.
//
//  1. the target must exist beforehand (a renamed field aborts the row instead
//     of silently degrading it into an unknown-field test),
//  2. the mutation must actually change the bytes,
//  3. reading the result back must yield exactly the requested token, and
//  4. no other lock or identity field may have moved.
func lkMutate(t *testing.T, graph string, tgt lkTarget, token string) string {
	t.Helper()
	if before := lkRead(t, graph, tgt); before == lkAbsent {
		t.Fatalf("lock field %s.%s is absent from the producer fixture; "+
			"this row would test 'unknown field rejected', not lock validation", tgt.nest, tgt.field)
	}
	before := lkFingerprint(t, graph)

	out := lkWrite(t, graph, tgt, token)
	if out == graph {
		t.Fatalf("mutation of %s did not change the document", tgt.label)
	}
	if got := lkRead(t, out, tgt); got != token {
		t.Fatalf("read-back of %s.%s = %q, want %q", tgt.nest, tgt.field, got, token)
	}
	after := lkFingerprint(t, out)
	for k, want := range before {
		if k == "lock:"+tgt.label {
			continue
		}
		if after[k] != want {
			t.Fatalf("mutation of %s collaterally changed %s: %q -> %q", tgt.label, k, want, after[k])
		}
	}
	return out
}

// lkSetTriple writes all three locks and read-back-verifies each one.
func lkSetTriple(t *testing.T, graph string, node, model, agent int64) string {
	t.Helper()
	want := map[string]int64{lkNode.label: node, lkModel.label: model, lkAgent.label: agent}
	out := graph
	for _, tgt := range lkAllTargets {
		out = lkWrite(t, out, tgt, itoa(want[tgt.label]))
	}
	for _, tgt := range lkAllTargets {
		if got := lkRead(t, out, tgt); got != itoa(want[tgt.label]) {
			t.Fatalf("triple write: %s = %q want %d", tgt.label, got, want[tgt.label])
		}
	}
	return out
}

// lkDuplicateKey forges a second copy of one lock key through raw text surgery,
// since a Go map cannot hold duplicate keys. forgedFirst controls whether the
// hostile value precedes the legitimate one, which distinguishes first-key-wins
// from last-key-wins decoders.
func lkDuplicateKey(t *testing.T, graph string, tgt lkTarget, forgedFirst bool) string {
	t.Helper()
	current := lkRead(t, graph, tgt)
	if current == lkAbsent {
		t.Fatalf("lock field %s.%s absent; duplicate-key row would be vacuous", tgt.nest, tgt.field)
	}
	needle := `"` + tgt.field + `":` + current
	if n := strings.Count(graph, needle); n != 1 {
		t.Fatalf("expected exactly one occurrence of %s in the producer document, found %d "+
			"(field renamed or fixture reshaped)", needle, n)
	}
	const forged = "9"
	replacement := `"` + tgt.field + `":` + current + `,"` + tgt.field + `":` + forged
	if forgedFirst {
		replacement = `"` + tgt.field + `":` + forged + `,"` + tgt.field + `":` + current
	}
	out := strings.Replace(graph, needle, replacement, 1)
	if out == graph {
		t.Fatal("duplicate-key mutation did not change the document")
	}
	// Read back on the raw text: both copies must be present, and the legitimate
	// value must still be one of them (otherwise the row proves nothing about
	// duplicate handling).
	if n := strings.Count(out, `"`+tgt.field+`":`); n != 2 {
		t.Fatalf("expected 2 copies of %q after mutation, found %d", tgt.field, n)
	}
	if !strings.Contains(out, `"`+tgt.field+`":`+current) ||
		!strings.Contains(out, `"`+tgt.field+`":`+forged) {
		t.Fatalf("duplicate-key mutation lost a value: %s", out)
	}
	return out
}

// TestAgenticInitial_LockReadbackDeviceIsEffective is the meta-test for the kit
// above: it proves the guards fire instead of being decoration. Without this a
// broken read-back would silently disarm every row in this file.
func TestAgenticInitial_LockReadbackDeviceIsEffective(t *testing.T) {
	graph := string(newAgenticFixture(t, nil).run.AgentGraphSnapshot)

	t.Run("read_reflects_write_for_every_target", func(t *testing.T) {
		for _, tgt := range lkAllTargets {
			out := lkWrite(t, graph, tgt, "42")
			if got := lkRead(t, out, tgt); got != "42" {
				t.Fatalf("%s: read-back %q", tgt.label, got)
			}
			gone := lkWrite(t, graph, tgt, lkAbsent)
			if got := lkRead(t, gone, tgt); got != lkAbsent {
				t.Fatalf("%s: delete read-back %q", tgt.label, got)
			}
		}
	})

	t.Run("write_to_one_target_leaves_the_other_two_alone", func(t *testing.T) {
		for _, tgt := range lkAllTargets {
			out := lkMutate(t, graph, tgt, "42")
			for _, other := range lkAllTargets {
				if other == tgt {
					continue
				}
				if lkRead(t, out, other) != lkRead(t, graph, other) {
					t.Fatalf("writing %s disturbed %s", tgt.label, other.label)
				}
			}
		}
	})

	t.Run("fingerprint_detects_a_non_lock_edit", func(t *testing.T) {
		before := lkFingerprint(t, graph)
		tampered := setGraphNodeKey(t, graph, "depth", "5")
		after := lkFingerprint(t, tampered)
		if before["node:depth"] == after["node:depth"] {
			t.Fatal("fingerprint did not observe the depth change")
		}
	})

	t.Run("producer_field_names_still_exist", func(t *testing.T) {
		// The whole kit rests on these three names matching the producer. If a
		// rename lands, this fails loudly here instead of quietly turning every
		// hostile row into an unknown-field test.
		for _, tgt := range lkAllTargets {
			if lkRead(t, graph, tgt) == lkAbsent {
				t.Fatalf("producer no longer emits %s.%s", tgt.nest, tgt.field)
			}
		}
	})
}

// TestAgenticInitial_LockThreeWayPermutations covers every ordering of three
// distinct locks at the Bridge level. Only 3/1/2 was covered before, which
// leaves five orderings where an implementation comparing the wrong pair (or
// only min/max) would still pass.
func TestAgenticInitial_LockThreeWayPermutations(t *testing.T) {
	perms := [][3]int64{
		{1, 2, 3}, {1, 3, 2}, {2, 1, 3}, {2, 3, 1}, {3, 1, 2}, {3, 2, 1},
	}
	rows := make([]runLevelRow, 0, len(perms))
	for _, p := range perms {
		p := p
		rows = append(rows, runLevelRow{
			name: "node" + itoa(p[0]) + "_model" + itoa(p[1]) + "_agent" + itoa(p[2]),
			mutate: func(t *testing.T, f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(
					lkSetTriple(t, string(f.run.AgentGraphSnapshot), p[0], p[1], p[2]))
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		})
	}
	runRunLevelRows(t, rows)
}

// lkExoticTokens are JSON encodings that must never be accepted as a lock
// version.
//
// The first group is deliberately built from the fixture's own lock value, so a
// lenient reader would coerce each one to exactly the number the other two lock
// fields already hold. That matters: if these tokens decoded to some *other*
// number, the three-way equality check would reject them and the row would tell
// us nothing about encoding strictness. Value-matching tokens can only be caught
// by the decoder itself.
//
// The second group cannot represent the lock value at all and covers the type
// and domain rules instead.
func lkExoticTokens(lock int64) []struct{ name, token string } {
	v := itoa(lock)
	return []struct{ name, token string }{
		// Coerce to the correct value under a lenient reader.
		{"decimal_same_value", v + ".0"},
		{"exponent_same_value", v + "e0"},
		{"quoted_same_value", `"` + v + `"`},
		{"quoted_padded_same_value", `" ` + v + `"`},
		{"quoted_plus_sign_same_value", `"+` + v + `"`},
		{"array_wrapped_same_value", "[" + v + "]"},
		// Cannot represent the lock value at all.
		{"boolean_true", "true"},
		{"empty_array", "[]"},
		{"empty_object", "{}"},
		{"fraction", "0.5"},
		{"negative_zero", "-0"},
		{"int64_overflow", "9223372036854775808"},
	}
}

// TestAgenticInitial_LockExoticEncodings drives all three lock fields through
// every exotic encoding on the real Bridge.Execute path.
func TestAgenticInitial_LockExoticEncodings(t *testing.T) {
	tokens := lkExoticTokens(testModelLockVersion)
	rows := make([]runLevelRow, 0, len(lkAllTargets)*len(tokens))
	for _, tgt := range lkAllTargets {
		tgt := tgt
		for _, tok := range tokens {
			tok := tok
			rows = append(rows, runLevelRow{
				name: tgt.label + "_" + tok.name,
				mutate: func(t *testing.T, f *agenticFixture) {
					f.run.AgentGraphSnapshot = json.RawMessage(
						lkMutate(t, string(f.run.AgentGraphSnapshot), tgt, tok.token))
				},
				wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
			})
		}
	}
	runRunLevelRows(t, rows)
}

// TestAgenticInitial_LockDuplicateJSONKeys covers duplicate raw keys on each of
// the three lock fields, in both orderings. A last-key-wins decoder accepts the
// forged-first form; a first-key-wins decoder accepts the legit-first form.
// Both must fail closed.
func TestAgenticInitial_LockDuplicateJSONKeys(t *testing.T) {
	rows := make([]runLevelRow, 0, len(lkAllTargets)*2)
	for _, tgt := range lkAllTargets {
		tgt := tgt
		for _, order := range []struct {
			suffix      string
			forgedFirst bool
		}{{"forged_first", true}, {"legit_first", false}} {
			order := order
			rows = append(rows, runLevelRow{
				name: tgt.label + "_dup_" + order.suffix,
				mutate: func(t *testing.T, f *agenticFixture) {
					f.run.AgentGraphSnapshot = json.RawMessage(
						lkDuplicateKey(t, string(f.run.AgentGraphSnapshot), tgt, order.forgedFirst))
				},
				wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
			})
		}
	}
	runRunLevelRows(t, rows)
}

// TestAgenticInitial_LockMatchingTripleStillProceeds keeps the exotic/duplicate
// rows honest: the same kit that builds them can also build a document that
// executes, so none of the rejections above come from a mutator that simply
// mangles the JSON.
func TestAgenticInitial_LockMatchingTripleStillProceeds(t *testing.T) {
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.AgentGraphSnapshot = json.RawMessage(lkSetTriple(t,
			string(f.run.AgentGraphSnapshot), testModelLockVersion, testModelLockVersion, testModelLockVersion))
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("kit-rebuilt matching triple must proceed: %v", err)
	}
	if f.agentic.calls.Load() != 1 || f.classic.calls.Load() != 0 {
		t.Fatalf("agentic=%d classic=%d", f.agentic.calls.Load(), f.classic.calls.Load())
	}
}
