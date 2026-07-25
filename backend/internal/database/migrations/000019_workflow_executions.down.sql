ALTER TABLE workflow_trial_runs
    DROP CONSTRAINT IF EXISTS workflow_trial_runs_execution_fk;
DELETE FROM workflow_trial_runs
WHERE EXISTS (
    SELECT 1
    FROM workflow_executions
    WHERE workflow_executions.workspace_id = workflow_trial_runs.workspace_id
      AND workflow_executions.id = workflow_trial_runs.execution_id
);
DROP TRIGGER IF EXISTS execution_steps_state_guard ON execution_steps;
DROP FUNCTION IF EXISTS enforce_execution_step_state();
DROP TABLE IF EXISTS execution_steps;
DROP TRIGGER IF EXISTS workflow_executions_state_guard ON workflow_executions;
DROP FUNCTION IF EXISTS enforce_workflow_execution_state();
DROP TABLE IF EXISTS workflow_executions;
