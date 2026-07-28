DROP TRIGGER IF EXISTS confirmation_resume_checkpoints_fact_guard
    ON confirmation_resume_checkpoints;
DROP FUNCTION IF EXISTS enforce_confirmation_resume_checkpoint();
DROP TABLE IF EXISTS confirmation_resume_checkpoints;

