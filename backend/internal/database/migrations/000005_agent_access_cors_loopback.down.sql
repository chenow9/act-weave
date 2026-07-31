-- Restore pre-loopback HTTPS-only CORS origin validation.

CREATE OR REPLACE FUNCTION agent_access_cors_origins_valid(origins JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
    SELECT CASE
        WHEN jsonb_typeof(origins) <> 'array' THEN FALSE
        WHEN jsonb_array_length(origins) > 32 THEN FALSE
        ELSE
            NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(origins) AS element(value)
                WHERE jsonb_typeof(element.value) <> 'string'
                   OR length(element.value #>> '{}') = 0
                   OR length(element.value #>> '{}') > 2048
                   OR btrim(element.value #>> '{}') <> element.value #>> '{}'
                   OR element.value #>> '{}' !~ '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$'
            )
            AND jsonb_array_length(origins) = (
                SELECT count(DISTINCT element.value #>> '{}')
                FROM jsonb_array_elements(origins) AS element(value)
            )
    END
$$;
