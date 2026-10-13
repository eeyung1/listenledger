# ListenLedger — Ambient Sales and Inventory Capture

ListenLedger is a voice-driven sales and inventory system for small businesses. It is designed to turn structured shop conversations into sales records, stock movements, and a live owner dashboard. Uncertain captures go to a human review queue.

**Core principle: conversation becomes the data-entry interface.**

> “Two Milo, four thousand naira. Thank you.”

The intended result is one sale for two Milo at NGN 4,000, a stock reduction of two units, and an updated dashboard. These end-to-end capabilities are the MVP goal, not a claim that the current prototype already delivers them.

## Current status

Implementation reviewed on **13 October 2026**, starting from commit `461e971`; owner authentication implementation added in the next milestone PR.

**Stage: early software prototype; not ready for real-shop operation.** The Go API, SQLite persistence, WebSocket transport, and three dashboard views exist. The Rust audio engine is a placeholder. Owner/shop authentication is implemented. Confirmed structured transactions now update stock atomically with retry protection. Owner review actions (confirm/reject) and catalogue add/edit workflows are now implemented; device pairing and voice capture remain unfinished.

“Implemented” means source code exists. Validation is tracked separately below; it does not imply the full real-shop acceptance workflow has passed.

| Component | Implemented foundation | Still required for MVP |
|---|---|---|
| Go HTTP server | Health endpoint, structured logging, timeouts, graceful shutdown; owner sessions, shop isolation, confirm/reject and product edit endpoints | Device credentials, request tracing, operational hardening |
| SQLite storage | Embedded migrations, products, transactions, shop-scoped queries, atomic review resolution and stock movements | Reversal audit history and wider recovery tests |
| Catalogue | Product creation & update API; aliases, units and packaging stored; Add Product and Edit UI forms | Alias conflict detection and batch imports |
| Transactions | Structured JSON ingestion, persistence, basic supplied-confidence classification, atomic confirm/reject review operations | Server-generated recognition validation and device intake |
| Realtime | Shop-partitioned WebSocket hub, browser reconnect/backoff, reconnect recovery across all views | Snapshot/event version reconciliation, dropped-event recovery |
| Dashboard | Overview, ledger/review display, inventory search and low-stock display, interactive review decisions, product add/edit modals, comprehensive stylesheet | Accurate server-side aggregates, basic analytics/settings |
| Rust edge engine | Responsibility documented in `edge/README.md` | Audio capture, VAD, local STT, extraction, device transport and offline queue |
| Delivery quality | Dependency lockfiles, authentication/isolation tests, Go/frontend CI, review resolution & API tests | Wider edge/voice tests, deployment/runbook, backup/restore verification |

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
- [x] Confidence classification holds missing/low-confidence anchors for review and rejects caller status overrides; **server-generated recognition confidence is still missing**.
- [x] Write handlers cap request bodies and reject unknown JSON fields.
- [x] Development-only sample-data seeding and synthetic event broadcasting, disabled unless `-dev` is enabled.

#### Realtime and dashboard foundations

- [x] Shop-partitioned WebSocket hub with versioned event envelopes.
- [x] Same-origin WebSocket policy by default, ping/pong keepalive and slow-client eviction.
- [x] Browser reconnect with exponential backoff and jitter.
- [x] Browser envelope checks reject malformed messages, unsupported versions/types and mismatched shop IDs.
- [x] SPA routing for Overview, Transactions and Inventory.
- [x] Overview renders the live feed, low-stock alerts and KPIs from fetched records; **complete server-side totals remain unfinished**.
- [x] All three views (Overview, Transactions, Inventory) refetch snapshots after reconnect.
- [x] Transaction view renders the ledger, type/status filters and review cards with working Confirm and Reject actions.
- [x] Inventory view renders products, aliases, packaging, low-stock indicators, search, and working Add/Edit modals.
- [x] Server-provided display text uses text-based DOM insertion.
- [x] TypeScript build/typecheck configuration and committed npm/Go dependency lockfiles.

