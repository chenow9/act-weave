CREATE OR REPLACE FUNCTION agent_access_grant_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    service_decision JSONB;
    subject_sharing JSONB;
    resource_value JSONB;
BEGIN
    IF jsonb_typeof(policy) <> 'object' OR EXISTS (
        SELECT 1 FROM jsonb_object_keys(policy) AS key
        WHERE key NOT IN ('serviceDecision', 'subjectSharing')
    ) THEN
        RETURN FALSE;
    END IF;

    IF policy ? 'serviceDecision' THEN
        service_decision := policy->'serviceDecision';
        IF jsonb_typeof(service_decision) <> 'object'
           OR NOT service_decision ? 'enabled'
           OR jsonb_typeof(service_decision->'enabled') <> 'boolean'
           OR EXISTS (
                SELECT 1 FROM jsonb_object_keys(service_decision) AS key
                WHERE key NOT IN ('enabled', 'maxRisk')
           ) THEN
            RETURN FALSE;
        END IF;
        IF (service_decision->>'enabled')::BOOLEAN THEN
            IF service_decision->>'maxRisk' NOT IN ('low', 'medium')
               OR jsonb_typeof(service_decision->'maxRisk') <> 'string' THEN
                RETURN FALSE;
            END IF;
        ELSIF service_decision ? 'maxRisk' THEN
            RETURN FALSE;
        END IF;
    END IF;

    IF NOT policy ? 'subjectSharing' THEN
        RETURN TRUE;
    END IF;
    subject_sharing := policy->'subjectSharing';
    IF jsonb_typeof(subject_sharing) <> 'object'
       OR NOT subject_sharing ? 'enabled'
       OR jsonb_typeof(subject_sharing->'enabled') <> 'boolean'
       OR EXISTS (
            SELECT 1 FROM jsonb_object_keys(subject_sharing) AS key
            WHERE key NOT IN ('enabled', 'resources')
       ) THEN
        RETURN FALSE;
    END IF;
    IF NOT (subject_sharing->>'enabled')::BOOLEAN THEN
        RETURN NOT subject_sharing ? 'resources';
    END IF;
    IF jsonb_typeof(subject_sharing->'resources') <> 'array'
       OR jsonb_array_length(subject_sharing->'resources') < 1
       OR jsonb_array_length(subject_sharing->'resources') > 5 THEN
        RETURN FALSE;
    END IF;
    FOR resource_value IN SELECT value FROM jsonb_array_elements(subject_sharing->'resources')
    LOOP
        IF jsonb_typeof(resource_value) <> 'string'
           OR resource_value #>> '{}' NOT IN (
                'conversation','run','event','interaction','artifact'
           ) THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN jsonb_array_length(subject_sharing->'resources') = (
        SELECT count(DISTINCT value #>> '{}')
        FROM jsonb_array_elements(subject_sharing->'resources')
    );
END;
$$;

