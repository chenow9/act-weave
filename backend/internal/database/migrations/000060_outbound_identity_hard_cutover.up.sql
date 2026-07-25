-- 000060: outbound identity hard cutover (T4 physical delete).
-- Production semantics: single transactional hard cut. After COMMIT only
-- roll-forward is allowed. Down migration exists only for schema tests and
-- cannot restore deleted secrets, versions, ciphertext, or credential_secret_id.
--
-- Target connections: service_connections whose provider_kind = 'HTTP_OPENAPI'
-- (including soft-deleted rows). No automatic mode inference from legacy auth.

-- ---------------------------------------------------------------------------
-- Schema: Provider / Connection policy columns
-- ---------------------------------------------------------------------------

ALTER TABLE capability_providers
    ADD COLUMN outbound_identity_policy_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE capability_providers
    ADD CONSTRAINT capability_providers_outbound_identity_policy_version_check
        CHECK (outbound_identity_policy_version > 0);

ALTER TABLE service_connections
    ADD COLUMN outbound_identity JSONB,
    ADD COLUMN outbound_identity_policy_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN migration_state TEXT NOT NULL DEFAULT 'NONE',
    ADD COLUMN machine_credential_secret_id UUID;

ALTER TABLE service_connections
    ADD CONSTRAINT service_connections_outbound_identity_object_check
        CHECK (outbound_identity IS NULL OR jsonb_typeof(outbound_identity) = 'object'),
    ADD CONSTRAINT service_connections_outbound_identity_policy_version_check
        CHECK (outbound_identity_policy_version > 0),
    ADD CONSTRAINT service_connections_migration_state_check
        CHECK (migration_state IN ('NONE', 'MIGRATION_REQUIRED')),
    ADD CONSTRAINT service_connections_machine_credential_secret_fk
        FOREIGN KEY (workspace_id, machine_credential_secret_id)
        REFERENCES secrets (workspace_id, id) ON DELETE RESTRICT;

CREATE INDEX service_connections_workspace_migration_state_idx
    ON service_connections (workspace_id, migration_state, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE INDEX service_connections_machine_credential_secret_idx
    ON service_connections (workspace_id, machine_credential_secret_id)
    WHERE machine_credential_secret_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Schema: runtime instance / affinity metadata (no Token / Vault locator)
-- ---------------------------------------------------------------------------

CREATE TABLE outbound_runtime_instances (
    instance_id TEXT NOT NULL,
    boot_id TEXT NOT NULL,
    workspace_scope TEXT NOT NULL DEFAULT 'cluster',
    internal_address TEXT NOT NULL,
    routing_public_key BYTEA NOT NULL,
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    draining BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_id, boot_id),
    CONSTRAINT outbound_runtime_instances_instance_id_not_blank
        CHECK (length(btrim(instance_id)) BETWEEN 1 AND 128),
    CONSTRAINT outbound_runtime_instances_boot_id_not_blank
        CHECK (length(btrim(boot_id)) BETWEEN 1 AND 128),
    CONSTRAINT outbound_runtime_instances_internal_address_not_blank
        CHECK (length(btrim(internal_address)) BETWEEN 1 AND 512),
    CONSTRAINT outbound_runtime_instances_routing_public_key_not_empty
        CHECK (octet_length(routing_public_key) > 0),
    CONSTRAINT outbound_runtime_instances_timestamps_check
        CHECK (updated_at >= started_at)
);

CREATE INDEX outbound_runtime_instances_heartbeat_idx
    ON outbound_runtime_instances (heartbeat_at DESC, instance_id, boot_id);

CREATE TABLE outbound_runtime_affinities (
    workspace_id UUID NOT NULL,
    root_scope_type TEXT NOT NULL,
    root_scope_id UUID NOT NULL,
    owner_instance_id TEXT NOT NULL,
    owner_boot_id TEXT NOT NULL,
    root_deadline_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, root_scope_type, root_scope_id),
    CONSTRAINT outbound_runtime_affinities_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT outbound_runtime_affinities_owner_fk
        FOREIGN KEY (owner_instance_id, owner_boot_id)
        REFERENCES outbound_runtime_instances (instance_id, boot_id)
        ON DELETE RESTRICT,
    CONSTRAINT outbound_runtime_affinities_root_scope_type_check
        CHECK (root_scope_type IN (
            'AGENT_RUN',
            'DIRECT_INVOCATION',
            'TOOL_TEST',
            'WORKFLOW_TRIAL',
            'WORKFLOW_EXECUTION',
            'DEBUG_ATTACHMENT'
        )),
    CONSTRAINT outbound_runtime_affinities_timestamps_check
        CHECK (updated_at >= created_at)
);

CREATE INDEX outbound_runtime_affinities_owner_idx
    ON outbound_runtime_affinities (owner_instance_id, owner_boot_id, root_deadline_at);

CREATE INDEX outbound_runtime_affinities_deadline_idx
    ON outbound_runtime_affinities (root_deadline_at, workspace_id);

