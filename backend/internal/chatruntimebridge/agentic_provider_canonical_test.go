package chatruntimebridge

import (
	"testing"

	"actweave/backend/internal/modelconfig"
)

// TestAgenticSupportedProvidersMatchModelConfigCanonicalSet is the structural
// guard for the R11-2 root cause: model_configs.provider is written through
// modelconfig.CanonicalProvider, while the Agentic initial path accepts only the
// exact strings in agenticSupportedProviders. When those two sets disagree — as
// they did when the console wrote "OpenAI Compatible" and the bridge only knew
// "openai-compatible" — every stored config is unusable and the failure shows up
// only at run time. Both directions are pinned so neither side can drift.
func TestAgenticSupportedProvidersMatchModelConfigCanonicalSet(t *testing.T) {
	canonical := modelconfig.CanonicalProviders()
	if len(canonical) == 0 {
		t.Fatal("modelconfig must expose at least one canonical provider")
	}
	for _, provider := range canonical {
		if err := requireSupportedAgenticProvider(provider); err != nil {
			t.Fatalf("canonical provider %q must be accepted by the agentic path: %v", provider, err)
		}
	}
	if len(agenticSupportedProviders) != len(canonical) {
		t.Fatalf("agentic provider set %v and canonical set %v have different sizes",
			agenticSupportedProviders, canonical)
	}
	for provider := range agenticSupportedProviders {
		if !modelconfig.IsCanonicalProvider(provider) {
			t.Fatalf("agentic path accepts %q, which modelconfig would never persist", provider)
		}
	}

	// Every alias the canonicalizer accepts on input must still be rejected here
	// when it reaches the bridge unnormalized: the bridge is an exact-match
	// boundary, not a second normalizer.
	for _, alias := range modelconfig.ProviderAliases() {
		if modelconfig.IsCanonicalProvider(alias) {
			continue
		}
		if err := requireSupportedAgenticProvider(alias); err == nil {
			t.Fatalf("agentic path must reject the non-canonical alias %q", alias)
		}
	}
}
