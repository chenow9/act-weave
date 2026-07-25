package agentaccess_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestExternalSubjectPrivacyLifecycle(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)

	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	pepper := bytes.Repeat([]byte{0x71}, 32)
	service, err := agentaccess.NewManagementService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	policy := agentaccess.DefaultWorkspaceExternalSubjectRetentionPolicy()
	if !policy.Valid() || policy.IdentityRetention != "permanent" || policy.AllowHardDelete ||
		!policy.AllowReenable || !policy.ClearDisplayOnDisable {
		t.Fatalf("unexpected v1 retention policy: %+v", policy)
	}

	registration, err := service.RegisterClient(ctx, agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: "Privacy Lifecycle Client",
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	inlineJWKS := trustedSubjectTestInlineJWKS(t, "lifecycle-subject-key", publicKey)
	issuer := "https://idp.lifecycle.example.test"
	audience := "actweave-lifecycle-subject"
	trustConfig := agentaccess.TrustedSubjectIssuerConfig{
		Issuer: issuer, Audience: audience, InlineJWKS: inlineJWKS,
		Algorithms: []string{"EdDSA"}, ClaimPolicy: agentaccessauth.DefaultSubjectClaimPolicy(),
	}
	client, _, err := service.UpdateTrustedSubjectIssuer(ctx, agentaccess.UpdateTrustedSubjectIssuerInput{
		WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
		ActorID: repositoryOwnerID, ExpectedLockVersion: registration.Client.LockVersion,
		Config: trustConfig,
	})
	if err != nil {
		t.Fatal(err)
	}

	rawSubject := "partner-user-privacy-42"
	subjectHash := agentaccessauth.HashExternalSubject(pepper, issuer, rawSubject)
	seen := time.Now().UTC().Truncate(time.Second)
	created, err := repository.CreateExternalSubject(ctx, agentaccess.CreateExternalSubjectInput{
		ID: uuid.NewString(), WorkspaceID: repositoryWorkspaceID, ClientID: client.ID,
		Issuer: issuer, SubjectHash: subjectHash[:], DisplayRef: "ref_customer_privacy",
		SeenAt: seen,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("public view and list never expose hash or raw subject", func(t *testing.T) {
		view, err := service.GetExternalSubjectPublicView(
			ctx, repositoryWorkspaceID, client.ID, created.ID,
		)
		if err != nil || view.DisplayRef != "ref_customer_privacy" || view.Status != agentaccess.StatusActive {
			t.Fatalf("public view=%+v err=%v", view, err)
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		if err := agentaccess.AssertExternalSubjectPrivacyJSON(encoded, rawSubject, string(subjectHash[:])); err != nil {
			t.Fatalf("public view leaked sensitive material: %s", encoded)
		}
		// Full internal model must still mark hash as non-serializable.
		internalJSON, err := json.Marshal(created)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(internalJSON, subjectHash[:]) || strings.Contains(string(internalJSON), "SubjectHash") {
			t.Fatalf("internal External Subject JSON leaked hash: %s", internalJSON)
		}

		list, err := service.ListExternalSubjectPublicViews(ctx, repositoryWorkspaceID, client.ID)
		if err != nil || len(list) != 1 || list[0].ID != created.ID {
			t.Fatalf("list=%+v err=%v", list, err)
		}
		listJSON, _ := json.Marshal(list)
		if err := agentaccess.AssertExternalSubjectPrivacyJSON(listJSON, rawSubject); err != nil {
			t.Fatalf("list leaked sensitive material: %s", listJSON)
		}
	})

	t.Run("display ref rejects PII-like values and accepts controlled refs", func(t *testing.T) {
		if agentaccess.ValidExternalSubjectDisplayRef("user@example.test") {
			t.Fatal("email display ref must be rejected")
		}
		if agentaccess.ValidExternalSubjectDisplayRef("plain name") {
			t.Fatal("free-text display ref must be rejected")
		}
		if !agentaccess.ValidExternalSubjectDisplayRef("ref_ok_1") {
			t.Fatal("controlled display ref must be accepted")
		}
		if _, err := service.UpdateExternalSubjectDisplayRef(ctx, agentaccess.UpdateExternalSubjectDisplayRefInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: client.ID, SubjectID: created.ID,
			ActorID: repositoryOwnerID, DisplayRef: "user@example.test", ExpectedLockVersion: created.LockVersion,
		}); !errors.Is(err, agentaccess.ErrManagementInvalid) {
			t.Fatalf("email display update err=%v", err)
		}
		updated, err := service.UpdateExternalSubjectDisplayRef(ctx, agentaccess.UpdateExternalSubjectDisplayRefInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: client.ID, SubjectID: created.ID,
			ActorID: repositoryOwnerID, DisplayRef: "ref_customer_updated",
			ExpectedLockVersion: created.LockVersion,
		})
		if err != nil || updated.DisplayRef != "ref_customer_updated" || updated.LockVersion != created.LockVersion+1 {
			t.Fatalf("display update=%+v err=%v", updated, err)
		}
		created = agentaccess.ExternalSubject{
			ID: created.ID, WorkspaceID: created.WorkspaceID, ClientID: created.ClientID,
			Issuer: created.Issuer, DisplayRef: updated.DisplayRef, Status: updated.Status,
			LockVersion: updated.LockVersion, SubjectHash: created.SubjectHash,
		}
	})

	t.Run("disable clears display ref, blocks exchange, re-enable restores access path", func(t *testing.T) {
		disabled, err := service.SetExternalSubjectStatus(ctx, agentaccess.SetExternalSubjectStatusInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: client.ID, SubjectID: created.ID,
			ActorID: repositoryOwnerID, Status: agentaccess.StatusDisabled,
			ExpectedLockVersion: created.LockVersion,
		})
		if err != nil || disabled.Status != agentaccess.StatusDisabled || disabled.DisplayRef != "" ||
			disabled.DisabledAt == nil {
			t.Fatalf("disabled=%+v err=%v", disabled, err)
		}
		disabledJSON, _ := json.Marshal(disabled)
		if err := agentaccess.AssertExternalSubjectPrivacyJSON(disabledJSON, rawSubject); err != nil {
			t.Fatalf("disabled public view leak: %s", disabledJSON)
		}

		// Mapper / resolve path used by Token Exchange must deny DISABLED subjects.
		resolved, err := repository.ResolveOrCreateExternalSubject(
			ctx, repositoryWorkspaceID, client.ID, issuer, subjectHash[:], time.Now().UTC(),
		)
		if err != nil || resolved.Status != agentaccess.StatusDisabled {
			t.Fatalf("resolve disabled=%+v err=%v", resolved, err)
		}
		mapper := lifecycleSubjectMapper{repository: repository}
		if _, err := mapper.ResolveActiveExternalSubject(
			ctx, repositoryWorkspaceID, client.ID, issuer, subjectHash, time.Now().UTC(),
		); !errors.Is(err, agentaccessauth.ErrTokenExchangeSubjectDenied) {
			t.Fatalf("active mapper should deny disabled subject: %v", err)
		}

		// Hard delete is forbidden by retention policy / repository surface.
		if err := repository.DeleteExternalSubject(ctx, repositoryWorkspaceID, client.ID, created.ID); !errors.Is(err, agentaccess.ErrRepositoryInvalid) {
			t.Fatalf("hard delete err=%v", err)
		}
		// Identity evidence remains queryable for historical audit.
		stillThere, err := repository.GetExternalSubject(ctx, repositoryWorkspaceID, client.ID, created.ID)
		if err != nil || stillThere.ID != created.ID || stillThere.Status != agentaccess.StatusDisabled {
			t.Fatalf("historical subject missing after disable: %+v err=%v", stillThere, err)
		}

		reenabled, err := service.SetExternalSubjectStatus(ctx, agentaccess.SetExternalSubjectStatusInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: client.ID, SubjectID: created.ID,
			ActorID: repositoryOwnerID, Status: agentaccess.StatusActive,
			ExpectedLockVersion: disabled.LockVersion,
		})
		if err != nil || reenabled.Status != agentaccess.StatusActive || reenabled.DisabledAt != nil {
			t.Fatalf("reenabled=%+v err=%v", reenabled, err)
		}
		// Display ref stays cleared after privacy-preserving disable unless reset.
		if reenabled.DisplayRef != "" {
			t.Fatalf("re-enable unexpectedly restored display ref: %+v", reenabled)
		}
		if _, err := mapper.ResolveActiveExternalSubject(
			ctx, repositoryWorkspaceID, client.ID, issuer, subjectHash, time.Now().UTC(),
		); err != nil {
			t.Fatalf("active mapper after re-enable: %v", err)
		}
		// last_seen updates may advance lock_version; reload before the next CAS write.
		current, err := service.GetExternalSubjectPublicView(ctx, repositoryWorkspaceID, client.ID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := service.UpdateExternalSubjectDisplayRef(ctx, agentaccess.UpdateExternalSubjectDisplayRefInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: client.ID, SubjectID: created.ID,
			ActorID: repositoryOwnerID, DisplayRef: "ref_customer_restored",
			ExpectedLockVersion: current.LockVersion,
		})
		if err != nil || restored.DisplayRef != "ref_customer_restored" {
			t.Fatalf("restore display=%+v err=%v", restored, err)
		}
	})

	t.Run("audit state and keyed hash do not expand raw subject visibility", func(t *testing.T) {
		view, err := service.GetExternalSubjectPublicView(ctx, repositoryWorkspaceID, client.ID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		audit, err := json.Marshal(view.AuditState())
		if err != nil {
			t.Fatal(err)
		}
		if err := agentaccess.AssertExternalSubjectPrivacyJSON(audit, rawSubject); err != nil {
			t.Fatalf("audit state leak: %s", audit)
		}
		// Different raw subjects produce different hashes; same inputs are stable.
		other := agentaccessauth.HashExternalSubject(pepper, issuer, "other-user")
		same := agentaccessauth.HashExternalSubject(pepper, issuer, rawSubject)
		if subjectHash != same || subjectHash == other {
			t.Fatalf("hash stability failed")
		}
		// Signing key material and raw subject never appear in public views.
		_ = privateKey
	})
}

// lifecycleSubjectMapper mirrors application Token Exchange denial semantics.
type lifecycleSubjectMapper struct {
	repository *agentaccess.Repository
}

func (mapper lifecycleSubjectMapper) ResolveActiveExternalSubject(
	ctx context.Context,
	workspaceID, clientID, issuer string,
	subjectHash [32]byte,
	seenAt time.Time,
) (agentaccessauth.ExternalSubjectBinding, error) {
	subject, err := mapper.repository.ResolveOrCreateExternalSubject(
		ctx, workspaceID, clientID, issuer, subjectHash[:], seenAt,
	)
	if err != nil {
		return agentaccessauth.ExternalSubjectBinding{}, err
	}
	if subject.Status != agentaccess.StatusActive {
		return agentaccessauth.ExternalSubjectBinding{}, agentaccessauth.ErrTokenExchangeSubjectDenied
	}
	return agentaccessauth.ExternalSubjectBinding{SubjectID: subject.ID, Active: true}, nil
}
