-- 000017: A2A principal-bound inbound identity, durable protocol TaskStore,
-- terminal delegation evidence immutability, and actor-scoped aliases.

-- ---------------------------------------------------------------------------
-- 1) Principal columns on durable inbound tasks
-- ---------------------------------------------------------------------------
ALTER TABLE agent_a2a_inbound_tasks
    ADD COLUMN IF NOT EXISTS actor_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN agent_a2a_inbound_tasks.actor_type IS
    'Authenticated actor type (USER/SERVICE_PRINCIPAL/SYSTEM). Part of idempotency identity.';
COMMENT ON COLUMN agent_a2a_inbound_tasks.actor_id IS
    'Authenticated actor id. AuthMode NONE uses exposure id under SYSTEM.';

-- Backfill from linked agent_run triggered_by_* (fail-closed if missing).
-- triggered_by_id is UUID; cast to text before btrim.
UPDATE agent_a2a_inbound_tasks t
SET actor_type = UPPER(BTRIM(r.triggered_by_type::text)),
    actor_id = BTRIM(r.triggered_by_id::text)
FROM agent_runs r
WHERE t.workspace_id = r.workspace_id
  AND t.run_id = r.id
  AND (BTRIM(t.actor_type) = '' OR BTRIM(t.actor_id) = '');

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agent_a2a_inbound_tasks
        WHERE length(btrim(actor_type)) = 0 OR length(btrim(actor_id)) = 0
    ) THEN
        RAISE EXCEPTION
            '000017 refuse: agent_a2a_inbound_tasks rows without backfillable actor from agent_runs.triggered_by_*'
            USING ERRCODE = '23514';
    END IF;
END $$;

ALTER TABLE agent_a2a_inbound_tasks
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_tasks_actor_not_blank;
ALTER TABLE agent_a2a_inbound_tasks
    ADD CONSTRAINT agent_a2a_inbound_tasks_actor_not_blank
        CHECK (length(btrim(actor_type)) > 0 AND length(btrim(actor_id)) > 0);

-- Unique external identity is now principal-scoped.
DROP INDEX IF EXISTS agent_a2a_inbound_tasks_idempotency_uidx;
CREATE UNIQUE INDEX agent_a2a_inbound_tasks_idempotency_uidx
    ON agent_a2a_inbound_tasks (workspace_id, exposure_id, actor_type, actor_id, external_key);

CREATE INDEX IF NOT EXISTS agent_a2a_inbound_tasks_actor_idx
    ON agent_a2a_inbound_tasks (workspace_id, exposure_id, actor_type, actor_id);

-- ---------------------------------------------------------------------------
-- 2) Principal columns on task aliases
-- ---------------------------------------------------------------------------
ALTER TABLE agent_a2a_inbound_task_aliases
    ADD COLUMN IF NOT EXISTS actor_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT '';

UPDATE agent_a2a_inbound_task_aliases a
SET actor_type = t.actor_type,
    actor_id = t.actor_id
FROM agent_a2a_inbound_tasks t
WHERE a.workspace_id = t.workspace_id
  AND a.inbound_task_id = t.id
  AND (BTRIM(a.actor_type) = '' OR BTRIM(a.actor_id) = '');

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agent_a2a_inbound_task_aliases
        WHERE length(btrim(actor_type)) = 0 OR length(btrim(actor_id)) = 0
    ) THEN
        RAISE EXCEPTION
            '000017 refuse: agent_a2a_inbound_task_aliases rows without backfillable actor'
            USING ERRCODE = '23514';
    END IF;
END $$;

ALTER TABLE agent_a2a_inbound_task_aliases
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_task_aliases_actor_not_blank;
ALTER TABLE agent_a2a_inbound_task_aliases
    ADD CONSTRAINT agent_a2a_inbound_task_aliases_actor_not_blank
        CHECK (length(btrim(actor_type)) > 0 AND length(btrim(actor_id)) > 0);

-- Recreate primary key to include actor (same external_task_id allowed for different principals).
ALTER TABLE agent_a2a_inbound_task_aliases
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_task_aliases_pkey;
ALTER TABLE agent_a2a_inbound_task_aliases
    ADD CONSTRAINT agent_a2a_inbound_task_aliases_pkey
        PRIMARY KEY (workspace_id, exposure_id, actor_type, actor_id, external_task_id);

-- Immutability trigger must include actor columns.
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
            NEW.workspace_id, NEW.exposure_id, NEW.actor_type, NEW.actor_id,
            NEW.external_task_id, NEW.inbound_task_id, NEW.created_at
        ) IS DISTINCT FROM ROW(
            OLD.workspace_id, OLD.exposure_id, OLD.actor_type, OLD.actor_id,
            OLD.external_task_id, OLD.inbound_task_id, OLD.created_at
        ) THEN
            RAISE EXCEPTION 'agent_a2a_inbound_task_aliases rows are permanent evidence (UPDATE denied)'
                USING ERRCODE = '55000';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------------------
-- 3) Durable a2a-go TaskStore (optimistic version, principal-scoped)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_a2a_protocol_tasks (
    workspace_id  UUID NOT NULL,
    exposure_id   UUID NOT NULL,
    actor_type    TEXT NOT NULL,
    actor_id      TEXT NOT NULL,
    task_id       TEXT NOT NULL,
    version       BIGINT NOT NULL DEFAULT 0,
    task_json     JSONB NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, exposure_id, actor_type, actor_id, task_id),
    CONSTRAINT agent_a2a_protocol_tasks_actor_not_blank
        CHECK (length(btrim(actor_type)) > 0 AND length(btrim(actor_id)) > 0),
    CONSTRAINT agent_a2a_protocol_tasks_task_id_not_blank
        CHECK (length(btrim(task_id)) > 0),
    CONSTRAINT agent_a2a_protocol_tasks_version_check
        CHECK (version > 0),
    CONSTRAINT agent_a2a_protocol_tasks_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_protocol_tasks_exposure_fk
        FOREIGN KEY (workspace_id, exposure_id)
        REFERENCES agent_a2a_exposures (workspace_id, id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS agent_a2a_protocol_tasks_updated_idx
    ON agent_a2a_protocol_tasks (workspace_id, exposure_id, actor_type, actor_id, updated_at DESC);

COMMENT ON TABLE agent_a2a_protocol_tasks IS
    'PostgreSQL-backed a2asrv.TaskStore: principal-scoped Task JSON with optimistic version for multi-replica GetTask/CancelTask.';

-- ---------------------------------------------------------------------------
-- 4) Terminal delegation evidence is fully immutable (not only status sticky)
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION agent_run_delegations_terminal_evidence_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RETURN NEW;
    END IF;
    IF OLD.status NOT IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT') THEN
        RETURN NEW;
    END IF;
    -- Strict no-op allowed (identical row); any field change rejected.
    IF ROW(NEW.*) IS NOT DISTINCT FROM ROW(OLD.*) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION
        'agent_run_delegations terminal evidence is immutable (id=%, status=%)',
        OLD.id, OLD.status
        USING ERRCODE = '55000';
END;
$$;

DROP TRIGGER IF EXISTS agent_run_delegations_terminal_evidence_immutable_trg
    ON agent_run_delegations;
CREATE TRIGGER agent_run_delegations_terminal_evidence_immutable_trg
    BEFORE UPDATE ON agent_run_delegations
    FOR EACH ROW
    EXECUTE FUNCTION agent_run_delegations_terminal_evidence_immutable();

COMMENT ON FUNCTION agent_run_delegations_terminal_evidence_immutable() IS
    'After terminal status, every evidence column is frozen (output/error/remote/tokens/attempt/finished_at).';