### Where to continue

**Next: Milestone 2 — Usable owner workflow completion & Milestone 3 — Voice loop.** Review actions, catalogue add/edit forms, all-view reconnect recovery, and dashboard styling have landed. Remaining in Milestone 2: server-side summary endpoint (`GET /api/summary`) and paginated server filters. Next major deliverable is the Rust edge audio capture engine and authenticated device pairing.

Update this checked list and the relevant roadmap task in the same PR when each feature is implemented. Record validation separately so “implemented” and “verified” remain distinguishable.

### Confirmed gaps in the current code

- Production routes now resolve shops from persisted owner sessions. Device pairing is still missing; the query resolver remains only a development/test helper.
- `POST /api/transactions` commits confirmed ledger/stock changes together and requires an `Idempotency-Key`. Review records do not change stock until confirmed.
- Caller-supplied status overrides are rejected. Confidence must include all anchors in [0,1]; the recognition engine that generates those scores is still pending.
- There is no `POST /api/captures` or device authentication contract.
- Confidence scores are supplied by the client; no catalogue recognition engine generates them.
- Overview totals use at most the newest 500 records, not a complete daily aggregate.
- Snapshot replacement can race with incoming events; timestamp/version reconciliation is not implemented.
- Authentication, ledger, and review API tests and Go/frontend CI have been added. Edge and wider browser integration tests remain pending.

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

All roadmap checkboxes below are **remaining work**, not completed features.

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
- [Edge status](edge/README.md): current edge placeholder.

This README is the MVP delivery checklist. Some older TODO statements are stale: all-view reconnect recovery and universal transaction deduplication are not implemented. Reconcile those statements when the corresponding work lands. Repository code and verified tests take precedence over completion labels in documentation.

## 1. Identity, onboarding and device access

- [x] Add owner signup, login, logout and a defined account-recovery process.
- [x] Store passwords securely if password-based authentication is selected; add session expiry/revocation, secure cookies, appropriate CSRF protection and login throttling.
- [ ] Add persisted shop ownership and settings: name, currency, timezone, confidence threshold and sale/restock closing phrases.
- [x] Replace query-string identity with authenticated shop membership for both HTTP and WebSocket requests.
- [ ] Pair edge devices to a shop using revocable credentials; support unpairing and credential rotation.
- [ ] Derive device/shop identity server-side; reject attempts to submit records for another shop.
- [ ] Provide a simple onboarding flow: create shop → load catalogue/opening stock → configure microphone/device → test capture.
- [x] Keep destructive development endpoints disabled outside explicit local development.

**Acceptance:** two test owners cannot access each other's catalogue, ledger or live feed. Revoking a device prevents further submissions. Closing a session removes access.

## 2. Catalogue and stock setup

- [x] Wire the Add Product form to the existing API.
- [x] Add product editing API and UI for names, SKU, selling price, aliases, reorder threshold and packaging.
- [ ] Validate duplicate SKUs and ambiguous aliases with clear owner feedback.
- [x] Define one base unit per product and validate packaging paths, including chained conversions such as carton → pack → bottle.
- [x] Reject cycles, ambiguous paths, unsupported units, non-positive factors and integer overflow. MVP quantities are positive whole units.
- [ ] Prevent base-unit/packaging edits from silently reinterpreting historical movements.
- [ ] Support explicit opening-stock entry and reasoned stock-count corrections with an audit trail.
- [ ] Provide product movement history and safe product archiving without deleting historical transactions.
- [ ] Hold unknown products for review; allow an owner to create or select a product before confirmation.

**Acceptance:** an owner can set up the pilot's 20–50 products without developer commands. Restocking three cartons correctly changes stock in the configured base unit.

## 3. Transaction and inventory correctness

