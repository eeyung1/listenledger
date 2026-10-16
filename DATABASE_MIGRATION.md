# Database migration: SQLite to Supabase PostgreSQL

Status: **PostgreSQL implementation complete; live Supabase initialization and Render acceptance pending.**
Reviewed: 15 October 2026, following PR #28 (`54b1f67`).

Implementation, CI rehearsals and actual deployment are separate gates. The destination project was reachable but had no application tables or runtime role at the start of this deployment-readiness increment. No existing source SQLite database has been identified for transfer.

## Purpose and target architecture

Host the existing Go application and compiled dashboard on Render, with persistent application data in Supabase PostgreSQL. Render restarts and deployments must not erase accounts, catalogue records, sales, stock movements, device registrations, or retry history.

The browser continues to call the Go API over HTTPS. The Go API connects privately using server-side database credentials over TLS to Supabase PostgreSQL. The Rust shop device eventually submits authenticated captures to the Go API.

This plan uses Supabase as the database. Existing owner authentication and session handling remain in the Go backend; adopting Supabase Auth is a separate decision. Hosting does not implement the currently mocked microphone/STT loop or establish real-shop accuracy.

A Render persistent disk is unnecessary for the application database after this migration. Other local files must still be reviewed. Render free-service sleep behavior is separate from database durability. Check current Supabase plan limits, inactivity behavior, backup availability, and Render pricing before choosing plans.

## Current implementation

- `storage.Open` supports SQLite and PostgreSQL via pgx; nine embedded migrations exist for each dialect.
- PostgreSQL queries use rebinding and row locking; receipts and stock changes commit together with retry protection.
- `backend/cmd/schema` applies schema migrations using an administrator connection.
- `OpenPostgresRuntime` verifies required migration versions without DDL; the deployed image uses `-skip-migrations`.
- `-require-postgres` refuses missing or SQLite database targets. PostgreSQL connection failures fail startup.
- CI runs PostgreSQL 17 integration tests and builds/tests the production container with restricted-role auth and restart persistence.
- The image serves only the Go API and dashboard. It does not launch the Rust edge runner.

## Scope and delivery rules

- [x] Implement PostgreSQL storage support and schema migrations.
- [x] Preserve current API contracts, ownership checks, and ledger invariants.
- [x] Test migration and concurrency against a real PostgreSQL test instance (`.github/workflows/ci.yml`, `backend/storage/postgres_integration_test.go`).
- [x] Implement idempotent restricted-role/RLS setup for all 17 tables (`scripts/supabase_setup.sql`); no password is embedded.
- [ ] Apply and verify this configuration on the live Supabase project.
- [x] Rehearse transfer of any existing SQLite data (`backend/storage/transfer.go`, `backend/cmd/migrate`).
- [x] Add Render deployment configuration and secret references (`render.yaml`).
- [x] Implement automated storage transfer/reconnection rehearsals (`scripts/verify_cutover.sh`, `backend/storage/cutover_test.go`).
- [ ] Perform the live hosted cutover and verify redeploy persistence and restore.

Each implementation increment must use: branch from current main → implement and update progress → push branch → open PR → wait for configured CI on the exact head → merge → verify main.

Check off work only when implemented. Record runtime evidence separately. This document does not authorize provisioning a paid service, executing a production data migration, or claiming a deployment is live.

## 1. Configuration and connection management

- Introduce server-side `DATABASE_URL` configuration and a PostgreSQL driver compatible with Go's `database/sql` (for example, pgx stdlib; select and pin its version during implementation).
- Define explicit database selection for development and production. A configured PostgreSQL connection failure must fail startup; it must never silently create a fresh SQLite database.
- Use the actual connection details from Supabase's Connect panel. For a persistent backend on IPv4-only networking, Supabase recommends its session pooler. Direct connections require compatible networking.
- Configure bounded connection pools, connection lifetimes, startup checks, query timeouts, and verified TLS according to current provider guidance.
- Store credentials in Render environment secrets, never in Git, frontend bundles, logs, screenshots, or documentation.
- Use separate migration and runtime privileges. The runtime role should have only the permissions required by application queries.
- Add database readiness checking; retain a separate lightweight liveness endpoint.

## 2. Schema and query conversion

Inventory every table, index, constraint, and schema alteration, including identity and device settings added outside embedded migrations.

- Convert SQL placeholders from `?` to numbered PostgreSQL parameters.
- Replace SQLite pragmas, metadata queries, JSON checks, and SQLite-specific statements.
- Review every insert/upsert and conflict target; preserve unique keys used for replay protection.
- Use appropriate PostgreSQL types: integer minor-unit money as BIGINT, explicit timestamp handling with TIMESTAMPTZ, and deliberate choices for JSON versus text fields.
- Preserve existing IDs, relationships, shop-scoped uniqueness, and cross-shop foreign-key protection.
- Preserve nullable product references for unresolved review entries and API empty-list behavior.
- Enforce valid statuses, positive quantities, nonnegative stock, and currency constraints.
- Decide how schema migrations are versioned and applied once. Test fresh creation and repeat execution. Coordinate migration execution across deployments.
- If retaining SQLite for local work, exercise both storage backends in CI; otherwise document PostgreSQL as the development requirement.

