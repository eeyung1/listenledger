-- Run as the migration administrator AFTER all embedded PostgreSQL migrations.
-- Idempotent, scoped to ListenLedger tables in current_schema(). Never stores a password.
-- New runtime roles are NOLOGIN until a strong password is provisioned securely.
-- The Go API enforces shop authorization; this role is server-only, never a JWT role.
DO $$
DECLARE
    target_schema text := current_schema();
    table_name text;
    api_role text;
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'listenledger_app') THEN
        CREATE ROLE listenledger_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
    END IF;
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO listenledger_app', current_database());
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO listenledger_app', target_schema);
    EXECUTE format('REVOKE CREATE ON SCHEMA %I FROM PUBLIC, listenledger_app', target_schema);
    FOREACH table_name IN ARRAY ARRAY[
        'schema_migrations', 'owners', 'shops', 'sessions', 'auth_attempts',
        'products', 'transactions', 'stock_movements', 'ledger_requests',
        'devices', 'review_decisions', 'shop_target_gates',
        'pilot_reconciliations', 'pilot_discrepancies', 'shop_cohorts',
        'expenses'
    ] LOOP
        IF to_regclass(format('%I.%I', target_schema, table_name)) IS NULL THEN
            RAISE EXCEPTION 'Missing table %.%; apply migrations before security setup', target_schema, table_name;
        END IF;
        EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', target_schema, table_name);
        EXECUTE format('REVOKE ALL ON TABLE %I.%I FROM PUBLIC, listenledger_app', target_schema, table_name);
        FOREACH api_role IN ARRAY ARRAY['anon', 'authenticated'] LOOP
            IF EXISTS (SELECT FROM pg_roles WHERE rolname = api_role) THEN
                EXECUTE format('REVOKE ALL ON TABLE %I.%I FROM %I', target_schema, table_name, api_role);
            END IF;
        END LOOP;
        -- Remove policies created by the previous script, if any.
        EXECUTE format('DROP POLICY IF EXISTS %I ON %I.%I', 'allow_app_full_access_' || table_name, target_schema, table_name);
        EXECUTE format('DROP POLICY IF EXISTS listenledger_runtime ON %I.%I', target_schema, table_name);
        IF table_name = 'schema_migrations' THEN
            EXECUTE format('GRANT SELECT ON TABLE %I.%I TO listenledger_app', target_schema, table_name);
            EXECUTE format('CREATE POLICY listenledger_runtime ON %I.%I FOR SELECT TO listenledger_app USING (true)', target_schema, table_name);
        ELSE
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE %I.%I TO listenledger_app', target_schema, table_name);
            EXECUTE format('CREATE POLICY listenledger_runtime ON %I.%I FOR ALL TO listenledger_app USING (true) WITH CHECK (true)', target_schema, table_name);
        END IF;
    END LOOP;
END
$$;

