ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_lock_version_check;

ALTER TABLE users
    DROP COLUMN IF EXISTS lock_version;
