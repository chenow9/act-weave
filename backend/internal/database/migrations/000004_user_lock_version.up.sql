ALTER TABLE users
    ADD COLUMN lock_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE users
    ADD CONSTRAINT users_lock_version_check CHECK (lock_version > 0);
