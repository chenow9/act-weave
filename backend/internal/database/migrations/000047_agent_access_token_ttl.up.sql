ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_token_ttl_check;

-- AAP v1 only issues 5-15 minute Access Tokens. Fail the migration instead of
-- silently lengthening an operator-selected TTL on an existing Client.
ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_token_ttl_check
    CHECK (token_ttl_seconds BETWEEN 300 AND 900);
