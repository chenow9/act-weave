ALTER TABLE agent_runs
    ADD COLUMN principal_snapshot_version TEXT,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN grant_id UUID,
    ADD COLUMN grant_version BIGINT,
    ADD COLUMN agent_policy_version BIGINT;

ALTER TABLE workflow_executions
    ADD COLUMN principal_snapshot_version TEXT,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN grant_id UUID,
    ADD COLUMN grant_version BIGINT,
    ADD COLUMN agent_policy_version BIGINT;

ALTER TABLE tool_invocations
    ADD COLUMN principal_snapshot_version TEXT,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN grant_id UUID,
    ADD COLUMN grant_version BIGINT,
    ADD COLUMN agent_policy_version BIGINT,
    ADD COLUMN authorization_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB;

-- Historical typed actors are preserved exactly. Only User has an unambiguous
-- historical Subject; old machine calls remain explicitly legacy rather than
-- inventing Client/Grant authorization facts.
ALTER TABLE agent_runs DISABLE TRIGGER agent_runs_permanent_snapshot;
ALTER TABLE workflow_executions DISABLE TRIGGER workflow_executions_state_guard;
ALTER TABLE tool_invocations DISABLE TRIGGER tool_invocations_state_guard;

UPDATE agent_runs SET
    principal_snapshot_version='legacy.v1',
    subject_type=CASE WHEN triggered_by_type='USER' THEN 'USER' END,
    subject_id=CASE WHEN triggered_by_type='USER' THEN triggered_by_id END;
UPDATE workflow_executions SET
    principal_snapshot_version='legacy.v1',
    subject_type=CASE WHEN triggered_by_type='USER' THEN 'USER' END,
    subject_id=CASE WHEN triggered_by_type='USER' THEN triggered_by_id END;
UPDATE tool_invocations SET
    principal_snapshot_version='legacy.v1',
    subject_type=CASE WHEN actor_type='USER' THEN 'USER' END,
    subject_id=CASE WHEN actor_type='USER' THEN actor_id END;

ALTER TABLE agent_runs ENABLE TRIGGER agent_runs_permanent_snapshot;
ALTER TABLE workflow_executions ENABLE TRIGGER workflow_executions_state_guard;
ALTER TABLE tool_invocations ENABLE TRIGGER tool_invocations_state_guard;

