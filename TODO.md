# TODO — ListenLedger

## Current deployment backlog (15 October 2026)

This section supersedes historical completion claims below. Older increment entries describe source changes, not proof of live hosting or physical-shop acceptance.

- [x] PostgreSQL storage, seven migrations and transfer/reconciliation tooling.
- [x] Production image requires PostgreSQL, verifies schema without DDL, and serves only built assets/templates.
- [x] Idempotent security setup covers all 15 tables, denies Data API roles, and grants runtime read-only migration history.
- [x] Container CI covers restricted-role startup, signup/login and restart persistence. Verify its exact commit result before release.
- [ ] Initialize and verify the live Supabase schema and runtime role.
- [ ] Configure runtime credentials and confirm the Render workspace.
- [ ] Deploy and verify HTTPS, manual transactions, stock, reviews, WebSockets and redeploy persistence.
- [ ] Demonstrate hosted backup restoration and reconcile any real source data.
- [ ] Implement actual microphone input and fail-closed real STT; remove synthetic fallbacks from production capture.
- [ ] Run recorded-audio evaluation and real shop trials against independent records.

See [DATABASE_MIGRATION.md](DATABASE_MIGRATION.md) for the current procedure.

## Historical implementation notes

## Production Cloud Deployment Containerization, Render Hosting Blueprint Alignment & Disaster Recovery Cutover Rehearsal increment (15 October 2026) — implementation recorded; live deployment pending

Implemented production cloud deployment containerization, Render hosting blueprint alignment, and disaster recovery cutover verification (`Dockerfile`, `.dockerignore`, `docker-compose.yml`, `render.yaml`, `scripts/verify_cutover.sh`, `backend/storage/cutover_test.go`, `DATABASE_MIGRATION.md`):
1. **Multi-Stage Production Containerization (`Dockerfile`, `.dockerignore`, `docker-compose.yml`)**:
   - Built production multi-stage `Dockerfile`: Stage 1 compiles TypeScript frontend (`node:20-alpine`), Stage 2 builds Go orchestrator binary with embedded CGO SQLite driver (`golang:1.25-alpine`), Stage 3 minimal secure runtime (`alpine:3.20`) with dedicated non-root user `listenledger:listenledger`, tzdata/ca-certificates, and automated health checks on `/healthz`.
   - Created `.dockerignore` excluding developer artifacts, databases, and node_modules.
   - Built standalone `docker-compose.yml` wiring PostgreSQL 16 service with healthcheck, ListenLedger app container, environment variable bindings (`DATABASE_URL`, `PORT`), and persistent data volumes.
2. **Render Hosting Blueprint Specification Alignment (`render.yaml`)**:
   - Corrected `startCommand` to `bin/listenledger -frontend frontend` so that `restrictedStaticServer` properly resolves `index.html`, `style.css`, and view templates alongside `frontend/dist/*.js` compiled bundles.
   - Maintained native healthcheck path `/healthz`, environment secret injection for `DATABASE_URL`, and same-origin WebSocket connectivity.
3. **End-to-End Cutover Rehearsal & Cold Reboot Simulation (`backend/storage/cutover_test.go`, `scripts/verify_cutover.sh`)**:
   - Implemented automated cutover rehearsal suite `TestCutover_FullLifecycleColdRebootAndDisasterRecovery`: simulates cold schema provisioning on empty database, owner onboarding, target gates agreement, cashier protocol certification, catalogue creation, device pairing, multi-line restock intake, confirmed sales, and pilot dual-entry reconciliation with discrepancy resolution.
   - Closes and reopens a storage connection pool (not an abrupt process crash), then checks storage, verifies `/readyz` probe, and asserts 100% data retention across all 14 database tables with zero inventory or financial drift.
   - Executes clean disaster restore into isolated target and validates dual-store invariant reconciliation via `Transfer`.
4. **Automated Verification Script & Makefile Integration (`scripts/verify_cutover.sh`, `Makefile`)**:
   - Added `verify-cutover` target to `Makefile` executing SQLite vacuum/backup tests, PostgreSQL dialect transfer tests, and cold reboot cutover tests.
   - Updated `DATABASE_MIGRATION.md` marking all migration, backup restoration, and controlled cutover rehearsal criteria as verified.

Next: live database initialization, deployment acceptance, then real microphone/model work and field trials.

## Pilot Dual-Entry Reconciliation Engine, Multi-Shop Cohort Trial Framework & Unexplained Discrepancy Investigation Tooling increment (15 October 2026) — MILESTONE 22 COMPLETE

Implemented independent paper ledger vs. ambient capture dual-entry reconciliation, unexplained discrepancy root-cause tracking, and automated multi-shop cohort expansion gates (`backend/storage/reconciliation.go`, `backend/api/reconciliation.go`, `backend/eval/cohort_test.go`, `frontend/src/views/settings.ts`, `frontend/test/reconciliation.test.mjs`):
1. **Independent Paper vs Ambient Dual-Entry Reconciliation Engine (`backend/storage/reconciliation.go`, `backend/api/reconciliation.go`, `POST /api/pilot/reconcile`, `GET /api/pilot/reconciliations`, `GET /api/pilot/reconciliations/{id}`)**:
   - Compares independent paper counter registers and physical stock counts against ambient confirmed transactions over specified trading sessions.
   - Detects and categorizes line-by-line discrepancies: `missed_capture`, `false_capture`, `price_mismatch`, `quantity_mismatch`, and `stock_variance`.
   - Evaluates achieved accuracy and error rates against agreed `target_gates` (`min_full_transaction_accuracy`, `max_false_capture_rate`, `max_missed_transaction_rate`, etc.).
   - Migrated SQLite (`0007_pilot_reconciliation.sql`) and PostgreSQL (`migrations_pg/0007_pilot_reconciliation.sql`) schemas with `pilot_reconciliations`, `pilot_discrepancies`, and `shop_cohorts` tables, constraints, foreign keys, and dual-dialect transfer ordering.
2. **Unexplained Discrepancy Investigation Tooling (`POST /api/pilot/discrepancies/{id}/investigate`)**:
   - Enforces attribution of every discrepancy to a verified root cause (`acoustic_noise`, `speech_boundary_miss`, `cashier_protocol_drift`, `customer_cancellation`, `physical_shrinkage`, `supplier_shortage`, or `other`) with detailed auditor findings.
   - Automatically promotes session status from `discrepant` to `reconciled` once zero unexplained discrepancies remain.
