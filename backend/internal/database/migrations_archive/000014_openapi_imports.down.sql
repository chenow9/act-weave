ALTER TABLE tools
    DROP CONSTRAINT IF EXISTS tools_source_endpoint_fk;
DROP TABLE IF EXISTS openapi_endpoints;
DROP TABLE IF EXISTS openapi_imports;