ALTER TABLE agent_runs ALTER COLUMN principal_snapshot_version SET NOT NULL;
ALTER TABLE workflow_executions ALTER COLUMN principal_snapshot_version SET NOT NULL;
ALTER TABLE tool_invocations ALTER COLUMN principal_snapshot_version SET NOT NULL;

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_principal_snapshot_version_check CHECK (
        principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT agent_runs_subject_pair_check
        CHECK ((subject_type IS NULL)=(subject_id IS NULL)),
    ADD CONSTRAINT agent_runs_subject_ref_fk
        FOREIGN KEY(workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_client_scope_fk
        FOREIGN KEY(workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_grant_scope_fk
        FOREIGN KEY(workspace_id,grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_external_version_pair_check CHECK (
        (client_id IS NULL AND grant_id IS NULL AND grant_version IS NULL
         AND agent_policy_version IS NULL)
        OR
        (client_id IS NOT NULL AND grant_id IS NOT NULL AND grant_version > 0
         AND agent_policy_version > 0)
    );

ALTER TABLE workflow_executions
    ADD CONSTRAINT workflow_executions_principal_snapshot_version_check CHECK (
        principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT workflow_executions_subject_pair_check
        CHECK ((subject_type IS NULL)=(subject_id IS NULL)),
    ADD CONSTRAINT workflow_executions_subject_ref_fk
        FOREIGN KEY(workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_client_scope_fk
        FOREIGN KEY(workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_grant_scope_fk
        FOREIGN KEY(workspace_id,grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_external_version_pair_check CHECK (
        (client_id IS NULL AND grant_id IS NULL AND grant_version IS NULL
         AND agent_policy_version IS NULL)
        OR
        (client_id IS NOT NULL AND grant_id IS NOT NULL AND grant_version > 0
         AND agent_policy_version > 0)
    );

ALTER TABLE tool_invocations
    ADD CONSTRAINT tool_invocations_principal_snapshot_version_check CHECK (
        principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT tool_invocations_subject_pair_check
        CHECK ((subject_type IS NULL)=(subject_id IS NULL)),
    ADD CONSTRAINT tool_invocations_subject_ref_fk
        FOREIGN KEY(workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_client_scope_fk
        FOREIGN KEY(workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_grant_scope_fk
        FOREIGN KEY(workspace_id,grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_external_version_pair_check CHECK (
        (client_id IS NULL AND grant_id IS NULL AND grant_version IS NULL
         AND agent_policy_version IS NULL)
        OR
        (client_id IS NOT NULL AND grant_id IS NOT NULL AND grant_version > 0
         AND agent_policy_version > 0)
    ),
    ADD CONSTRAINT tool_invocations_authorization_snapshot_object_check
        CHECK(jsonb_typeof(authorization_snapshot)='object');

CREATE FUNCTION execution_authorization_envelope_matches(
    snapshot JSONB,workspace_id UUID,actor_type TEXT,actor_id UUID,
    subject_type TEXT,subject_id UUID,client_id UUID,grant_id UUID,
    grant_version BIGINT,agent_policy_version BIGINT
)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT jsonb_typeof(snapshot)='object'
       AND snapshot->>'specVersion'='execution.principal.v1'
       AND snapshot->>'workspaceId'=workspace_id::TEXT
       AND jsonb_typeof(snapshot->'actor')='object'
       AND snapshot#>>'{actor,type}'=actor_type
       AND snapshot#>>'{actor,id}'=actor_id::TEXT
       AND (
         (subject_id IS NULL AND NOT snapshot ? 'subject')
         OR
         (subject_id IS NOT NULL AND jsonb_typeof(snapshot->'subject')='object'
          AND snapshot#>>'{subject,type}'=subject_type
          AND snapshot#>>'{subject,id}'=subject_id::TEXT)
       )
       AND (snapshot->>'clientId') IS NOT DISTINCT FROM client_id::TEXT
       AND (snapshot->>'grantId') IS NOT DISTINCT FROM grant_id::TEXT
       AND (snapshot->>'grantVersion') IS NOT DISTINCT FROM grant_version::TEXT
       AND (snapshot->>'agentPolicyVersion') IS NOT DISTINCT FROM agent_policy_version::TEXT
       AND jsonb_typeof(snapshot->'evidence')='object'
$$;

CREATE FUNCTION validate_execution_principal_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    actor_type_value TEXT;
    actor_id_value UUID;
    target_agent_id UUID;
    parent_version TEXT;
    parent_actor_type TEXT;
    parent_actor_id UUID;
    parent_subject_type TEXT;
    parent_subject_id UUID;
    parent_client_id UUID;
    parent_grant_id UUID;
    parent_grant_version BIGINT;
    parent_policy_version BIGINT;
BEGIN
    IF TG_TABLE_NAME='tool_invocations' THEN
        actor_type_value := NEW.actor_type;
        actor_id_value := NEW.actor_id;
    ELSE
        actor_type_value := NEW.triggered_by_type;
        actor_id_value := NEW.triggered_by_id;
    END IF;

    IF NEW.principal_snapshot_version IS NULL THEN
        IF actor_type_value='USER' THEN
            NEW.principal_snapshot_version := 'execution.principal.v1';
            NEW.subject_type := 'USER';
            NEW.subject_id := actor_id_value;
        ELSIF actor_type_value='SYSTEM' THEN
            NEW.principal_snapshot_version := 'execution.principal.v1';
            NEW.subject_type := NULL;
            NEW.subject_id := NULL;
        ELSE
            RAISE EXCEPTION 'Service Principal execution requires explicit authorization snapshot'
                USING ERRCODE='23514',CONSTRAINT='execution_external_snapshot_required';
        END IF;
        NEW.authorization_snapshot := jsonb_strip_nulls(jsonb_build_object(
            'specVersion','execution.principal.v1','workspaceId',NEW.workspace_id,
            'actor',jsonb_build_object('type',actor_type_value,'id',actor_id_value),
            'subject',CASE WHEN NEW.subject_id IS NULL THEN NULL ELSE
                jsonb_build_object('type',NEW.subject_type,'id',NEW.subject_id) END,
            'evidence',NEW.authorization_snapshot
        ));
    ELSIF NEW.principal_snapshot_version='legacy.v1' THEN
        RAISE EXCEPTION 'legacy Principal snapshots are reserved for migrated facts'
            USING ERRCODE='23514',CONSTRAINT='execution_legacy_snapshot_reserved';
    END IF;

    IF actor_type_value='USER' THEN
        IF NEW.subject_type IS DISTINCT FROM 'USER'
           OR NEW.subject_id IS DISTINCT FROM actor_id_value
           OR NEW.client_id IS NOT NULL OR NEW.grant_id IS NOT NULL
           OR NEW.grant_version IS NOT NULL OR NEW.agent_policy_version IS NOT NULL THEN
            RAISE EXCEPTION 'User execution Principal snapshot is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='execution_user_snapshot_check';
        END IF;
    ELSIF actor_type_value='SYSTEM' THEN
        IF NEW.subject_id IS NOT NULL OR NEW.client_id IS NOT NULL OR NEW.grant_id IS NOT NULL
           OR NEW.grant_version IS NOT NULL OR NEW.agent_policy_version IS NOT NULL THEN
            RAISE EXCEPTION 'System execution Principal snapshot is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='execution_system_snapshot_check';
        END IF;
    ELSIF actor_type_value='SERVICE_PRINCIPAL' THEN
        IF NEW.client_id IS NULL OR NEW.grant_id IS NULL OR NEW.grant_version IS NULL
           OR NEW.agent_policy_version IS NULL OR NOT EXISTS (
             SELECT 1 FROM agent_access_clients c
             WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.client_id
               AND c.service_principal_id=actor_id_value AND c.status='ACTIVE'
           ) OR (NEW.subject_id IS NOT NULL AND (
             NEW.subject_type <> 'EXTERNAL_SUBJECT' OR NOT EXISTS (
               SELECT 1 FROM external_subjects s
               WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.subject_id
                 AND s.client_id=NEW.client_id
             )
           )) THEN
            RAISE EXCEPTION 'External execution Principal binding is invalid'
                USING ERRCODE='23514',CONSTRAINT='execution_external_binding_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'Unknown execution Actor type'
            USING ERRCODE='23514',CONSTRAINT='execution_actor_type_check';
    END IF;

    IF TG_TABLE_NAME='agent_runs' THEN
        target_agent_id := NEW.agent_id;
    ELSIF TG_TABLE_NAME='workflow_executions' THEN
        IF NEW.agent_run_id IS NOT NULL THEN
            SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
                   subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
            INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
                 parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
                 parent_policy_version
            FROM agent_runs WHERE workspace_id=NEW.workspace_id AND id=NEW.agent_run_id;
        END IF;
    ELSIF TG_TABLE_NAME='tool_invocations' THEN
        IF NEW.workflow_execution_id IS NOT NULL THEN
            SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
                   subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
            INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
                 parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
                 parent_policy_version
            FROM workflow_executions
            WHERE workspace_id=NEW.workspace_id AND id=NEW.workflow_execution_id;
        ELSIF NEW.agent_run_id IS NOT NULL THEN
            SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
                   subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
            INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
                 parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
                 parent_policy_version
            FROM agent_runs WHERE workspace_id=NEW.workspace_id AND id=NEW.agent_run_id;
        END IF;
    END IF;

    IF parent_version IS NOT NULL AND ROW(
        NEW.principal_snapshot_version,actor_type_value,actor_id_value,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.grant_id,
        NEW.grant_version,NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
        parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
        parent_policy_version
    ) THEN
        RAISE EXCEPTION 'Child execution Principal snapshot differs from its parent'
            USING ERRCODE='23514',CONSTRAINT='execution_parent_snapshot_check';
    END IF;

    IF actor_type_value='SERVICE_PRINCIPAL' AND parent_version IS NULL AND NOT EXISTS (
        SELECT 1 FROM agent_access_grants g
        JOIN agents a ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
        WHERE g.workspace_id=NEW.workspace_id AND g.id=NEW.grant_id
          AND g.client_id=NEW.client_id AND g.lock_version=NEW.grant_version
          AND a.lock_version=NEW.agent_policy_version
          AND (target_agent_id IS NULL OR g.agent_id=target_agent_id)
          AND g.status='ACTIVE' AND g.valid_from <= clock_timestamp()
          AND (g.expires_at IS NULL OR g.expires_at > clock_timestamp())
    ) THEN
        RAISE EXCEPTION 'External execution Grant snapshot is stale or mismatched'
            USING ERRCODE='23514',CONSTRAINT='execution_grant_snapshot_check';
    END IF;

    IF NOT execution_authorization_envelope_matches(
        NEW.authorization_snapshot,NEW.workspace_id,actor_type_value,actor_id_value,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.grant_id,
        NEW.grant_version,NEW.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'Execution authorization envelope differs from typed snapshot'
            USING ERRCODE='23514',CONSTRAINT='execution_authorization_envelope_check';
    END IF;
    RETURN NEW;
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE EXCEPTION 'Execution parent does not exist'
            USING ERRCODE='23503',CONSTRAINT='execution_parent_snapshot_fk';
END;
$$;

CREATE TRIGGER agent_runs_principal_snapshot_guard
BEFORE INSERT ON agent_runs FOR EACH ROW
EXECUTE FUNCTION validate_execution_principal_snapshot();
CREATE TRIGGER workflow_executions_principal_snapshot_guard
BEFORE INSERT ON workflow_executions FOR EACH ROW
EXECUTE FUNCTION validate_execution_principal_snapshot();
CREATE TRIGGER tool_invocations_principal_snapshot_guard
BEFORE INSERT ON tool_invocations FOR EACH ROW
EXECUTE FUNCTION validate_execution_principal_snapshot();

CREATE OR REPLACE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'agent runs are permanently retained and cannot be deleted'
            USING ERRCODE='55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.agent_id,NEW.trigger_type,
        NEW.triggered_by_type,NEW.triggered_by_id,NEW.trace_id,NEW.model_snapshot,
        NEW.capability_snapshot,NEW.context_policy_snapshot,NEW.authorization_snapshot,
        NEW.snapshot_schema_version,NEW.input_summary,NEW.started_at,
        NEW.principal_snapshot_version,NEW.subject_type,NEW.subject_id,NEW.client_id,
        NEW.grant_id,NEW.grant_version,NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.agent_id,OLD.trigger_type,
        OLD.triggered_by_type,OLD.triggered_by_id,OLD.trace_id,OLD.model_snapshot,
        OLD.capability_snapshot,OLD.context_policy_snapshot,OLD.authorization_snapshot,
        OLD.snapshot_schema_version,OLD.input_summary,OLD.started_at,
        OLD.principal_snapshot_version,OLD.subject_type,OLD.subject_id,OLD.client_id,
        OLD.grant_id,OLD.grant_version,OLD.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal agent run is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version+1 THEN
        RAISE EXCEPTION 'agent run update requires the next lock version' USING ERRCODE='40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR
        (OLD.status='RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION','SUCCEEDED','FAILED','CANCELLED')) OR
        (OLD.status='WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING','FAILED','CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal agent run status transition from % to %',OLD.status,NEW.status
            USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_workflow_execution_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted'
            USING ERRCODE='55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.workflow_id,NEW.revision_id,NEW.compilation_id,
        NEW.agent_run_id,NEW.trigger_type,NEW.triggered_by_type,NEW.triggered_by_id,
        NEW.trace_id,NEW.snapshot_schema_version,NEW.authorization_snapshot,
        NEW.input_summary,NEW.started_at,NEW.principal_snapshot_version,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.grant_id,
        NEW.grant_version,NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.workflow_id,OLD.revision_id,OLD.compilation_id,
        OLD.agent_run_id,OLD.trigger_type,OLD.triggered_by_type,OLD.triggered_by_id,
        OLD.trace_id,OLD.snapshot_schema_version,OLD.authorization_snapshot,
        OLD.input_summary,OLD.started_at,OLD.principal_snapshot_version,
        OLD.subject_type,OLD.subject_id,OLD.client_id,OLD.grant_id,
        OLD.grant_version,OLD.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable'
            USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version+1 THEN
        RAISE EXCEPTION 'workflow execution update requires the next lock version' USING ERRCODE='40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR
        (OLD.status='RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION','SUCCEEDED','FAILED','CANCELLED')) OR
        (OLD.status='WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING','FAILED','CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution status transition from % to %',OLD.status,NEW.status
            USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_tool_invocation_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'tool invocations are permanently retained and cannot be deleted'
            USING ERRCODE='55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.tool_id,NEW.tool_version_id,
        NEW.capability_release_id,NEW.provider_id,NEW.connection_id,
        NEW.execution_lease_id,NEW.agent_run_id,NEW.workflow_execution_id,
        NEW.execution_step_id,NEW.actor_type,NEW.actor_id,NEW.trace_id,
        NEW.idempotency_key,NEW.input_summary,NEW.started_at,
        NEW.principal_snapshot_version,NEW.subject_type,NEW.subject_id,NEW.client_id,
        NEW.grant_id,NEW.grant_version,NEW.agent_policy_version,NEW.authorization_snapshot
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.tool_id,OLD.tool_version_id,
        OLD.capability_release_id,OLD.provider_id,OLD.connection_id,
        OLD.execution_lease_id,OLD.agent_run_id,OLD.workflow_execution_id,
        OLD.execution_step_id,OLD.actor_type,OLD.actor_id,OLD.trace_id,
        OLD.idempotency_key,OLD.input_summary,OLD.started_at,
        OLD.principal_snapshot_version,OLD.subject_type,OLD.subject_id,OLD.client_id,
        OLD.grant_id,OLD.grant_version,OLD.agent_policy_version,OLD.authorization_snapshot
    ) THEN
        RAISE EXCEPTION 'tool invocation identity and request evidence are immutable'
            USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal tool invocation is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR
        (OLD.status='RUNNING' AND NEW.status IN ('SUCCEEDED','FAILED','CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal tool invocation status transition from % to %',OLD.status,NEW.status
            USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE INDEX agent_runs_workspace_principal_started_idx ON agent_runs(
    workspace_id,client_id,subject_type,subject_id,started_at DESC,id
) WHERE client_id IS NOT NULL;
CREATE INDEX workflow_executions_workspace_principal_started_idx ON workflow_executions(
    workspace_id,client_id,subject_type,subject_id,started_at DESC,id
) WHERE client_id IS NOT NULL;
CREATE INDEX tool_invocations_workspace_principal_started_idx ON tool_invocations(
    workspace_id,client_id,subject_type,subject_id,started_at DESC,id
) WHERE client_id IS NOT NULL;
