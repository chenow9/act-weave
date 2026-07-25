CREATE EXTENSION IF NOT EXISTS citext;

DO $$
BEGIN
    EXECUTE format(
        'ALTER DATABASE %I SET timezone TO %L',
        current_database(),
        'UTC'
    );
END
$$;
