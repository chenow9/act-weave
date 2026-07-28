CREATE FUNCTION agent_access_grant_scopes_valid(scopes JSONB)
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
       OR jsonb_array_length(scopes) > 9 THEN
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
                'artifact:read'
           ) THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN jsonb_array_length(scopes) = (
        SELECT count(DISTINCT value #>> '{}') FROM jsonb_array_elements(scopes)
    );
END;
$$;

CREATE FUNCTION agent_access_grant_policy_valid(policy JSONB)
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

CREATE TABLE agent_access_grants (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    client_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    scopes JSONB NOT NULL,
    policy JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    valid_from TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_access_grants_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agent_access_grants_client_scope_fk
        FOREIGN KEY (workspace_id, client_id)
        REFERENCES agent_access_clients (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_agent_scope_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_revoked_by_fk
        FOREIGN KEY (revoked_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_scopes_check
        CHECK (agent_access_grant_scopes_valid(scopes)),
    CONSTRAINT agent_access_grants_policy_check
        CHECK (agent_access_grant_policy_valid(policy)),
    CONSTRAINT agent_access_grants_status_check
        CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT agent_access_grants_validity_check
        CHECK (expires_at IS NULL OR expires_at > valid_from),
    CONSTRAINT agent_access_grants_revocation_state_check CHECK (
        (status = 'ACTIVE' AND revoked_at IS NULL AND revoked_by IS NULL)
        OR (status = 'REVOKED' AND revoked_at IS NOT NULL AND revoked_by IS NOT NULL)
    ),
    CONSTRAINT agent_access_grants_revoked_at_check
        CHECK (revoked_at IS NULL OR revoked_at >= valid_from),
    CONSTRAINT agent_access_grants_lock_version_check
        CHECK (lock_version > 0),
    CONSTRAINT agent_access_grants_timestamps_check
        CHECK (updated_at >= created_at)
);

CREATE INDEX agent_access_grants_client_agent_validity_idx
    ON agent_access_grants (
        workspace_id, client_id, agent_id, valid_from, expires_at, id
    )
    WHERE status = 'ACTIVE';

CREATE INDEX agent_access_grants_agent_status_updated_idx
    ON agent_access_grants (workspace_id, agent_id, status, updated_at DESC, id);

CREATE FUNCTION enforce_agent_access_grant_window()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(NEW.id, NEW.workspace_id, NEW.client_id, NEW.agent_id)
        IS DISTINCT FROM ROW(OLD.id, OLD.workspace_id, OLD.client_id, OLD.agent_id) THEN
        RAISE EXCEPTION 'Agent Access Grant identity is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_grants_immutable_identity';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status = 'REVOKED' AND ROW(NEW.status, NEW.revoked_at, NEW.revoked_by)
        IS DISTINCT FROM ROW(OLD.status, OLD.revoked_at, OLD.revoked_by) THEN
        RAISE EXCEPTION 'Agent Access Grant revocation is permanent'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_grants_permanent_revocation';
    END IF;
    IF NEW.status = 'ACTIVE' THEN
        -- Serialize all Grant mutations for one Client. This closes the race
        -- between the overlap query and a concurrent insert without requiring
        -- a database-wide extension or a coarse table lock.
        PERFORM 1
        FROM agent_access_clients
        WHERE workspace_id = NEW.workspace_id AND id = NEW.client_id
        FOR UPDATE;
        IF EXISTS (
            SELECT 1
            FROM agent_access_grants existing
            WHERE existing.workspace_id = NEW.workspace_id
              AND existing.client_id = NEW.client_id
              AND existing.agent_id = NEW.agent_id
              AND existing.status = 'ACTIVE'
              AND existing.id <> NEW.id
              AND tstzrange(existing.valid_from, existing.expires_at, '[)')
                  && tstzrange(NEW.valid_from, NEW.expires_at, '[)')
        ) THEN
            RAISE EXCEPTION 'overlapping active Agent Access Grant'
                USING ERRCODE = '23P01',
                      CONSTRAINT = 'agent_access_grants_active_window_excl';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_access_grants_window_guard
BEFORE INSERT OR UPDATE ON agent_access_grants
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_grant_window();

