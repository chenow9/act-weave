CREATE TABLE agent_access_credentials (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    client_id UUID NOT NULL,
    credential_type TEXT NOT NULL,
    secret_hash BYTEA,
    jwk_thumbprint BYTEA,
    certificate_thumbprint BYTEA,
    public_hint VARCHAR(120) NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_access_credentials_workspace_client_id_key
        UNIQUE (workspace_id, client_id, id),
    CONSTRAINT agent_access_credentials_client_scope_fk
        FOREIGN KEY (workspace_id, client_id)
        REFERENCES agent_access_clients (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_credentials_revoked_by_fk
        FOREIGN KEY (revoked_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_credentials_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_credentials_type_check CHECK (
        credential_type IN ('client_secret', 'jwk', 'mtls_certificate')
    ),
    CONSTRAINT agent_access_credentials_material_check CHECK (
        (
            credential_type = 'client_secret'
            AND secret_hash IS NOT NULL
            AND octet_length(secret_hash) = 32
            AND jwk_thumbprint IS NULL
            AND certificate_thumbprint IS NULL
        ) OR (
            credential_type = 'jwk'
            AND secret_hash IS NULL
            AND jwk_thumbprint IS NOT NULL
            AND octet_length(jwk_thumbprint) = 32
            AND certificate_thumbprint IS NULL
        ) OR (
            credential_type = 'mtls_certificate'
            AND secret_hash IS NULL
            AND jwk_thumbprint IS NULL
            AND certificate_thumbprint IS NOT NULL
            AND octet_length(certificate_thumbprint) = 32
        )
    ),
    CONSTRAINT agent_access_credentials_public_hint_check
        CHECK (length(btrim(public_hint)) > 0),
    CONSTRAINT agent_access_credentials_validity_check
        CHECK (expires_at IS NULL OR expires_at > valid_from),
    CONSTRAINT agent_access_credentials_last_used_check
        CHECK (last_used_at IS NULL OR last_used_at >= valid_from),
    CONSTRAINT agent_access_credentials_revocation_pair_check
        CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)),
    CONSTRAINT agent_access_credentials_revoked_at_check
        CHECK (revoked_at IS NULL OR revoked_at >= valid_from),
    CONSTRAINT agent_access_credentials_lock_version_check
        CHECK (lock_version > 0)
);

CREATE INDEX agent_access_credentials_active_lookup_idx
    ON agent_access_credentials (
        workspace_id, client_id, credential_type, valid_from DESC, id
    )
    WHERE revoked_at IS NULL;

CREATE INDEX agent_access_credentials_expiry_idx
    ON agent_access_credentials (expires_at, id)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;

CREATE UNIQUE INDEX agent_access_credentials_secret_hash_key
    ON agent_access_credentials (secret_hash)
    WHERE secret_hash IS NOT NULL;

CREATE FUNCTION enforce_agent_access_credential_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Agent Access credentials must be revoked and cannot be deleted'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_permanent_evidence';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.client_id, NEW.credential_type,
        NEW.secret_hash, NEW.jwk_thumbprint, NEW.certificate_thumbprint,
        NEW.public_hint, NEW.valid_from,
        NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.client_id, OLD.credential_type,
        OLD.secret_hash, OLD.jwk_thumbprint, OLD.certificate_thumbprint,
        OLD.public_hint, OLD.valid_from,
        OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Agent Access credential authentication evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_immutable_auth';
    END IF;
    IF NEW.expires_at IS DISTINCT FROM OLD.expires_at AND (
        NEW.expires_at IS NULL
        OR NEW.expires_at <= clock_timestamp()
        OR (OLD.expires_at IS NOT NULL AND NEW.expires_at >= OLD.expires_at)
    ) THEN
        RAISE EXCEPTION 'Agent Access credential expiry may only be shortened to a future instant'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_expiry_extension_forbidden';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND ROW(NEW.revoked_at, NEW.revoked_by)
        IS DISTINCT FROM ROW(OLD.revoked_at, OLD.revoked_by) THEN
        RAISE EXCEPTION 'Agent Access credential revocation is permanent'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_permanent_revocation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_access_credentials_permanent_evidence
BEFORE UPDATE OR DELETE ON agent_access_credentials
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_credential_evidence();
