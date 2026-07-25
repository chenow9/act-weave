DROP INDEX IF EXISTS openapi_imports_workspace_provider_revision_idx;
ALTER TABLE openapi_imports
    DROP CONSTRAINT IF EXISTS openapi_imports_source_revision_not_blank,
    DROP COLUMN IF EXISTS source_revision;
