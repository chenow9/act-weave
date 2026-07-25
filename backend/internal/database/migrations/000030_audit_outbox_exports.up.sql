CREATE TABLE audit_events (
    id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id UUID,
    actor_type TEXT NOT NULL,
    actor_id UUID,
    actor_display TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID,
    result TEXT NOT NULL,
    request_id TEXT,
    trace_id TEXT,
    source_ip INET,
    user_agent TEXT,
    changes JSONB NOT NULL DEFAULT '{}'::JSONB,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    payload_object_id UUID,
    schema_version TEXT NOT NULL,
    PRIMARY KEY (occurred_at, id),
    CONSTRAINT audit_events_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT audit_events_payload_workspace_check
        CHECK (payload_object_id IS NULL OR workspace_id IS NOT NULL),
    CONSTRAINT audit_events_payload_object_fk
        FOREIGN KEY (workspace_id, payload_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT audit_events_actor_type_check
        CHECK (actor_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT audit_events_actor_id_check
        CHECK (actor_type <> 'USER' OR actor_id IS NOT NULL),
    CONSTRAINT audit_events_actor_display_check
        CHECK (length(btrim(actor_display)) BETWEEN 1 AND 255),
    CONSTRAINT audit_events_action_check
        CHECK (action ~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_-]*)+$'),
    CONSTRAINT audit_events_resource_type_check
        CHECK (resource_type ~ '^[A-Z][A-Z0-9_]{0,127}$'),
    CONSTRAINT audit_events_result_check CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED')),
    CONSTRAINT audit_events_request_id_check
        CHECK (request_id IS NULL OR length(btrim(request_id)) BETWEEN 1 AND 255),
    CONSTRAINT audit_events_trace_id_check
        CHECK (trace_id IS NULL OR length(btrim(trace_id)) BETWEEN 1 AND 255),
    CONSTRAINT audit_events_user_agent_check
        CHECK (user_agent IS NULL OR length(user_agent) <= 1024),
    CONSTRAINT audit_events_changes_object_check CHECK (jsonb_typeof(changes) = 'object'),
    CONSTRAINT audit_events_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT audit_events_schema_version_check
        CHECK (schema_version ~ '^[a-z][a-z0-9_.-]{0,63}$')
) PARTITION BY RANGE (occurred_at);

CREATE TABLE audit_events_default PARTITION OF audit_events DEFAULT;

CREATE INDEX audit_events_workspace_occurred_idx
    ON audit_events (workspace_id, occurred_at DESC, id);
CREATE INDEX audit_events_workspace_actor_occurred_idx
    ON audit_events (workspace_id, actor_type, actor_id, occurred_at DESC, id);
CREATE INDEX audit_events_workspace_resource_occurred_idx
    ON audit_events (workspace_id, resource_type, resource_id, occurred_at DESC, id);
CREATE INDEX audit_events_workspace_action_occurred_idx
    ON audit_events (workspace_id, action, occurred_at DESC, id);
CREATE INDEX audit_events_request_id_idx
    ON audit_events (request_id, occurred_at DESC, id) WHERE request_id IS NOT NULL;
CREATE INDEX audit_events_trace_id_idx
    ON audit_events (trace_id, occurred_at DESC, id) WHERE trace_id IS NOT NULL;
CREATE INDEX audit_events_platform_occurred_idx
    ON audit_events (occurred_at DESC, id) WHERE workspace_id IS NULL;

CREATE FUNCTION reject_audit_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit events are insert-only'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER audit_events_insert_only
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION reject_audit_event_mutation();

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    workspace_id UUID,
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    schema_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT outbox_events_idempotency_key_key UNIQUE (idempotency_key),
    CONSTRAINT outbox_events_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT outbox_events_aggregate_type_check
        CHECK (aggregate_type ~ '^[A-Z][A-Z0-9_]{0,127}$'),
    CONSTRAINT outbox_events_event_type_check
        CHECK (event_type ~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_-]*)+$'),
    CONSTRAINT outbox_events_payload_object_check CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_schema_version_check
        CHECK (schema_version ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    CONSTRAINT outbox_events_idempotency_key_check
        CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 255),
    CONSTRAINT outbox_events_attempts_check CHECK (attempts >= 0),
    CONSTRAINT outbox_events_last_error_check
        CHECK (last_error IS NULL OR length(last_error) <= 2048),
    CONSTRAINT outbox_events_timestamps_check CHECK (
        available_at >= occurred_at
        AND created_at >= occurred_at
        AND (published_at IS NULL OR published_at >= occurred_at)
    )
);

CREATE INDEX outbox_events_unpublished_available_idx
    ON outbox_events (available_at, occurred_at, id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_workspace_aggregate_idx
    ON outbox_events (workspace_id, aggregate_type, aggregate_id, occurred_at DESC, id);
CREATE INDEX outbox_events_event_type_occurred_idx
    ON outbox_events (event_type, occurred_at DESC, id);

CREATE TABLE audit_exports (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    filter_snapshot JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    object_id UUID,
    requested_by UUID NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    error_code TEXT,
    CONSTRAINT audit_exports_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT audit_exports_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT audit_exports_object_fk
        FOREIGN KEY (workspace_id, object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT audit_exports_requested_by_fk
        FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT audit_exports_filter_object_check CHECK (jsonb_typeof(filter_snapshot) = 'object'),
    CONSTRAINT audit_exports_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'EXPIRED')),
    CONSTRAINT audit_exports_expiry_check CHECK (expires_at > requested_at),
    CONSTRAINT audit_exports_result_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND object_id IS NULL AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'SUCCEEDED' AND object_id IS NOT NULL AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (status = 'FAILED' AND object_id IS NULL AND completed_at IS NOT NULL AND error_code IS NOT NULL)
        OR (status = 'EXPIRED' AND completed_at IS NOT NULL)
    ),
    CONSTRAINT audit_exports_completed_at_check
        CHECK (completed_at IS NULL OR completed_at >= requested_at),
    CONSTRAINT audit_exports_error_code_check
        CHECK (error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{0,127}$')
);

CREATE INDEX audit_exports_workspace_requested_idx
    ON audit_exports (workspace_id, requested_at DESC, id);
CREATE INDEX audit_exports_pending_idx
    ON audit_exports (status, requested_at, id) WHERE status IN ('PENDING', 'RUNNING');
CREATE INDEX audit_exports_expiry_idx
    ON audit_exports (expires_at, id) WHERE status = 'SUCCEEDED';

CREATE FUNCTION enforce_audit_export_object()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.object_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM stored_objects
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.object_id
          AND kind = 'AUDIT_EXPORT'
          AND retention_mode = 'EXPIRING'
          AND retention_until IS NOT NULL
          AND retention_until <= NEW.expires_at
    ) THEN
        RAISE EXCEPTION 'audit export requires a matching expiring AUDIT_EXPORT object'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_exports_object_guard
BEFORE INSERT OR UPDATE OF workspace_id, object_id, expires_at ON audit_exports
FOR EACH ROW EXECUTE FUNCTION enforce_audit_export_object();
