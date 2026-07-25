DROP INDEX IF EXISTS outbox_events_claimable_idx;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_claim_lease_check,
    DROP COLUMN IF EXISTS claim_expires_at,
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS claim_token;
