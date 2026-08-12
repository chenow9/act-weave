package chatruntimebridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionCodeNeverSetsSubAgents pins §7.4.1: ActWeave uses AgentTool /
// NewTypedAgentTool, never SetSubAgents. adk only injects the transfer system
// message when subAgents/parentAgent are set; hanging that would break the
// single frozen system prompt invariant from Task 4A.
func TestProductionCodeNeverSetsSubAgents(t *testing.T) {
	t.Parallel()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"SetSubAgents(", "OnSetAsSubAgent(", "OnSetSubAgents("}
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
				t.Errorf("%s uses %s; Task 5 must stay on NewTypedAgentTool", name, needle)
			}
		}
	}
}

// TestAgenticDelegationUsesTypedAgentTool ensures the Agentic attach path names
// NewTypedAgentTool (not classic NewAgentTool / NewChatModelAgent).
func TestAgenticDelegationUsesTypedAgentTool(t *testing.T) {
	t.Parallel()
	body := functionBody(t, "func (b *Bridge) attachAgenticDelegationTools(")
	if !strings.Contains(body, "NewTypedAgentTool") &&
		!strings.Contains(functionBody(t, "func wrapTypedAgentTool("), "NewTypedAgentTool") {
		t.Fatal("Agentic delegation must wrap children with NewTypedAgentTool")
	}
	if strings.Contains(body, "NewChatModelAgent") {
		t.Fatal("Agentic delegation must not build classic ChatModelAgent")
	}
	if strings.Contains(body, "SetSubAgents") {
		t.Fatal("Agentic delegation must not SetSubAgents")
	}
}
