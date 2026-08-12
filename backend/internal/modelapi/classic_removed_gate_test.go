package modelapi_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 9 / §19.1: production modelapi must not reintroduce Chat Completions.
func TestProductionModelAPIHasNoClassicChatCompletionsBuilder(t *testing.T) {
	t.Parallel()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"func NewEinoOpenAIChatModel",
		"github.com/cloudwego/eino-ext/components/model/openai",
	}
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Errorf("%s still contains %q", name, needle)
			}
		}
	}
}
