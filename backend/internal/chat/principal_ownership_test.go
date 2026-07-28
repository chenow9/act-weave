package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

func TestPrincipalAwareChatOwnershipMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("expected migration 48, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertChatRunFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'pre-Principal Chat',$4)
	`, chatLegacySessionID, chatRunWorkspaceID, chatRunAgentID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,content_length,status,created_by
		) VALUES
		($1,$3,$4,'USER','legacy input',repeat('a',64),12,'EXECUTED',$5),
		($2,$3,$4,'ASSISTANT','legacy output',repeat('b',64),13,'EXECUTED',NULL)
	`, chatExternalMessageID, chatExternalAssistID, chatRunWorkspaceID,
		chatLegacySessionID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("Principal-aware Chat migration=%+v", version)
	}
	repository, err := chat.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := repository.GetSession(context.Background(), chatRunWorkspaceID, chatLegacySessionID)
	if err != nil || legacy.CreatedBy != chatRunOwnerID ||
		legacy.Ownership.Identity.Actor.Type != principal.TypeUser ||
		legacy.Ownership.Identity.Subject == nil || legacy.Ownership.Identity.Subject.ID != chatRunOwnerID ||
		legacy.Ownership.Mode != chat.OwnershipSubjectOwned || legacy.Ownership.PolicyVersion != 1 {
		t.Fatalf("legacy Session backfill=%+v err=%v", legacy, err)
	}
	messages, err := repository.ListMessages(context.Background(), chatRunWorkspaceID, chatLegacySessionID)
	if err != nil || len(messages) != 2 || messages[0].Identity.Actor.Type != principal.TypeUser ||
		messages[1].Identity.Actor.Type != principal.TypeSystem ||
		messages[1].Identity.Actor.ID != chat.RuntimeSystemPrincipalID ||
		messages[1].Content != "legacy output" {
		t.Fatalf("legacy Message backfill=%+v err=%v", messages, err)
	}
	if _, err := db.Exec(`UPDATE chat_messages SET actor_id=$2 WHERE id=$1`, chatExternalAssistID, chatRunOwnerID); err == nil {
		t.Fatal("backfilled Message identity was mutable")
	}

}

const (
	chatPrincipalID       = "a18f1f2e-7b5a-7c3d-8e9f-123456789001"
	chatClientID          = "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	chatGrantID           = "a18f1f2e-7b5a-7c3d-8e9f-12345678900f"
	chatSubjectAID        = "a18f1f2e-7b5a-7c3d-8e9f-123456789003"
	chatSubjectBID        = "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
	chatSubjectSessionID  = "a18f1f2e-7b5a-7c3d-8e9f-123456789005"
	chatSubjectBSessionID = "a18f1f2e-7b5a-7c3d-8e9f-123456789006"
	chatServiceSessionID  = "a18f1f2e-7b5a-7c3d-8e9f-123456789007"
	chatSharedSessionID   = "a18f1f2e-7b5a-7c3d-8e9f-123456789008"
	chatLegacySessionID   = "a18f1f2e-7b5a-7c3d-8e9f-123456789009"
	chatExternalMessageID = "a18f1f2e-7b5a-7c3d-8e9f-12345678900a"
	chatExternalRunID     = "a18f1f2e-7b5a-7c3d-8e9f-12345678900b"
	chatExternalAssistID  = "a18f1f2e-7b5a-7c3d-8e9f-12345678900c"
)

