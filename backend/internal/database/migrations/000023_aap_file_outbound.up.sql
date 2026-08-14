-- Expand aap_files for agent-generated outbound attachments (design §5.2).
-- Additive: purpose AGENT_OUTPUT + optional source_run_id. No rewrite.

ALTER TABLE aap_files DROP CONSTRAINT IF EXISTS aap_files_purpose_check;
ALTER TABLE aap_files ADD CONSTRAINT aap_files_purpose_check
    CHECK (purpose IN ('GENERAL', 'VISION', 'DOCUMENT', 'TOOL_INPUT', 'AGENT_OUTPUT'));

ALTER TABLE aap_files ADD COLUMN IF NOT EXISTS source_run_id UUID;

CREATE INDEX IF NOT EXISTS aap_files_source_run_idx
    ON aap_files (workspace_id, source_run_id, created_at)
    WHERE source_run_id IS NOT NULL AND purpose = 'AGENT_OUTPUT';