- [x] Create a service operation that validates, records the ledger entry and changes stock in one database transaction.
- [x] Confirmed sales decrease stock; confirmed restocks increase it. Review-required and rejected captures have no stock effect.
- [x] Convert spoken packaging units into base units before adjusting stock.
- [x] Require a shop-scoped `Idempotency-Key`, persisted with a payload hash and unique database constraint. Device identity is added when pairing/capture intake is implemented.
- [x] Return the existing result for a matching retry; reject reuse of a capture ID with conflicting content.
- [x] Enforce legal status transitions and prevent repeated confirmation or concurrent requests from applying stock twice.
- [x] Define insufficient-stock handling: block or hold for review; never silently create negative stock.
- [x] Reject arbitrary client status overrides and derive structured-ingestion status server-side. Recognition confidence generation and owner review confirmation are separate pending work.
- [ ] Validate product identity, positive quantity, monetary values, currency and confidence ranges.
- [x] Keep sales revenue separate from purchase/restock cost and enforce the shop currency for the initial MVP.
- [ ] Add auditable corrections/reversals so an incorrect confirmed capture can be fixed without destroying history.
- [ ] Publish events only after commit; ensure failed event delivery can be recovered from durable state.
- [ ] Group multi-line receipts and explicitly define whether confirmation is receipt-wide or line-by-line; prevent silent partial recording.

**Acceptance:** retry, simultaneous confirmation, server crash and insufficient-stock scenarios never produce partial or duplicate stock effects. Every adjustment is traceable.

## 4. Validation and recognition decisions

- [ ] Build catalogue matching: exact SKU/name → configured aliases → conservative fuzzy matching.
- [ ] Resolve matches within the authenticated shop only.
- [ ] Parse spoken quantity, amount, unit and product; distinguish total amount from unit price.
- [ ] Track missing and uncertain anchors individually; do not treat an absent score as evidence of correctness.
- [ ] Compute meaningful confidence/validation results rather than blindly trusting supplied scores.
- [ ] Apply configurable shop thresholds; flag ambiguous products, unexpected units and implausible amounts.
- [ ] Distinguish sales from restocking by explicit capture context and configured boundary phrases.
- [ ] Handle corrections, repeated phrases, incomplete speech and unrelated conversation without silently creating sales.
- [ ] Preserve the original transcript, extracted values, reason for review and eventual owner correction.
- [ ] Build a representative set of English shop phrases and catalogue aliases for regression evaluation.

**Acceptance:** ambiguous or incomplete phrases go to review. A low product score cannot be hidden by a high average, and a closing phrase alone does not create a transaction.

## 5. Rust audio and edge capture

- [ ] Initialise the Rust project and define a versioned capture contract with the backend.
- [ ] Implement microphone selection, permission/error handling and an obvious listening/paused indicator.
- [ ] Capture PCM audio and add voice activity detection to avoid processing silence continuously.
- [ ] Integrate and benchmark a local speech-to-text model on the chosen prototype hardware.
- [ ] Detect sale and restock closing phrases; implement bounded capture sessions and incomplete-capture timeouts.
- [ ] Extract structured anchors and support multi-line spoken supplier receipts.
- [ ] Send transcript, extracted lines, confidence data, capture ID, timestamp, device identity and boundary through an authenticated `POST /api/captures` contract.
- [ ] Persist a bounded offline queue; retry with backoff, keep stable capture IDs, and remove queued captures only after acknowledgment.
- [ ] Survive power/process restarts without losing acknowledged records or duplicating queued ones.
- [ ] Expose microphone, model, backend and queue health with actionable errors.
- [ ] Discard raw audio after processing by default; document any temporary buffering and cleanup.
- [ ] Provide repeatable setup instructions for the supported hardware and model files.

**Acceptance:** actual microphone speech reaches the ledger through the normal application path. Network loss is visible; pending captures synchronise once connectivity returns.

## 6. Review and manual recovery

- [x] Add authenticated confirm, correct-and-confirm and reject operations.
- [x] Show original transcript, candidate product, quantity, amount, confidence and reason for review.
- [ ] Allow product selection, quantity/unit correction and amount correction before confirmation.
- [x] Apply confirmation and stock movement atomically with duplicate/stale-action protection.
- [ ] Record who made a decision, when, and which values changed.
- [x] Refresh the ledger, review count, inventory and totals after a decision.
- [ ] Provide a minimal manual sale/restock fallback using the same validation and stock service.
- [ ] Clearly display rejected, corrected and reversed records without counting them incorrectly.