func TestPrincipalAwareChatOwnership(t *testing.T) {
	repository, runs, service, db := newChatRunTest(t)
	ctx := context.Background()
	insertPrincipalChatFixtures(t, db)

	actor := principal.Ref{WorkspaceID: chatRunWorkspaceID, Type: principal.TypeServicePrincipal, ID: chatPrincipalID}
	subjectA := principal.Ref{WorkspaceID: chatRunWorkspaceID, Type: principal.TypeExternalSubject, ID: chatSubjectAID}
	subjectB := principal.Ref{WorkspaceID: chatRunWorkspaceID, Type: principal.TypeExternalSubject, ID: chatSubjectBID}
	identityA, err := principal.NewInvocationIdentity(actor, &subjectA)
	if err != nil {
		t.Fatal(err)
	}
	identityB, err := principal.NewInvocationIdentity(actor, &subjectB)
	if err != nil {
		t.Fatal(err)
	}
	serviceIdentity, err := principal.NewInvocationIdentity(actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	accessA := chat.Access{Identity: identityA, ClientID: chatClientID}
	accessB := chat.Access{Identity: identityB, ClientID: chatClientID}
	serviceAccess := chat.Access{Identity: serviceIdentity, ClientID: chatClientID}
	executionSnapshot, err := principal.NewExecutionSnapshot(
		identityA, chatClientID, chatGrantID, 1, 1,
	)
	if err != nil {
		t.Fatal(err)
	}

	sessionA := createPrincipalSession(t, repository, chatSubjectSessionID, chat.Ownership{
		Identity: identityA, ClientID: chatClientID,
		Mode: chat.OwnershipSubjectOwned, PolicyVersion: 7,
	})
	createPrincipalSession(t, repository, chatSubjectBSessionID, chat.Ownership{
		Identity: identityB, ClientID: chatClientID,
		Mode: chat.OwnershipSubjectOwned, PolicyVersion: 7,
	})
	createPrincipalSession(t, repository, chatServiceSessionID, chat.Ownership{
		Identity: serviceIdentity, ClientID: chatClientID,
		Mode: chat.OwnershipSubjectOwned, PolicyVersion: 7,
	})
	createPrincipalSession(t, repository, chatSharedSessionID, chat.Ownership{
		Identity: serviceIdentity, ClientID: chatClientID,
		Mode: chat.OwnershipPolicyShared, PolicyVersion: 8,
	})

	assertVisibleSessionIDs(t, repository, accessA, chatSubjectSessionID)
	assertVisibleSessionIDs(t, repository, accessB, chatSubjectBSessionID)
	assertVisibleSessionIDs(t, repository, serviceAccess, chatServiceSessionID, chatSharedSessionID)
	if _, err := repository.GetSessionForPrincipal(ctx, accessB, chatSubjectSessionID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("same Client different Subject observed private Session: %v", err)
	}
	if _, err := repository.GetSessionForPrincipal(ctx, accessA, chatServiceSessionID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("delegated Subject observed pure Service Principal private Session: %v", err)
	}
	if _, err := repository.GetSessionForPrincipal(ctx, accessA, chatSharedSessionID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("policy-shared Session was visible without explicit Policy result: %v", err)
	}
	sharedA := accessA
	sharedA.AllowPolicyShared = true
	sharedB := accessB
	sharedB.AllowPolicyShared = true
	if value, err := repository.GetSessionForPrincipal(ctx, sharedA, chatSharedSessionID); err != nil || value.ID != chatSharedSessionID {
		t.Fatalf("explicit Policy did not expose shared Service Principal Session: %+v %v", value, err)
	}
	if value, err := repository.GetSessionForPrincipal(ctx, sharedB, chatSharedSessionID); err != nil || value.ID != chatSharedSessionID {
		t.Fatalf("shared Session not consistently visible inside Client: %+v %v", value, err)
	}

	legacy, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatLegacySessionID, WorkspaceID: chatRunWorkspaceID,
		AgentID: chatRunAgentID, Title: "legacy user", CreatedBy: chatRunOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CreatedBy != chatRunOwnerID || legacy.Ownership.Identity.Actor.Type != principal.TypeUser ||
		legacy.Ownership.Identity.Subject == nil || legacy.Ownership.Identity.Subject.ID != chatRunOwnerID {
		t.Fatalf("legacy User Session semantics changed: %+v", legacy)
	}
	userSessions, err := repository.ListSessions(ctx, chatRunWorkspaceID, chatRunOwnerID, 10)
	if err != nil || !containsSession(userSessions, chatLegacySessionID) {
		t.Fatalf("legacy User could not list existing ownership: %+v %v", userSessions, err)
	}

	sent, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatExternalMessageID, RunID: chatExternalRunID,
		WorkspaceID: chatRunWorkspaceID, SessionID: sessionA.ID,
		Content: "external subject A", TraceID: "trace-principal-chat", Access: &accessA,
		PrincipalSnapshot: &executionSnapshot,
	})
	if err != nil {
		t.Fatalf("send external Principal message: %v", err)
	}
	if sent.Message.CreatedBy != "" || sent.Message.Identity.Actor.ID != chatPrincipalID ||
		sent.Message.Identity.Subject == nil || sent.Message.Identity.Subject.ID != chatSubjectAID ||
		sent.Message.PolicyVersion != 7 {
		t.Fatalf("external Message identity snapshot is incomplete: %+v", sent.Message)
	}
	run, err := runs.GetAgentRun(ctx, chatRunWorkspaceID, chatExternalRunID)
	if err != nil || run.TriggeredByType != "SERVICE_PRINCIPAL" || run.TriggeredByID != chatPrincipalID {
		t.Fatalf("external Chat Run used the wrong transport Actor: %+v %v", run, err)
	}
	if _, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID:   "a18f1f2e-7b5a-7c3d-8e9f-12345678900d",
		RunID:       "a18f1f2e-7b5a-7c3d-8e9f-12345678900e",
		WorkspaceID: chatRunWorkspaceID, SessionID: sessionA.ID,
		Content: "subject B attempts IDOR", TraceID: "trace-subject-idor", Access: &accessB,
		PrincipalSnapshot: &executionSnapshot,
	}); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("cross-Subject SendMessage did not conceal Session: %v", err)
	}
	if _, err := repository.ListMessagesForPrincipal(ctx, accessB, sessionA.ID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("cross-Subject Message list did not conceal Session: %v", err)
	}

	assistant, err := service.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: chatExternalAssistID, WorkspaceID: chatRunWorkspaceID,
		SessionID: sessionA.ID, UserMessageID: chatExternalMessageID,
		RunID: chatExternalRunID, Content: "external answer",
		ExpectedRunStatus: "RUNNING", ExpectedRunLock: 1, RunStatus: "SUCCEEDED",
		RunOutputSummary: []byte(`{"answer":"ok"}`),
	})
	if err != nil {
		t.Fatalf("record external assistant result: %v", err)
	}
	if assistant.Message.Identity.Actor.Type != principal.TypeSystem ||
		assistant.Message.Identity.Actor.ID != chat.RuntimeSystemPrincipalID ||
		assistant.Message.Identity.Subject == nil || assistant.Message.Identity.Subject.ID != chatSubjectAID {
		t.Fatalf("assistant Message did not retain owner and explicit SYSTEM actor: %+v", assistant.Message)
	}

	if _, err := repository.ArchiveSessionForPrincipal(ctx, accessB, sessionA.ID, 2); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("cross-Subject archive did not conceal Session: %v", err)
	}
	if _, err := repository.ArchiveSessionForPrincipal(ctx, accessA, sessionA.ID, 2); err != nil {
		t.Fatalf("owner could not archive Session: %v", err)
	}
	for _, mutation := range []struct {
		statement string
		id        string
		argument  any
	}{
		{`UPDATE chat_sessions SET subject_id=$2 WHERE id=$1`, chatSubjectSessionID, chatSubjectBID},
		{`UPDATE chat_messages SET subject_id=$2 WHERE id=$1`, chatExternalMessageID, chatSubjectBID},
		{`UPDATE chat_messages SET content='rewritten' WHERE id=$1`, chatExternalMessageID, nil},
		{`UPDATE chat_messages SET content_length=999 WHERE id=$1`, chatExternalMessageID, nil},
		{`DELETE FROM chat_messages WHERE id=$1`, chatExternalMessageID, nil},
	} {
		var err error
		if mutation.argument == nil {
			_, err = db.Exec(mutation.statement, mutation.id)
		} else {
			_, err = db.Exec(mutation.statement, mutation.id, mutation.argument)
		}
		if err == nil {
			t.Fatalf("permanent Chat ownership/content mutation unexpectedly succeeded: %s", mutation.statement)
		}
	}
	var fakeUsers int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE id=$1`, chatPrincipalID).Scan(&fakeUsers); err != nil || fakeUsers != 0 {
		t.Fatalf("external Chat manufactured a User: count=%d err=%v", fakeUsers, err)
	}
	var createdBy *string
	if err := db.QueryRow(`SELECT created_by::TEXT FROM chat_sessions WHERE id=$1`, chatSubjectSessionID).Scan(&createdBy); err != nil || createdBy != nil {
		t.Fatalf("external Session should not project a fake created_by: %v %v", createdBy, err)
	}
}

func TestSubjectOwnershipPolicyChatRepository(t *testing.T) {
	const (
		ownedRunID          = "a18f1f2e-7b5a-7c3d-8e9f-123456789021"
		artifactID          = "a18f1f2e-7b5a-7c3d-8e9f-123456789022"
		duplicateArtifactID = "a18f1f2e-7b5a-7c3d-8e9f-123456789023"
		sharedRunID         = "a18f1f2e-7b5a-7c3d-8e9f-123456789024"
	)
	repository, runs, _, db := newChatRunTest(t)
	insertPrincipalChatFixtures(t, db)
	actor := principal.Ref{WorkspaceID: chatRunWorkspaceID, Type: principal.TypeServicePrincipal, ID: chatPrincipalID}
	subjectA := principal.Ref{WorkspaceID: chatRunWorkspaceID, Type: principal.TypeExternalSubject, ID: chatSubjectAID}
	subjectB := principal.Ref{WorkspaceID: chatRunWorkspaceID, Type: principal.TypeExternalSubject, ID: chatSubjectBID}
	identityA, err := principal.NewInvocationIdentity(actor, &subjectA)
	if err != nil {
		t.Fatal(err)
	}
	identityB, err := principal.NewInvocationIdentity(actor, &subjectB)
	if err != nil {
		t.Fatal(err)
	}
	createPrincipalSession(t, repository, chatSubjectSessionID, chat.Ownership{
		Identity: identityA, ClientID: chatClientID,
		Mode: chat.OwnershipSubjectOwned, PolicyVersion: 7,
	})
	serviceIdentity, err := principal.NewInvocationIdentity(actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	createPrincipalSession(t, repository, chatSharedSessionID, chat.Ownership{
		Identity: serviceIdentity, ClientID: chatClientID,
		Mode: chat.OwnershipPolicyShared, PolicyVersion: 8,
	})
	executionSnapshot, err := principal.NewExecutionSnapshot(identityA, chatClientID, chatGrantID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.StartAgentRun(context.Background(), execution.StartAgentRunInput{
		ID: ownedRunID, WorkspaceID: chatRunWorkspaceID, SessionID: chatSubjectSessionID,
		AgentID: chatRunAgentID, TriggerType: "AAP", TriggeredByType: "SERVICE_PRINCIPAL",
		TriggeredByID: chatPrincipalID, TraceID: "trace-subject-ownership-policy",
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: "run.v1", Model: json.RawMessage(`{"model":"ownership"}`),
			Capabilities: json.RawMessage(`{"releases":[]}`), ContextPolicy: json.RawMessage(`{}`),
		},
		AuthorizationSnapshot: json.RawMessage(`{"action":"run:create"}`),
		InputSummary:          json.RawMessage(`{"contentType":"text"}`), PrincipalSnapshot: &executionSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.StartAgentRun(context.Background(), execution.StartAgentRunInput{
		ID: sharedRunID, WorkspaceID: chatRunWorkspaceID, SessionID: chatSharedSessionID,
		AgentID: chatRunAgentID, TriggerType: "AAP", TriggeredByType: "SERVICE_PRINCIPAL",
		TriggeredByID: chatPrincipalID, TraceID: "trace-shared-subject-ownership-policy",
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: "run.v1", Model: json.RawMessage(`{"model":"ownership"}`),
			Capabilities: json.RawMessage(`{"releases":[]}`), ContextPolicy: json.RawMessage(`{}`),
		},
		AuthorizationSnapshot: json.RawMessage(`{"action":"run:create"}`),
		InputSummary:          json.RawMessage(`{"contentType":"text"}`), PrincipalSnapshot: &executionSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	store, err := agentaccessauth.NewSubjectOwnershipRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := agentaccessauth.NewSubjectOwnershipPolicy(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caller := agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: chatSubjectAID, ServicePrincipalID: chatPrincipalID,
		AuthorizedParty: "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		WorkspaceID:     chatRunWorkspaceID, AgentID: chatRunAgentID,
		Scopes: []string{"conversation:read"}, SecurityVersion: 1,
		TokenID:  "a18f1f2e-7b5a-7c3d-8e9f-123456789020",
		IssuedAt: now, NotBefore: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	state := agentaccessauth.AAPAuthorizationState{
		WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		ClientID: chatClientID, PublicClientID: caller.AuthorizedParty,
		ServicePrincipalID: chatPrincipalID, CurrentSecurityVersion: 1,
		GrantID: chatGrantID, GrantScopes: []string{"conversation:read"},
		AgentPolicyScopes: []string{"conversation:read"},
		WorkspaceVersion:  1, ClientVersion: 1, GrantVersion: 1, AgentPolicyVersion: 9,
	}
	privateResource := agentaccessauth.AAPAuthorizationResource{
		Type: agentaccessauth.ResourceConversation, ID: chatSubjectSessionID,
	}
	privateDecision, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, state, agentaccessauth.ActionConversationRead, privateResource,
	)
	if err != nil || privateDecision.Mode != agentaccessauth.OwnershipModeSubjectOwned ||
		privateDecision.OwnerID != chatSubjectAID {
		t.Fatalf("private ownership decision=%+v err=%v", privateDecision, err)
	}
	for _, request := range []struct {
		action   agentaccessauth.AAPAction
		resource agentaccessauth.AAPAuthorizationResource
	}{
		{agentaccessauth.ActionRunRead, agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceRun, ID: ownedRunID,
		}},
		{agentaccessauth.ActionEventRead, agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceRun, ID: ownedRunID,
		}},
	} {
		decision, err := policy.ResolveSubjectOwnership(
			context.Background(), caller, state, request.action, request.resource,
		)
		if err != nil || decision.OwnerID != chatSubjectAID {
			t.Fatalf("Run/Event ownership action=%s decision=%+v err=%v", request.action, decision, err)
		}
	}
	sharedRunResource := agentaccessauth.AAPAuthorizationResource{
		Type: agentaccessauth.ResourceRun, ID: sharedRunID,
	}
	if _, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, state, agentaccessauth.ActionRunRead, sharedRunResource,
	); !errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound) {
		t.Fatalf("shared Run was visible without run sharing policy: %v", err)
	}
	sharedRunState := state
	sharedRunState.SubjectSharingResources = []string{"run"}
	sharedRunDecision, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, sharedRunState,
		agentaccessauth.ActionRunRead, sharedRunResource,
	)
	if err != nil || sharedRunDecision.Mode != agentaccessauth.OwnershipModePolicyShared ||
		sharedRunDecision.PolicyVersion != sharedRunState.GrantVersion {
		t.Fatalf("shared Run ownership=%+v err=%v", sharedRunDecision, err)
	}
	other := caller
	other.PrincipalID = chatSubjectBID
	if _, err := policy.ResolveSubjectOwnership(
		context.Background(), other, state, agentaccessauth.ActionConversationRead, privateResource,
	); !errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound) {
		t.Fatalf("cross-Subject policy did not conceal Session: %v", err)
	}
	if _, err := repository.GetSessionForPrincipal(context.Background(), chat.Access{
		Identity: identityB, ClientID: chatClientID,
	}, chatSubjectSessionID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("cross-Subject SQL access did not return not found: %v", err)
	}

	sharedResource := agentaccessauth.AAPAuthorizationResource{
		Type: agentaccessauth.ResourceConversation, ID: chatSharedSessionID,
	}
	if _, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, state, agentaccessauth.ActionConversationRead, sharedResource,
	); !errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound) {
		t.Fatalf("shared Session was visible without current Grant policy: %v", err)
	}
	state.SubjectSharingResources = []string{"conversation"}
	sharedDecision, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, state, agentaccessauth.ActionConversationRead, sharedResource,
	)
	if err != nil || sharedDecision.Mode != agentaccessauth.OwnershipModePolicyShared ||
		sharedDecision.PolicyVersion != state.GrantVersion {
		t.Fatalf("shared ownership decision=%+v err=%v", sharedDecision, err)
	}
	accessA := chat.Access{Identity: identityA, ClientID: chatClientID,
		AllowPolicyShared: sharedDecision.Mode == agentaccessauth.OwnershipModePolicyShared}
	if value, err := repository.GetSessionForPrincipal(
		context.Background(), accessA, chatSharedSessionID,
	); err != nil || value.ID != chatSharedSessionID {
		t.Fatalf("explicit shared policy did not reach Chat SQL predicate: %+v err=%v", value, err)
	}

	if _, err := db.Exec(`INSERT INTO stored_objects(
	 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
	 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
	 ) VALUES($1,$2,'ownership-artifacts','runs/owned-output.txt','PROMPT_RUN_OUTPUT',
	 'text/plain',2,$3,'ownership-key-v1','SENSITIVE','PERMANENT','USER',$4)`,
		artifactID, chatRunWorkspaceID, strings.Repeat("a", 64), chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO run_items(
	 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,
	 source_id,snapshot,completed_at
	 ) VALUES($1,$2,$3,$4,1,'artifact','completed','STORED_OBJECT',$1,'{}',clock_timestamp())`,
		artifactID, chatRunWorkspaceID, chatRunAgentID, ownedRunID); err != nil {
		t.Fatal(err)
	}
	artifactResource := agentaccessauth.AAPAuthorizationResource{
		Type: agentaccessauth.ResourceArtifact, ID: artifactID,
	}
	artifactDecision, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, state, agentaccessauth.ActionArtifactRead, artifactResource,
	)
	if err != nil || artifactDecision.OwnerID != chatSubjectAID {
		t.Fatalf("unique Artifact ownership decision=%+v err=%v", artifactDecision, err)
	}
	if _, err := db.Exec(`INSERT INTO run_items(
	 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,
	 source_id,snapshot,completed_at
	 ) VALUES($1,$2,$3,$4,2,'artifact','completed','STORED_OBJECT',$5,'{}',clock_timestamp())`,
		duplicateArtifactID, chatRunWorkspaceID, chatRunAgentID, ownedRunID, artifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.ResolveSubjectOwnership(
		context.Background(), caller, state, agentaccessauth.ActionArtifactRead, artifactResource,
	); !errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound) {
		t.Fatalf("ambiguous Artifact Run ownership was not concealed: %v", err)
	}
}

func insertPrincipalChatFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO service_principals(
		 id,workspace_id,name,created_by,updated_by
		) VALUES($1,$2,'Principal Chat Client',$3,$3)
	`, chatPrincipalID, chatRunWorkspaceID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,
		 token_ttl_seconds,created_by,updated_by
		) VALUES($1,$2,$3,'awcl_12345678901234567890123456789012',
		 'Principal Chat Client','client_secret_basic',600,$4,$4)
	`, chatClientID, chatRunWorkspaceID, chatPrincipalID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO external_subjects(
		 id,workspace_id,client_id,issuer,subject_hash,display_ref
		) VALUES
		($1,$3,$4,'https://identity.example.test',decode(repeat('11',32),'hex'),'ref_subject_a'),
		($2,$3,$4,'https://identity.example.test',decode(repeat('22',32),'hex'),'ref_subject_b')
	`, chatSubjectAID, chatSubjectBID, chatRunWorkspaceID, chatClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_grants(
		 id,workspace_id,client_id,agent_id,scopes,policy,created_by,updated_by
		) VALUES($1,$2,$3,$4,'["run:create"]','{}',$5,$5)
	`, chatGrantID, chatRunWorkspaceID, chatClientID, chatRunAgentID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
}

func createPrincipalSession(t *testing.T, repository *chat.Repository, id string, ownership chat.Ownership) chat.Session {
	t.Helper()
	value, err := repository.CreateSession(context.Background(), chat.CreateSessionInput{
		ID: id, WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		Title: id, Ownership: &ownership,
	})
	if err != nil {
		t.Fatalf("create Principal Session %s: %v", id, err)
	}
	return value
}

func assertVisibleSessionIDs(t *testing.T, repository *chat.Repository, access chat.Access, expected ...string) {
	t.Helper()
	values, err := repository.ListSessionsForPrincipal(context.Background(), access, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != len(expected) {
		t.Fatalf("unexpected visible Sessions: got=%v want=%v", sessionIDs(values), expected)
	}
	for _, id := range expected {
		if !containsSession(values, id) {
			t.Fatalf("missing visible Session %s in %v", id, sessionIDs(values))
		}
	}
}

func containsSession(values []chat.Session, id string) bool {
	for _, value := range values {
		if value.ID == id {
			return true
		}
	}
	return false
}

func sessionIDs(values []chat.Session) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].ID
	}
	return result
}
