ALTER TABLE outbox_events
    ADD COLUMN claim_token UUID,
    ADD COLUMN claimed_at TIMESTAMPTZ,
    ADD COLUMN claim_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT outbox_events_claim_lease_check CHECK (
        (claim_token IS NULL AND claimed_at IS NULL AND claim_expires_at IS NULL)
        OR (
            claim_token IS NOT NULL
            AND claimed_at IS NOT NULL
            AND claim_expires_at IS NOT NULL
            AND claim_expires_at > claimed_at
            AND published_at IS NULL
        )
    );

CREATE INDEX outbox_events_claimable_idx
    ON outbox_events (available_at, claim_expires_at, occurred_at, id)
    WHERE published_at IS NULL;
