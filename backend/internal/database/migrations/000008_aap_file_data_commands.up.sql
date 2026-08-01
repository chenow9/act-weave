-- Allow AAP file command receipts on agent_access_data_commands (file.create / file.complete).

ALTER TABLE agent_access_data_commands
    DROP CONSTRAINT IF EXISTS agent_access_data_commands_operation_check;

ALTER TABLE agent_access_data_commands
    ADD CONSTRAINT agent_access_data_commands_operation_check CHECK (operation IN (
        'conversation.create',
        'run.create',
        'run.cancel',
        'interaction.decide',
        'file.create',
        'file.complete'
    ));

ALTER TABLE agent_access_data_commands
    DROP CONSTRAINT IF EXISTS agent_access_data_commands_resource_type_check;

-- Postgres named the resource type check without a stable name; drop by redefining via
-- constraint discovery if present, otherwise drop the anonymous check matching the old set.
DO $$
DECLARE
    constraint_name text;
BEGIN
    SELECT con.conname INTO constraint_name
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
    WHERE nsp.nspname = 'public'
      AND rel.relname = 'agent_access_data_commands'
      AND con.contype = 'c'
      AND pg_get_constraintdef(con.oid) LIKE '%CONVERSATION%RUN%INTERACTION%';
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_access_data_commands DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

ALTER TABLE agent_access_data_commands
    ADD CONSTRAINT agent_access_data_commands_resource_type_check CHECK (
        resource_type IS NULL
        OR resource_type IN ('CONVERSATION', 'RUN', 'INTERACTION', 'FILE')
    );
