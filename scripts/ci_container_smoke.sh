#!/usr/bin/env bash
# Disposable CI PostgreSQL only. Never run against the live Supabase project.
set -euo pipefail
pg_container="${1:?PostgreSQL CI container ID required}"
image="listenledger:ci"
admin_url="postgres://postgres:postgrespassword@127.0.0.1:5432/listenledger_test?sslmode=disable"
runtime_url="postgres://listenledger_app:ci_runtime_only@127.0.0.1:5432/listenledger_test?sslmode=disable"
cleanup() { docker rm -f listenledger-smoke >/dev/null 2>&1 || true; }
trap cleanup EXIT
wait_ready() {
  for attempt in $(seq 1 30); do
    if curl -fsS http://127.0.0.1:8080/readyz > /tmp/listenledger-ready.json; then return; fi
    sleep 1
  done
  docker logs listenledger-smoke
  return 1
}
sql() { docker exec -i "$pg_container" psql -U postgres -d listenledger_test -v ON_ERROR_STOP=1; }

# Production image must fail closed without a configured PostgreSQL database.
if docker run --rm "$image" > /tmp/listenledger-missing-db.log 2>&1; then
  echo 'ERROR: production image accepted an absent database'; exit 1
fi
grep -q 'PostgreSQL is required' /tmp/listenledger-missing-db.log

# Migration administrator is used only to initialize this disposable CI database.
docker run -d --name listenledger-smoke --network host -e DATABASE_URL="$admin_url" "$image" \
  -frontend /app/frontend -require-postgres > /dev/null
wait_ready
cleanup
sql <<'SQL'
CREATE ROLE anon NOLOGIN;
CREATE ROLE authenticated NOLOGIN;
SQL
sql < scripts/supabase_setup.sql
sql < scripts/supabase_setup.sql
sql <<'SQL'
ALTER ROLE listenledger_app LOGIN PASSWORD 'ci_runtime_only';
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND NOT rowsecurity) THEN
    RAISE EXCEPTION 'Application table lacks RLS';
  END IF;
  IF EXISTS (
    SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND (
      has_table_privilege('anon', format('%I.%I', schemaname, tablename), 'SELECT,INSERT,UPDATE,DELETE') OR
      has_table_privilege('authenticated', format('%I.%I', schemaname, tablename), 'SELECT,INSERT,UPDATE,DELETE')
    )
  ) THEN RAISE EXCEPTION 'Data API roles can access application tables'; END IF;
  IF has_schema_privilege('listenledger_app', 'public', 'CREATE') OR
     has_table_privilege('listenledger_app', 'public.schema_migrations', 'INSERT,UPDATE,DELETE') THEN
    RAISE EXCEPTION 'Runtime role has migration privileges';
  END IF;
END $$;
SQL

# Real image defaults: require PostgreSQL and never run schema-changing statements.
docker run -d --name listenledger-smoke --network host -e DATABASE_URL="$runtime_url" "$image" > /dev/null
wait_ready
curl -fsS http://127.0.0.1:8080/ > /dev/null
curl -fsS http://127.0.0.1:8080/dist/app.js > /dev/null
python3 scripts/ci_runtime_smoke.py
# Verify persisted account after the container is restarted with the same restricted role.
docker restart listenledger-smoke > /dev/null
wait_ready
python3 scripts/ci_runtime_smoke.py --login-only