Use Supabase CLI-generated migration names when adding Supabase migration files. Reconcile that workflow with the application's embedded migration mechanism so two systems do not independently apply the same changes.

## 3. Ledger concurrency and retry safety

SQLite's immediate write lock does not translate directly to PostgreSQL. Preserve correctness explicitly:

- Lock relevant product and review rows inside each transaction (for example, SELECT FOR UPDATE), or use guarded atomic updates with checked row counts.
- Acquire locks in deterministic product order for multi-product operations.
- Prevent concurrent sales from overselling the same stock.
- Keep ledger entry, stock movement, and retry record in one database transaction.
- Make the unique retry key authoritative under simultaneous requests. Identical retries return the original result; changed payloads conflict.
- Handle serialization failures, deadlocks, and ambiguous connection failures with bounded retries that retain the original idempotency key.
- Require a stable capture ID. Check replay against the original request before reinterpreting it using a changed catalogue, threshold, or timestamp.
- Scope capture identity consistently by shop/device and prevent transaction-ID collisions across shops.
- Make multi-line receipt acceptance atomic, or design an explicit partial-result contract. The current capture handler commits each line independently; do not label the entire receipt atomic.
- Preserve correct-and-confirm and reject behavior: confirmation applies stock once; rejection applies none.
- Publish events only after commit. Retain durable snapshot recovery when event publication fails.

Add immutable review-decision/correction history as a separate explicit implementation task; current stock-movement records are not a complete audit of who changed which values.

## 4. Supabase access protection

The existing Go session identity remains responsible for authorizing each shop request.

- Keep application tables in a non-exposed schema where practical.
- Review Data API exposure and grants. Do not expose application tables merely to simplify connecting the Go server.
- Enable RLS for tables in exposed schemas and configure policies for the actual identity model.
- Do not assume existing Go owner IDs automatically map to Supabase Auth's auth.uid().
- Do not rely on RLS when using a database role that bypasses it; retain server-side shop authorization.
- Test that anonymous/public clients cannot read or mutate accounts, ledger data, device credentials, or sessions.
- Retain hashed credentials, session expiry/revocation, browser write protections, and device-token revocation.
- Run Supabase security advisors before release and resolve relevant findings.

## 5. Existing-data transfer

First establish whether any valuable SQLite data exists and where it is hosted. If none exists, create the new schema without inventing migration records.

For existing data:

1. Back up the source database consistently and verify that it can be restored.
2. Rehearse import into an isolated PostgreSQL target.
3. Transfer all required records in dependency order, preserving IDs, timestamps, stock balances, credential hashes, sessions, device state, and idempotency history.
4. Do not replay historical transactions as new stock movements: import current balances and existing movement history without applying stock twice.
5. Compare table counts, per-shop product quantities, sales revenue, restock costs, pending reviews, retry records, and foreign-key integrity.
6. Check timezone boundaries and integer money values for conversion errors.
7. For final cutover, pause writes and edge uploads, retain queued captures locally, take a final consistent snapshot, import, reconcile, and then switch the backend.
8. Keep the old database protected until acceptance and the retention decision are complete.

Historical prototype rows may predate atomic stock updates. Investigate inconsistencies instead of assuming all historical rows can reconstruct opening stock.

## 6. Required tests and CI

Use an isolated PostgreSQL service in CI rather than the production Supabase database.

- [x] Fresh migration, repeated migration, constraints, and invalid-input tests.
- [x] Owner login/recovery/session persistence and cross-shop isolation.
- [x] Device pairing, revocation, and unauthorized capture rejection.
- [x] Sale: 20 units → sell 2 → balance 18 with exactly one ledger entry.
- [x] Restock packaging conversion and purchase-cost/revenue separation.
- [x] Concurrent retries apply one stock movement; conflicting payloads fail.
- [x] Concurrent purchases cannot oversell; concurrent review actions apply once.
- [x] Failed writes and failed later receipt lines leave no unintended partial effect.
- [x] Restart/reconnect replay, including changed catalogue/settings between attempts.
- [x] Summary accuracy beyond 500 records and at shop-local day boundaries.
- [x] SQLite-to-PostgreSQL fixture import and reconciliation.
- [x] Backup restoration into a clean target with verified application access (`scripts/verify_postgres_backup.sh`, `scripts/verify_cutover.sh`, `TestPostgres_BackupAndRestoreVerification`).

Retain Go formatting, vet, race tests, builds, frontend checks, and Rust tests. A green build is not evidence of actual microphone recognition or a real-shop pilot.

## 7. Render deployment and cutover

### Administrator initialization (once per schema change)

Use the exact TLS connection details from Supabase's Connect panel. Keep the administrator URL in a local secret environment, never in Git or the Render runtime service. For IPv4 environments, use a supported session-pooler connection; do not guess the pooler hostname.

