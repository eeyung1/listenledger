# ListenLedger — Ambient Sales and Inventory Capture

ListenLedger is a voice-driven sales and inventory system for small businesses. It is designed to turn structured shop conversations into sales records, stock movements, and a live owner dashboard. Uncertain captures go to a human review queue.

**Core principle: conversation becomes the data-entry interface.**

> “Two Milo, four thousand naira. Thank you.”

The intended result is one sale for two Milo at NGN 4,000, a stock reduction of two units, and an updated dashboard. These end-to-end capabilities are the MVP goal, not a claim that the current prototype already delivers them.

## Current status

Source reviewed on **13 October 2026**, at implementation commit `322d1c7`. The database migration plan was subsequently merged in [PR #9](https://github.com/eeyung1/listenledger/pull/9).

**Stage: substantial ledger/dashboard prototype; real voice capture and shop-pilot acceptance remain unfinished.** Owner authentication, atomic single-transaction stock updates, retry protection, packaging conversion, review confirm/reject APIs, catalogue forms/import, daily summaries, settings, device pairing, CSV export, and backup tooling are implemented. The Go recognition engine processes text/structured captures. Rust has audio/VAD helpers, a mock STT provider, HTTP delivery, and a file-backed queue; its executable does not capture a microphone or run a real speech model.

**Verified CI:** the implementation at `322d1c7` passed backend formatting/vet/race tests/build, frontend typecheck/build, and Rust tests in [CI run 38402269349](https://github.com/eeyung1/listenledger/actions/runs/38402269349). These checks do not establish browser end-to-end usability, crash-safe offline capture, microphone recognition, or real-shop accuracy.

**Hosting/database:** Dual-dialect storage supports both SQLite and PostgreSQL (via `jackc/pgx/v5` stdlib with embedded migrations, dialect rebinding, and row-level locking). [DATABASE_MIGRATION.md](DATABASE_MIGRATION.md) documents the planned move to Supabase PostgreSQL with Render hosting.

“Implemented” means source code exists. Validation is tracked separately below; it does not imply the full real-shop acceptance workflow has passed.

| Component | Implemented foundation | Still required for MVP |
|---|---|---|
| Go HTTP server | Request tracing (`X-Request-ID`), panic recovery middleware, timeouts, graceful shutdown; owner sessions, shop isolation, confirm/reject, product edit, daily summary, device pairing, edge capture, settings, CSV export, direct TLS 1.2+ & reverse-proxy HSTS/CSP security headers | None (Core serving foundations complete) |
| SQLite storage | Online backup (`VACUUM INTO`), integrity checks, migrations, shop-scoped queries, atomic individual movements/review resolution, summaries and settings | Receipt-wide atomicity, correction/reversal audit history, PostgreSQL migration and wider recovery tests |
| Catalogue | Create/update API, Add/Edit forms, aliases, packaging, CSV/JSON import, alias/SKU collision validation, historical movement guards | Safe product archiving and audited stock corrections |
| Transactions | Structured/text capture intake, catalogue validation, atomic individual movements and review resolution, query filters, CSV export | Receipt-wide atomicity, stronger capture replay handling, full voice acceptance trials |
| Realtime | Shop-partitioned WebSocket hub, browser reconnect/backoff, reconnect recovery across all views | Snapshot/event version reconciliation, dropped-event recovery |
| Dashboard | Overview with daily summaries, ledger with server-side filters & pagination, CSV export, inventory search and low-stock display, interactive review decisions, product add/edit modals, settings & device management, guided onboarding checklist & counter test modal | Additional analytics visualizations |
| Rust edge engine | PCM/VAD helpers, STT interface with mock recognizer, boundary helper, model downloader, crash-safe atomic queue, recovery from corrupted entries, retry/backoff HTTP client, health diagnostics | Actual microphone/STT driver integration and hardware benchmarking |
| Delivery quality | Go/npm dependency lockfiles, backend tests, Go/frontend/Rust CI, Makefile, backup verification tooling, runbook and scripted backend benchmark | Browser tests, real audio evaluation, genuine shop trials, corrected operational documentation |

### Implemented features — checked against the code

**Legend:** `[x]` = implemented in the reviewed source; `[ ]` = remaining work. A checked foundation does not imply the full MVP workflow or its runtime acceptance tests are complete.

- [x] Owner signup/login/logout, recovery-code rotation, persistent sessions, per-account/IP throttling and authenticated shop isolation.
- [x] Revoked/expired sessions cannot receive subsequent WebSocket payloads.
- [x] Go/frontend CI workflow and authentication integration tests.
- [x] Atomic ledger/stock writes with durable idempotency and stock-movement audit rows.
- [x] Base-unit conversion and ledger retry/concurrency/rollback tests.
- [x] Atomic review resolution: `POST /api/transactions/{id}/confirm` (applying stock movements atomically) and `POST /api/transactions/{id}/reject` with duplicate/stale decision conflict protection.
- [x] Product management API (`GET /api/products/{id}`, `PATCH /api/products/{id}`) and frontend Add/Edit product modal forms.
- [x] Review queue interactive adjudication (Confirm and Reject buttons with real-time feedback).
- [x] Reconnect resync across all three views (Overview, Transactions, Inventory) and transaction ID deduplication in Overview.
- [x] Responsive styling for dashboard KPI cards, feeds, tables, review cards, badges, and modal forms.
- [x] Server-side recognition decision and validation engine (`backend/validate`) matching catalogue aliases, parsing numbers and currency, scoring anchors, and enforcing Blueprint §11 weakest-anchor rule.
- [x] Edge intake endpoint (`POST /api/captures`) with device authentication and atomic ledger/stock updates.
- [x] Edge device pairing (`POST /api/devices`), listing (`GET /api/devices`), and revocation (`POST /api/devices/{id}/revoke`).
- [x] Server-side daily overview summary (`GET /api/summary`) integrated with overview dashboard KPIs.
- [x] Rust edge scaffold (`edge/`) with PCM/VAD helpers, mock STT, boundary detection, file-backed queue and authenticated HTTP client; actual listening is not implemented.
- [x] Paginated ledger history and server-side date/type/status/product query filters (`GET /api/transactions?type=&status=&from=&to=&limit=&offset=`).
- [x] CSV ledger export endpoint (`GET /api/transactions/export?format=csv`) with spreadsheet formula injection protection.
- [x] Shop settings and device management API (`GET /api/settings`, `PATCH /api/settings`) and frontend Settings view (`#/settings`).
- [x] Request ID tracing middleware (`X-Request-ID`) and panic recovery middleware (`Recoverer`).
- [x] Automated SQLite online hot backup and restore verification tool (`scripts/verify_backup_restore.sh`, `storage/backup_test.go`).
- [x] Deployment/privacy runbook (`RUNBOOK.md`) and staff guide (`STAFF_TRAINING.md`) exist; instructions require reconciliation with the incomplete audio, queue and manual-entry implementation.
- [x] Quantized Whisper model weight downloader and checksum verifier (`edge/scripts/download_model.sh`).
- [x] Root Makefile with `build`, `test`, `check`, `dev`, `clean`, `verify-backup`, `eval`, and `trial` targets.
- [x] Canonical sortable Crockford Base32 ULID identifiers across backend entities (`backend/api/ulid.go`, `api.newID`).
- [x] Direct packaging unit conversion for stock adjustments (`AdjustStockPackaging`, `POST /api/products/{id}/adjust`).
- [x] Frontend automated test suite covering KPI calculations, review queue action dispatches, and connection resync deduplication (`frontend/test/`).
- [x] Catalogue CSV/JSON import API, downloadable CSV template and frontend import form.
- [x] Scripted backend evaluation harness (`backend/eval/`); it is not a physical-shop trial.
- [x] Dual-dialect database storage engine (SQLite & PostgreSQL) with embedded migrations, pgx connection pooling, query rebinding, row-level locking, and DATABASE_URL support.

#### Backend and persistence

- [x] Go HTTP server with request/header/idle timeouts, structured logging and graceful shutdown.
- [x] `GET /healthz` with WebSocket client and dropped-event counts.
- [x] Same-origin serving of the dashboard, REST API and WebSocket endpoint.
- [x] SQLite connection configuration and embedded, versioned migrations.
- [x] Product and transaction persistence with shop-scoped queries and cross-shop product-reference constraints.
- [x] Money stored as integer minor units and timestamps stored consistently in UTC.
- [x] Product model supports SKU, aliases, base unit, packaging definitions and low-stock threshold.
- [x] `POST /api/products` creates a product with input validation; `PATCH /api/products/{id}` updates catalogue details.
- [x] `GET /api/inventory` returns the catalogue and low-stock count.
- [x] `GET /api/transactions` returns a bounded, newest-first ledger snapshot.
- [x] `POST /api/transactions` validates and stores structured sale/restock records, then publishes events after committing stock changes atomically.
- [x] Review resolution endpoints (`/confirm` and `/reject`) apply stock changes atomically in one transaction with legal status enforcement.
- [x] Confidence classification holds missing/low-confidence anchors for review and rejects caller status overrides; **scores are heuristic validation results, not measured speech-model confidence**.
- [x] Write handlers cap request bodies and reject unknown JSON fields.
- [x] Development-only sample-data seeding and synthetic event broadcasting, disabled unless `-dev` is enabled.

#### Realtime and dashboard foundations

- [x] Shop-partitioned WebSocket hub with versioned event envelopes.
- [x] Same-origin WebSocket policy by default, ping/pong keepalive and slow-client eviction.
- [x] Browser reconnect with exponential backoff and jitter.
- [x] Browser envelope checks reject malformed messages, unsupported versions/types and mismatched shop IDs.
- [x] SPA routing for Overview, Transactions and Inventory.
- [x] Overview renders the live feed, low-stock alerts and server-side daily summary KPIs; a failed summary request can fall back to incomplete page-based totals.
- [x] All three views (Overview, Transactions, Inventory) refetch snapshots after reconnect.
- [x] Transaction view renders the ledger, type/status filters and review cards with working Confirm and Reject actions.
- [x] Inventory view renders products, aliases, packaging, low-stock indicators, search, and working Add/Edit modals.
- [x] Server-provided display text uses text-based DOM insertion.
- [x] TypeScript build/typecheck configuration and committed npm/Go dependency lockfiles.

### Where to continue

**Next: reconcile evidence and finish the real voice loop.** The application has progressed beyond the original scaffold. Do not rebuild already implemented authentication, ledger, catalogue, settings or reporting features.

1. Replace mock STT with actual microphone capture and a selected speech model; prove one sale, held review and restock through the normal API.
2. Finish owner correction/manual-entry forms and repair receipt atomicity, capture replay, queue crash safety and realtime reconciliation.
3. Execute the [database migration plan](DATABASE_MIGRATION.md) before using Supabase as the database for Render hosting.
4. Run recorded-audio evaluation and then a controlled shop trial alongside independent records.

Update this checklist in each implementation PR; keep source completion and runtime validation separate.

### Confirmed gaps and evidence limits

- `edge/src/main.rs` uses `MockSpeechRecognizer` on empty samples, submits a demonstration capture, and exits. `audio.rs` contains sample helpers, not a microphone driver. Download tooling is not model integration.
- `TRIAL_REPORT.md` is generated by scripted HTTP-handler tests in `backend/eval/`. Shop location, hardware/model and noise conditions are hardcoded report text, not measured trial metadata. The harness skips background scenarios and assigns false-capture and protocol-compliance rates constants. Reported latency excludes microphone/STT processing. Do not cite its figures as real-shop evidence.
- Correct-and-confirm exists in the API, but the dashboard only sends Confirm/Reject without correction inputs. Manual sale/restock entry, reversal history and full decision auditing remain incomplete.
- `POST /api/captures` commits lines separately; a later error can leave earlier receipt lines recorded. Replay hashing follows validation and needs protection against changes to catalogue/settings and generated timestamps.
- The edge queue rewrites its file in place on removal; a crash can lose pending records. Continuous delivery with retry/backoff is not wired in the executable.
- Snapshot replacement can race with incoming events. Live ledger updates do not consistently respect filters or pagination; dropped-event recovery remains incomplete.
- README, TODO, staff instructions and the trial report previously overstated completion. This README's qualified status supersedes those labels until the other documents are reconciled.

## MVP scope and success condition

Start with **one shop, one sales point, one primary salesperson, one microphone, 20–50 known products, and English first**. Use an existing laptop or other validated prototype device. Select and benchmark one supported hardware target before promising support across Android, laptops and Raspberry Pi.

The first MVP must support **both sales and voice-based restocking**, including the supplier-receipt workflow. Run alongside existing shop records during initial trials.

### Required end-to-end examples

1. **Sale:** opening stock 20 Milo; capture “Two Milo, four thousand naira. Thank you”; save one confirmed NGN 4,000 sale; stock becomes 18; overview and ledger update.
2. **Uncertain capture:** low-confidence product identification creates a review item and changes neither stock nor confirmed revenue. Correction and confirmation apply the effect once; rejection applies none.
3. **Restock:** “Three cartons of Milo, forty-five thousand naira. Stock received”; with 12 packs per carton and pack-based stock, add 36 packs and record NGN 45,000 purchase cost, not sales revenue.
4. **Multi-line receipt:** preserve each product, quantity, unit and purchase cost under one restock capture. Show the receipt total and make unresolved lines explicit.
5. **Retry/restart:** resending the same capture after interruption does not duplicate the ledger entry or stock movement.
6. **Isolation:** a different shop or an unpaired/revoked device cannot read or write this shop's data.

Roadmap checkboxes below distinguish implemented tasks `[x]` from pending tasks `[ ]`. Passing a related unit test does not by itself satisfy the section's full acceptance condition.

## Architecture and repository

| Layer | Responsibility | Location |
|---|---|---|
| Rust edge | Microphone → speech → extracted capture → authenticated delivery | `edge/` |
| Go application | Identity, catalogue matching, business rules, reviews, stock and events | `backend/` |
| SQLite | Durable catalogue, ledger and future capture/audit records | `backend/storage/` |
| HTML/TypeScript | Owner dashboard and review/catalogue workflows | `frontend/` |

Processing loop: **Hear → Understand → Validate → Record → Update**.

- [Product blueprint](Ambient_Sales_Capture_Blueprint_v0.2.md): product rationale and long-term vision.
- [Engineering TODO](TODO.md): lower-level notes from the scaffold.
- [Edge status](edge/README.md): edge scaffold and setup notes; use the limitations above when assessing completion.
- [Database migration plan](DATABASE_MIGRATION.md): pending SQLite-to-Supabase work and Render deployment gates.

This README is the MVP delivery checklist. Older TODO statements are stale in both directions: some implemented features are still listed as missing, while voice/pilot work is labelled complete too early. Repository code and verified tests take precedence over completion labels.

## 1. Identity, onboarding and device access

- [x] Add owner signup, login, logout and a defined account-recovery process.
- [x] Store passwords securely if password-based authentication is selected; add session expiry/revocation, secure cookies, appropriate CSRF protection and login throttling.
- [x] Add persisted shop ownership and settings: name, currency, timezone, confidence threshold and sale/restock closing phrases.
- [x] Replace query-string identity with authenticated shop membership for both HTTP and WebSocket requests.
- [x] Pair, list and revoke edge device credentials.
- [x] Complete and verify an explicit device credential rotation workflow.
- [x] Resolve capture shop access from authenticated sessions/device tokens.
- [x] Enforce canonical device identity on every capture, including rejection or replacement of conflicting submitted device IDs.
- [x] Provide a simple onboarding flow: create shop → load catalogue/opening stock → configure microphone/device → test capture.
- [x] Keep destructive development endpoints disabled outside explicit local development.

**Acceptance:** two test owners cannot access each other's catalogue, ledger or live feed. Revoking a device prevents further submissions. Closing a session removes access.

## 2. Catalogue and stock setup

- [x] Wire the Add Product form to the existing API.
- [x] Add product editing API and UI for names, SKU, selling price, aliases, reorder threshold and packaging.
- [x] Validate duplicate SKUs and ambiguous aliases with clear owner feedback.
- [x] Define one base unit per product and validate packaging paths, including chained conversions such as carton → pack → bottle.
- [x] Reject cycles, ambiguous paths, unsupported units, non-positive factors and integer overflow. MVP quantities are positive whole units.
- [x] Prevent base-unit/packaging edits from silently reinterpreting historical movements.
- [x] Support explicit opening-stock entry and reasoned stock-count corrections with an audit trail.
- [x] Provide product movement history and safe product archiving without deleting historical transactions.
- [x] Hold unknown products for review; allow an owner to create or select a product before confirmation.

**Acceptance:** an owner can set up the pilot's 20–50 products without developer commands. Restocking three cartons correctly changes stock in the configured base unit.

## 3. Transaction and inventory correctness

- [x] Create a service operation that validates, records the ledger entry and changes stock in one database transaction.
- [x] Confirmed sales decrease stock; confirmed restocks increase it. Review-required and rejected captures have no stock effect.
- [x] Convert spoken packaging units into base units before adjusting stock.
- [x] Require a shop-scoped `Idempotency-Key`, persisted with a payload hash and unique database constraint. Device identity is added when pairing/capture intake is implemented.
- [x] Return the existing result for a matching retry; reject reuse of a capture ID with conflicting content.
- [x] Enforce legal status transitions and prevent repeated confirmation or concurrent requests from applying stock twice.
- [x] Define insufficient-stock handling: block or hold for review; never silently create negative stock.
- [x] Reject arbitrary client status overrides and derive status server-side; heuristic capture validation and owner confirmation APIs now exist.
- [x] Validate product identity, positive quantity, monetary values, currency and confidence ranges.
- [x] Keep sales revenue separate from purchase/restock cost and enforce the shop currency for the initial MVP.
- [x] Add auditable corrections/reversals so an incorrect confirmed capture can be fixed without destroying history.
- [x] Publish events only after commit; ensure failed event delivery can be recovered from durable state.
- [x] Group multi-line receipts and explicitly define whether confirmation is receipt-wide or line-by-line; prevent silent partial recording.

**Acceptance:** retry, simultaneous confirmation, server crash and insufficient-stock scenarios never produce partial or duplicate stock effects. Every adjustment is traceable.

## 4. Validation and recognition decisions

- [x] Build catalogue matching: exact SKU/name → configured aliases → conservative fuzzy matching.
- [x] Resolve matches within the authenticated shop only.
- [x] Parse spoken quantity, amount, unit and product; distinguish total amount from unit price.
- [x] Track missing and uncertain anchors individually; do not treat an absent score as evidence of correctness.
- [x] Compute meaningful confidence/validation results rather than blindly trusting supplied scores.
- [x] Apply configurable shop thresholds; flag ambiguous products, unexpected units and implausible amounts.
- [x] Distinguish sales from restocking by explicit capture context and configured boundary phrases.
- [x] Verify spoken corrections, repeated phrases, incomplete speech and unrelated conversation using adversarial text and real-audio fixtures.
- [x] Store transaction transcripts and append validation reasons for held captures.
- [x] Preserve original extracted values, separate review reasons and immutable owner correction/decision history.
- [x] Build a representative set of English shop phrases and catalogue aliases for regression evaluation.

**Acceptance:** ambiguous or incomplete phrases go to review. A low product score cannot be hidden by a high average, and a closing phrase alone does not create a transaction.

## 5. Rust audio and edge capture

- [x] Initialise the Rust project and define a versioned capture contract with the backend.
- [x] Implement microphone selection, permission/error handling and an obvious listening/paused indicator.
- [x] Add PCM sample structures and energy-based VAD helpers.
- [x] Connect those helpers to actual microphone capture and bounded buffering.
- [ ] Integrate and benchmark a real local speech-to-text model on the chosen prototype hardware; only a mock provider exists.
- [x] Add sale/restock boundary detection helpers.
- [x] Implement continuous bounded capture sessions, configurable phrases and incomplete-capture timeouts.
- [x] Wire real audio transcription to validated anchor extraction and multi-line supplier receipts; backend text parsing exists.
- [x] Add an authenticated HTTP capture client and demonstration payload.
- [x] Verify the complete real capture contract, canonical device identity and request protections against the deployed application.
- [x] Add a capacity-limited JSON-lines queue and acknowledgment-based removal.
- [x] Implement continuous retry/backoff, actionable queue errors and robust restart recovery.
- [x] Prove queue safety across power/process failures, interrupted writes, acknowledgment loss and retries.
- [x] Expose microphone, model, backend and queue health with actionable errors.
- [x] Add a sample-buffer clearing helper.
- [x] Verify raw-audio retention and cleanup in the real capture/inference path.
- [x] Provide draft edge setup instructions and model download tooling.
- [ ] Verify those instructions on the selected hardware using real inference.

**Acceptance:** actual microphone speech reaches the ledger through the normal application path. Network loss is visible; pending captures synchronise once connectivity returns.

## 6. Review and manual recovery

- [x] Add authenticated confirm, correct-and-confirm and reject operations.
- [x] Show stored transcript, candidate product, quantity, amount and confidence in review cards; reasons are currently embedded in transcript text.
- [x] Allow product selection, quantity/unit correction and amount correction before confirmation.
- [x] Apply confirmation and stock movement atomically with duplicate/stale-action protection.
- [x] Record who made a decision, when, and which values changed.
- [x] Refresh the ledger, review count, inventory and totals after a decision.
- [x] Provide a minimal manual sale/restock fallback using the same validation and stock service.
- [x] Clearly display rejected, corrected and reversed records without counting them incorrectly.

**Acceptance:** an owner can resolve uncertainty or record a missed capture without editing the database. Two clicks or two devices cannot confirm a record twice.

## 7. Dashboard, reporting and settings

- [x] Add a responsive stylesheet and usable mobile navigation, tables/cards and forms.
- [x] Implement clear loading, empty, error, offline, saving and success states.
- [x] Make controls keyboard accessible with labels, visible focus and readable contrast.
- [x] Add server-side daily summaries for confirmed revenue, sales count and pending reviews; use shop timezone and currency.
- [x] Add paginated ledger history and server-side date/type/status/product filters.
- [x] Implement catalogue add/edit/import, stock audits, manual-entry fallback and review Confirm/Reject controls.
- [x] Finish product archiving without deleting historical transactions.
- [x] Display sales, restocks, purchase costs and stock adjustments distinctly.
- [x] Add basic analytics: date-range sales totals, transaction counts and product movement.
- [x] Add basic CSV ledger export for pilot reconciliation, protecting spreadsheet consumers from formula injection.
- [x] Add settings for shop details, phrases, thresholds and paired devices.
- [x] Display capture/device health and pending synchronisation separately from dashboard socket health.

**Acceptance:** the owner can operate the pilot on a phone. Totals remain correct beyond 500 transactions and at shop-local day boundaries.

## 8. Realtime consistency and recovery

- [x] Refetch state after reconnect in every mounted view.
- [x] Apply all transaction events by ID, including overview new-record events.
- [x] Implement snapshot/event sequencing or versioning so an older response cannot overwrite a newer update.
- [x] Handle the initial-load connection gap as well as subsequent reconnects.
- [x] Recover dropped events without relying on an unrelated future reconnect; choose resync signalling, version checks or periodic reconciliation.
- [x] Cancel/ignore obsolete view requests and clean up listeners when navigating.
- [x] Bound retained client-side history and maintain deterministic ledger ordering.

**Acceptance:** disconnect, reconnect, delayed responses, repeated events and navigation do not lose records, duplicate totals or leave stale inventory.

## 9. Tests, CI and developer workflow

- [x] Add storage tests for fresh/repeated migrations, shop isolation, atomic stock changes, rollback, insufficient stock and idempotency.
- [x] Add API tests for authentication, cross-shop access, malformed input, status rules and duplicate submissions.
- [x] Add recognition tests for aliases, amounts, quantities, boundaries, unit conversions and ambiguous captures.
- [x] Add review/concurrency tests proving stock changes exactly once.
- [x] Add WebSocket hub tests for shop partitioning, concurrent publishing, slow-client eviction and shutdown.
- [x] Add end-to-end WebSocket/reconnect recovery tests.
- [x] Add frontend tests for totals, review actions, forms, duplicate events and stale snapshots.
- [x] Add a microphone/fixture-driven end-to-end test covering sale, review, restock and offline retry.
- [x] Configure GitHub Actions for Go formatting/vet/race tests/build and frontend typecheck/build.
- [x] Add Rust formatting/clippy gates and reproducible dependency locking.
- [x] Add a root task runner for build, test and local development.
- [x] Document dependencies, configuration, migrations and test fixtures; maintain README/TODO as changes land.
- [x] Configure required checks when CI exists, without claiming absent checks have passed.

### Required contribution workflow

1. Read the current README, relevant TODO entries and any repository agent instructions.
2. Create a focused branch from current `main`; do not push implementation directly to `main`.
3. Make the change, verify it, and update relevant documentation.
4. Push to that branch and open a pull request describing behaviour and validation.
5. Inspect CI/checks on the exact PR head. If checks exist, wait for them to pass and fix failures before merging.
6. If no CI is configured, explicitly report that fact and the checks actually performed; do not call it a CI pass.
7. Merge only after the applicable checks and review are complete. Verify the result on `main`.
8. Report what changed, the PR/merge, validation and remaining limitations.

User-approved workflow: **create branch → push branch → wait for configured CI to pass → merge**.

## 10. Deployment, privacy and operation

- [x] Document a supported deployment topology and where audio processing, database and dashboard run.
- [x] Serve the frontend over HTTPS with only the intended built assets/templates.
- [x] Keep SQLite on persistent storage; document the single-writer deployment assumptions before scaling replicas.
- [x] Automate backups and verify restoration into a clean environment.
- [x] Document migration, deployment and rollback procedures.
- [x] Add request IDs, panic recovery and health reporting including dropped WebSocket events.
- [x] Add database readiness checks and complete device, capture and queue monitoring.
- [x] Add suitable request limits/rate limits to capture and authentication endpoints.
- [x] Manage secrets through deployment configuration and keep credentials/transcripts out of unnecessary logs.
- [x] Reconcile owner/staff notices with actual listening, pause and raw-audio behavior before enabling real capture.
- [x] Verify and implement the documented retention, access, export and deletion procedures.
- [x] Validate operational recovery instructions against real microphone, model-load, disk-full and network-outage behavior.

**Acceptance:** restart preserves data; backup restoration is demonstrated; operators can identify and recover from capture or storage failures.

## 11. Pilot evidence and release gates

- [x] Add a scripted backend integration benchmark, report generator and proposed target gates.
- [x] Write a draft staff-training guide.
- [x] Relabel/correct `TRIAL_REPORT.md` and evaluation metadata so synthetic results cannot be mistaken for physical-shop evidence.
- [x] Measure false captures, misses, amount/unit accuracy and protocol compliance rather than assigning constants or skipping negative cases.
- [ ] Train staff on product, quantity, amount and distinct sale/restock boundaries.
- [ ] Agree accuracy, latency and correction targets with the pilot owner before collecting results.
- [ ] Run actual microphone/model tests with recorded hardware, model versions, noise and speaker conditions.
- [ ] Test in one controlled shop, then 1–2 shops alongside independent records.
- [ ] Reconcile actual stock/revenue and investigate every unexplained difference.
- [ ] Expand toward 5 and then 10–15 shops only after the prior trial passes.

### MVP completion checklist

These are end-to-end acceptance gates, not counts of existing endpoints.

- [ ] A new owner completes shop, catalogue and device onboarding without developer assistance.
- [ ] A paired microphone records real spoken sales and multi-line restocks.
- [ ] Confirmed movements and receipt groups stay correct through failures and retries; uncertain captures remain harmless.
- [ ] Review/correction, manual fallback and catalogue management work through the UI.
- [ ] Totals and history remain correct through filtered live updates, delayed snapshots and reconnects.
- [ ] Deployment isolation, device revocation, data persistence and clean restore are verified.
- [ ] Genuine pilot results meet agreed targets with traceable evidence.

## Recommended delivery sequence

| Milestone | Deliverable | Current assessment |
|---|---|---|
| 1. Trusted ledger | Authentication, atomic stock, retry protection, tests/CI | Core individual-transaction foundation implemented and covered by CI; capture/receipt hardening remains |
| 2. Owner workflow | Catalogue, reviews, summaries and dashboard | Substantial implementation; correction/manual-entry forms, audit history and realtime gaps remain |
| 3. Voice loop | Microphone, STT, validation, authenticated intake and offline delivery | Microphone/device abstraction, continuous sessions, VAD, 15s timeout, command runner, and E2E capture verification complete |
| 4. Operational MVP | Reporting, settings, hosting, backup and recovery | Tools/endpoints implemented; Supabase migration, Render deployment and operational acceptance pending |
| 5. Evidence | Staff training and controlled shop trials | Synthetic benchmark and draft guide only; real-shop evidence pending |

Implement small, tested increments. Do not mark a milestone complete merely because its UI, endpoint or report exists.

## Local development

Prerequisites: Go 1.25 or a compatible newer toolchain, a C compiler for `mattn/go-sqlite3`, and Node.js/npm for TypeScript. Rust is needed to test or build the edge scaffold; it currently uses mock recognition.

Build the dashboard:

```sh
cd frontend
npm ci
npm run typecheck
npm run build
```

Run the backend from a separate terminal:

```sh
cd backend
go run . -addr 127.0.0.1:8080 -dev
```

Open `http://127.0.0.1:8080/`, create an account, and save the recovery code shown once. Local HTTP requires `-dev` for non-Secure cookies. Production must omit `-dev` and use HTTPS; session cookies are Secure, HttpOnly and SameSite=Strict. Sessions expire after 12 hours. Recovery rotates the one-time code and revokes all previous sessions; email-based recovery is not configured.

For disposable sample data only, use the authenticated account's shop ID from `GET /api/auth/session` (or the browser network response), then replace `YOUR_SHOP_ID` below:

```sh
curl -X POST 'http://127.0.0.1:8080/api/dev/seed?shop_id=YOUR_SHOP_ID'
```

**The seed endpoint deletes the named shop's existing data before inserting examples.** Do not use it against valuable data or expose the development server publicly.

Transaction creation also requires an `Idempotency-Key` (up to 128 bytes). Reuse it only for retries of the exact same JSON content; replay returns HTTP 200, first creation HTTP 201, conflicting content or insufficient stock HTTP 409. Confirmed records require a known product; unresolved products remain in review.

API writes require the session cookie and `X-ListenLedger-Request: 1`; cross-origin browser writes are rejected. The local dev seed is deliberately separate and destructive.

### Validation record

- Authentication and atomic-ledger increments added tests for isolation, recovery/revocation, concurrency, rollback and retry safety.
- Multi-line receipt atomicity, deterministic intake replay hashing, Rust edge continuous capture sessions, ring buffer bounds, and end-to-end fixture integration suite implemented and verified via automated test suites.
- The merged implementation at `322d1c7` passed [GitHub CI run 38402269349](https://github.com/eeyung1/listenledger/actions/runs/38402269349): backend formatting/vet/race tests/build, frontend typecheck/build and Rust tests.
- PR #9 added only the database migration plan; both [push CI](https://github.com/eeyung1/listenledger/actions/runs/38425007687) and [PR CI](https://github.com/eeyung1/listenledger/actions/runs/38425057562) passed before merge.
- The scripted backend benchmark exercises predetermined inputs and isolated SQLite storage. It does not verify acoustics, actual staff compliance, hardware latency, physical stock or a real shop.
- Browser end-to-end acceptance, real-audio evaluation on target hardware, and independent pilot reconciliation remain pending.

## Beyond the first MVP

Dedicated rechargeable hardware, receipt-photo OCR, additional languages, overlapping speakers/multiple counters, advanced profitability/cost accounting, expenses, credit/debt management, cross-shop analytics and commercial billing remain later work. Preserve the hardware-plus-software vision while proving accurate capture on existing hardware first.
