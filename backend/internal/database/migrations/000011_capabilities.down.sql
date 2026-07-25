DROP TRIGGER IF EXISTS capability_releases_immutable ON capability_releases;
DROP FUNCTION IF EXISTS enforce_capability_release_immutability();
DROP TRIGGER IF EXISTS capabilities_active_release_integrity ON capabilities;
DROP FUNCTION IF EXISTS enforce_capability_active_release();
ALTER TABLE provider_assets DROP CONSTRAINT IF EXISTS provider_assets_materialized_capability_fk;
ALTER TABLE capabilities DROP CONSTRAINT IF EXISTS capabilities_active_release_fk;
DROP TABLE IF EXISTS capability_releases;
DROP TABLE IF EXISTS capabilities;
