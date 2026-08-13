package modelconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestCreateCanonicalizesProviderAndPersistsCanonicalForm proves the persisted
// column — not just the returned struct — holds the canonical identity, for every
// alias in the closed set. Before R11-2 the repository only trimmed, so
// 'OPENAI_COMPATIBLE' and 'OpenAI Compatible' rows were written verbatim and then
// rejected by the Agentic initial path's exact provider match.
func TestCreateCanonicalizesProviderAndPersistsCanonicalForm(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	for index, alias := range ProviderAliases() {
		id := fmt.Sprintf("018f1f2e-7b5a-7c3d-8e9f-e2345678%04d", 9000+index)
		input := validNewConfig(id, fmt.Sprintf("Canonical Provider %d", index))
		input.Provider = alias
		created, err := repository.Create(context.Background(), input)
		if err != nil {
			t.Fatalf("create with provider %q: %v", alias, err)
		}
		want, err := CanonicalProvider(alias)
		if err != nil {
			t.Fatal(err)
		}
		if created.Provider != want {
			t.Fatalf("create %q: returned provider %q want %q", alias, created.Provider, want)
		}
		var stored string
		if err := db.QueryRow(`SELECT provider FROM model_configs WHERE id=$1`, id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != want {
			t.Fatalf("create %q: stored provider %q want %q", alias, stored, want)
		}
		// Get/List must report the same canonical value (no read-time rewrite).
		got, err := repository.Get(context.Background(), repositoryWorkspaceID, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Provider != want {
			t.Fatalf("get %q: provider %q want %q", alias, got.Provider, want)
		}
	}
}

// TestCreateRejectsNonCanonicalProviderWithoutWriting is the fail-closed half:
// an unrecognized provider must abort the write with ErrInvalid (422) and leave no
// row behind, rather than being stored and failing later at the agentic boundary.
func TestCreateRejectsNonCanonicalProviderWithoutWriting(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	for index, provider := range []string{
		"", "   ", "test", "x", "azure", "azure-openai", "anthropic",
		"openai compatible", "Openai-Compatible", "openaicompatible",
		"openai-compatible-v2", "OPENAI_COMPATIBLE_LEGACY",
	} {
		id := fmt.Sprintf("018f1f2e-7b5a-7c3d-8e9f-e2345678%04d", 9500+index)
		input := validNewConfig(id, fmt.Sprintf("Rejected Provider %d", index))
		input.Provider = provider
		if _, err := repository.Create(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("create with provider %q must fail with ErrInvalid, got %v", provider, err)
		}
		var rows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM model_configs WHERE id=$1`, id).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("rejected provider %q still wrote a row", provider)
		}
	}
}

// TestUpdateCanonicalizesProviderAndRejectsNonCanonical covers the update path,
// which rewrites the provider column and therefore the row's wire identity.
func TestUpdateCanonicalizesProviderAndRejectsNonCanonical(t *testing.T) {
	repository, db := newModelConfigRepositoryTest(t, nil)
	created := createRepositoryConfig(t, repository, repositoryConfigID, "Update Provider Model")
	if created.Provider != ProviderOpenAICompatible {
		t.Fatalf("fixture provider must already be canonicalized on create, got %q", created.Provider)
	}

	updated, err := repository.Update(context.Background(), repositoryWorkspaceID, created.ID, UpdateConfig{
		Name: created.Name, Provider: "OpenAI Compatible", APIBase: created.APIBase,
		ModelName: created.ModelName, CredentialSecretID: created.CredentialSecretID,
		Options: created.Options, Status: StatusUnverified, UpdatedBy: repositoryOwnerID,
		ExpectedLockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("update with display alias: %v", err)
	}
	if updated.Provider != ProviderOpenAICompatible {
		t.Fatalf("update provider: got %q want %q", updated.Provider, ProviderOpenAICompatible)
	}
	var stored string
	if err := db.QueryRow(`SELECT provider FROM model_configs WHERE id=$1`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != ProviderOpenAICompatible {
		t.Fatalf("stored provider after update: %q", stored)
	}

	// Non-canonical update fails closed and applies no part of the CAS write.
	for _, provider := range []string{"", " ", "azure", "test", "openai compatible"} {
		if _, err := repository.Update(context.Background(), repositoryWorkspaceID, created.ID, UpdateConfig{
			Name: "Renamed By Rejected Update", Provider: provider, APIBase: "https://rejected.example/v1",
			ModelName: "rejected", Options: json.RawMessage(`{}`), Status: StatusUnverified,
			UpdatedBy: repositoryOwnerID, ExpectedLockVersion: updated.LockVersion,
		}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("update with provider %q must fail with ErrInvalid, got %v", provider, err)
		}
		after, err := repository.Get(context.Background(), repositoryWorkspaceID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Provider != ProviderOpenAICompatible || after.Name != updated.Name ||
			after.APIBase != updated.APIBase || after.ModelName != updated.ModelName ||
			after.LockVersion != updated.LockVersion {
			t.Fatalf("rejected update mutated the row: %+v", after)
		}
	}
}

// TestVerificationDigestUsesCanonicalProvider proves the capability document
// stamped by verification is bound to the canonical provider, so the digest the
// Agentic path recomputes from a frozen snapshot matches.
func TestVerificationDigestUsesCanonicalProvider(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	input := validNewConfig(repositoryConfigID, "Digest Provider Model")
	input.Provider = "OPENAI_COMPATIBLE"
	created, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewVerificationService(repository, VerifierFunc(
		func(context.Context, Config) (AgenticCapabilities, error) {
			return AgenticCapabilities{ToolCalling: ToolCallingNativeClientSearch}, nil
		}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(context.Background(), created.WorkspaceID, created.ID, repositoryOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Provider != ProviderOpenAICompatible {
		t.Fatalf("verified provider: %q", verified.Provider)
	}
	doc, _, err := ParseAgenticCapabilities(verified.AgenticCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest := WireConfigDigest(verified)
	legacy := verified
	legacy.Provider = "OPENAI_COMPATIBLE"
	legacyDigest := WireConfigDigest(legacy)
	if doc.VerifiedConfigDigest != canonicalDigest {
		t.Fatalf("verified digest %q must be bound to the canonical provider (%q)",
			doc.VerifiedConfigDigest, canonicalDigest)
	}
	if doc.VerifiedConfigDigest == legacyDigest {
		t.Fatal("verified digest must not match the legacy provider spelling")
	}
}
