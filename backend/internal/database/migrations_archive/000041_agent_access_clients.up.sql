CREATE FUNCTION agent_access_cors_origins_valid(origins JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
    SELECT CASE
        WHEN jsonb_typeof(origins) <> 'array' THEN FALSE
        WHEN jsonb_array_length(origins) > 32 THEN FALSE
        ELSE
            NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(origins) AS element(value)
                WHERE jsonb_typeof(element.value) <> 'string'
                   OR length(element.value #>> '{}') = 0
                   OR length(element.value #>> '{}') > 2048
                   OR btrim(element.value #>> '{}') <> element.value #>> '{}'
                   OR element.value #>> '{}' !~ '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$'
            )
            AND jsonb_array_length(origins) = (
                SELECT count(DISTINCT element.value #>> '{}')
                FROM jsonb_array_elements(origins) AS element(value)
            )
    END
$$;

CREATE TABLE service_principals (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    security_version BIGINT NOT NULL DEFAULT 1,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    disabled_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT service_principals_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT service_principals_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT service_principals_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT service_principals_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT service_principals_name_not_blank
        CHECK (length(btrim(name)) > 0),
    CONSTRAINT service_principals_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT service_principals_security_version_check
        CHECK (security_version > 0),
    CONSTRAINT service_principals_lock_version_check
        CHECK (lock_version > 0),
    CONSTRAINT service_principals_timestamps_check
        CHECK (updated_at >= created_at),
    CONSTRAINT service_principals_disabled_state_check CHECK (
        (status = 'ACTIVE' AND disabled_at IS NULL)
        OR (status = 'DISABLED' AND disabled_at IS NOT NULL)
    ),
    CONSTRAINT service_principals_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= created_at)
);

CREATE INDEX service_principals_workspace_status_updated_idx
    ON service_principals (workspace_id, status, updated_at DESC, id);

CREATE TABLE agent_access_clients (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    service_principal_id UUID NOT NULL,
    client_id TEXT NOT NULL,
    name VARCHAR(120) NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    auth_method TEXT NOT NULL,
    jwks_uri TEXT,
    trusted_subject_issuer TEXT,
    trusted_subject_jwks_uri TEXT,
    allowed_cors_origins JSONB NOT NULL DEFAULT '[]'::JSONB,
    token_ttl_seconds INTEGER NOT NULL DEFAULT 600,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    disabled_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_access_clients_client_id_key UNIQUE (client_id),
    CONSTRAINT agent_access_clients_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agent_access_clients_workspace_principal_key
        UNIQUE (workspace_id, service_principal_id),
    CONSTRAINT agent_access_clients_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_principal_scope_fk
        FOREIGN KEY (workspace_id, service_principal_id)
        REFERENCES service_principals (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_client_id_format_check CHECK (
        client_id ~ '^awcl_[A-Za-z0-9_-]{32,128}$'
    ),
    CONSTRAINT agent_access_clients_name_not_blank
        CHECK (length(btrim(name)) > 0),
    CONSTRAINT agent_access_clients_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT agent_access_clients_auth_method_check
        CHECK (auth_method IN ('client_secret_basic', 'private_key_jwt')),
    CONSTRAINT agent_access_clients_auth_config_check CHECK (
        (auth_method = 'client_secret_basic' AND jwks_uri IS NULL)
        OR (auth_method = 'private_key_jwt' AND jwks_uri IS NOT NULL)
    ),
    CONSTRAINT agent_access_clients_jwks_uri_check CHECK (
        jwks_uri IS NULL OR (
            length(jwks_uri) <= 2048
            AND btrim(jwks_uri) = jwks_uri
            AND jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    ),
    CONSTRAINT agent_access_clients_subject_trust_pair_check CHECK (
        (trusted_subject_issuer IS NULL) = (trusted_subject_jwks_uri IS NULL)
    ),
    CONSTRAINT agent_access_clients_subject_issuer_check CHECK (
        trusted_subject_issuer IS NULL OR (
            length(trusted_subject_issuer) <= 2048
            AND btrim(trusted_subject_issuer) = trusted_subject_issuer
            AND trusted_subject_issuer ~ '^https://[^[:space:]?#]+/?$'
        )
    ),
    CONSTRAINT agent_access_clients_subject_jwks_uri_check CHECK (
        trusted_subject_jwks_uri IS NULL OR (
            length(trusted_subject_jwks_uri) <= 2048
            AND btrim(trusted_subject_jwks_uri) = trusted_subject_jwks_uri
            AND trusted_subject_jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    ),
    CONSTRAINT agent_access_clients_cors_origins_check
        CHECK (agent_access_cors_origins_valid(allowed_cors_origins)),
    CONSTRAINT agent_access_clients_token_ttl_check
        CHECK (token_ttl_seconds BETWEEN 60 AND 900),
    CONSTRAINT agent_access_clients_lock_version_check
        CHECK (lock_version > 0),
    CONSTRAINT agent_access_clients_timestamps_check
        CHECK (updated_at >= created_at),
    CONSTRAINT agent_access_clients_disabled_state_check CHECK (
        (status = 'ACTIVE' AND disabled_at IS NULL)
        OR (status = 'DISABLED' AND disabled_at IS NOT NULL)
    ),
    CONSTRAINT agent_access_clients_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= created_at)
);

CREATE INDEX agent_access_clients_workspace_status_updated_idx
    ON agent_access_clients (workspace_id, status, updated_at DESC, id);

