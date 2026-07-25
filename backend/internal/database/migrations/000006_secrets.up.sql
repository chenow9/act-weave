CREATE TABLE secrets (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name CITEXT NOT NULL,
    kind TEXT NOT NULL,
    active_version_id UUID,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT secrets_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT secrets_workspace_name_key UNIQUE (workspace_id, name),
    CONSTRAINT secrets_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT secrets_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT secrets_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT secrets_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT secrets_kind_not_blank CHECK (length(btrim(kind)) > 0),
    CONSTRAINT secrets_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT secrets_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX secrets_workspace_updated_idx
    ON secrets (workspace_id, updated_at DESC, id);

CREATE TABLE secret_versions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    secret_id UUID NOT NULL,
    version_no BIGINT NOT NULL,
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    key_id VARCHAR(255) NOT NULL,
    fingerprint VARCHAR(128) NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT secret_versions_workspace_secret_id_key
        UNIQUE (workspace_id, secret_id, id),
    CONSTRAINT secret_versions_secret_version_key UNIQUE (secret_id, version_no),
    CONSTRAINT secret_versions_secret_fk
        FOREIGN KEY (workspace_id, secret_id)
        REFERENCES secrets (workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT secret_versions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT secret_versions_version_no_check CHECK (version_no > 0),
    CONSTRAINT secret_versions_ciphertext_not_empty CHECK (octet_length(ciphertext) > 0),
    CONSTRAINT secret_versions_nonce_not_empty CHECK (octet_length(nonce) > 0),
    CONSTRAINT secret_versions_key_id_not_blank CHECK (length(btrim(key_id)) > 0),
    CONSTRAINT secret_versions_fingerprint_not_blank CHECK (length(btrim(fingerprint)) > 0),
    CONSTRAINT secret_versions_revoked_at_check
        CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

ALTER TABLE secrets
    ADD CONSTRAINT secrets_active_version_fk
    FOREIGN KEY (workspace_id, id, active_version_id)
    REFERENCES secret_versions (workspace_id, secret_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX secret_versions_unrevoked_idx
    ON secret_versions (workspace_id, secret_id, version_no DESC)
    WHERE revoked_at IS NULL;