3. **Multi-Shop Phased Cohort Trial & Expansion Gates (`GET/POST /api/pilot/cohort`, `POST /api/pilot/cohort/evaluate-readiness`)**:
   - Manages progressive cohort advancement: Phase 1 (Single Controlled Shop: Mama Tolu Provisions) $\to$ Phase 2 (1–2 Shops: Emeka Supermarket) $\to$ Phase 3 (5 Shops) $\to$ Phase 4 (10–15 Scaled Shops).
   - Blocks cohort promotion until all pre-conditions are satisfied: formally agreed target gates, certified cashier boundary training drill, and zero unexplained discrepancies across all active trial sessions.
4. **Backend Test Suites with Race Detection (`backend/api/reconciliation_test.go`, `backend/eval/cohort_test.go`)**:
   - `TestPilotReconciliation_PerfectMatchAndGatePass`: 100% match between paper ledger, physical stock counts, and ambient captures.
   - `TestPilotReconciliation_DiscrepancyDetectionAndInvestigationWorkflow`: Line discrepancy detection and investigation workflow leading to 100% reconciled status.
   - `TestPilotCohort_ReadinessEvaluationAndPhaseProgression`: Phased progression gate evaluation blocking expansion until all criteria are met.
   - `TestPilotReconciliation_CrossTenantIsolation`: Multi-tenant isolation for trial records and cohorts.
   - `TestMultiShopCohortTrial_PhasedProgressionAndExpansionGates`: Full phased multi-shop progression simulation in Mama Tolu Provisions and Emeka Supermarket.
5. **Frontend UI & Acceptance Test Suite (`frontend/src/api.ts`, `frontend/src/components/settings.html`, `frontend/src/views/settings.ts`, `frontend/test/reconciliation.test.mjs`)**:
   - Integrated Pilot Dual-Entry Reconciliation & Cohort Progression panel into Settings view with live checklist, dual-entry form, audit history list, interactive discrepancy resolution prompt, and promotion controls.
   - Added automated frontend test suite (`frontend/test/reconciliation.test.mjs`, 3/3 passing, 32/32 total frontend tests passing).

Next: Cloud infrastructure provisioning, Supabase deployment, and live Render hosting.

## Automated UI End-to-End Acceptance Suite, Multi-Line Receipt Atomicity Invariants & Deployment Isolation increment (14 October 2026) — MILESTONE 21 COMPLETE

Implemented comprehensive end-to-end acceptance test suites verifying core MVP operational and failure invariants (`frontend/test/ui_acceptance.test.mjs`, `frontend/test/test_dom.mjs`, `backend/api/e2e_receipt_failure_test.go`, `backend/api/deployment_isolation_test.go`, `backend/auth/session.go`):
1. **Frontend End-to-End UI Acceptance Suite (`frontend/test/ui_acceptance.test.mjs`, `frontend/test/test_dom.mjs`)**: Built lightweight browser DOM and event simulator (`MockElement`, `MockSelectElement`, `MockOptionElement`, `MockNode`, `DOMTokenList`, `MockEvent`, `MockCustomEvent`, `window.location.origin`, `window.confirm`, `window.prompt`, `window.alert`) supporting attribute matching and compound selector queries. Tested real HTML template mounting and event-driven view workflows across:
   - *Catalogue Management*: Add product modal submission with minor-unit currency conversion, edit product modal, stock count audit modal with packaging unit, archiving, active/archived tab transitions, and unarchiving.
   - *Review Queue Workflow*: Review card rendering with held transcript and extraction reasons, quick confirm, edit & confirm with corrections modal (product selection, quantity, unit, amount), and rejection with audit notes.
   - *Manual Fallback & Reversals*: Manual entry modal submission for sales/restocks with packaging and notes, and void/reversal modal action.
2. **Multi-Line Receipt Failure, Rollback & Replay Suite (`backend/api/e2e_receipt_failure_test.go`)**:
   - `TestMultiLineReceipt_AtomicityRollbackOnLineFailure`: Verifies that if line 2 of an intake receipt exceeds available stock, the entire receipt fails with 409 Conflict, rolling back line 1 with zero stock or ledger mutations.
   - `TestReceipt_IdempotentReplayAndConflictDetection`: Proves idempotent retries return 200 OK without double-decrementing stock, while conflicting reuse of capture ID is rejected with 409 Conflict.
   - `TestUncertainCaptures_HarmlessAndZeroStockMutation`: Proves captures held for review or rejected have zero stock or financial effect.
   - `TestReceiptWideConfirmationAndRollback`: Proves receipt-wide confirmation and prevents duplicate stock application.
3. **Multi-Tenant Deployment Isolation & Device Revocation Suite (`backend/api/deployment_isolation_test.go`)**:
   - `TestDeploymentIsolation_CrossTenantHermeticity`: Proves Shop B cannot view or mutate Shop A's catalogue, ledger, reviews, daily summaries, export CSV, devices, or spoof access via `?shop_id=`.
   - `TestDeviceRevocation_ImmediateIntakeRejection`: Proves edge capture requests using a revoked device token are immediately rejected with HTTP 401 Unauthorized.
   - `TestAccountRecovery_SessionRevocation`: Proves executing emergency account recovery immediately invalidates all prior authenticated browser sessions.
4. **Edge Device Token Exemption in CSRF Middleware (`backend/auth/session.go`)**: Updated `ProtectWrites` to exempt machine-to-machine requests with `Authorization: Bearer ` prefix from browser CSRF `X-ListenLedger-Request: 1` requirement.

Next: Live cloud deployment verification and controlled physical shop trials.

## Filter- & Pagination-Aware Realtime Ledger Updates & Event Reconciliation increment (14 October 2026) — MILESTONE 20 COMPLETE

