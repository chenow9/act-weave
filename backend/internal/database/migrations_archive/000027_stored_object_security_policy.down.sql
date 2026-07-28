ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_audit_export_retention_check,
    DROP CONSTRAINT IF EXISTS stored_objects_openapi_retention_check,
    DROP CONSTRAINT IF EXISTS stored_objects_permanent_content_policy_check,
    DROP CONSTRAINT IF EXISTS stored_objects_classification_encryption_check;
