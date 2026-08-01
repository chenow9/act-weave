-- Restore pre-file data command receipt constraints.

ALTER TABLE agent_access_data_commands
    DROP CONSTRAINT IF EXISTS agent_access_data_commands_operation_check;

ALTER TABLE agent_access_data_commands
    ADD CONSTRAINT agent_access_data_commands_operation_check CHECK (operation IN (
        'conversation.create',
        'run.create',
        'run.cancel',
        'interaction.decide'
    ));

ALTER TABLE agent_access_data_commands
    DROP CONSTRAINT IF EXISTS agent_access_data_commands_resource_type_check;

ALTER TABLE agent_access_data_commands
    ADD CONSTRAINT agent_access_data_commands_resource_type_check CHECK (
        resource_type IS NULL
        OR resource_type IN ('CONVERSATION', 'RUN', 'INTERACTION')
    );
