DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM interaction_decision_commands)
       OR EXISTS (SELECT 1 FROM execution_confirmations WHERE target_item_id IS NOT NULL)
       OR EXISTS (SELECT 1 FROM confirmation_resume_checkpoints WHERE target_item_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot remove interaction decision binding while bound interactions exist'
            USING ERRCODE = '55000';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS interaction_decision_commands_fact_guard
    ON interaction_decision_commands;
DROP FUNCTION IF EXISTS enforce_interaction_decision_command_fact();
DROP TABLE IF EXISTS interaction_decision_commands;

DROP TRIGGER IF EXISTS confirmation_resume_interaction_binding_guard
    ON confirmation_resume_checkpoints;
DROP FUNCTION IF EXISTS enforce_confirmation_resume_interaction_binding();
ALTER TABLE confirmation_resume_checkpoints
    DROP CONSTRAINT IF EXISTS confirmation_resume_interaction_binding_hash_check,
    DROP CONSTRAINT IF EXISTS confirmation_resume_interaction_binding_pair_check,
    DROP COLUMN IF EXISTS interaction_binding_hash,
    DROP COLUMN IF EXISTS target_item_id;

DROP TRIGGER IF EXISTS execution_confirmations_interaction_binding_guard
    ON execution_confirmations;
DROP FUNCTION IF EXISTS enforce_interaction_confirmation_binding();
ALTER TABLE execution_confirmations
    DROP CONSTRAINT IF EXISTS execution_confirmations_interaction_binding_hash_check,
    DROP CONSTRAINT IF EXISTS execution_confirmations_interaction_binding_pair_check,
    DROP COLUMN IF EXISTS interaction_binding_hash,
    DROP COLUMN IF EXISTS target_item_id;
