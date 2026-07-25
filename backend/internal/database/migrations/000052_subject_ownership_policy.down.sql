DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agent_access_grants WHERE policy ? 'subjectSharing'
    ) THEN
        RAISE EXCEPTION 'cannot remove Subject Sharing policy while Grants use it'
            USING ERRCODE='23514',CONSTRAINT='agent_access_subject_sharing_rollback_blocked';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION agent_access_grant_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    service_decision JSONB;
BEGIN
    IF jsonb_typeof(policy) <> 'object' OR EXISTS (
        SELECT 1 FROM jsonb_object_keys(policy) AS key
        WHERE key <> 'serviceDecision'
    ) THEN
        RETURN FALSE;
    END IF;
    IF NOT policy ? 'serviceDecision' THEN
        RETURN TRUE;
    END IF;
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
        RETURN service_decision->>'maxRisk' IN ('low', 'medium')
               AND jsonb_typeof(service_decision->'maxRisk') = 'string';
    END IF;
    RETURN NOT service_decision ? 'maxRisk';
END;
$$;

