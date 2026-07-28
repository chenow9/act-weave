-- M9-T2: durable Subject Token JTI replay protection for OAuth Token Exchange.
-- Mirrors Client Assertion JTI evidence: immutable rows, 32-byte keyed hashes.

CREATE TABLE agent_access_subject_token_jtis (
    client_id UUID NOT NULL,
    jti_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (client_id, jti_hash),
    CONSTRAINT agent_access_subject_token_jtis_client_fk
        FOREIGN KEY (client_id) REFERENCES agent_access_clients (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_subject_token_jtis_hash_check
        CHECK (octet_length(jti_hash) = 32),
    CONSTRAINT agent_access_subject_token_jtis_expiry_check
        CHECK (expires_at > created_at)
);

CREATE INDEX agent_access_subject_token_jtis_expiry_idx
    ON agent_access_subject_token_jtis (expires_at, client_id);

CREATE FUNCTION enforce_agent_access_subject_token_jti_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'Agent Access Subject Token JTI evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_subject_token_jtis_immutable';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER agent_access_subject_token_jtis_immutable
BEFORE UPDATE ON agent_access_subject_token_jtis
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_subject_token_jti_immutable();
