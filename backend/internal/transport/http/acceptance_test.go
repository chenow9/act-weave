package httptransport

import "testing"

// TestAcceptanceV1CoreFlows is the stable entry point for the release-level
// HTTP acceptance command. Each case exercises the production router and its
// real PostgreSQL repositories through the focused contract test named here.
func TestAcceptanceV1CoreFlows(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "authentication", run: TestV1AuthLoginRefreshLogoutAndMe},
		{name: "admin_user_commands", run: TestV1UserProfilePasswordAndAdminCommands},
		{name: "workspace_crud_and_isolation", run: TestV1WorkspaceCRUDStatusAndIsolation},
		{name: "member_rbac", run: TestV1MemberRBACPathOwnershipAndCrossWorkspaceNotVisible},
		{name: "model_config_crud", run: TestV1ModelConfigRoutes},
		{name: "provider_crud", run: TestV1ProviderRoutes},
		{name: "connection_crud", run: TestV1ConnectionRoutes},
		{name: "secret_rotation", run: TestV1SecretRotateDoesNotExposePlaintext},
		{name: "agent_crud", run: TestV1AgentRoutesPersistPreviewAndAcceptedEnhancement},
		{name: "capability_binding", run: TestV1CapabilityBindingRoutesDeriveCounts},
		{name: "tool_test_publish_and_invoke", run: TestV1ToolLifecyclePublishesExactVersionAndForwardsIdempotency},
		{name: "workflow_compile_trial_publish_and_rollback", run: TestV1WorkflowLifecycleUsesCASAndImmutableCompilationIDs},
		{name: "smart_dag_generate_compile_trial_publish", run: TestV1SmartDAGGeneratesFormalDraftAndUsesWorkflowLifecycle},
		{name: "chat_permanent_original", run: TestV1ChatSessionMessageArchiveAndPermanentOriginal},
		{name: "execution_and_sse", run: TestV1ExecutionListDetailAndSSEContinuation},
		{name: "chat_confirmation_idempotency", run: TestV1ConfirmationIsRequesterOnlyAndIdempotent},
		{name: "audit_query_and_export", run: TestV1AuditRoleCroppingAndExport},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
