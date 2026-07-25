ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_claim_policy_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_algorithms_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_inline_jwks_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_jwks_uri_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_audience_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_issuer_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_trust_presence_check;

ALTER TABLE agent_access_clients
    DROP COLUMN IF EXISTS trusted_subject_claim_policy,
    DROP COLUMN IF EXISTS trusted_subject_algorithms,
    DROP COLUMN IF EXISTS trusted_subject_inline_jwks,
    DROP COLUMN IF EXISTS trusted_subject_audience;

-- Restore migration 41 pair constraints. Any expanded M9 trust config is cleared
-- on downgrade because audience/algorithms/claim policy cannot be represented.
UPDATE agent_access_clients
SET
    trusted_subject_issuer = NULL,
    trusted_subject_jwks_uri = NULL
WHERE trusted_subject_issuer IS NOT NULL
   OR trusted_subject_jwks_uri IS NOT NULL;

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_trust_pair_check CHECK (
        (trusted_subject_issuer IS NULL) = (trusted_subject_jwks_uri IS NULL)
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_issuer_check CHECK (
        trusted_subject_issuer IS NULL OR (
            length(trusted_subject_issuer) <= 2048
            AND btrim(trusted_subject_issuer) = trusted_subject_issuer
            AND trusted_subject_issuer ~ '^https://[^[:space:]?#]+/?$'
        )
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_jwks_uri_check CHECK (
        trusted_subject_jwks_uri IS NULL OR (
            length(trusted_subject_jwks_uri) <= 2048
            AND btrim(trusted_subject_jwks_uri) = trusted_subject_jwks_uri
            AND trusted_subject_jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    );

DROP FUNCTION IF EXISTS agent_access_subject_inline_jwks_valid(JSONB);
DROP FUNCTION IF EXISTS agent_access_subject_claim_policy_valid(JSONB);
DROP FUNCTION IF EXISTS agent_access_subject_algorithms_valid(JSONB);
