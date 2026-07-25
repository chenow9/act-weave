package workflowtranslator_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/workflowtranslator"
)

func TestCacheKeyStability(t *testing.T) {
	t.Parallel()

	a := workflowtranslator.CacheKey("ws-1", "rev-2", "planhashabc", "eino-graph-v1")
	b := workflowtranslator.CacheKey("ws-1", "rev-2", "planhashabc", "eino-graph-v1")
	if a != b {
		t.Fatalf("CacheKey not stable: %q vs %q", a, b)
	}
	if !strings.Contains(a, "ws|ws-1") {
		t.Fatalf("missing workspace segment: %q", a)
	}
	if !strings.Contains(a, "rev|rev-2") {
		t.Fatalf("missing revision segment: %q", a)
	}
	if !strings.Contains(a, "plan|planhashabc") {
		t.Fatalf("missing planHash segment: %q", a)
	}
	if !strings.Contains(a, "engver|eino-graph-v1") {
		t.Fatalf("missing engineVersion segment: %q", a)
	}
}

func TestCacheKeyDistinguishesIdentity(t *testing.T) {
	t.Parallel()

	base := workflowtranslator.CacheKey("ws-1", "rev-2", "hash-a", "eino-graph-v1")
	cases := []struct {
		name string
		key  string
	}{
		{"workspace", workflowtranslator.CacheKey("ws-2", "rev-2", "hash-a", "eino-graph-v1")},
		{"revision", workflowtranslator.CacheKey("ws-1", "rev-3", "hash-a", "eino-graph-v1")},
		{"planHash", workflowtranslator.CacheKey("ws-1", "rev-2", "hash-b", "eino-graph-v1")},
		{"engineVersion", workflowtranslator.CacheKey("ws-1", "rev-2", "hash-a", "eino-graph-v2")},
	}
	for _, tc := range cases {
		if tc.key == base {
			t.Fatalf("%s change must alter cache key", tc.name)
		}
	}
}

func TestCacheKeyFromDefaultsEngineVersion(t *testing.T) {
	t.Parallel()

	key := workflowtranslator.CacheKeyFrom(workflowtranslator.CacheKeyParts{
		WorkspaceID: "ws-1",
		RevisionID:  "rev-2",
		PlanHash:    "abc",
	})
	if !strings.Contains(key, "engver|"+workflowtranslator.GraphEngineVersion) {
		t.Fatalf("expected default GraphEngineVersion, got %q", key)
	}

	withMode := workflowtranslator.CacheKeyFrom(workflowtranslator.CacheKeyParts{
		WorkspaceID:   "ws-1",
		RevisionID:    "rev-2",
		PlanHash:      "abc",
		EngineVersion: workflowtranslator.GraphEngineVersion,
		Engine:        workflowtranslator.EngineEinoCore,
	})
	if !strings.Contains(withMode, "mode|eino_core") {
		t.Fatalf("expected mode segment, got %q", withMode)
	}
	withoutMode := workflowtranslator.CacheKeyFrom(workflowtranslator.CacheKeyParts{
		WorkspaceID:   "ws-1",
		RevisionID:    "rev-2",
		PlanHash:      "abc",
		EngineVersion: workflowtranslator.GraphEngineVersion,
	})
	if withMode == withoutMode {
		t.Fatal("engine mode must distinguish cache keys")
	}
}

func TestCacheKeyTrimsWhitespace(t *testing.T) {
	t.Parallel()

	a := workflowtranslator.CacheKey("  ws-1  ", " rev-2 ", " hash ", " ver ")
	b := workflowtranslator.CacheKey("ws-1", "rev-2", "hash", "ver")
	if a != b {
		t.Fatalf("trim mismatch: %q vs %q", a, b)
	}
}
