package a2ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the prompt golden file")

const promptGoldenPath = "testdata/prompt_appendix_v2.golden.md"

// TestPromptAppendixMatchesGolden makes every prompt change visible in review.
// The prompt is generated from the catalog, so it cannot drift from validation;
// the golden file exists so a catalog edit that reshapes what the model is told
// shows up as a diff instead of passing silently.
//
// Refresh with: go test ./internal/a2ui/ -run TestPromptAppendixMatchesGolden -update-golden
func TestPromptAppendixMatchesGolden(t *testing.T) {
	got := BuildPromptAppendix()
	if got == "" {
		t.Fatal("BuildPromptAppendix returned empty")
	}
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(promptGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(promptGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(promptGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-golden to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("prompt appendix changed; rerun with -update-golden after reviewing.\n--- got ---\n%s", got)
	}
}

// maxPromptTokenBudget caps what the appendix costs on every single request.
// The estimate is deliberately crude and pessimistic; it exists to catch a
// catalog change that doubles the prompt, not to be precise.
const maxPromptTokenBudget = 1200

func TestPromptAppendixStaysWithinTokenBudget(t *testing.T) {
	appendix := BuildPromptAppendix()
	estimate := estimatePromptTokens(appendix)
	if estimate > maxPromptTokenBudget {
		t.Fatalf("prompt appendix is ~%d tokens, budget is %d; trim the catalog "+
			"presentation or raise the budget deliberately", estimate, maxPromptTokenBudget)
	}
}

// estimatePromptTokens approximates BPE cost: ~4 bytes per token for ASCII, but
// CJK runs closer to one token per character, so those are counted separately.
func estimatePromptTokens(text string) int {
	ascii, wide := 0, 0
	for _, symbol := range text {
		if symbol > 0x2E80 {
			wide++
			continue
		}
		ascii++
	}
	return ascii/4 + wide
}

// TestPromptOnlyNamesCatalogMembers is the drift guard: every component name and
// enum value the model is shown must exist in the catalog. Generation makes this
// true by construction, and this test keeps it true if generation is ever
// hand-edited.
func TestPromptOnlyNamesCatalogMembers(t *testing.T) {
	appendix := BuildPromptAppendix()
	catalog := loadCatalog()
	if catalog.err != nil {
		t.Fatalf("catalog: %v", catalog.err)
	}
	for _, name := range promptComponentOrder {
		if _, exists := catalog.components[name]; !exists {
			t.Fatalf("prompt presents component %q which the catalog does not define", name)
		}
		if !strings.Contains(appendix, "**"+name+"**") {
			t.Fatalf("component %q missing from the prompt", name)
		}
	}
	if len(promptComponentOrder) != len(catalog.components) {
		t.Fatalf("prompt lists %d components, catalog defines %d",
			len(promptComponentOrder), len(catalog.components))
	}
	// Chart enums are the values models most often invent; assert each is shown.
	for _, chartType := range []string{"bar", "hbar", "line", "area", "pie", "donut"} {
		if !strings.Contains(appendix, chartType) {
			t.Fatalf("chartType %q missing from the prompt", chartType)
		}
	}
}

// TestPromptForbidsPlatformOwnedFields keeps the model from being taught to emit
// fields the platform assigns, and from expecting a return channel.
func TestPromptForbidsPlatformOwnedFields(t *testing.T) {
	appendix := BuildPromptAppendix()
	if !strings.Contains(appendix, "Never emit surfaceId or catalogId") {
		t.Fatal("prompt must tell the model not to emit platform-owned identity")
	}
	if strings.Contains(appendix, "sendDataModel") {
		t.Fatal("prompt must not mention sendDataModel: there is no return channel")
	}
	// No legacy shapes from the hand-written v1 appendix may survive.
	for _, legacy := range []string{`"type":"form"`, `"type":"chart"`, "PieChart", `"labels"`, `"data":[`} {
		if strings.Contains(appendix, legacy) {
			t.Fatalf("prompt still advertises the pre-catalog shape %q", legacy)
		}
	}
}
