DO $$
BEGIN
    EXECUTE format(
        'ALTER DATABASE %I RESET timezone',
        current_database()
    );
END
$$;

DROP EXTENSION IF EXISTS citext;
