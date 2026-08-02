-- Inbound request body hash for idempotent claim identity (exposure+context+message key).
ALTER TABLE agent_a2a_inbound_tasks
    ADD COLUMN IF NOT EXISTS request_hash TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN agent_a2a_inbound_tasks.request_hash IS
    'SHA-256 hex of normalized inbound user text; claim replay must match or ErrConflict';

-- Sticky terminal: once SUCCEEDED/FAILED/CANCELLED/TIMED_OUT, status cannot change.
CREATE OR REPLACE FUNCTION agent_run_delegations_sticky_terminal()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.status IN ('SUCCEEDED','FAILED','CANCELLED','TIMED_OUT')
       AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'agent_run_delegations terminal status is sticky (was %, tried %)',
            OLD.status, NEW.status
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agent_run_delegations_sticky_terminal_trg ON agent_run_delegations;
CREATE TRIGGER agent_run_delegations_sticky_terminal_trg
    BEFORE UPDATE OF status ON agent_run_delegations
    FOR EACH ROW
    EXECUTE FUNCTION agent_run_delegations_sticky_terminal();