**Acceptance:** an owner can resolve uncertainty or record a missed capture without editing the database. Two clicks or two devices cannot confirm a record twice.

## 7. Dashboard, reporting and settings

- [x] Add a responsive stylesheet and usable mobile navigation, tables/cards and forms.
- [ ] Implement clear loading, empty, error, offline, saving and success states.
- [ ] Make controls keyboard accessible with labels, visible focus and readable contrast.
- [ ] Add server-side daily summaries for confirmed revenue, sales count and pending reviews; use shop timezone and currency.
- [ ] Add paginated ledger history and server-side date/type/status/product filters.
- [x] Finish catalogue add/edit/archive and review controls.
- [ ] Display sales, restocks, purchase costs and stock adjustments distinctly.
- [ ] Add basic analytics: date-range sales totals, transaction counts and product movement.
- [ ] Add basic CSV ledger export for pilot reconciliation, protecting spreadsheet consumers from formula injection.
- [ ] Add settings for shop details, phrases, thresholds and paired devices.
- [ ] Display capture/device health and pending synchronisation separately from dashboard socket health.

**Acceptance:** the owner can operate the pilot on a phone. Totals remain correct beyond 500 transactions and at shop-local day boundaries.

## 8. Realtime consistency and recovery

- [x] Refetch state after reconnect in every mounted view.
- [x] Apply all transaction events by ID, including overview new-record events.
- [ ] Implement snapshot/event sequencing or versioning so an older response cannot overwrite a newer update.
- [ ] Handle the initial-load connection gap as well as subsequent reconnects.
- [ ] Recover dropped events without relying on an unrelated future reconnect; choose resync signalling, version checks or periodic reconciliation.
- [ ] Cancel/ignore obsolete view requests and clean up listeners when navigating.
- [ ] Bound retained client-side history and maintain deterministic ledger ordering.

**Acceptance:** disconnect, reconnect, delayed responses, repeated events and navigation do not lose records, duplicate totals or leave stale inventory.

## 9. Tests, CI and developer workflow

- [ ] Add storage tests for fresh/repeated migrations, shop isolation, atomic stock changes, rollback, insufficient stock and idempotency.
- [ ] Add API tests for authentication, cross-shop access, malformed input, status rules and duplicate submissions.
- [ ] Add recognition tests for aliases, amounts, quantities, boundaries, unit conversions and ambiguous captures.
- [ ] Add review/concurrency tests proving stock changes exactly once.
- [ ] Add WebSocket tests for shop partitioning, reconnect recovery and slow clients.
- [ ] Add frontend tests for totals, review actions, forms, duplicate events and stale snapshots.
- [ ] Add a microphone/fixture-driven end-to-end test covering sale, review, restock and offline retry.
- [x] Configure GitHub Actions for Go formatting/vet/race tests/build and frontend typecheck/build.
- [ ] Add Rust formatting/clippy/tests when the crate exists.
- [ ] Add a root task runner for build, test and local development.
- [ ] Document dependencies, configuration, migrations and test fixtures; maintain README/TODO as changes land.
- [ ] Configure required checks when CI exists, without claiming absent checks have passed.

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

- [ ] Document a supported deployment topology and where audio processing, database and dashboard run.
- [ ] Serve the frontend over HTTPS with only the intended built assets/templates.
- [ ] Keep SQLite on persistent storage; document the single-writer deployment assumptions before scaling replicas.
- [ ] Automate backups and verify restoration into a clean environment.
- [ ] Document migration, deployment and rollback procedures.
- [ ] Add request IDs, actionable error logs and readiness checks; monitor device health, failed captures, queue backlog and dropped events.
- [ ] Add suitable request limits/rate limits to capture and authentication endpoints.
- [ ] Manage secrets through deployment configuration and keep credentials/transcripts out of unnecessary logs.
- [ ] Add an owner/staff notice explaining listening, pausing, captured data and raw-audio handling.
- [ ] Define transcript/ledger retention, access, export and deletion procedures.
- [ ] Document support and recovery for a disconnected microphone, full disk, failed model load and prolonged network outage.

