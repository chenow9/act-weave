CREATE TABLE users (
    id UUID PRIMARY KEY,
    username CITEXT NOT NULL,
    email CITEXT,
    display_name VARCHAR(120) NOT NULL,
    avatar_url TEXT,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    platform_role TEXT NOT NULL DEFAULT 'USER',
    locale VARCHAR(16) NOT NULL DEFAULT 'zh-CN',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Singapore',
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_username_key UNIQUE (username),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_username_not_blank CHECK (length(btrim(username::TEXT)) > 0),
    CONSTRAINT users_display_name_not_blank CHECK (length(btrim(display_name)) > 0),
    CONSTRAINT users_status_check CHECK (status IN ('ACTIVE', 'LOCKED', 'DISABLED')),
    CONSTRAINT users_platform_role_check CHECK (platform_role IN ('USER', 'PLATFORM_ADMIN')),
    CONSTRAINT users_timestamps_check CHECK (updated_at >= created_at)
);

CREATE INDEX users_status_updated_idx
    ON users (status, updated_at DESC, id);

CREATE TABLE user_credentials (
    user_id UUID PRIMARY KEY,
    password_hash TEXT NOT NULL,
    password_algo TEXT NOT NULL,
    password_changed_at TIMESTAMPTZ NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    CONSTRAINT user_credentials_user_fk
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT user_credentials_password_hash_not_blank
        CHECK (length(password_hash) > 0),
    CONSTRAINT user_credentials_password_algo_not_blank
        CHECK (length(btrim(password_algo)) > 0),
    CONSTRAINT user_credentials_failed_attempts_check
        CHECK (failed_attempts >= 0)
);

CREATE INDEX user_credentials_locked_until_idx
    ON user_credentials (locked_until)
    WHERE locked_until IS NOT NULL;

CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    refresh_token_hash TEXT NOT NULL,
    user_agent TEXT,
    ip INET,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT auth_sessions_user_fk
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT auth_sessions_refresh_token_hash_key UNIQUE (refresh_token_hash),
    CONSTRAINT auth_sessions_refresh_token_hash_not_blank
        CHECK (length(refresh_token_hash) > 0),
    CONSTRAINT auth_sessions_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT auth_sessions_revocation_check
        CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CONSTRAINT auth_sessions_last_seen_check CHECK (last_seen_at >= created_at)
);

CREATE INDEX auth_sessions_user_active_idx
    ON auth_sessions (user_id, expires_at DESC, id)
    WHERE revoked_at IS NULL;

CREATE INDEX auth_sessions_expires_at_idx
    ON auth_sessions (expires_at);
