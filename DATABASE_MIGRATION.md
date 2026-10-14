# Database migration: SQLite to Supabase PostgreSQL

Status: **planned, not implemented**  
Prepared: 13 October 2026  
Source reviewed: `322d1c766c8c15f63cb1cef4fea98b11e8a730cf`

## Purpose and target architecture

Host the existing Go application and compiled dashboard on Render, with persistent application data in Supabase PostgreSQL. Render restarts and deployments must not erase accounts, catalogue records, sales, stock movements, device registrations, or retry history.

The browser continues to call the Go API over HTTPS. The Go API connects privately using server-side database credentials over TLS to Supabase PostgreSQL. The Rust shop device eventually submits authenticated captures to the Go API.

This plan uses Supabase as the database. Existing owner authentication and session handling remain in the Go backend; adopting Supabase Auth is a separate decision. Hosting does not implement the currently mocked microphone/STT loop or establish real-shop accuracy.

A Render persistent disk is unnecessary for the application database after this migration. Other local files must still be reviewed. Render free-service sleep behavior is separate from database durability. Check current Supabase plan limits, inactivity behavior, backup availability, and Render pricing before choosing plans.

## Current implementation

- `backend/storage/storage.go` opens SQLite and runs embedded SQL migrations.
- `backend/go.mod` uses `github.com/mattn/go-sqlite3`.
- `backend/storage/migrations/0001_init.sql` through `0004_devices.sql` define catalogue, transactions, identity, stock movements, retry records, and devices. Inventory any additional schema setup in Go as well.
- Storage queries use SQLite placeholders and connection pragmas.
- `RecordMovement` and `ResolveReview` in `backend/storage/ledger.go` rely on SQLite immediate transaction locking.
- Existing backup tooling uses SQLite-specific operations such as `VACUUM INTO`.
- `backend/main.go` accepts a SQLite file through `-db`; a PostgreSQL URL is not supported yet.
- Existing CI covers Go checks/race tests, frontend typecheck/build, and Rust tests. It does not prove PostgreSQL compatibility.

Changing a connection string alone will not migrate this application.

## Scope and delivery rules

- [x] Implement PostgreSQL storage support and schema migrations.
- [x] Preserve current API contracts, ownership checks, and ledger invariants.
- [ ] Test migration and concurrency against a real PostgreSQL test instance.
- [ ] Configure a dedicated Supabase project/schema and restricted application role.
- [ ] Rehearse transfer of any existing SQLite data.
- [ ] Add Render deployment configuration and secret references.
- [ ] Perform a controlled cutover and verify persistence, restore, and reconciliation.

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

- [ ] Fresh migration, repeated migration, constraints, and invalid-input tests.
- [ ] Owner login/recovery/session persistence and cross-shop isolation.
- [ ] Device pairing, revocation, and unauthorized capture rejection.
- [ ] Sale: 20 units → sell 2 → balance 18 with exactly one ledger entry.
- [ ] Restock packaging conversion and purchase-cost/revenue separation.
- [ ] Concurrent retries apply one stock movement; conflicting payloads fail.
- [ ] Concurrent purchases cannot oversell; concurrent review actions apply once.
- [ ] Failed writes and failed later receipt lines leave no unintended partial effect.
- [ ] Restart/reconnect replay, including changed catalogue/settings between attempts.
- [ ] Summary accuracy beyond 500 records and at shop-local day boundaries.
- [ ] SQLite-to-PostgreSQL fixture import and reconciliation.
- [ ] Backup restoration into a clean target with verified application access.

Retain Go formatting, vet, race tests, builds, frontend checks, and Rust tests. A green build is not evidence of actual microphone recognition or a real-shop pilot.

## 7. Render deployment and cutover

- Build the TypeScript dashboard and Go binary using pinned compatible toolchains.
- Configure the process to bind to Render's PORT and serve the intended frontend path.
- Inject DATABASE_URL only on the server.
- Run without `-dev`; do not expose destructive sample-data endpoints.
- Use HTTPS, secure cookies, and a same-origin dashboard/API/WebSocket setup.
- Apply verified schema migrations before accepting application traffic.
- Deploy to a test environment first; verify login, product creation, sale/restock, reviews, summaries, export, and WebSocket updates.
- Restart/redeploy and confirm that all persistent data remains in Supabase.
- Document any free-plan sleep/inactivity limitations; persistent data does not imply continuous availability.

No Render or Supabase resource is created by this documentation change.

## 8. Backup and rollback

Replace SQLite-only backup verification with a PostgreSQL-compatible procedure. Check backup/PITR availability for the selected Supabase plan; do not assume paid features are included.

Test restoring a logical backup or supported provider backup into an isolated target and reconcile it. Protect backups because they contain sensitive business and authentication data.

Before target writes begin, rollback can return to the untouched SQLite source. After PostgreSQL accepts new writes, switching back to the old SQLite copy would lose those writes. Pause writes, preserve the target, and execute a rehearsed reverse-transfer/reconciliation procedure or recover forward. Never run both databases as independent writable sources.

## Acceptance and evidence

Migration is complete only when:

- [ ] Required PostgreSQL CI checks pass on the merged implementation.
- [ ] Render connects securely to the intended Supabase database.
- [ ] Shop isolation, stock consistency, concurrency, and retries pass.
- [ ] Existing-data reconciliation passes, or absence of existing data is recorded.
- [ ] Restart/redeploy persistence and restore are demonstrated.
- [ ] The owner can use the hosted dashboard through normal application flows.
- [ ] README/TODO reflect verified behavior and remaining voice/pilot gaps.

Validation record for this change: documentation only. No database schema, application code, secrets, live data, or hosting resources have been changed. Add implementation commit IDs, CI run links, test environment details, and reconciliation results as work is completed.

## References

Verify current provider instructions again at implementation time:

- [Supabase PostgreSQL connections](https://supabase.com/docs/guides/database/connecting-to-postgres)
- [Supabase API security](https://supabase.com/docs/guides/api/securing-your-api)
- [Supabase database backups](https://supabase.com/docs/guides/platform/backups)
- [Render persistent disks](https://render.com/docs/disks)
- [Render free-service limitations](https://render.com/docs/free)