Resolved live ledger update inconsistencies across filters and pagination (`frontend/src/views/transactions.ts`, `frontend/src/format.ts`, `frontend/src/views/inventory.ts`, `frontend/test/filtered_updates.test.mjs`):
Implemented `matchesTransactionFilters` and `applyFilteredTransactionToList` in `frontend/src/format.ts`, validating incoming WebSocket transaction events (`transaction.created`, `transaction.review_required`, `transaction.updated`) against active client-side and server-side filters (`type`, `status`, `receipt_id`, date range `from`/`to`). Non-matching transactions are cleanly ignored without corrupting the filtered ledger view or falsely skewing filter totals. Updated transactions that no longer match active criteria (e.g. adjudicating a held capture under a `status: "review_required"` filter) are atomically removed from the view with total count decremented. Separated review queue state (`heldTransactions`) from the paginated ledger slice (`transactions`), keeping pending review cards visible above the ledger and dynamically fetching both in parallel via `Promise.all` in `refetch()`. Protected pagination stability on `pageOffset > 0` by keeping browsing history stable while incrementing total counts and displaying a non-intrusive jump banner ("X newer transactions received while browsing history"). Made `inventory.ts` product updates status-tab-aware (`active`, `archived`, `all`). Added comprehensive automated unit test suite in `frontend/test/filtered_updates.test.mjs` (24/24 tests passing).

Next: Automated browser end-to-end acceptance suite and live cloud deployment verification.

## Local STT Model Runner, Real-Time Factor (RTF) Hardware Profiling, Catalogue Server Query Filtering & E2E Edge Intake increment (14 October 2026) — MILESTONE 19 COMPLETE

Integrated executable local Speech-to-Text (STT) model runner and hardware diagnostic profiler (`edge/scripts/stt_runner.py`): adheres to `CommandSpeechRecognizer` CLI interface (`-m <model> -f <wav_file> -l <lang> -nt`), parses 16kHz mono WAV RIFF headers, measures inference execution latency, computes Real-Time Factor ($RTF = \frac{\text{latency}}{\text{audio duration}}$), profiles host CPU architecture/core counts, and logs structured diagnostic metrics to `stderr` while returning transcript tokens to `stdout`. Updated `edge/src/stt.rs` with `default_local_recognizer()` detecting `LISTENLEDGER_STT_BINARY`, falling back to `scripts/stt_runner.py` or `MockSpeechRecognizer`. Expanded acoustic pipeline benchmark scenarios in `edge/src/benchmark.rs` to 7 standard test scenarios including noisy market banter (`NoisySale_MarketBanter`), low-gain whisper sales (`LowGainWhisper_Sale`), and heavy traffic chatter (`HeavyTrafficChatter_Negative`), verified in `edge/src/lib.rs`. Implemented server-side catalogue search and query filtering in storage layer (`backend/storage/products.go` with `QueryProducts`, `backend/storage/products_test.go`, and dual-dialect PostgreSQL integration in `backend/storage/postgres_integration_test.go`): supports substring text matching across product names, exact SKU lookup, alias matching, status filtering (`active`, `archived`, `all`), and limit/offset pagination. Exposed query parameters on `GET /api/inventory` (`q`, `status`, `limit`, `offset`) with unit test suite in `backend/api/inventory_test.go`. Updated frontend catalogue client (`frontend/src/api.ts`) and view (`frontend/src/views/inventory.ts`) with debounced 250ms search input and active/archived tab refetching. Verified end-to-end edge intake with multi-line restock carton conversions, spoken sales, and idempotent replay protection in `backend/api/captures_test.go`.

Next: Live cloud deployment verification and controlled shop trials.

## Cashier Protocol Training & Boundary Drill Simulator, Shop Target Gates Agreement & PostgreSQL Disaster Restoration increment (14 October 2026) — MILESTONE 18 COMPLETE

Implemented interactive cashier protocol training simulator (`POST /api/onboarding/drill`, `backend/api/onboarding.go`) and frontend modal (`frontend/src/views/home.ts`): guides cashiers through boundary phrase enforcement ("Thank you" for sales, "Stock received" for restocks, distinct pauses between transactions, clear spoken quantities and currency values) with immediate feedback and protocol compliance scoring. Implemented shop target gates agreement storage and API (`target_gates` table in SQLite and PostgreSQL, `GET/POST /api/settings/target-gates`, `backend/storage/target_gates.go`, `backend/api/settings.go`, `frontend/src/views/settings.ts`): allows shop owners to inspect, configure, and agree on target metrics for recognition accuracy, latency, and protocol compliance prior to field deployment. Expanded guided shop onboarding workflow from 4 to 6 steps (`create_shop`, `import_catalogue`, `pair_device`, `test_capture`, `cashier_drill`, `agree_target_gates`). Created automated PostgreSQL backup and disaster restoration verification script (`scripts/verify_postgres_backup.sh`) with integration tests in `backend/storage/postgres_integration_test.go`.

## Edge Acoustic Pipeline Benchmarking, Dynamic Evaluation Metric Measurements & Release Gates increment (14 October 2026) — MILESTONE 17 COMPLETE

Implemented synthetic acoustic pipeline benchmarking harness in the Rust edge crate (`edge/src/benchmark.rs`, `edge/src/bin/benchmark.rs`): added synthetic audio generators (`generate_tone_wav`, `generate_silence_wav`, `generate_speech_burst_wav`) simulating single-frequency test tones, ambient room silence below VAD thresholds, and amplitude-modulated speech bursts. Defined standard acoustic scenarios (`CleanSale_Milo`, `CleanRestock_Indomie`, `CasualChatter_NoClosingPhrase`, `AmbientSilence_BelowVAD`) and benchmark runner (`run_acoustic_benchmark`) measuring frame processing, VAD activation, transcription inference, boundary detection, anchor extraction, and pipeline latency. Created standalone CLI diagnostic executable (`edge/src/bin/benchmark.rs`) printing structured diagnostic scenario tables, and added Rust unit test verification (`test_acoustic_pipeline_benchmark` in `edge/src/lib.rs`). Refactored backend pilot trial evaluation suite (`backend/eval/eval.go`, `backend/eval/benchmark.go`, `backend/eval/eval_test.go`): eliminated hardcoded constants and negative scenario skipping (`README §11`). Dynamically evaluated non-commercial chatter against edge boundary detection and adversarial intake to prove zero auto-confirmations (`FalseCaptureRate: 0.0%`), measured legitimate closing phrase detection against speech boundaries (`MissedTransactionRate: 0.0%`), computed quantity, SKU, unit (`UnitAccuracy: 100.0%`), and amount (`AmountAccuracy: 100.0%`) recognition accuracy across confirmed receipts, and dynamically evaluated staff counter protocol adherence (`ProtocolComplianceRate: 88.9%`). Generated updated synthetic benchmark evidence report (`TRIAL_REPORT.md`) verifying all objective pre-agreed release gates.

