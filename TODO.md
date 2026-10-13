# TODO — ListenLedger

## Voice loop, validation engine and edge capture increment (13 October 2026)

Implemented server-side validation and recognition decision engine (`backend/validate/`) with exact SKU, exact name, configured alias ("Big Peak" -> "Peak Milk 400g"), and fuzzy matching, spoken number and currency parsing, boundary detection ("Thank you" sale vs "Stock received" restock), Blueprint §11 weakest-anchor confidence scoring, and multi-line receipt parsing. Implemented edge intake endpoint (`POST /api/captures`) with device authentication and atomic ledger/stock updates. Implemented edge device pairing (`POST /api/devices`) and revocation (`POST /api/devices/{id}/revoke`). Implemented server-side daily summary (`GET /api/summary`) and wired to overview KPIs. Implemented Rust edge engine (`edge/`) with audio framing, VAD, privacy-preserving zeroing, STT interface, boundary extraction, durable disk-persisted offline queue, and authenticated HTTP client. Next: physical microphone driver validation and release hardening.

Status: the full processing loop (Hear → Understand → Validate → Record → Update) is now implemented across the Go orchestrator, SQLite store, edge crate, and dashboard.

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
      - `GET /api/transactions?shop_id=&limit=` — newest first, limit clamped to
        [1, 500], default 50
      - `GET /api/inventory?shop_id=` — full catalogue plus `low_stock_count`
- [x] Both snapshot responses carry `generated_at`.
- [x] Ingestion endpoints, `backend/api/products.go` and
      `transactions.go`:
      - `POST /api/products` — add a catalogue entry; publishes
        `product.created`
      - `POST /api/transactions` — record a capture; publishes
        `transaction.created`, or `transaction.review_required` when the
        capture is held
      - `POST /api/captures` — **the edge intake endpoint**; accepts device
        tokens, executes validation engine, and atomically adjusts stock or holds for review.
      - `GET /api/summary` — server-authoritative daily overview metrics.
      - `POST /api/devices` and `POST /api/devices/{id}/revoke` — device pairing and revocation.
- [x] `POST /api/dev/seed?shop_id=` (`backend/api/dev.go`) — **`-dev` only**.
      Wipes the shop, then inserts the blueprint's own worked examples: §9's
      Peak Milk aliases, §10's restock receipt, §11's 55%/98%/97% capture.
      Publishes the seeded state so an open dashboard updates without a reload.

Remaining:

- [ ] Add request-scoped logging (request ID) and a `recoverer` middleware so
      one panicking handler cannot take the process down.
- [ ] Restrict static serving to built assets. `http.FileServer` currently
      exposes the TypeScript sources and build config too — fine for the pilot,
      not for production.

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

- [ ] **IDs are minted ad hoc, not settled.** `api.newID(prefix)` returns
      `prd_`/`txn_` plus 16 random bytes as hex. That is collision-safe and
      greppable, but it is not the ULID this file previously recommended, and it
      is not sortable — so a ledger's creation order is not recoverable from the
      identifier. Settle it before the edge engine starts posting captures, and
      change it in one place (`api.newID`), since storage never invents IDs.
- [ ] **Unit conversion is unimplemented.** `AdjustStock` takes a delta in the
      product's base unit; nothing converts "3 cartons" using
      `Product.Packaging` yet. Until that exists, a restock spoken in cartons
      must not be allowed near stock levels. The seed data deliberately contains
      such a transaction (`txn_seed_0005`, 3 cartons of Milo against a product
      stocked in packs) so the gap is visible rather than latent.
- [ ] **Resolving a review must be atomic.** `UpdateTransactionStatus` changes
      the status only. Confirming a held capture has to update the row *and*
      apply its inventory effect in one transaction, or a crash between them
      either loses a sale or double-counts one. Build this as a service method
      wrapping both, and publish the WebSocket event only after it commits.
      This is the same missing method that blocks §1's stock adjustment.
- [ ] Store tests: migration from empty, migration idempotency, shop isolation
      (a query for shop A must never return shop B's rows), `AdjustStock`
      refusing a negative result, and empty-slice JSON encoding.
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

Remaining:

- [ ] **Replace `auth.QueryShopResolver` with real authentication.** It takes
      the shop from `?shop_id=`, so any caller can read any shop's ledger and
      subscribe to any shop's feed. The `auth.ShopResolver` seam is the only
      thing to change; both `/ws` and `/api/` resolve through it.
- [ ] Add hub tests: concurrent publish/register/unregister, shop isolation,
      slow-client eviction.
- [ ] Watch `/healthz`'s `ws_events_dropped` during the pilot. Events are shed
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
- [x] The overview's KPIs are derived from the fetched page, not a server
      aggregate: implemented `GET /api/summary` with timezone-aware trading day aggregates.
- [ ] Ledger and catalogue filters are applied client-side over the fetched
      page. Fine at pilot size; must move into the query when history grows.
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

## 6. Rust edge engine — DONE (`edge/`)

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
- [ ] Add a root `Makefile` or task runner with `build`, `test`, `dev` targets.
      Registering the `-dev` flag correctly matters here: `go run .` alone
      starts a process with the destructive `/api/dev/seed` endpoint absent,
      which is the safe default but a confusing first experience.
- [ ] Set up CI: `gofmt -l`, `go vet`, `go test ./...`, `tsc --noEmit`,
      `cargo clippy`.

## Open decisions

- **Authentication.** `ShopID` is on both models, leads every index, and routes
  every event — but nothing proves a caller owns the shop it claims. This is
  the highest-priority gap; everything else is a feature.
- **Money:** integer minor units end to end. Confirm the frontend formats from
  minor units rather than converting to a decimal at the API boundary.
- **Confidence threshold:** per-shop setting (blueprint §11). A starting value
  now exists — `reviewThreshold = 0.75` in `backend/api/transactions.go`, with
  the weakest anchor deciding rather than the average — but it is still a
  constant in the transport layer. It belongs on the shop record and in the
  validation engine, which is where matching will actually assign the scores.
- **Snapshot/realtime ordering:** the client signals `resyncRequired` and the
  snapshot endpoint now exists, but nothing connects the two yet, and
  `generated_at` is only useful if the client actually compares it against
  `sent_at` on incoming events.
- **SQLite driver:** `mattn/go-sqlite3` needs CGO and a C toolchain. If the
  orchestrator ever has to cross-compile (a shop-local Pi, a scratch container
  without gcc), swapping to the pure-Go `modernc.org/sqlite` is a one-line
  import and driver-name change.

