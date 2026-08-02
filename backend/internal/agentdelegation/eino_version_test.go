package agentdelegation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEinoDependencyLockedToV0913 fails if go.mod is not on the official stable v0.9.13.
func TestEinoDependencyLockedToV0913(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// backend/internal/agentdelegation → backend/go.mod
	modPath := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "go.mod"))
	raw, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "github.com/cloudwego/eino v0.9.13") {
		t.Fatalf("go.mod must require github.com/cloudwego/eino v0.9.13\n%s", text)
	}
	if strings.Contains(text, "replace github.com/cloudwego/eino") {
		t.Fatal("go.mod must not replace cloudwego/eino")
	}
	// Reject pseudo-versions for eino direct require line.
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "github.com/cloudwego/eino ") {
			if strings.Contains(line, "-") && strings.Contains(line, "+") {
				t.Fatalf("pseudo-version not allowed: %s", line)
			}
			if !strings.HasSuffix(line, "v0.9.13") && !strings.Contains(line, "v0.9.13 ") {
				// exact match on require
				if line != "github.com/cloudwego/eino v0.9.13" {
					t.Fatalf("unexpected eino line: %s", line)
				}
			}
		}
	}
}