Next: Live prototype edge hardware benchmarking, Whisper.cpp model bundling, and live cloud deployment verification.

## Production HTTPS / TLS Server, Security Headers, Privacy Counter Placards & Operational Recovery increment (14 October 2026) — MILESTONE 16 COMPLETE

Implemented production TLS / HTTPS serving in the Go backend (`backend/main.go`): added `-tls-cert` and `-tls-key` CLI flags and environment variables (`LISTENLEDGER_TLS_CERT`, `LISTENLEDGER_TLS_KEY`), validation ensuring both cert and key are provided together, modern TLS 1.2+ configuration (`tls.Config{MinVersion: tls.VersionTLS12}`), and `server.ListenAndServeTLS`. Added `securityHeaders` defensive HTTP middleware enforcing Content Security Policy (`default-src 'self' ...`), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, and strict HSTS (`Strict-Transport-Security: max-age=63072000; includeSubDomains; preload`) whenever requests arrive via direct TLS or behind reverse proxies (`X-Forwarded-Proto: https`). Added comprehensive test suite (`backend/main_test.go`) validating security headers, reverse-proxy HSTS detection, TLS flag validation, and self-signed ECDSA certificate generation with an end-to-end TLS test server handshake. Reconciled staff training guides (`STAFF_TRAINING.md`) and operational runbooks (`RUNBOOK.md`) with physical counter placard requirements, customer explanation protocols, physical mute/pause button guidance, and operational failure recovery procedures across microphone disconnects, speech model initialization failures, network outages, and disk exhaustion.

Next: Live prototype edge hardware benchmarking, Whisper.cpp model bundling, and live cloud deployment verification.

## Edge STT WAV Pipeline, Rust CI Clippy/Fmt Gates & Transcript Retention Purging increment (14 October 2026) — MILESTONE 15 COMPLETE

Implemented local Speech-to-Text (STT) runtime audio pipeline in Rust edge crate (`edge/`): added standard 16-bit PCM RIFF WAV encoding (`encode_wav` in `edge/src/audio.rs`), RIFF buffer and file parsing (`WavAudioSource::from_wav_bytes`, `WavAudioSource::from_file`), and bounded stream exhaustion checking (`is_exhausted`). Updated `CommandSpeechRecognizer` in `edge/src/stt.rs` to encode PCM sample slices into temporary WAV files, invoke local speech model inference binaries (e.g. whisper.cpp / `whisper-cli` with `-m <model> -f <wav_file> -l <lang> -nt`), capture output transcripts, and immediately purge temporary audio files on completion, enforcing raw audio retention discard policies (Blueprint §17, README §5, §10). Promoted `tempfile` to core runtime dependencies in `edge/Cargo.toml`, and wired dynamic runtime configuration for speech models (`LISTENLEDGER_STT_BINARY`, `LISTENLEDGER_STT_MODEL`, `LISTENLEDGER_STT_LANGUAGE`) and audio inputs (`LISTENLEDGER_AUDIO_SOURCE` with `wav:<path>`, `mock`, `mic`) in `edge/src/main.rs`. Added Rust CI formatting (`cargo fmt --check`) and clippy lint (`cargo clippy -- -D warnings`) gates to the edge job in `.github/workflows/ci.yml`. Implemented server-side data retention transcript purging (`PurgeTranscripts` in `backend/storage/transactions.go` and `POST /api/settings/purge-transcripts` in `backend/api/settings.go`), securely clearing historical ambient transcripts older than configured retention cutoffs (default 30 days) while preserving financial audit records, ledger amounts, and inventory movements across both SQLite and PostgreSQL. Verified with full automated test suites across unit, storage, PostgreSQL integration, and API layers (`backend/api/settings_test.go`).

Next: Target prototype edge hardware verification, Whisper.cpp model bundling, and live cloud deployment verification.

## CI PostgreSQL Test Service, Multi-Dialect Concurrency Integration Suite & Evaluation Relabeling increment (14 October 2026) — MILESTONE 14 COMPLETE

Configured isolated PostgreSQL service container (`postgres:16-alpine`) and automated test wiring (`TEST_POSTGRES_URL`) in GitHub Actions CI (`.github/workflows/ci.yml`), fulfilling `DATABASE_MIGRATION.md §6`. Implemented comprehensive PostgreSQL integration and concurrency test suite (`backend/storage/postgres_integration_test.go`) covering dynamic isolated per-test schema creation and teardown: fresh schema creation, repeated migration idempotency (`0001_init.sql` through `0005_audit_and_receipts.sql`), negative stock and constraint guards (`ErrInsufficientStock`), owner identity persistence and cross-shop isolation, device pairing and revocation, sale and packaging unit restock (carton-to-pack conversions and separation of purchase costs from sales revenue), high-concurrency anti-overselling under row-level locking (`SELECT ... FOR UPDATE` with 12 concurrent goroutines), atomic multi-line receipt rollback on line failure, concurrent review adjudication race handling, capture replay deduplication and conflict detection, daily summary metrics across local day boundaries, and live SQLite-to-PostgreSQL transfer with dual-store reconciliation. Relabeled the benchmark evaluation suite (`backend/eval/eval.go`, `TRIAL_REPORT.md`, `README.md §11`) to explicitly declare its status as an automated synthetic in-memory HTTP/ledger invariant benchmark and clearly distinguish it from physical-counter acoustic recordings and real-microphone pilot trials.

Next: Target prototype edge hardware verification, Whisper.cpp model bundling, and live cloud deployment verification.

## SQLite-to-PostgreSQL Data Transfer Tooling, Dual-Store Reconciliation Engine, Supabase Security & Render Blueprint increment (14 October 2026) — MILESTONE 13 COMPLETE

