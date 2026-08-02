-- Reverse 000017 (best-effort).

DROP TRIGGER IF EXISTS agent_run_delegations_terminal_evidence_immutable_trg
    ON agent_run_delegations;
DROP FUNCTION IF EXISTS agent_run_delegations_terminal_evidence_immutable();

DROP TABLE IF EXISTS agent_a2a_protocol_tasks;

-- Restore alias PK without actor (may fail if multi-actor same external_task_id exists).
ALTER TABLE agent_a2a_inbound_task_aliases
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_task_aliases_pkey;
ALTER TABLE agent_a2a_inbound_task_aliases
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_task_aliases_actor_not_blank;

-- Collapse to pre-actor uniqueness: keep one row per (workspace, exposure, external_task_id).
DELETE FROM agent_a2a_inbound_task_aliases a
USING agent_a2a_inbound_task_aliases b
WHERE a.workspace_id = b.workspace_id
  AND a.exposure_id = b.exposure_id
  AND a.external_task_id = b.external_task_id
  AND a.ctid < b.ctid;

ALTER TABLE agent_a2a_inbound_task_aliases
    ADD CONSTRAINT agent_a2a_inbound_task_aliases_pkey
        PRIMARY KEY (workspace_id, exposure_id, external_task_id);

ALTER TABLE agent_a2a_inbound_task_aliases
    DROP COLUMN IF EXISTS actor_type,
    DROP COLUMN IF EXISTS actor_id;

CREATE OR REPLACE FUNCTION enforce_agent_a2a_inbound_task_aliases_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent_a2a_inbound_task_aliases rows are permanent evidence (DELETE denied)'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF ROW(
            NEW.workspace_id, NEW.exposure_id, NEW.external_task_id,
            NEW.inbound_task_id, NEW.created_at
        ) IS DISTINCT FROM ROW(
            OLD.workspace_id, OLD.exposure_id, OLD.external_task_id,
            OLD.inbound_task_id, OLD.created_at
        ) THEN
            RAISE EXCEPTION 'agent_a2a_inbound_task_aliases rows are permanent evidence (UPDATE denied)'
                USING ERRCODE = '55000';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP INDEX IF EXISTS agent_a2a_inbound_tasks_actor_idx;
DROP INDEX IF EXISTS agent_a2a_inbound_tasks_idempotency_uidx;
CREATE UNIQUE INDEX agent_a2a_inbound_tasks_idempotency_uidx
    ON agent_a2a_inbound_tasks (workspace_id, exposure_id, external_key);

ALTER TABLE agent_a2a_inbound_tasks
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_tasks_actor_not_blank;
ALTER TABLE agent_a2a_inbound_tasks
    DROP COLUMN IF EXISTS actor_type,
    DROP COLUMN IF EXISTS actor_id;
