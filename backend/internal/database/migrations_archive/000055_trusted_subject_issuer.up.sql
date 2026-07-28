-- M9-T1: Trusted Subject Issuer configuration for Agent Access Clients.
-- Inline JWKS and JWKS URI are mutually exclusive. OIDC Discovery is not used;
-- only an explicit fixed HTTPS JWKS URI or an inline JWKS document is allowed.

CREATE FUNCTION agent_access_subject_algorithms_valid(algorithms JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN algorithms IS NULL THEN TRUE
        WHEN jsonb_typeof(algorithms) <> 'array' THEN FALSE
        WHEN jsonb_array_length(algorithms) < 1 OR jsonb_array_length(algorithms) > 8 THEN FALSE
        ELSE
            NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(algorithms) AS element(value)
                WHERE jsonb_typeof(element.value) <> 'string'
                   OR element.value #>> '{}' NOT IN ('EdDSA', 'PS256')
            )
            AND jsonb_array_length(algorithms) = (
                SELECT count(DISTINCT element.value #>> '{}')
                FROM jsonb_array_elements(algorithms) AS element(value)
            )
    END
$$;

CREATE FUNCTION agent_access_subject_claim_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN policy IS NULL THEN TRUE
        WHEN jsonb_typeof(policy) <> 'object' THEN FALSE
        WHEN policy ?| ARRAY['subject', 'email', 'phone', 'rawToken', 'subjectToken'] THEN FALSE
        ELSE
            (SELECT count(*) FROM jsonb_object_keys(policy)) = 4
            AND policy ? 'subjectClaim'
            AND policy ? 'requireJti'
            AND policy ? 'maxSubjectBytes'
            AND policy ? 'maxTokenTTLSeconds'
            AND jsonb_typeof(policy->'subjectClaim') = 'string'
            AND policy->>'subjectClaim' = 'sub'
            AND jsonb_typeof(policy->'requireJti') = 'boolean'
            AND jsonb_typeof(policy->'maxSubjectBytes') = 'number'
            AND (policy->>'maxSubjectBytes') ~ '^[0-9]+$'
            AND (policy->>'maxSubjectBytes')::INTEGER BETWEEN 1 AND 256
            AND jsonb_typeof(policy->'maxTokenTTLSeconds') = 'number'
            AND (policy->>'maxTokenTTLSeconds') ~ '^[0-9]+$'
            AND (policy->>'maxTokenTTLSeconds')::INTEGER BETWEEN 60 AND 86400
    END
$$;

CREATE FUNCTION agent_access_subject_inline_jwks_valid(jwks JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN jwks IS NULL THEN TRUE
        WHEN jsonb_typeof(jwks) <> 'object' THEN FALSE
        WHEN NOT (jwks ? 'keys') THEN FALSE
        WHEN jsonb_typeof(jwks->'keys') <> 'array' THEN FALSE
        WHEN jsonb_array_length(jwks->'keys') < 1 OR jsonb_array_length(jwks->'keys') > 32 THEN FALSE
        WHEN length(jwks::text) > 262144 THEN FALSE
        ELSE TRUE
    END
$$;

ALTER TABLE agent_access_clients
    ADD COLUMN trusted_subject_audience TEXT,
    ADD COLUMN trusted_subject_inline_jwks JSONB,
    ADD COLUMN trusted_subject_algorithms JSONB,
    ADD COLUMN trusted_subject_claim_policy JSONB;

-- Incomplete pre-M9 trust pairs cannot satisfy the expanded contract.
-- No production Token Exchange depends on these rows yet.
UPDATE agent_access_clients
SET
    trusted_subject_issuer = NULL,
    trusted_subject_jwks_uri = NULL
WHERE trusted_subject_issuer IS NOT NULL
   OR trusted_subject_jwks_uri IS NOT NULL;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_subject_trust_pair_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_subject_issuer_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_subject_jwks_uri_check;

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_trust_presence_check CHECK (
        (
            trusted_subject_issuer IS NULL
            AND trusted_subject_audience IS NULL
            AND trusted_subject_jwks_uri IS NULL
            AND trusted_subject_inline_jwks IS NULL
            AND trusted_subject_algorithms IS NULL
            AND trusted_subject_claim_policy IS NULL
        )
        OR (
            trusted_subject_issuer IS NOT NULL
            AND trusted_subject_audience IS NOT NULL
            AND trusted_subject_algorithms IS NOT NULL
            AND trusted_subject_claim_policy IS NOT NULL
            AND (
                (trusted_subject_jwks_uri IS NOT NULL AND trusted_subject_inline_jwks IS NULL)
                OR (trusted_subject_jwks_uri IS NULL AND trusted_subject_inline_jwks IS NOT NULL)
            )
        )
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
    ADD CONSTRAINT agent_access_clients_subject_audience_check CHECK (
        trusted_subject_audience IS NULL OR (
            length(trusted_subject_audience) BETWEEN 1 AND 2048
            AND btrim(trusted_subject_audience) = trusted_subject_audience
            AND position(E'\n' IN trusted_subject_audience) = 0
            AND position(E'\r' IN trusted_subject_audience) = 0
            AND position(E'\t' IN trusted_subject_audience) = 0
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

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_inline_jwks_check
        CHECK (agent_access_subject_inline_jwks_valid(trusted_subject_inline_jwks));

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_algorithms_check
        CHECK (agent_access_subject_algorithms_valid(trusted_subject_algorithms));

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_claim_policy_check
        CHECK (agent_access_subject_claim_policy_valid(trusted_subject_claim_policy));