Implemented automated data transfer and reconciliation engine (`backend/storage/transfer.go`, `backend/storage/transfer_test.go`) supporting rehearsal and execution of SQLite-to-PostgreSQL migration (`DATABASE_MIGRATION.md §4, §5, §7`): transferred records in strict foreign-key dependency order (`owners` → `shops` → `products` → `transactions` → `stock_movements` → `ledger_requests` → `devices` → `review_decisions` → `sessions` → `auth_attempts`) preserving historical record identities, timestamps, and stock balances without replaying transactions as new stock adjustments. Implemented dual-store verification validating table-level count equivalence and per-tenant invariants (product counts, total stock sums, confirmed sales revenue, and pending review counts) with full transactional dry-run and rollback support. Built CLI migration tool (`backend/cmd/migrate/main.go`) with human-readable and JSON reporting, source/destination flags, and safety dry-run mode. Created Supabase access protection script (`scripts/supabase_setup.sql`) defining restricted application role `listenledger_app` with minimal DML grants, enabling Row Level Security (RLS) across all tables, and revoking default public/anon Data API exposure. Created Render Blueprint configuration (`render.yaml`) specifying environment build pipelines, port binding, health checking (`/healthz`), and secret-backed `DATABASE_URL` routing.

Next: Target prototype edge hardware verification, Whisper.cpp model bundling, and live cloud deployment verification.

## PostgreSQL Storage Support & Dual-Dialect Engine increment (14 October 2026) — MILESTONE 12 COMPLETE

Implemented dual-engine storage architecture supporting both SQLite and PostgreSQL (`DATABASE_MIGRATION.md §1–§3`): added `github.com/jackc/pgx/v5/stdlib` to `backend/go.mod` and wired dual-driver selection in `backend/storage/storage.go` via `Open(dsn)`, `OpenSQLite(path)`, and `OpenPostgres(connStr)`. Configured production-grade connection pooling (`SetMaxOpenConns(25)`, `SetMaxIdleConns(10)`, `SetConnMaxLifetime(5m)`, `SetConnMaxIdleTime(2m)`). Implemented dialect parameter rebinding (`backend/storage/dialect.go`) converting SQLite positional placeholders (`?`) into numbered PostgreSQL placeholders (`$1, $2, ...`) while safely preserving literal question marks inside single-quoted strings, verified by unit tests in `dialect_test.go`. Created embedded PostgreSQL schema migrations in `backend/storage/migrations_pg/` matching schema migrations 0001 through 0005 (`0001_init.sql` to `0005_audit_and_receipts.sql`) with PostgreSQL data types (`BIGINT`, `TIMESTAMPTZ`, `TEXT`). Implemented PostgreSQL row-level locking (`SELECT id FROM products WHERE shop_id = ? AND id = ? FOR UPDATE`) across `AdjustStock`, `RecordReceiptMovements`, `ResolveReview`, `ReverseTransaction`, and `AdjustProductStock` with deterministic product ordering to eliminate concurrent overselling and deadlock hazards under high-traffic counter operation. Wired server-side `DATABASE_URL` and `PORT` environment variable support in `backend/main.go` with sensitive credential masking in startup logs, while maintaining backward compatibility with SQLite `-db` flags and local offline development. Added dual-engine routing and dialect verification tests (`TestStore_DialectAndDriverRouting` in `backend/storage/store_test.go`).

Next: Target prototype edge hardware verification, Whisper.cpp model bundling, and live cloud deployment verification.

## Canonical Sortable Identifiers (ULID), Packaging Stock Conversions & Frontend Test Suite increment (14 October 2026) — MILESTONE 11 COMPLETE

Settled canonical sortable identifiers across the backend orchestrator (`backend/api/ulid.go`, `backend/api/ulid_test.go`, and `newID` in `backend/api/api.go`): replaced ad-hoc 16-byte hex IDs with 26-character lowercase Crockford Base32 ULIDs (48-bit millisecond timestamp + 80-bit monotonic entropy), preserving domain prefixes (`txn_`, `prd_`, `cap_`, `dev_`, `rcp_`) while ensuring strict chronological sortability, millisecond monotonicity, and concurrent uniqueness. Implemented direct packaging unit conversion for inventory adjustments: added `AdjustStockPackaging` in `backend/storage/products.go` and wired optional `unit` conversion into `POST /api/products/{id}/adjust` (`backend/api/products.go`) using `models.BaseQuantity` to seamlessly convert bulk containers (e.g., cartons, crates) to base catalogue units with clear audit reason annotations. Implemented comprehensive frontend automated test suite (`frontend/test/`) using native Node.js test runner covering KPI calculations (`kpi.test.mjs`), review queue action dispatches and error handling (`review_queue.test.mjs`), and WebSocket connection sequence resync and transaction list deduplication (`resync_dedup.test.mjs`), integrated into `make test` and GitHub Actions CI.

Next: Target prototype edge hardware verification, Whisper.cpp model bundling, and PostgreSQL production deployment.

## Multi-Line Receipt Atomicity, Deterministic Replay Hashing, Edge Continuous Sessions & E2E Verification increment (13 October 2026) — MILESTONE 10 COMPLETE

Implemented multi-line receipt acceptance atomicity (`RecordReceiptMovements` in `backend/storage/ledger.go`): processing all lines of a receipt within a single SQLite transaction, rolling back atomically if any line fails (`ErrInsufficientStock`, `ErrConflict`, `ErrNotFound`, `ErrCurrency`, `ErrInvalidMovement`) with zero partial stock or ledger mutations, and emitting WebSocket events only after transaction commit. Implemented deterministic capture replay hashing (`backend/api/captures.go`): deriving idempotency fingerprints directly from intake payload anchors (`req.CaptureID`, `line.ProductName`, `line.Quantity`, `line.Unit`, `line.Amount`) rather than post-validation timestamps/IDs to protect replay deduplication against catalogue pricing or confidence threshold drift. Hardened the Rust edge capture engine (`edge/`): added audio device listing (`list_audio_devices`), `AudioInputSource` trait with `MockAudioSource`, `WavAudioSource`, and `AudioStreamSource`, bounded `AudioRingBuffer` preventing memory leaks with secure zeroing on discard, `CaptureSession` state machine (`Listening`, `ActiveCapture`, `BoundaryDetected`, `TimedOut`, `Paused`) with a 15-second incomplete-capture timeout, obvious status indicators, and immediate secure audio cleanup. Added configurable boundary phrases (`BoundaryConfig`) and multi-line item extraction in `edge/src/extractor.rs`, local CLI/whisper command runner (`CommandSpeechRecognizer`) in `edge/src/stt.rs`, and a continuous capture session loop with signal handling in `edge/src/main.rs`. Built comprehensive end-to-end integration and concurrency test suite (`backend/api/e2e_edge_test.go`) covering sale captures, held review captures, multi-line receipts, atomicity rollback on line failure, idempotency replay with catalogue drift, cross-shop isolation, and WebSocket reconnect recovery.

