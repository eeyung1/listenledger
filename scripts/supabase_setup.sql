-- Supabase Security & Restricted Role Setup for ListenLedger
-- Reference: DATABASE_MIGRATION.md §4
--
-- This script provisions the restricted application database role, grants minimal
-- necessary DML privileges, enables Row Level Security (RLS) across all application tables,
-- and locks down the schema against unauthenticated public access.

-- 1. Create dedicated application role if not already provisioned
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'listenledger_app') THEN
        CREATE ROLE listenledger_app WITH LOGIN PASSWORD 'change_me_to_strong_secret';
    END IF;
END
$$;

-- 2. Schema-level permissions
GRANT CONNECT ON DATABASE postgres TO listenledger_app;
GRANT USAGE ON SCHEMA public TO listenledger_app;

-- 3. Grant minimal required DML privileges (NO DDL, NO DROP, NO SUPERUSER)
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO listenledger_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO listenledger_app;

ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO listenledger_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO listenledger_app;

-- 4. Enable Row Level Security (RLS) on all application tables
-- By default, tables with RLS enabled and no public policies reject all requests from anon/public clients.
ALTER TABLE IF EXISTS schema_migrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS owners ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS shops ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS products ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS stock_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS ledger_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS review_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS auth_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS shop_target_gates ENABLE ROW LEVEL SECURITY;

-- 5. Policies allowing server application role access
-- The Go orchestrator authenticates with listenledger_app credentials, which operates server-side.
CREATE POLICY "allow_app_full_access_schema_migrations" ON schema_migrations FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_owners" ON owners FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_shops" ON shops FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_products" ON products FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_transactions" ON transactions FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_stock_movements" ON stock_movements FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_ledger_requests" ON ledger_requests FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_devices" ON devices FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_review_decisions" ON review_decisions FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_sessions" ON sessions FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_auth_attempts" ON auth_attempts FOR ALL TO listenledger_app USING (true) WITH CHECK (true);
CREATE POLICY "allow_app_full_access_shop_target_gates" ON shop_target_gates FOR ALL TO listenledger_app USING (true) WITH CHECK (true);

-- 6. Disallow anonymous (anon) PostgREST Data API exposure
-- Ensure public/anon roles cannot query or mutate business data directly.
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM anon, public;

