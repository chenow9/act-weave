package agentdelegation_test

import (
	"encoding/json"
	"testing"

	"actweave/backend/internal/agentdelegation"
)

// TestParseSnapshot_LockIdentityCompleteMatrix is the permanent data-driven
// lock-closure regression suite (cycle 9). For each of the three producer lock
// fields, every invalid representation is tested independently; equality cases
// cover pairwise and 3-way divergence plus a fully matching positive control.
//
// Prior cycle 1–8 structural/semantic matrices remain elsewhere; this suite
// owns complete lock coverage so the permanent matrix cannot silently shrink.
func TestParseSnapshot_LockIdentityCompleteMatrix(t *testing.T) {
	base := validEmptyGraph(t)
	if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, base); err != nil {
		t.Fatalf("baseline valid graph: %v", err)
	}

	// --- Positive control: all three layers equal ---
	t.Run("valid_three_layer_match", func(t *testing.T) {
		raw := setThreeLocks(t, base, 1, 1, 1)
		if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(raw)); err != nil {
			t.Fatalf("matching locks must accept: %v", err)
		}
		// Also prove non-default matching value.
		raw7 := setThreeLocks(t, base, 7, 7, 7)
		if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(raw7)); err != nil {
			t.Fatalf("matching lock=7 must accept: %v", err)
		}
	})

	type fieldSpec struct {
		name   string
		target lockTarget
	}
	fields := []fieldSpec{
		{"node_modelConfigLockVersion", lockTargetNode},
		{"modelSnapshot_lockVersion", lockTargetModel},
		{"agentSnapshot_modelConfigLockVer", lockTargetAgent},
	}
	// Invalid representations applied independently to one field while the other
	// two remain valid and equal (1).
	invalids := []struct {
		name string
		val  string // raw JSON token, or "" meaning delete
	}{
		{"missing", ""},
		{"null", `null`},
		{"wrong_type_string", `"1"`},
		{"zero", `0`},
		{"negative", `-1`},
	}

	for _, f := range fields {
		f := f
		for _, inv := range invalids {
			inv := inv
			t.Run(f.name+"_"+inv.name, func(t *testing.T) {
				raw := mutateOneLock(t, base, f.target, inv.val)
				if raw == string(base) {
					t.Fatal("mutation did not change raw")
				}
				_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(raw))
				if err == nil {
					t.Fatalf("expected reject for %s %s", f.name, inv.name)
				}
			})
		}
	}

	// Equality / divergence cases
	equality := []struct {
		name       string
		node, m, a int64
	}{
		{"node_model_mismatch", 3, 1, 3},
		{"node_agent_mismatch", 3, 3, 1},
		{"model_agent_mismatch_node_ok", 1, 2, 3}, // all three diverge if node kept at 1? actually 1/2/3 is 3-way
		{"three_way_divergent_3_1_2", 3, 1, 2},
		{"three_way_divergent_1_2_3", 1, 2, 3},
		{"node_only_high", 9, 1, 1},
		{"model_only_high", 1, 9, 1},
		{"agent_only_high", 1, 1, 9},
	}
	for _, tc := range equality {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw := setThreeLocks(t, base, tc.node, tc.m, tc.a)
			if tc.node == tc.m && tc.m == tc.a {
				t.Fatal("test case must be a mismatch")
			}
			_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(raw))
			if err == nil {
				t.Fatalf("expected reject node=%d model=%d agent=%d", tc.node, tc.m, tc.a)
			}
		})
	}
}

type lockTarget int

const (
	lockTargetNode lockTarget = iota
	lockTargetModel
	lockTargetAgent
)

// mutateOneLock keeps the other two locks at 1 (valid) and sets one target to
// val. Empty val means delete the key.
func mutateOneLock(t *testing.T, base json.RawMessage, target lockTarget, val string) string {
	t.Helper()
	// Start from fully matching triple=1
	raw := setThreeLocks(t, base, 1, 1, 1)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	switch target {
	case lockTargetNode:
		if val == "" {
			delete(nodes[0], "modelConfigLockVersion")
		} else {
			nodes[0]["modelConfigLockVersion"] = json.RawMessage(val)
		}
	case lockTargetModel:
		var ms map[string]json.RawMessage
		if err := json.Unmarshal(nodes[0]["modelSnapshot"], &ms); err != nil {
			t.Fatal(err)
		}
		if val == "" {
			delete(ms, "lockVersion")
		} else {
			ms["lockVersion"] = json.RawMessage(val)
		}
		b, _ := json.Marshal(ms)
		nodes[0]["modelSnapshot"] = b
	case lockTargetAgent:
		var as map[string]json.RawMessage
		if err := json.Unmarshal(nodes[0]["agentSnapshot"], &as); err != nil {
			t.Fatal(err)
		}
		if val == "" {
			delete(as, "modelConfigLockVer")
		} else {
			as["modelConfigLockVer"] = json.RawMessage(val)
		}
		b, _ := json.Marshal(as)
		nodes[0]["agentSnapshot"] = b
	}
	nb, _ := json.Marshal(nodes)
	m["nodes"] = nb
	out, _ := json.Marshal(m)
	return string(out)
}

// setThreeLocks sets node/model/agent lock integers simultaneously.
func setThreeLocks(t *testing.T, base json.RawMessage, node, model, agent int64) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	nRaw, _ := json.Marshal(node)
	mRaw, _ := json.Marshal(model)
	aRaw, _ := json.Marshal(agent)
	nodes[0]["modelConfigLockVersion"] = nRaw
	var ms map[string]json.RawMessage
	if err := json.Unmarshal(nodes[0]["modelSnapshot"], &ms); err != nil {
		t.Fatal(err)
	}
	ms["lockVersion"] = mRaw
	msb, _ := json.Marshal(ms)
	nodes[0]["modelSnapshot"] = msb
	var as map[string]json.RawMessage
	if err := json.Unmarshal(nodes[0]["agentSnapshot"], &as); err != nil {
		t.Fatal(err)
	}
	as["modelConfigLockVer"] = aRaw
	asb, _ := json.Marshal(as)
	nodes[0]["agentSnapshot"] = asb
	nb, _ := json.Marshal(nodes)
	m["nodes"] = nb
	out, _ := json.Marshal(m)
	return string(out)
}