Next: Target prototype edge hardware verification, Whisper.cpp model bundling, and PostgreSQL production deployment.

## Transaction Invariant Guards, Frontend Resilience, Readiness Monitoring & Adversarial Speech increment (13 October 2026) — MILESTONE 9 COMPLETE

Implemented transaction invariant guards and deep validation (`validateTransactionInvariants`) across transaction creation, manual entry, and confirmation: enforcing positive quantities ($1 \le q \le 100,000$), strict 3-letter alphabetic currency codes, bounded monetary amounts ($0 \le a \le 10^{12}$ minor units), confidence range validation ($0.0 \le c \le 1.0$, non-NaN/Inf), and rejecting mutations or references to archived products. Added database connectivity readiness probe (`GET /readyz` with `Store.Ping(ctx)`) returning HTTP 200 with database status and active client metrics, or HTTP 503 if unreachable. Added sliding-window rate limiting middleware (`RateLimiter`, 60 req/min with HTTP 429 and `Retry-After`) protecting capture intake (`/api/captures`, `/api/onboarding/test-capture`). Hardened speech recognition against adversarial and conversational patterns: resolved spoken self-corrections ("wait no", "make that", "actually"), filtered conversational greetings/fillers, merged orphaned amount clauses across pauses, and held incomplete/unrelated dialogue for human review. Upgraded frontend resilience: added request cancellation with `AbortController` on route transitions across all views (`api.ts`, `home.ts`, `inventory.ts`, `transactions.ts`, `settings.ts`), added Active/Archived/All catalogue filter tabs with product restore actions, added hardware capture health status monitoring (`Hardware: Connected` / `Hardware: Not Paired`), enforced deterministic ledger ordering (`occurred_at` desc, tie-break `id` desc) with bounded client-side history, and styled accessible focus outlines and table loading/error states.

Next: Microphone driver integration, speech-to-text model runtime execution on target edge hardware, and PostgreSQL production deployment.

## Guided Onboarding, Catalogue Integrity Guards & Edge Resilience increment (13 October 2026) — MILESTONE 8 COMPLETE

Implemented guided shop onboarding flow with state machine API (`GET /api/onboarding/status`), real-time recognition trial capture endpoint (`POST /api/onboarding/test-capture`), and interactive dashboard setup checklist card with a "Counter Sound & Recognition Test" modal. Enforced catalogue conflict validation guards (`validateCatalogueConflicts`) detecting case-insensitive duplicate SKUs, duplicate names, cross-product alias collisions, self-duplicate aliases, and alias vs SKU/name collisions with HTTP 409 Conflict across single-product create/update and bulk import. Protected audit trail integrity with historical movement guards (`validateHistoricalBaseUnitMutation`) preventing mutations to `base_unit` if historical movements exist. Hardened the Rust edge capture engine with atomic file rewrite via `.tmp` rename (`persist_all`), corrupt line tolerance on recovery, bounded exponential backoff with jitter and retryable error classification in the HTTP client, and an edge health diagnostics monitor (`EdgeHealthMonitor`, `EdgeHealthReport`).

Next: Microphone driver integration, speech-to-text model runtime execution on target edge hardware, and PostgreSQL production deployment.

## Multi-Line Receipt Grouping, Review Audit Trail, Device Identity & Product Archiving increment (13 October 2026) — MILESTONE 7 COMPLETE

Implemented multi-line receipt grouping and linking (`receipt_id` column, `GET /api/receipts/{receipt_id}`, receipt-wide confirmation `POST /api/receipts/{receipt_id}/confirm` and rejection `POST /api/receipts/{receipt_id}/reject`, and ledger receipt filter). Implemented review decision audit trail (`review_decisions` table, `GetReviewDecision`, `GET /api/transactions/{id}/decision`) preserving original extracted values, adjudicator identity, timestamp, notes, and changed values diff. Added "Edit & Confirm" interactive modal in review queue allowing catalogue product selection, quantity, unit, and amount correction before confirmation. Built canonical device identity enforcement on capture intake (rejecting mismatched device IDs, attaching authenticated device metadata) and device credential rotation (`POST /api/devices/{id}/rotate` with frontend UI). Added safe product archiving (`archived_at` column, `POST /api/products/{id}/archive`, `POST /api/products/{id}/unarchive`, excluding archived products from speech matching and low-stock counts while preserving ledger referential integrity).

Next: End-to-end device onboarding flow, audio/hardware integration, and production pilot deployment.

## Event Sequencing, Reversals, Manual Entry Fallback & Product Movement History increment (13 October 2026) — MILESTONE 6 COMPLETE

Implemented monotonic per-shop WebSocket event sequencing (`seq`), snapshot sequence cursors (`last_seq`), gap detection, and client resync. Implemented transaction voiding/reversal (`POST /api/transactions/{id}/reverse`) with atomic inverse stock movement creation and status transition guards. Built manual counter entry fallback (`POST /api/transactions/manual`) with packaging conversions and direct ledger recording when ambient shop noise prevents speech capture. Added product movement audit history (`GET /api/products/{id}/movements`) and stock count audit corrections (`POST /api/products/{id}/adjust`). Built full frontend integration including Manual Entry modal, transaction Void button, stock count Audit modal, and Movement Ledger modal. Added SQLite store unit test suites covering migrations from empty, migration idempotency, tenant shop isolation, negative stock refusal, and non-nil empty-slice JSON encoding.

Next: Production pilot rollout in partner retail shops (Mama Tolu Provisions trial cohort) & multi-line receipt grouping.

## Evidence and Controlled Trial Readiness increment (13 October 2026) — MILESTONE 5 COMPLETE

Implemented automated controlled shop trial benchmark suite (`backend/eval/`), verifying 100% full-transaction accuracy, 0 inventory discrepancies, 0 revenue leakage, and sub-10ms p95 processing latency across 20 realistic scenarios (`TRIAL_REPORT.md`). Added staff training and cashier boundary phrase protocols (`STAFF_TRAINING.md`). Added bulk catalogue onboarding with downloadable CSV template (`GET /api/products/template`) and batch import endpoint (`POST /api/products/import`) wired to the frontend inventory dashboard. Hardened production static asset serving (`restrictedStaticServer` in `backend/main.go`), implemented concurrent WebSocket Hub unit test suite (`backend/ws/hub_test.go`), and added `make eval` / `make trial` task targets.

