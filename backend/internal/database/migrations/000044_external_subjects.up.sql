CREATE TABLE external_subjects (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    client_id UUID NOT NULL,
    issuer TEXT NOT NULL,
    subject_hash BYTEA NOT NULL,
    display_ref VARCHAR(120),
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT external_subjects_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT external_subjects_identity_key
        UNIQUE (workspace_id, client_id, issuer, subject_hash),
    CONSTRAINT external_subjects_client_scope_fk
        FOREIGN KEY (workspace_id, client_id)
        REFERENCES agent_access_clients (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT external_subjects_issuer_check CHECK (
        length(issuer) <= 2048
        AND btrim(issuer) = issuer
        AND issuer ~ '^https://[^[:space:]?#]+/?$'
    ),
    CONSTRAINT external_subjects_subject_hash_check
        CHECK (octet_length(subject_hash) = 32),
    CONSTRAINT external_subjects_display_ref_check CHECK (
        display_ref IS NULL OR display_ref ~ '^ref_[A-Za-z0-9_-]{1,116}$'
    ),
    CONSTRAINT external_subjects_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT external_subjects_seen_order_check
        CHECK (last_seen_at >= first_seen_at),
    CONSTRAINT external_subjects_disabled_state_check CHECK (
        (status = 'ACTIVE' AND disabled_at IS NULL)
        OR (status = 'DISABLED' AND disabled_at IS NOT NULL)
    ),
    CONSTRAINT external_subjects_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= first_seen_at),
    CONSTRAINT external_subjects_timestamps_check CHECK (
        created_at >= first_seen_at AND updated_at >= created_at
    ),
    CONSTRAINT external_subjects_lock_version_check
        CHECK (lock_version > 0)
);

CREATE INDEX external_subjects_client_status_seen_idx
    ON external_subjects (
        workspace_id, client_id, status, last_seen_at DESC, id
    );

CREATE FUNCTION enforce_external_subject_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.client_id, NEW.issuer,
        NEW.subject_hash, NEW.first_seen_at, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.client_id, OLD.issuer,
        OLD.subject_hash, OLD.first_seen_at, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'External Subject identity evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'external_subjects_immutable_identity';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER external_subjects_identity_guard
BEFORE UPDATE ON external_subjects
FOR EACH ROW EXECUTE FUNCTION enforce_external_subject_identity();