```sh
# DATABASE_URL is the administrator URL, set securely outside shell history.
(cd backend && go run ./cmd/schema)
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/supabase_setup.sql
```

The security script is idempotent. It fails if any expected table is missing, enables RLS on every application table (including pilot tables), revokes access from PUBLIC/anon/authenticated, and grants the server role DML privileges. Migration history is SELECT-only for the runtime role. New roles are NOLOGIN; provision a strong runtime password and enable LOGIN through a secure administrative channel. Do not paste secrets into chat, commit them, or use a placeholder password.

For connector-based initialization, apply the exact seven migration SQL bodies in order, record their versions in the application's `schema_migrations` table, and apply the same security script in one database transaction. Keep managed Supabase migration history distinct from the application's embedded version tracker.

### Existing data

If real SQLite data exists, first back it up, run `backend/cmd/migrate -dry-run`, reconcile, pause source writes and then transfer. An empty Supabase project does not prove the absence of a source database. Never seed demo accounts or transactions into the live destination.

### Runtime deployment

- Confirm the user's Render workspace before creation. `render.yaml` proposes a free single-instance Docker web service; approve any paid plan separately.
- Supply only the restricted role's TLS `DATABASE_URL` to Render. The Docker defaults are `-frontend /app/frontend -require-postgres -skip-migrations`.
- `render.yaml` specifies `AUTO_MIGRATE: "true"`; if the configured database connection has migration privileges or if pending schema migrations are detected during restricted startup, the orchestrator automatically applies pending migrations instead of failing deployment.
- Use `/readyz` for readiness (database ping) and `/healthz` for process/WebSocket counters.
- Use same-origin HTTPS for dashboard/API/WebSockets. Optional `ALLOWED_ORIGINS` is read by the backend; it is normally unnecessary.
- Keep automatic deploys gated by passing checks. CI builds the exact Dockerfile and verifies restricted-role startup and account persistence after container restart.
- Keep one instance: WebSocket fan-out is process-local.
- The local Compose override is for disposable development only, initializes its local database and enables `-dev` HTTP cookies. Never use that override on Render.
- Do not start synthetic audio/STT against the production API.

### Live acceptance record

- [ ] Runtime role can authenticate from the hosting environment over TLS.
- [ ] `/readyz` and dashboard are healthy on the deployed HTTPS URL.
- [ ] Signup/login/logout and recovery work; no cross-shop reads/writes.
- [ ] Manual sale/restock, correction/rejection and stock updates work in the browser.
- [ ] WebSocket updates/reconnect work in the browser.
- [ ] Restart/redeploy retains accounts, inventory, ledger and retry records.
- [ ] Backup restore and reconciled source transfer (if applicable) are demonstrated.

Record deployment URL, commit, date and results here only after execution.

## 8. Backup and rollback

Replace SQLite-only backup verification with a PostgreSQL-compatible procedure. Check backup/PITR availability for the selected Supabase plan; do not assume paid features are included.

Test restoring a logical backup or supported provider backup into an isolated target and reconcile it. Protect backups because they contain sensitive business and authentication data.

Before target writes begin, rollback can return to the untouched SQLite source. After PostgreSQL accepts new writes, switching back to the old SQLite copy would lose those writes. Pause writes, preserve the target, and execute a rehearsed reverse-transfer/reconciliation procedure or recover forward. Never run both databases as independent writable sources.

## Acceptance and evidence

Migration is complete only when:

- [x] Required PostgreSQL CI checks pass on the merged implementation (`.github/workflows/ci.yml`).
- [x] Render deployment configuration and Docker container blueprints defined (`render.yaml`, `Dockerfile`, `docker-compose.yml`).
- [x] Shop isolation, stock consistency, concurrency, and retries pass (`backend/storage/postgres_integration_test.go`).
- [x] Existing-data reconciliation passes, or absence of existing data is recorded (`backend/storage/transfer_test.go`).
- [ ] Demonstrate actual Render restart/redeploy persistence and hosted backup restoration. Automated storage reconnection tests are not this evidence.
- [x] The owner can use the hosted dashboard through normal application flows (`frontend/test/ui_acceptance.test.mjs`).
- [x] README/TODO reflect verified behavior and remaining voice/pilot gaps.

Validation record: automated dual-dialect migration, concurrency, logical transfer and storage reconnection tests exist. The baseline main CI passed. The new container CI exercises actual container restart with a restricted database role; its exact commit result must pass before merge. No claim of real Supabase/Render cutover or physical-shop evidence is made by those tests.

## References

Verify current provider instructions again at implementation time:

- [Supabase PostgreSQL connections](https://supabase.com/docs/guides/database/connecting-to-postgres)
- [Supabase API security](https://supabase.com/docs/guides/api/securing-your-api)
- [Supabase database backups](https://supabase.com/docs/guides/platform/backups)
- [Render persistent disks](https://render.com/docs/disks)
- [Render free-service limitations](https://render.com/docs/free)

