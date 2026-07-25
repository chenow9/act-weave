ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_token_ttl_check;

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_token_ttl_check
    CHECK (token_ttl_seconds BETWEEN 60 AND 900);
