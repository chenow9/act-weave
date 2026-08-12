package modelconfig

import (
	"errors"
	"strings"
	"testing"
)

// TestCanonicalProviderAcceptsEveryAliasInTheClosedSet pins the accepted alias
// set. Each accepted spelling must map to a canonical identity, and
// canonicalizing an already-canonical value must be a no-op (idempotence), so a
// second write of the same config cannot change the wire identity.
func TestCanonicalProviderAcceptsEveryAliasInTheClosedSet(t *testing.T) {
	accepted := map[string]string{
		"openai":            ProviderOpenAI,
		"OPENAI":            ProviderOpenAI,
		"OpenAI":            ProviderOpenAI,
		"openai-compatible": ProviderOpenAICompatible,
		"OPENAI-COMPATIBLE": ProviderOpenAICompatible,
		"openai_compatible": ProviderOpenAICompatible,
		"OPENAI_COMPATIBLE": ProviderOpenAICompatible,
		"OpenAI Compatible": ProviderOpenAICompatible,
	}
	for alias, want := range accepted {
		got, err := CanonicalProvider(alias)
		if err != nil {
			t.Fatalf("CanonicalProvider(%q): unexpected error %v", alias, err)
		}
		if got != want {
			t.Fatalf("CanonicalProvider(%q): got %q want %q", alias, got, want)
		}
		if !IsCanonicalProvider(got) {
			t.Fatalf("CanonicalProvider(%q) returned non-canonical %q", alias, got)
		}
		// Idempotent: canonical output re-canonicalizes to itself.
		again, err := CanonicalProvider(got)
		if err != nil || again != got {
			t.Fatalf("CanonicalProvider(%q) not idempotent: got %q err %v", got, again, err)
		}
		// Surrounding whitespace is trimmed, never rejected outright, and never
		// leaks into the stored value (the agentic provider check rejects any
		// provider that differs from its own trimmed form).
		for _, padded := range []string{" " + alias, alias + " ", "\t" + alias + "\n", "  " + alias + "  "} {
			trimmed, err := CanonicalProvider(padded)
			if err != nil {
				t.Fatalf("CanonicalProvider(%q): unexpected error %v", padded, err)
			}
			if trimmed != want {
				t.Fatalf("CanonicalProvider(%q): got %q want %q", padded, trimmed, want)
			}
		}
	}
	// The map under test and the production alias set must be the same size, so a
	// silently added alias fails this test instead of slipping through.
	if aliases := ProviderAliases(); len(aliases) != len(accepted) {
		t.Fatalf("alias set changed: production has %d (%v), test pins %d",
			len(aliases), aliases, len(accepted))
	}
	for _, alias := range ProviderAliases() {
		if _, ok := accepted[alias]; !ok {
			t.Fatalf("production alias %q is not pinned by this test", alias)
		}
	}
	canonical := CanonicalProviders()
	if len(canonical) != 2 || canonical[0] != ProviderOpenAI || canonical[1] != ProviderOpenAICompatible {
		t.Fatalf("canonical providers: got %v", canonical)
	}
}

// TestCanonicalProviderFailsClosedOutsideTheAliasSet is the R11-2 regression:
// the canonicalizer must reject, never pass through, anything it does not
// recognize. A passed-through provider becomes part of WireConfigDigest and of
// frozen snapshots, and would then be rejected only at the agentic boundary.
func TestCanonicalProviderFailsClosedOutsideTheAliasSet(t *testing.T) {
	rejected := []string{
		"",                  // missing
		" ",                 // whitespace only
		"\t\n",              // whitespace only
		"openai compatible", // space form that was never live
		"Openai-Compatible", // case variant outside the set
		"openai-Compatible",
		"OPENAI COMPATIBLE",
		"openaicompatible",
		"openai-compatible-v2",
		"openai-compatible ", // handled by trim, but with an inner suffix below
		"xopenai-compatible",
		"azure",
		"azure-openai",
		"azure_openai",
		"AZURE_OPENAI",
		"anthropic",
		"gemini",
		"ollama",
		"test",
		"x",
		"http",
		"null",
		"openai\u0000",             // embedded NUL
		"openai\n",                 // trailing newline is trimmed -> accepted; see below
		"open\nai",                 // inner newline
		"openai; DROP TABLE users", // hostile input is rejected, not sanitized
		"OPENAI_COMPATIBLE_LEGACY",
		"openai_compatible2",
	}
	for _, provider := range rejected {
		got, err := CanonicalProvider(provider)
		// Values that are only padded with whitespace are legitimately accepted;
		// keep this table honest by skipping those explicitly.
		if trimmed := strings.TrimSpace(provider); trimmed == ProviderOpenAI ||
			trimmed == ProviderOpenAICompatible {
			if err != nil || got != trimmed {
				t.Fatalf("CanonicalProvider(%q) must accept padded canonical value: got %q err %v",
					provider, got, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("CanonicalProvider(%q) must fail closed, got %q", provider, got)
		}
		if got != "" {
			t.Fatalf("CanonicalProvider(%q) must not return a value on rejection, got %q", provider, got)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("CanonicalProvider(%q) must wrap ErrInvalid (422 at the API), got %v", provider, err)
		}
	}
	if IsCanonicalProvider("") || IsCanonicalProvider(" openai") || IsCanonicalProvider("OPENAI_COMPATIBLE") {
		t.Fatal("IsCanonicalProvider must only accept the exact canonical identities")
	}
	if !IsCanonicalProvider(ProviderOpenAI) || !IsCanonicalProvider(ProviderOpenAICompatible) {
		t.Fatal("IsCanonicalProvider must accept the canonical identities")
	}
}

// TestCanonicalProviderChangesWireConfigDigest documents why canonicalization
// forces re-verification: two spellings of the same provider hash differently, so
// a capability document stamped under the legacy spelling is stale the moment the
// provider text changes.
func TestCanonicalProviderChangesWireConfigDigest(t *testing.T) {
	legacy := Config{
		ID: repositoryConfigID, WorkspaceID: repositoryWorkspaceID,
		Provider: "OPENAI_COMPATIBLE", APIBase: "https://models.example/v1",
		ModelName: "gpt-test", LockVersion: 1,
	}
	canonical := legacy
	provider, err := CanonicalProvider(legacy.Provider)
	if err != nil {
		t.Fatal(err)
	}
	canonical.Provider = provider
	if WireConfigDigest(legacy) == WireConfigDigest(canonical) {
		t.Fatal("provider must participate in WireConfigDigest")
	}
}
