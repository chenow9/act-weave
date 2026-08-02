-- 000018: Freeze terminal AGENT_DELEGATION agent_run_steps as complete audit evidence.
-- Delegation rows were frozen in 000017; paired steps still allowed status/output/error
-- rewrites which agentaudit/UI load — close that window.

CREATE OR REPLACE FUNCTION enforce_agent_run_step_terminal_delegation_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RETURN NEW;
    END IF;
    -- Only AGENT_DELEGATION frames that already reached a terminal status.
    IF OLD.step_type IS DISTINCT FROM 'AGENT_DELEGATION' THEN
        RETURN NEW;
    END IF;
    IF OLD.status NOT IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED', 'TIMED_OUT') THEN
        RETURN NEW;
    END IF;
    -- Strict no-op allowed (identical row); any field/byte change rejected.
    IF ROW(NEW.*) IS NOT DISTINCT FROM ROW(OLD.*) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION
        'agent_run_steps terminal AGENT_DELEGATION evidence is immutable (id=%, status=%)',
        OLD.id, OLD.status
        USING ERRCODE = '55000';
END;
$$;

DROP TRIGGER IF EXISTS agent_run_steps_terminal_delegation_immutable_trg
    ON agent_run_steps;
CREATE TRIGGER agent_run_steps_terminal_delegation_immutable_trg
    BEFORE UPDATE ON agent_run_steps
    FOR EACH ROW
    EXECUTE FUNCTION enforce_agent_run_step_terminal_delegation_immutable();

COMMENT ON FUNCTION enforce_agent_run_step_terminal_delegation_immutable() IS
    'After AGENT_DELEGATION step reaches a terminal status, every evidence column is frozen (status/input/output/error/timestamps/attribution/linkage).';
