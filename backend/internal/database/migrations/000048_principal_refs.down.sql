ALTER TABLE tool_invocations
    DROP CONSTRAINT tool_invocations_principal_ref_fk;
ALTER TABLE workflow_executions
    DROP CONSTRAINT workflow_executions_principal_ref_fk;
ALTER TABLE agent_runs
    DROP CONSTRAINT agent_runs_principal_ref_fk;

DROP TRIGGER external_subjects_principal_ref ON external_subjects;
DROP TRIGGER service_principals_principal_ref ON service_principals;
DROP TRIGGER workspace_members_principal_ref ON workspace_members;
DROP TRIGGER workspaces_owner_principal_ref ON workspaces;
DROP FUNCTION register_directory_principal_ref();

DROP TRIGGER principal_refs_immutable_guard ON principal_refs;
DROP FUNCTION reject_principal_ref_mutation();
DROP TRIGGER principal_refs_target_guard ON principal_refs;
DROP FUNCTION validate_principal_ref_target();

DROP TABLE principal_refs;
