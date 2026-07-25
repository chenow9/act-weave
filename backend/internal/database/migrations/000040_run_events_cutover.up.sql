DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM run_events re
        JOIN agent_runs ar
          ON ar.workspace_id=re.workspace_id AND ar.id=re.run_id
        WHERE ar.session_id IS NULL
    ) THEN
        RAISE EXCEPTION 'legacy run events require an explicit conversation mapping before AAP cutover'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'run_events_cutover_conversation_required';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM run_events re
        JOIN protocol_event_streams pes
          ON pes.workspace_id=re.workspace_id AND pes.run_id=re.run_id
    ) THEN
        RAISE EXCEPTION 'legacy run has an existing protocol stream; resolve dual-write state before cutover'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'run_events_cutover_single_source';
    END IF;
END;
$$;

INSERT INTO protocol_event_streams(
    id,workspace_id,agent_id,conversation_id,run_id,next_sequence,created_at
)
SELECT
    re.run_id,re.workspace_id,ar.agent_id,ar.session_id,re.run_id,
    max(re.sequence_no)+1,min(re.created_at)
FROM run_events re
JOIN agent_runs ar
  ON ar.workspace_id=re.workspace_id AND ar.id=re.run_id
GROUP BY re.run_id,re.workspace_id,ar.agent_id,ar.session_id;

WITH legacy AS (
    SELECT
        re.*,ar.agent_id,ar.session_id,ar.trace_id,ar.trigger_type,
        ar.started_at,ar.finished_at,
        CASE re.event_type
            WHEN 'RUN_STARTED' THEN 'run.started'
            WHEN 'STEP_STARTED' THEN 'item.started'
            WHEN 'STEP_COMPLETED' THEN 'item.completed'
            WHEN 'RUN_WAITING_CONFIRMATION' THEN 'run.waiting'
            WHEN 'RUN_RESUMED' THEN 'run.resumed'
            WHEN 'RUN_COMPLETED' THEN 'run.completed'
            WHEN 'RUN_FAILED' THEN 'run.failed'
            WHEN 'RUN_CANCELLED' THEN 'run.cancelled'
        END AS mapped_type,
        CASE
            WHEN re.payload->>'stepId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN (re.payload->>'stepId')::UUID
            ELSE re.id
        END AS mapped_item_id,
        CASE
            WHEN re.payload->>'chatConfirmationId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN (re.payload->>'chatConfirmationId')::UUID
            ELSE re.id
        END AS mapped_interaction_id
    FROM run_events re
    JOIN agent_runs ar
      ON ar.workspace_id=re.workspace_id AND ar.id=re.run_id
)
INSERT INTO protocol_events(
    id,workspace_id,agent_id,conversation_id,run_id,stream_id,
    sequence_no,event_type,spec_version,item_id,payload,occurred_at
)
SELECT
    legacy.id,legacy.workspace_id,legacy.agent_id,legacy.session_id,legacy.run_id,
    legacy.run_id,legacy.sequence_no,legacy.mapped_type,'1.0',
    CASE WHEN legacy.event_type LIKE 'STEP_%' THEN legacy.mapped_item_id END,
    jsonb_build_object(
        'specVersion','1.0',
        'type',legacy.mapped_type,
        'eventId',legacy.id,
        'streamId','run:' || legacy.run_id::TEXT,
        'sequence',legacy.sequence_no,
        'occurredAt',legacy.created_at,
        'workspaceId',legacy.workspace_id,
        'agentId',legacy.agent_id,
        'conversationId',legacy.session_id,
        'runId',legacy.run_id,
        'traceId',legacy.trace_id,
        'data',
            jsonb_build_object(
                'legacyEventType',legacy.event_type,
                'legacyPayload',legacy.payload
            ) ||
            CASE
                WHEN legacy.event_type LIKE 'RUN_%' THEN
                    jsonb_build_object(
                        'run',jsonb_strip_nulls(jsonb_build_object(
                            'id',legacy.run_id,
                            'conversationId',legacy.session_id,
                            'agentId',legacy.agent_id,
                            'status',CASE legacy.event_type
                                WHEN 'RUN_STARTED' THEN 'running'
                                WHEN 'RUN_WAITING_CONFIRMATION' THEN 'waiting_interaction'
                                WHEN 'RUN_RESUMED' THEN 'running'
                                WHEN 'RUN_COMPLETED' THEN 'completed'
                                WHEN 'RUN_FAILED' THEN 'failed'
                                WHEN 'RUN_CANCELLED' THEN 'cancelled'
                            END,
                            'trigger',CASE upper(legacy.trigger_type)
                                WHEN 'CHAT' THEN 'message'
                                WHEN 'WORKFLOW' THEN 'workflow'
                                WHEN 'API' THEN 'api'
                                ELSE 'system'
                            END,
                            'startedAt',legacy.started_at,
                            'completedAt',CASE
                                WHEN legacy.event_type IN ('RUN_COMPLETED','RUN_FAILED','RUN_CANCELLED')
                                    THEN coalesce(legacy.finished_at,legacy.created_at)
                            END
                        ))
                    ) || CASE legacy.event_type
                        WHEN 'RUN_WAITING_CONFIRMATION' THEN jsonb_build_object(
                            'interactionIds',jsonb_build_array(legacy.mapped_interaction_id)
                        )
                        WHEN 'RUN_RESUMED' THEN jsonb_build_object(
                            'interactionId',legacy.mapped_interaction_id
                        )
                        ELSE '{}'::JSONB
                    END
                ELSE
                    jsonb_build_object(
                        'item',jsonb_build_object(
                            'id',legacy.mapped_item_id,
                            'type','notice',
                            'status',CASE legacy.event_type
                                WHEN 'STEP_STARTED' THEN 'in_progress'
                                ELSE 'completed'
                            END,
                            'code','LEGACY_' || legacy.event_type,
                            'message','Imported legacy execution step event'
                        )
                    )
            END
    ),
    legacy.created_at
FROM legacy
ORDER BY legacy.created_at,legacy.run_id,legacy.sequence_no;

CREATE FUNCTION reject_legacy_run_event_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'run_events cutover is complete; append through protocol_events'
        USING ERRCODE = '55000',
              CONSTRAINT = 'run_events_cutover_complete';
END;
$$;

CREATE TRIGGER run_events_cutover_complete
BEFORE INSERT ON run_events
FOR EACH ROW EXECUTE FUNCTION reject_legacy_run_event_insert();
