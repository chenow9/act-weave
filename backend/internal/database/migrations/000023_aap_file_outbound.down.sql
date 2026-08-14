-- Reverse 000023 only when no AGENT_OUTPUT rows exist.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM aap_files WHERE purpose = 'AGENT_OUTPUT') THEN
        RAISE EXCEPTION '000023 refuse: aap_files AGENT_OUTPUT rows exist';
    END IF;
END $$;

DROP INDEX IF EXISTS aap_files_source_run_idx;

ALTER TABLE aap_files DROP COLUMN IF EXISTS source_run_id;

ALTER TABLE aap_files DROP CONSTRAINT IF EXISTS aap_files_purpose_check;
ALTER TABLE aap_files ADD CONSTRAINT aap_files_purpose_check
    CHECK (purpose IN ('GENERAL', 'VISION', 'DOCUMENT', 'TOOL_INPUT'));