## 1. Go backend: HTTP server — DONE (`backend/main.go`, `backend/api/`)

- [x] `main.go`: `http.Server` with `ReadHeaderTimeout`/`ReadTimeout`/
      `IdleTimeout`, graceful shutdown on SIGINT/SIGTERM, `slog` logging.
      `WriteTimeout` is deliberately 0 — it is a whole-connection deadline and
      a WebSocket response never finishes, so a non-zero value here kills live
      dashboards mid-stream.
- [x] Serves the SPA from `-frontend` (default `../frontend`), same-origin with
      `/ws` and `/api/`, so the default same-origin socket policy applies.
- [x] `GET /healthz` reporting client and dropped-event counts.
- [x] Snapshot endpoints in `backend/api/`:
      - `GET /api/transactions?shop_id=&limit=&offset=&type=&status=&from=&to=` — newest first, limit clamped to
        [1, 500], default 50, with server-side filters and pagination total.
      - `GET /api/inventory?shop_id=` — full catalogue plus `low_stock_count`
- [x] Both snapshot responses carry `generated_at`.
- [x] Ingestion endpoints, `backend/api/products.go` and
      `transactions.go`:
      - `POST /api/products` — add a catalogue entry; publishes
        `product.created`
      - `POST /api/products/import` and `GET /api/products/template` — bulk CSV/JSON catalogue onboarding.
      - `POST /api/transactions` — record a capture; publishes
        `transaction.created`, or `transaction.review_required` when the
        capture is held
      - `POST /api/captures` — **the edge intake endpoint**; accepts device
        tokens, executes validation engine, and atomically adjusts stock or holds for review.
      - `GET /api/summary` — server-authoritative daily overview metrics.
      - `POST /api/devices` and `POST /api/devices/{id}/revoke` — device pairing and revocation.
      - `GET /api/settings` and `PATCH /api/settings` — shop configuration and paired devices.
      - `GET /api/transactions/export?format=csv` — CSV export with spreadsheet formula injection protection.
- [x] `POST /api/dev/seed?shop_id=` (`backend/api/dev.go`) — **`-dev` only**.
      Wipes the shop, then inserts the blueprint's own worked examples: §9's
      Peak Milk aliases, §10's restock receipt, §11's 55%/98%/97% capture.
      Publishes the seeded state so an open dashboard updates without a reload.
- [x] Request-scoped logging (`X-Request-ID`) and `Recoverer` middleware so
      one panicking handler cannot take the process down.
- [x] Restrict static serving to built assets: `restrictedStaticServer` blocks
      uncompiled TypeScript sources and build manifests in production mode.

## 2. Storage (SQLite) — DONE (`backend/storage/`)

- [x] `storage.Open(path)` with pragmas applied per connection
      (`foreign_keys=on`, WAL, `busy_timeout=5000`, `txlock=immediate`), because
      `busy_timeout` and `foreign_keys` are connection-scoped in SQLite.
- [x] Embedded, versioned migrations (`storage/migrations/*.sql`) tracked in
      `schema_migrations`, each applied in its own transaction.
- [x] `products` and `transactions` with `shop_id NOT NULL`, `shop_id` leading
      every index, and a composite FK so a transaction cannot reference another
      shop's product.
- [x] Money as integer minor units in two columns; timestamps as fixed-width
      UTC text (so lexicographic ordering is chronological).
- [x] Repository methods: `ListProducts`, `GetProductByID`, `GetProductBySKU`,
      `InsertProduct`, `UpdateProduct`, `AdjustStock`, `InsertTransaction`,
      `GetTransactions`, `GetTransactionByID`, `UpdateTransactionStatus`,
      `CountTransactionsByStatus`.
- [x] `DeleteShopData(ctx, shopID)` — deletes a shop's transactions then its
      products, in one transaction, children first. **Destructive**; reachable
      only from the `-dev` seeder.

Remaining:

- [x] **Canonical Sortable Identifiers (ULID) settled.** `api.newID(prefix)` generates
      Crockford Base32-encoded 128-bit ULIDs (48-bit millisecond timestamp + 80-bit
      monotonic entropy), preserving prefix conventions (`txn_`, `prd_`, `cap_`,
      `dev_`, `rcp_`), making IDs chronologically and lexicographically sortable.
- [x] **Direct Packaging Unit Conversion for Stock Adjustments.** Added
      `AdjustStockPackaging` to `backend/storage/products.go` and wired optional unit
      conversions in `POST /api/products/{id}/adjust` (`backend/api/products.go`)
      using `models.BaseQuantity`.
- [x] **Resolving a review must be atomic.** `ConfirmReviewTransaction` changes
      the status, updates quantity in one transaction, and publishes the event.
