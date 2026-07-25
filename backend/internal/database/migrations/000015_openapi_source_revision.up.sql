ALTER TABLE openapi_imports
    ADD COLUMN source_revision TEXT,
    ADD CONSTRAINT openapi_imports_source_revision_not_blank
        CHECK (source_revision IS NULL OR length(btrim(source_revision)) > 0);

CREATE INDEX openapi_imports_workspace_provider_revision_idx
    ON openapi_imports (
        workspace_id, provider_id, source_revision, content_sha256, created_at DESC, id
    )
    WHERE provider_id IS NOT NULL AND source_revision IS NOT NULL;
