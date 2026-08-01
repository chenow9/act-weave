-- Expand agent_access_grant_scopes_valid for AAP file scopes (IC-03/IC-04).
-- Previous maxItems=9 and enum lacked file:write / file:read, so management
-- grants including file scopes failed DB CHECK even when Go schema accepted them.

CREATE OR REPLACE FUNCTION agent_access_grant_scopes_valid(scopes JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    scope_value JSONB;
BEGIN
    IF jsonb_typeof(scopes) <> 'array'
       OR jsonb_array_length(scopes) < 1
       OR jsonb_array_length(scopes) > 11 THEN
        RETURN FALSE;
    END IF;
    FOR scope_value IN SELECT value FROM jsonb_array_elements(scopes)
    LOOP
        IF jsonb_typeof(scope_value) <> 'string'
           OR scope_value #>> '{}' NOT IN (
                'agent:read',
                'conversation:create',
                'conversation:read',
                'run:create',
                'run:read',
                'run:cancel',
                'event:read',
                'interaction:decide',
                'artifact:read',
                'file:write',
                'file:read'
           ) THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN jsonb_array_length(scopes) = (
        SELECT count(DISTINCT value #>> '{}') FROM jsonb_array_elements(scopes)
    );
END;
$$;
