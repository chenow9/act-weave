ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_default_model_config_fk;
UPDATE workspaces SET default_model_config_id = NULL WHERE default_model_config_id IS NOT NULL;
DROP TABLE IF EXISTS model_configs;
