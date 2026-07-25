ALTER TABLE execution_confirmations
    ADD COLUMN target_item_id UUID,
    ADD COLUMN interaction_binding_hash CHAR(64),
    ADD CONSTRAINT execution_confirmations_interaction_binding_pair_check CHECK (
        (target_item_id IS NULL) = (interaction_binding_hash IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_interaction_binding_hash_check CHECK (
        interaction_binding_hash IS NULL OR interaction_binding_hash ~ '^[0-9a-f]{64}$'
    );

ALTER TABLE confirmation_resume_checkpoints
    ADD COLUMN target_item_id UUID,
    ADD COLUMN interaction_binding_hash CHAR(64),
    ADD CONSTRAINT confirmation_resume_interaction_binding_pair_check CHECK (
        (target_item_id IS NULL) = (interaction_binding_hash IS NULL)
    ),
    ADD CONSTRAINT confirmation_resume_interaction_binding_hash_check CHECK (
        interaction_binding_hash IS NULL OR interaction_binding_hash ~ '^[0-9a-f]{64}$'
    );

CREATE FUNCTION enforce_interaction_confirmation_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(NEW.target_item_id, NEW.interaction_binding_hash)
        IS DISTINCT FROM ROW(OLD.target_item_id, OLD.interaction_binding_hash) THEN
        RAISE EXCEPTION 'interaction confirmation binding is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_interaction_binding_guard
BEFORE UPDATE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_interaction_confirmation_binding();

CREATE FUNCTION enforce_confirmation_resume_interaction_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    confirmation_target UUID;
    confirmation_hash CHAR(64);
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(NEW.target_item_id, NEW.interaction_binding_hash)
        IS DISTINCT FROM ROW(OLD.target_item_id, OLD.interaction_binding_hash) THEN
        RAISE EXCEPTION 'confirmation resume interaction binding is immutable'
            USING ERRCODE = '55000';
    END IF;
    SELECT target_item_id, interaction_binding_hash
      INTO confirmation_target, confirmation_hash
      FROM execution_confirmations
     WHERE workspace_id = NEW.workspace_id AND id = NEW.confirmation_id;
    IF ROW(NEW.target_item_id, NEW.interaction_binding_hash)
        IS DISTINCT FROM ROW(confirmation_target, confirmation_hash) THEN
        RAISE EXCEPTION 'confirmation resume interaction binding mismatch'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER confirmation_resume_interaction_binding_guard
BEFORE INSERT OR UPDATE ON confirmation_resume_checkpoints
FOR EACH ROW EXECUTE FUNCTION enforce_confirmation_resume_interaction_binding();

CREATE TABLE interaction_decision_commands (
    workspace_id UUID NOT NULL,
    confirmation_id UUID NOT NULL,
    principal_binding_hash CHAR(64) NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash CHAR(64) NOT NULL,
    decision TEXT NOT NULL,
    expected_version BIGINT NOT NULL,
    confirmation_status TEXT NOT NULL,
    confirmation_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, confirmation_id, principal_binding_hash, idempotency_key),
    CONSTRAINT interaction_decision_commands_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT interaction_decision_commands_principal_hash_check
        CHECK (principal_binding_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT interaction_decision_commands_request_hash_check
        CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT interaction_decision_commands_decision_check
        CHECK (decision IN ('approve', 'decline', 'cancel')),
    CONSTRAINT interaction_decision_commands_expected_version_check
        CHECK (expected_version > 0),
    CONSTRAINT interaction_decision_commands_result_check CHECK (
        (decision = 'approve' AND confirmation_status = 'CONFIRMED')
        OR (decision IN ('decline', 'cancel') AND confirmation_status = 'CANCELLED')
    ),
    CONSTRAINT interaction_decision_commands_confirmation_version_check
        CHECK (confirmation_version = expected_version + 1)
);

CREATE INDEX interaction_decision_commands_confirmation_created_idx
    ON interaction_decision_commands (workspace_id, confirmation_id, created_at, idempotency_key);

CREATE FUNCTION enforce_interaction_decision_command_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'interaction decision commands are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_decision_commands_fact_guard
BEFORE UPDATE OR DELETE ON interaction_decision_commands
FOR EACH ROW EXECUTE FUNCTION enforce_interaction_decision_command_fact();