**Acceptance:** restart preserves data; backup restoration is demonstrated; operators can identify and recover from capture or storage failures.

## 11. Pilot evidence and release gates

- [ ] Train staff on product + quantity + amount + the closing phrase; include the distinct restock phrase.
- [ ] Test in one controlled shop, then 1–2 shops alongside independent existing records.
- [ ] Record missed transactions, false captures, product/quantity/amount accuracy and full-transaction accuracy.
- [ ] Measure correction rate, latency, offline recovery and staff protocol compliance.
- [ ] Record hardware, model/software version and test conditions with evaluation results.
- [ ] Agree measurable accuracy/latency/correction targets before the trial; do not invent or claim achieved targets.
- [ ] Reconcile stock and revenue with ground truth and investigate every unexplained difference.
- [ ] Expand to roughly five shops, then 10–15 only after the preceding trial meets the agreed gates.

### MVP completion checklist

- [ ] A new owner can onboard a shop and its catalogue without developer assistance.
- [ ] A paired microphone device records real spoken sales and multi-line restocks.
- [ ] Confirmed movements change stock exactly once; uncertain captures remain harmless until resolved.
- [ ] Owner review/correction, manual fallback and product management work end to end.
- [ ] Totals, inventory and history stay correct through reconnects, retries and restarts.
- [ ] Shop isolation, device revocation, automated checks and backup/restore are verified.
- [ ] Pilot results meet predefined targets with documented evidence.

## Recommended delivery sequence

| Milestone | Deliverable | Exit condition |
|---|---|---|
| 1. Trusted ledger | Authentication, atomic stock service, idempotency, core tests/CI | Structured sale/restock requests are secure and inventory-correct |
| 2. Usable owner workflow | Catalogue forms, review actions, styling, summaries and realtime fixes | Owner can operate and reconcile a controlled manual-input pilot |
| 3. Voice loop | Rust capture, STT, extraction, validation, authenticated intake and offline queue | Actual speech passes the sale/restock/review acceptance examples |
| 4. Operational MVP | Reporting/settings, privacy controls, deployment, backup and recovery | Restart/outage/security and release checks pass |
| 5. Evidence | Staff training and controlled shop trials | Agreed accuracy and usability gates are met |

Implement a small vertical slice at each milestone; avoid marking a component complete merely because its UI or endpoint exists.

## Local development — current scaffold

Prerequisites: Go 1.25 or a compatible newer toolchain, a C compiler for `mattn/go-sqlite3`, and Node.js/npm for TypeScript. Rust is needed when the edge crate is implemented.

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

### Validation record — completed increments

- Authentication merged in PR #3 after both GitHub CI runs passed.
- Atomic ledger local validation: Go race tests and vet passed, including concurrent retry, rollback, restart persistence, cross-shop rejection, currency checks and chained packaging conversion.
- Atomic ledger remote CI must pass before merge.

#### Authentication

- Frontend typecheck and build passed locally.
- Authentication integration coverage includes shop isolation, WebSocket access, password recovery/code reuse, session revocation, logout, CSRF rejection and persisted throttling.
- Go vet and `go test -race ./...` passed locally. Remote CI must also pass before merge.
- Device pairing, email recovery, full browser automation and real-shop acceptance remain unverified/unimplemented as applicable.

## Beyond the first MVP

Dedicated rechargeable hardware, receipt-photo OCR, additional languages, overlapping speakers/multiple counters, advanced profitability/cost accounting, expenses, credit/debt management, cross-shop analytics and commercial billing remain later work. Preserve the hardware-plus-software vision while proving accurate capture on existing hardware first.
