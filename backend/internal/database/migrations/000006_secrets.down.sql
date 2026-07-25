ALTER TABLE secrets DROP CONSTRAINT IF EXISTS secrets_active_version_fk;
DROP TABLE IF EXISTS secret_versions;
DROP TABLE IF EXISTS secrets;
