package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	principalConfirmationID         = "c28f1f2e-7b5a-7c3d-8e9f-123456789001"
	principalConfirmationOtherID    = "c28f1f2e-7b5a-7c3d-8e9f-123456789002"
	principalConfirmationPureRunID  = "c28f1f2e-7b5a-7c3d-8e9f-123456789003"
	principalConfirmationLowID      = "c28f1f2e-7b5a-7c3d-8e9f-123456789004"
	principalConfirmationHighID     = "c28f1f2e-7b5a-7c3d-8e9f-123456789005"
	principalConfirmationOtherSubID = "c28f1f2e-7b5a-7c3d-8e9f-123456789006"
	principalConfirmationLegacyID   = "c28f1f2e-7b5a-7c3d-8e9f-123456789007"
	principalConfirmationUserID     = "c28f1f2e-7b5a-7c3d-8e9f-123456789008"
)

func TestPrincipalAwareConfirmation(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected Interaction decision binding migration 61, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	insertExternalExecutionFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO external_subjects(id,workspace_id,client_id,issuer,subject_hash,display_ref)
		VALUES($1,$2,$3,'https://execution-identity.example.test',
		 decode(repeat('44',32),'hex'),'ref_other_confirmation_subject')
	`, principalConfirmationOtherSubID, executionWorkspaceID, externalExecutionClientID); err != nil {
		t.Fatal(err)
	}

	requestSnapshot := externalConfirmationSnapshot(t, externalExecutionSubjectID, 1, 1)
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.StartAgentRun(context.Background(), externalAgentRunInput(
		externalExecutionRunID, &requestSnapshot,
	)); err != nil {
		t.Fatal(err)
	}
	repository, err := execution.NewConfirmationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	service, err := execution.NewConfirmationService(repository,
		execution.WithConfirmationClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	highInput := json.RawMessage(`{"orderId":"A-1","amount":88}`)
	highDecision := principalConfirmationDecision(t, highInput, "HIGH", "WRITE", "TEST")
	requested, err := service.Request(ctx, execution.RequestExecutionConfirmationInput{
		ID: principalConfirmationID, WorkspaceID: executionWorkspaceID,
		RunID: externalExecutionRunID, NodeID: "external-refund",
		TargetItemID: principalConfirmationID,
		ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, RequestedBy: externalExecutionPrincipalID,
		PrincipalSnapshot: &requestSnapshot, Decision: highDecision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requested.Confirmation.RequestedBy != "" ||
		!requested.Confirmation.RequestPrincipalSnapshot.SameBinding(requestSnapshot) {
		t.Fatalf("external request projection=%+v", requested.Confirmation)
	}
	ownershipRepository, err := agentaccessauth.NewSubjectOwnershipRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ownershipPolicy, err := agentaccessauth.NewSubjectOwnershipPolicy(ownershipRepository)
	if err != nil {
		t.Fatal(err)
	}
	ownershipCaller := agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: externalExecutionSubjectID, ServicePrincipalID: externalExecutionPrincipalID,
		AuthorizedParty: "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		WorkspaceID:     executionWorkspaceID, AgentID: executionAgentID,
		Scopes: []string{"interaction:decide"}, SecurityVersion: 1,
		TokenID:  "c28f1f2e-7b5a-7c3d-8e9f-123456789009",
		IssuedAt: now, NotBefore: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	ownershipState := agentaccessauth.AAPAuthorizationState{
		WorkspaceID: executionWorkspaceID, AgentID: executionAgentID,
		ClientID: externalExecutionClientID, PublicClientID: ownershipCaller.AuthorizedParty,
		ServicePrincipalID: externalExecutionPrincipalID, CurrentSecurityVersion: 1,
		GrantID: externalExecutionGrantID, GrantScopes: []string{"interaction:decide"},
		AgentPolicyScopes: []string{"interaction:decide"},
		WorkspaceVersion:  1, ClientVersion: 1, GrantVersion: 1, AgentPolicyVersion: 1,
	}
	interactionResource := agentaccessauth.AAPAuthorizationResource{
		Type: agentaccessauth.ResourceInteraction, ID: principalConfirmationID,
	}
	ownershipDecision, err := ownershipPolicy.ResolveSubjectOwnership(
		ctx, ownershipCaller, ownershipState,
		agentaccessauth.ActionInteractionDecide, interactionResource,
	)
	if err != nil || ownershipDecision.OwnerID != externalExecutionSubjectID ||
		ownershipDecision.Mode != agentaccessauth.OwnershipModeSubjectOwned {
		t.Fatalf("Interaction ownership=%+v err=%v", ownershipDecision, err)
	}
	otherOwnershipCaller := ownershipCaller
	otherOwnershipCaller.PrincipalID = principalConfirmationOtherSubID
	if _, err := ownershipPolicy.ResolveSubjectOwnership(
		ctx, otherOwnershipCaller, ownershipState,
		agentaccessauth.ActionInteractionDecide, interactionResource,
	); !errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound) {
		t.Fatalf("cross-Subject Interaction ownership was not concealed: %v", err)
	}

	// A currently authorized token can carry newer Grant/Agent versions while
	// the same Client+Subject remains the only permitted decider.
	if _, err := db.Exec(`
		UPDATE agent_access_grants SET scopes='["run:create","interaction:decide"]',
		 policy='{"serviceDecision":{"enabled":true,"maxRisk":"medium"}}',
		 lock_version=2,updated_at=clock_timestamp() WHERE id=$1
	`, externalExecutionGrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET lock_version=2,updated_at=clock_timestamp() WHERE id=$1`, executionAgentID); err != nil {
		t.Fatal(err)
	}
	decisionSnapshot := externalConfirmationSnapshot(t, externalExecutionSubjectID, 2, 2)
	otherSubjectSnapshot := externalConfirmationSnapshot(t, principalConfirmationOtherSubID, 2, 2)
	wrongSubject := principalConfirmationConfirmInput(requested, highInput, &otherSubjectSnapshot)
	if _, err := service.Confirm(ctx, wrongSubject); !errors.Is(err, execution.ErrConfirmationRequesterMismatch) {
		t.Fatalf("different Subject decision error=%v", err)
	}
	confirmed, err := service.Confirm(ctx, principalConfirmationConfirmInput(
		requested, highInput, &decisionSnapshot,
	))
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.ConfirmedBy != "" || confirmed.DecisionPrincipalSnapshot == nil ||
		confirmed.DecisionPrincipalSnapshot.GrantVersion != 2 ||
		!confirmed.RequestPrincipalSnapshot.SameDecisionPrincipal(*confirmed.DecisionPrincipalSnapshot) {
		t.Fatalf("external decision snapshot=%+v", confirmed)
	}

	var requestSubject, decisionSubject, clientID, grantID string
	var requestVersion, decisionVersion int64
	if err := db.QueryRow(`
		SELECT request_subject_id::TEXT,decision_subject_id::TEXT,
		 request_client_id::TEXT,request_grant_id::TEXT,
		 request_grant_version,decision_grant_version
		FROM execution_confirmations WHERE workspace_id=$1 AND id=$2
	`, executionWorkspaceID, principalConfirmationID).Scan(
		&requestSubject, &decisionSubject, &clientID, &grantID,
		&requestVersion, &decisionVersion,
	); err != nil {
		t.Fatal(err)
	}
	if requestSubject != externalExecutionSubjectID || decisionSubject != externalExecutionSubjectID ||
		clientID != externalExecutionClientID || grantID != externalExecutionGrantID ||
		requestVersion != 1 || decisionVersion != 2 {
		t.Fatalf("confirmation audit binding=%s/%s client=%s grant=%s versions=%d/%d",
			requestSubject, decisionSubject, clientID, grantID, requestVersion, decisionVersion)
	}
	if _, err := db.Exec(`
		UPDATE execution_confirmations SET decision_subject_id=$2,lock_version=lock_version+1 WHERE id=$1
	`, principalConfirmationID, principalConfirmationOtherSubID); err == nil {
		t.Fatal("terminal confirmation decision Principal was mutable")
	}

	// A pure Service Principal can decide only when the current Grant policy
	// explicitly enables a LOW/MEDIUM decision and the policy snapshot matches.
	pureSnapshot := pureServiceConfirmationSnapshot(t, 2, 2)
	if _, err := runs.StartAgentRun(ctx, externalAgentRunInput(
		principalConfirmationPureRunID, &pureSnapshot,
	)); err != nil {
		t.Fatal(err)
	}
	lowInput := json.RawMessage(`{"orderId":"A-2"}`)
	lowDecision := principalConfirmationDecision(t, lowInput, "LOW", "WRITE", "TEST")
	lowRequested, err := service.Request(ctx, execution.RequestExecutionConfirmationInput{
		ID: principalConfirmationLowID, WorkspaceID: executionWorkspaceID,
		RunID: principalConfirmationPureRunID, NodeID: "low-write",
		TargetItemID: principalConfirmationLowID,
		ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
		PrincipalSnapshot: &pureSnapshot, Decision: lowDecision,
	})
	if err != nil {
		t.Fatal(err)
	}
	lowConfirm := principalConfirmationConfirmInput(lowRequested, lowInput, &pureSnapshot)
	if _, err := service.Confirm(ctx, lowConfirm); !errors.Is(err, execution.ErrConfirmationRequesterMismatch) {
		t.Fatalf("pure Service Principal without policy error=%v", err)
	}
	lowConfirm.ServiceDecisionPolicy = &execution.ServicePrincipalDecisionPolicy{Enabled: true, MaxRisk: "medium"}
	if _, err := service.Confirm(ctx, lowConfirm); err != nil {
		t.Fatalf("explicit low-risk service decision: %v", err)
	}

	highRequested, err := service.Request(ctx, execution.RequestExecutionConfirmationInput{
		ID: principalConfirmationHighID, WorkspaceID: executionWorkspaceID,
		RunID: principalConfirmationPureRunID, NodeID: "high-write",
		TargetItemID: principalConfirmationHighID,
		ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
		PrincipalSnapshot: &pureSnapshot, Decision: highDecision,
	})
	if err != nil {
		t.Fatal(err)
	}
	highConfirm := principalConfirmationConfirmInput(highRequested, highInput, &pureSnapshot)
	highConfirm.ServiceDecisionPolicy = &execution.ServicePrincipalDecisionPolicy{Enabled: true, MaxRisk: "medium"}
	if _, err := service.Confirm(ctx, highConfirm); !errors.Is(err, execution.ErrConfirmationDecisionNotAllowed) {
		t.Fatalf("high-risk pure Service Principal decision error=%v", err)
	}

	// Legacy internal callers still map to User Actor=Subject without changing
	// the public Request/Confirm input shape.
	userDecision := principalConfirmationDecision(t, lowInput, "LOW", "WRITE", "TEST")
	userRequested, err := service.Request(ctx, execution.RequestExecutionConfirmationInput{
		ID: principalConfirmationUserID, WorkspaceID: executionWorkspaceID,
		RunID: executionAgentRunID, NodeID: "legacy-user",
		TargetItemID: principalConfirmationUserID,
		ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
		RequestedBy: executionOwnerID, Decision: userDecision,
	})
	if err != nil || userRequested.Confirmation.RequestedBy != executionOwnerID ||
		userRequested.Confirmation.RequestPrincipalSnapshot.Identity.Subject == nil {
		t.Fatalf("internal User compatibility=%+v err=%v", userRequested, err)
	}
}

func TestPrincipalAwareConfirmationMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("migration 50=%+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO execution_confirmations(
		 id,workspace_id,execution_id,run_id,node_id,reason,risk_reasons,
		 scope_snapshot,release_id,input_hash,connection_id,plan_hash,
		 resume_token_hash,requested_by,expires_at
		) VALUES($1,$2,$3,$4,'legacy-node','Legacy confirmation','["legacy"]',
		 '{}',$5,$6,$7,$6,$6,$8,clock_timestamp()+interval '10 minutes')
	`, principalConfirmationLegacyID, executionWorkspaceID, invocationWorkflowExecutionID,
		executionAgentRunID, invocationReleaseID, strings.Repeat("a", 64),
		invocationConnectionID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("migration 51=%+v", version)
	}
	var snapshotVersion, actorType, actorID, subjectType, subjectID string
	if err := db.QueryRow(`
		SELECT request_principal_snapshot_version,request_actor_type,request_actor_id::TEXT,
		 request_subject_type,request_subject_id::TEXT
		FROM execution_confirmations WHERE id=$1
	`, principalConfirmationLegacyID).Scan(
		&snapshotVersion, &actorType, &actorID, &subjectType, &subjectID,
	); err != nil {
		t.Fatal(err)
	}
	if snapshotVersion != "legacy.v1" || actorType != "USER" || actorID != executionOwnerID ||
		subjectType != "USER" || subjectID != executionOwnerID {
		t.Fatalf("legacy confirmation Principal=%s %s/%s %s/%s",
			snapshotVersion, actorType, actorID, subjectType, subjectID)
	}
}

func externalConfirmationSnapshot(
	t *testing.T,
	subjectID string,
	grantVersion, policyVersion int64,
) principal.ExecutionSnapshot {
	t.Helper()
	actor := principal.Ref{WorkspaceID: executionWorkspaceID, Type: principal.TypeServicePrincipal, ID: externalExecutionPrincipalID}
	subject := principal.Ref{WorkspaceID: executionWorkspaceID, Type: principal.TypeExternalSubject, ID: subjectID}
	identity, err := principal.NewInvocationIdentity(actor, &subject)
	if err != nil {
		t.Fatal(err)
	}
	value, err := principal.NewExecutionSnapshot(
		identity, externalExecutionClientID, externalExecutionGrantID, grantVersion, policyVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func pureServiceConfirmationSnapshot(
	t *testing.T,
	grantVersion, policyVersion int64,
) principal.ExecutionSnapshot {
	t.Helper()
	actor := principal.Ref{WorkspaceID: executionWorkspaceID, Type: principal.TypeServicePrincipal, ID: externalExecutionPrincipalID}
	identity, err := principal.NewInvocationIdentity(actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := principal.NewExecutionSnapshot(
		identity, externalExecutionClientID, externalExecutionGrantID, grantVersion, policyVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func principalConfirmationDecision(
	t *testing.T,
	input json.RawMessage,
	riskLevel, sideEffect, environment string,
) execution.ConfirmationDecision {
	t.Helper()
	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: invocationReleaseID, RiskLevel: riskLevel,
			SideEffectLevel: sideEffect, RequiresConfirmation: true,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: invocationConnectionID, Environment: environment,
		},
		Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func principalConfirmationConfirmInput(
	requested execution.RequestedExecutionConfirmation,
	input json.RawMessage,
	snapshot *principal.ExecutionSnapshot,
) execution.ConfirmExecutionConfirmationInput {
	return execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: requested.Confirmation.ID,
		ActorID: externalExecutionPrincipalID, PrincipalSnapshot: snapshot,
		ResumeToken: requested.ResumeToken, ReleaseID: invocationReleaseID,
		RunID: requested.Confirmation.RunID, TargetItemID: requested.Confirmation.TargetItemID,
		ConnectionID: invocationConnectionID, PlanHash: requested.Confirmation.PlanHash,
		Input: input, ExpectedLockVersion: requested.Confirmation.LockVersion,
	}
}