-- ---------------------------------------------------------------------------
-- Hard-cut data mutation + T4 physical Secret delete (all-or-nothing)
-- ---------------------------------------------------------------------------

DO $$
DECLARE
    blocked_model_refs BIGINT;
    blocked_nontarget_refs BIGINT;
    target_connection_count BIGINT;
    candidate_secret_count BIGINT;
    candidate_version_count BIGINT;
    deleted_secret_count BIGINT;
    deleted_version_count BIGINT;
    remaining_secret_refs BIGINT;
    remaining_version_refs BIGINT;
    remaining_connection_refs BIGINT;
    remaining_model_refs BIGINT;
    ws RECORD;
    audit_connection_count BIGINT;
    audit_secret_count BIGINT;
    audit_version_count BIGINT;
BEGIN
    -- 1) Lock target connections (HTTP_OPENAPI providers, including soft-deleted).
    PERFORM 1
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
    ORDER BY c.workspace_id, c.id
    FOR UPDATE OF c;

    -- 2) Determine candidate Secret set from target connections before clearing refs.
    CREATE TEMP TABLE outbound_cutover_candidate_secrets (
        workspace_id UUID NOT NULL,
        secret_id UUID NOT NULL,
        PRIMARY KEY (workspace_id, secret_id)
    ) ON COMMIT DROP;

    INSERT INTO outbound_cutover_candidate_secrets (workspace_id, secret_id)
    SELECT DISTINCT c.workspace_id, c.credential_secret_id
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL;

    -- Lock candidate secrets and their versions.
    PERFORM 1
    FROM secrets AS s
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = s.workspace_id AND cand.secret_id = s.id
    ORDER BY s.workspace_id, s.id
    FOR UPDATE OF s;

    PERFORM 1
    FROM secret_versions AS v
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id
    ORDER BY v.workspace_id, v.secret_id, v.id
    FOR UPDATE OF v;

    -- Lock referencing rows that could share candidates.
    PERFORM 1
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id
    ORDER BY m.workspace_id, m.id
    FOR UPDATE OF m;

    PERFORM 1
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id
    ORDER BY c.workspace_id, c.id
    FOR UPDATE OF c;

    -- 3) Preflight: model_configs or non-target Connection sharing blocks entire cutover.
    SELECT COUNT(*) INTO blocked_model_refs
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id;

    SELECT COUNT(*) INTO blocked_nontarget_refs
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind <> 'HTTP_OPENAPI';

    IF blocked_model_refs > 0 OR blocked_nontarget_refs > 0 THEN
        RAISE EXCEPTION
            'outbound identity hard cutover blocked: shared secret references exist (model_configs=%, non_target_connections=%). Rebind out-of-scope consumers before migration. No mutations applied.',
            blocked_model_refs, blocked_nontarget_refs
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*) INTO target_connection_count
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI';

    SELECT COUNT(*) INTO candidate_secret_count
    FROM outbound_cutover_candidate_secrets;

    SELECT COUNT(*) INTO candidate_version_count
    FROM secret_versions AS v
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id;

    -- 4) Disable active target connections; mark migration required.
    UPDATE service_connections AS c
    SET status = 'DISABLED',
        migration_state = 'MIGRATION_REQUIRED',
        last_error_code = NULL,
        updated_at = clock_timestamp(),
        lock_version = c.lock_version + 1
    FROM capability_providers AS p
    WHERE p.workspace_id = c.workspace_id
      AND p.id = c.provider_id
      AND p.provider_kind = 'HTTP_OPENAPI'
      AND c.deleted_at IS NULL;

    -- Soft-deleted targets also require migration if restored; do not change status.
    UPDATE service_connections AS c
    SET migration_state = 'MIGRATION_REQUIRED',
        updated_at = clock_timestamp(),
        lock_version = c.lock_version + 1
    FROM capability_providers AS p
    WHERE p.workspace_id = c.workspace_id
      AND p.id = c.provider_id
      AND p.provider_kind = 'HTTP_OPENAPI'
      AND c.deleted_at IS NOT NULL
      AND c.migration_state <> 'MIGRATION_REQUIRED';

    -- 5) Clear credential_secret_id on all target connections (incl. soft-deleted).
    UPDATE service_connections AS c
    SET credential_secret_id = NULL,
        updated_at = clock_timestamp(),
        lock_version = c.lock_version + 1
    FROM capability_providers AS p
    WHERE p.workspace_id = c.workspace_id
      AND p.id = c.provider_id
      AND p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL;

    -- 6) Re-prove candidate FK refs are zero before delete.
    SELECT COUNT(*) INTO remaining_connection_refs
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id;

    SELECT COUNT(*) INTO remaining_model_refs
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id;

    IF remaining_connection_refs <> 0 OR remaining_model_refs <> 0 THEN
        RAISE EXCEPTION
            'outbound identity hard cutover failed: candidate secret references remain after clear (connections=%, model_configs=%). Rolling back.',
            remaining_connection_refs, remaining_model_refs
            USING ERRCODE = 'check_violation';
    END IF;

    -- 7) SYSTEM audit per workspace — aggregate counts only (no Secret IDs/names).
    FOR ws IN
        SELECT workspace_id
        FROM (
            SELECT c.workspace_id
            FROM service_connections AS c
            INNER JOIN capability_providers AS p
                ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
            WHERE p.provider_kind = 'HTTP_OPENAPI'
            UNION
            SELECT cand.workspace_id
            FROM outbound_cutover_candidate_secrets AS cand
        ) AS scoped
        ORDER BY workspace_id
    LOOP
        SELECT COUNT(*) INTO audit_connection_count
        FROM service_connections AS c
        INNER JOIN capability_providers AS p
            ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
        WHERE p.provider_kind = 'HTTP_OPENAPI'
          AND c.workspace_id = ws.workspace_id;

        SELECT COUNT(*) INTO audit_secret_count
        FROM outbound_cutover_candidate_secrets
        WHERE workspace_id = ws.workspace_id;

        SELECT COUNT(*) INTO audit_version_count
        FROM secret_versions AS v
        INNER JOIN outbound_cutover_candidate_secrets AS cand
            ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id
        WHERE cand.workspace_id = ws.workspace_id;

        INSERT INTO audit_events (
            id, occurred_at, workspace_id, actor_type, actor_id, actor_display,
            action, resource_type, resource_id, result, request_id, trace_id,
            source_ip, user_agent, changes, metadata, payload_object_id, schema_version
        ) VALUES (
            gen_random_uuid(),
            clock_timestamp(),
            ws.workspace_id,
            'SYSTEM',
            NULL,
            'Outbound identity hard cutover',
            'outbound.identity.legacy_secret.deleted',
            'WORKSPACE',
            ws.workspace_id,
            'SUCCESS',
            NULL,
            NULL,
            NULL,
            NULL,
            '{}'::JSONB,
            jsonb_build_object(
                'targetConnectionCount', audit_connection_count,
                'deletedSecretCount', audit_secret_count,
                'deletedSecretVersionCount', audit_version_count,
                'migration', '000060_outbound_identity_hard_cutover',
                'note', 'aggregate counts only; secret identifiers are not recorded'
            ),
            NULL,
            'audit.v1'
        );
    END LOOP;

    -- 8) Physical delete: clear active_version_id, delete all versions (incl. revoked), delete secrets.
    UPDATE secrets AS s
    SET active_version_id = NULL,
        updated_at = clock_timestamp(),
        lock_version = s.lock_version + 1
    FROM outbound_cutover_candidate_secrets AS cand
    WHERE cand.workspace_id = s.workspace_id
      AND cand.secret_id = s.id;

    WITH deleted AS (
        DELETE FROM secret_versions AS v
        USING outbound_cutover_candidate_secrets AS cand
        WHERE cand.workspace_id = v.workspace_id
          AND cand.secret_id = v.secret_id
        RETURNING v.id
    )
    SELECT COUNT(*) INTO deleted_version_count FROM deleted;

    WITH deleted AS (
        DELETE FROM secrets AS s
        USING outbound_cutover_candidate_secrets AS cand
        WHERE cand.workspace_id = s.workspace_id
          AND cand.secret_id = s.id
        RETURNING s.id
    )
    SELECT COUNT(*) INTO deleted_secret_count FROM deleted;

    IF deleted_secret_count <> candidate_secret_count
       OR deleted_version_count <> candidate_version_count THEN
        RAISE EXCEPTION
            'outbound identity hard cutover delete count mismatch (secrets deleted=% expected=%, versions deleted=% expected=%). Rolling back.',
            deleted_secret_count, candidate_secret_count,
            deleted_version_count, candidate_version_count
            USING ERRCODE = 'check_violation';
    END IF;

    -- 9) Post-delete proof: candidates gone from secrets, versions, and both ref tables.
    SELECT COUNT(*) INTO remaining_secret_refs
    FROM secrets AS s
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = s.workspace_id AND cand.secret_id = s.id;

    SELECT COUNT(*) INTO remaining_version_refs
    FROM secret_versions AS v
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id;

    SELECT COUNT(*) INTO remaining_connection_refs
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id;

    SELECT COUNT(*) INTO remaining_model_refs
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id;

    IF remaining_secret_refs <> 0
       OR remaining_version_refs <> 0
       OR remaining_connection_refs <> 0
       OR remaining_model_refs <> 0 THEN
        RAISE EXCEPTION
            'outbound identity hard cutover post-delete proof failed (secrets=%, versions=%, connections=%, model_configs=%). Rolling back.',
            remaining_secret_refs, remaining_version_refs,
            remaining_connection_refs, remaining_model_refs
            USING ERRCODE = 'check_violation';
    END IF;

    -- Safe aggregate log (counts only).
    RAISE NOTICE
        'outbound identity hard cutover complete: target_connections=%, secrets_deleted=%, secret_versions_deleted=%',
        target_connection_count, deleted_secret_count, deleted_version_count;
END
$$;
