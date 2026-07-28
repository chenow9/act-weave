-- 000060 down: reversible SCHEMA only.
-- Intentionally does NOT restore:
--   - deleted secrets / secret_versions / ciphertext / nonces / key references
--   - cleared credential_secret_id values on target connections
--   - pre-cutover connection status or migration_state
-- Production must never rely on this down path after a successful up commit.
-- Disaster recovery that restores a pre-cutover infrastructure snapshot must
-- re-run 000060 and pass delete proofs before reopening traffic.

DROP TABLE IF EXISTS outbound_runtime_affinities;
DROP TABLE IF EXISTS outbound_runtime_instances;

DROP INDEX IF EXISTS service_connections_machine_credential_secret_idx;
DROP INDEX IF EXISTS service_connections_workspace_migration_state_idx;

ALTER TABLE service_connections
    DROP CONSTRAINT IF EXISTS service_connections_machine_credential_secret_fk,
    DROP CONSTRAINT IF EXISTS service_connections_migration_state_check,
    DROP CONSTRAINT IF EXISTS service_connections_outbound_identity_policy_version_check,
    DROP CONSTRAINT IF EXISTS service_connections_outbound_identity_object_check;

ALTER TABLE service_connections
    DROP COLUMN IF EXISTS machine_credential_secret_id,
    DROP COLUMN IF EXISTS migration_state,
    DROP COLUMN IF EXISTS outbound_identity_policy_version,
    DROP COLUMN IF EXISTS outbound_identity;

ALTER TABLE capability_providers
    DROP CONSTRAINT IF EXISTS capability_providers_outbound_identity_policy_version_check;

ALTER TABLE capability_providers
    DROP COLUMN IF EXISTS outbound_identity_policy_version;
