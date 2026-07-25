DROP TRIGGER IF EXISTS workflows_active_revision_integrity ON workflows;
DROP FUNCTION IF EXISTS enforce_workflow_active_revision();
ALTER TABLE workflows
    DROP CONSTRAINT IF EXISTS workflows_latest_compilation_fk,
    DROP CONSTRAINT IF EXISTS workflows_active_revision_fk,
    DROP CONSTRAINT IF EXISTS workflows_current_draft_fk;
DROP TABLE IF EXISTS workflow_trial_runs;
DROP TRIGGER IF EXISTS workflow_revisions_immutable ON workflow_revisions;
DROP FUNCTION IF EXISTS reject_workflow_revision_mutation();
DROP TABLE IF EXISTS workflow_revisions;
DROP TRIGGER IF EXISTS workflow_compilations_immutable ON workflow_compilations;
DROP FUNCTION IF EXISTS reject_workflow_compilation_mutation();
DROP TABLE IF EXISTS workflow_compilations;
DROP TABLE IF EXISTS workflow_drafts;
DROP TRIGGER IF EXISTS workflows_capability_kind_integrity ON workflows;
DROP FUNCTION IF EXISTS enforce_workflow_capability_kind();
DROP TABLE IF EXISTS workflows;
