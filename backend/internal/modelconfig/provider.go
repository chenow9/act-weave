package modelconfig

import (
	"fmt"
	"sort"
	"strings"
)

// Canonical model provider identities. These are the exact strings persisted in
// model_configs.provider, embedded in frozen model snapshots, hashed into
// WireConfigDigest, and accepted by the Agentic initial path
// (chatruntimebridge.agenticSupportedProviders). Any other spelling is a
// different string and therefore a different wire identity, which is how three
// live spellings of one provider ("OpenAI Compatible", "OPENAI_COMPATIBLE",
// "openai-compatible") made every stored config unusable by the agentic path.
const (
	ProviderOpenAI           = "openai"
	ProviderOpenAICompatible = "openai-compatible"
)

// canonicalProviderAliases is a CLOSED alias set: exact keys only, matched after
// TrimSpace, mapping every spelling that is actually live in this system to its
// canonical identity. It is deliberately not a permissive normalizer (no generic
// lowercase + separator folding), because that would silently accept future
// provider names nobody has verified against the Responses/agenticopenai tuple.
//
// Where each alias comes from:
//   - "openai" / "openai-compatible": the canonical forms (identity mappings, so
//     canonicalizing an already-canonical value is a no-op).
//   - "OPENAI_COMPATIBLE" / "openai_compatible" / "OPENAI-COMPATIBLE": SQL-style
//     spellings written by earlier releases; "OPENAI_COMPATIBLE" is what existing
//     database rows and most backend fixtures contain.
//   - "OpenAI Compatible": the frontend display label that used to be sent on the
//     wire (see R11-3, which makes the frontend send the canonical value).
//   - "OPENAI" / "OpenAI": case variants of the plain OpenAI provider.
//
// Extending this map means accepting a new provider identity into the verified
// agentic tuple, so it must be a deliberate, reviewed change.
var canonicalProviderAliases = map[string]string{
	ProviderOpenAI:           ProviderOpenAI,
	"OPENAI":                 ProviderOpenAI,
	"OpenAI":                 ProviderOpenAI,
	ProviderOpenAICompatible: ProviderOpenAICompatible,
	"OPENAI-COMPATIBLE":      ProviderOpenAICompatible,
	"openai_compatible":      ProviderOpenAICompatible,
	"OPENAI_COMPATIBLE":      ProviderOpenAICompatible,
	"OpenAI Compatible":      ProviderOpenAICompatible,
}

// CanonicalProvider maps a provider spelling from the closed alias set to its
// canonical identity. It fails closed: empty, whitespace-only, and any spelling
// outside the alias set are rejected with ErrInvalid instead of being passed
// through, so an unverifiable provider can never reach model_configs.provider
// (and from there WireConfigDigest and frozen snapshots).
func CanonicalProvider(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: model provider is required", ErrInvalid)
	}
	canonical, ok := canonicalProviderAliases[trimmed]
	if !ok {
		return "", fmt.Errorf("%w: model provider %q is not a canonical provider", ErrInvalid, trimmed)
	}
	return canonical, nil
}

// IsCanonicalProvider reports whether value is already a canonical identity
// (exact match, no trimming — chatruntimebridge.requireSupportedAgenticProvider
// rejects any provider that differs from its own trimmed form, so a padded value
// is not canonical).
func IsCanonicalProvider(value string) bool {
	switch value {
	case ProviderOpenAI, ProviderOpenAICompatible:
		return true
	default:
		return false
	}
}

// CanonicalProviders returns the canonical identities in sorted order. Used by
// cross-package tests to pin that the persisted canonical set and the Agentic
// initial path's accepted provider set cannot drift apart again.
func CanonicalProviders() []string {
	seen := make(map[string]struct{}, len(canonicalProviderAliases))
	providers := make([]string, 0, 2)
	for _, canonical := range canonicalProviderAliases {
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		providers = append(providers, canonical)
	}
	sort.Strings(providers)
	return providers
}

// ProviderAliases returns every accepted alias spelling in sorted order. Used by
// tests (including the migration test) to enumerate the closed set.
func ProviderAliases() []string {
	aliases := make([]string, 0, len(canonicalProviderAliases))
	for alias := range canonicalProviderAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}