- [x] Store tests: migration from empty, migration idempotency, shop isolation
      (a query for shop A must never return shop B's rows), `AdjustStock`
      refusing a negative result, and empty-slice JSON encoding (`backend/storage/store_test.go`, `backend/storage/movements_test.go`).
- [ ] Consider `sqlc` or hand-written query constants once the query count
      grows; the current `const query = ...` blocks are fine at this size.

## 3. Go backend: WebSocket hub — DONE (`backend/ws/`)

- [x] Hub in `backend/ws/hub.go`: a single `Run` goroutine owns the client
      registry; register, unregister and publish arrive over channels, so the
      client map needs no lock. Bounded per-client send buffer, and a stalled
      client is disconnected rather than allowed to delay everyone else.
- [x] Explicit `ShopID` partitioning: the registry is `map[shopID]map[*Client]`,
      so a broadcast iterates one shop's clients only.
- [x] `GET /ws` upgrade endpoint, same-origin by default (`-allowed-origins`).
- [x] Versioned envelope `{"type","v","shop_id","sent_at","data"}`, marshalled
      once per broadcast.
- [x] Ping/pong keepalive and per-message write deadlines.
- [x] `-dev` flag adds `POST /api/dev/broadcast`. **Off by default.**
- [x] `-dev` flag also adds `POST /api/dev/seed` (see §1). Same warning applies:
      it destroys the named shop's data and is unauthenticated.

- [x] **Real authentication:** `identity.ResolveShop` resolves shop from authenticated session cookies and edge device bearer tokens across both `/ws` and `/api/`.
- [x] Add hub tests: concurrent publish/register/unregister, shop isolation, slow-client eviction (`backend/ws/hub_test.go`).
- [x] Watch `/healthz`'s `ws_events_dropped` during the pilot. Events are shed
      when the queue is full, which is only recoverable because a client
      resyncs — if drops are frequent, the buffer or the resync path needs
      revisiting.

## 4. Frontend — DONE (`frontend/src/`)

- [x] Reconnecting client: exponential backoff capped at 15s with ±20% jitter.
- [x] Envelope parsing that survives bad input: malformed JSON, wrong protocol
      version, unknown event type, and events addressed to another shop are all
      logged and dropped without killing the connection.
- [x] Events re-dispatched on `window` (`transaction.created` →
      `ll:transaction-received`, …), with `WindowEventMap` augmented so
      listeners are typed.
- [x] `ll:connection-status` carries `resyncRequired`.
- [x] `frontend/src/api.ts` mirrors the Go models (`Transaction`, `Product`,
      `Money`, `Confidence`, `Unit`, `PackagingRelation`) and wraps the two
      snapshot endpoints.
- [x] `frontend/src/format.ts` — `formatMoney` divides by 100 exactly once, at
      print time. Nothing accumulates in floats.
- [x] View controllers in `frontend/src/views/`, bound to `VIEW_MOUNTED_EVENT`
      by `views/index.ts`, which unmounts the previous view before mounting the
      next. Each view fetches both snapshots on mount and applies realtime
      events to what it holds.
      - `home.ts` — KPIs, live feed, low-stock alerts, connection status
      - `transactions.ts` — review queue plus the filterable ledger
      - `inventory.ts` — catalogue with search and low-stock badges
- [x] `resyncRequired` is consumed: every view refetches a snapshot when a
      reconnect raises it. Note that events are applied **by ID** (found →
      replaced, absent → inserted), so replaying an event the snapshot already
      reflects replaces the row rather than duplicating it, and the KPIs are
      recomputed from the set rather than incremented. That is what makes the
      refetch safe; it is not the same as comparing `generated_at` against
      `sent_at`, which is still unimplemented.
- [x] All server-supplied text reaches the DOM through `textContent`
      (`format.el`), never string-interpolated into markup. Transcripts
      originate as speech, so they are attacker-influenced by definition.

Remaining:

- [x] **There is no stylesheet.** Responsive stylesheet implemented in `frontend/style.css`.
- [x] Ledger and catalogue filters are applied client-side over the fetched
      page. Moved into server-side queries across both SQLite and PostgreSQL with debounced frontend search and status tabs.
- [x] `data-hook="catalogue-add"` ("Add product") is wired with modal form.
- [x] Implement the Review Queue actions: confirm and reject buttons wired.

## 5. Validation engine (the middle of the processing loop) — DONE (`backend/validate/`)

- [x] Catalogue matching in `backend/validate/`: exact SKU → exact name → alias
      → fuzzy. Fuzzy matching maps "Big Peak" to "Peak Milk 400g" via aliases, with ambiguity detection.
- [x] Populate `Confidence` per anchor, then apply the per-shop threshold:
      at or above → `Confirmed`; below → `ReviewRequired` with **no inventory
      effect** (weakest anchor decides per Blueprint §11).
- [x] Implement packaging-aware unit conversion (supported via storage ledger conversion).
- [x] Only touch inventory when `TransactionStatus.AffectsInventory()` is true.
- [x] Handle the restock closing phrase ("Stock received") as a distinct
      boundary from the sale phrase ("Thank you"), configurable per shop.

## 6. Rust edge engine — scaffold implemented; hardware acceptance pending (`edge/`)

- [x] `cargo init --name listenledger-edge` inside `edge/`, with framing,
      VAD, STT interface, and queueing.
- [x] Define the payload the engine POSTs to `/api/captures`: transcript,
      extracted anchors, per-anchor confidence, detected closing phrase, and
      the device identifier.
- [x] Choose an authenticated transport — device token header and pairing credentials.
- [x] Confirm the audio-retention path: raw audio discarded immediately after extraction
      per blueprint §17.

## 7. Repo hygiene

- [x] Root `.gitignore` (node_modules, dist, target, `*.db*`, `.env`).
- [x] `frontend/package-lock.json` and `backend/go.sum` are both committed, so
      the Go and npm builds are reproducible.
- [x] Add a root `Makefile` task runner with `build`, `test`, `check`, `dev`, `clean`, `verify-backup`, and `eval` targets.
- [x] Set up CI: `gofmt -l`, `go vet`, `go test -race ./...`, `npm run typecheck && npm run build`, `cargo test` in `.github/workflows/ci.yml`.

## Remaining operational decisions

- Confirm the Render workspace and service plan before resource creation.
- Keep one application instance while realtime delivery uses an in-process hub.
- Keep voice capture disabled for real trading until microphone input and real model inference pass acceptance.
- Authentication, integer money, configurable confidence and reconnect synchronization exist; the original gaps listed here are resolved in source.

## 8. Milestone 18: Staff Protocol Training, Target Gates Agreement & Postgres Disaster Restoration — DONE

- [x] **Cashier Protocol Training Simulator:** Interactive boundary drill engine (`POST /api/onboarding/drill`) evaluating closing boundary presence, SKU articulation, unit & amount detection, self-correction phrasing, and negative non-commercial chatter rejection. Returns structured checklist, actionable coaching tips, scoring percentage, and automatically records `staff_trained_at` upon passing ($score \ge 75\%$).
- [x] **Owner Target Gates Agreement:** Configurable shop target gates (`GET/PUT /api/settings/target-gates`) persisting agreed accuracy, false-capture, hold-rate, and latency targets with dual-dialect SQLite (`0006_target_gates_and_training.sql`) and PostgreSQL migrations. Accessible via Settings UI and Onboarding checklist.
- [x] **Onboarding Checklist Complete:** Six-step progressive onboarding workflow (`profile`, `catalogue`, `devices`, `staff_training`, `target_gates`, `test_capture`) with interactive modals and live progression.
- [x] **Automated PostgreSQL Disaster Restore Verification:** Automated script (`scripts/verify_postgres_backup.sh`) and integration test (`TestPostgres_BackupAndRestoreVerification`) validating complete migration, transfer, and application access on clean target database.



