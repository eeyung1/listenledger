# ListenLedger codebase and MVP readiness audit

Audit date: 19 October 2026. Source baseline: `4337c78` on local `main`. Scope: browser-first, one shop, one counter, one cashier, 20–50 products; sales **and** restocks, human review, manual fallback and independent reconciliation. Read alongside [realProductpath.md](realProductpath.md), [frontreadme.md](frontreadme.md) and [README.md](README.md).

## Assessment and percentage remaining

**Approximately 55% ready, with 45% remaining to a field-validated MVP.** Treat 40–50% remaining as a judgment range, not statistical confidence or a time estimate. The ledger foundation is substantial; reliable real-world speech capture, safe interruption recovery and demonstrated customer value are unfinished. No labelled shop dataset or completed pilot session is recorded in the reviewed checkout.

These figures answer different questions:

| Outcome | Estimated readiness | Remaining | Interpretation |
|---|---:|---:|---|
| Begin P1 real-counter observation/recording | No further app development required | 0% additional application engineering | Consent, notice, shop/cashier arrangements, secure recording and labels are still prerequisites; their operational completion is unknown. |
| Start a supervised trial of the application at a counter | About 70% | About 30% (25–35% range) | Fix critical capture/queue gaps, correct notices and verify the actual build on physical devices; independent records remain authoritative. |
| Call the product an accepted, field-validated MVP | About 55% | About 45% (40–50% range) | Requires P1–P7 evidence and successful two-week reconciliation, not just source completion. |
| Finish the entire frontend redesign checklist | 15.6% checked | 84.4% unchecked | Exactly 7/45 checked at this baseline. Much of this scope is not required to begin field discovery. |

### How the MVP estimate was calculated

Weights reflect importance to this product's core promise. Readiness ratings are engineering judgments from implementation and acceptance evidence; they are not measured fractions of remaining labor. Weighted readiness = sum(weight × readiness) / 100 = **55.5%**, rounded to about 55%.

| Workstream | Weight | Readiness rating | Weighted points | Basis |
|---|---:|---:|---:|---|
| Ledger, stock, identity, catalogue and review foundations | 20 | 90% | 18.0 | Atomic receipt/stock writes, audit and authorization exist with regression coverage; hosted acceptance remains. |
| Essential owner/cashier UI | 15 | 80% | 12.0 | Existing routes support core work; operator/device usability is not established. |
| Capture, recognition and understanding | 25 | 50% | 12.5 | Several P4 fixes merged; P4.2/P4.5 and P5 defects remain; no real-audio accuracy measurement. |
| Offline operation and interruption recovery | 10 | 40% | 4.0 | Queue exists but manual replay and shop binding have correctness gaps; opening the manual form requires a catalogue fetch. |
| Privacy and operational instructions | 10 | 40% | 4.0 | New raw snippets disabled; legacy data, notice accuracy and enforcement still need work. |
| Hosting, release and restore acceptance | 10 | 50% | 5.0 | Source/tooling and a historical deployment note exist; current release/schema/restore are not verified here. |
| Shop evidence and product acceptance | 10 | 0% | 0.0 | Evidence register is empty; synthetic trial figures do not qualify. |
| **Total** | **100** | | **55.5** | Hard gates remain binding regardless of score. |

The supervised-trial estimate uses four equal areas: core app availability 90%, safe capture/recovery 50%, privacy/release verification 50%, and the documented trial protocol/evidence plan 90%; average 70%. The last rating credits the written plan, not completed owner agreement, staff training or collected evidence. Those remain mandatory before the trial. Reassess both estimates after P1/P2: evidence may reveal a small tuning task or a necessary change of capture mode/engine.

## What the codebase already provides

The production path is browser TypeScript → authenticated Go HTTP intake → parsing/catalogue validation → atomic ledger/stock storage → WebSocket updates and dashboard refresh. SQLite and PostgreSQL implementations exist. The optional Rust edge runner is a separate scaffold and remains parked for the phone pilot.

| Area | Source evidence | Assessment |
|---|---|---|
| Business transaction core | `backend/storage/ledger.go`, `backend/api/captures.go`, receipt failure/concurrency tests | Receipt movements commit together; request fingerprints support replay/conflict checks; held captures avoid stock changes; confirm/reject/reverse workflows exist. The manual endpoint does not inherit retry safety merely by calling this storage layer. |
| Catalogue and stock | `backend/storage/products.go`, `backend/models/packaging.go`, inventory/product APIs and views | Aliases, packaging conversion, import/edit/archive, opening stock and adjustments exist. Real-owner setup and mixed-unit correctness need acceptance. |
| Identity and isolation | `backend/auth/session.go`, device APIs, deployment-isolation tests | Sessions, recovery and server-side shop separation exist. A separate client queue association bug can submit data under the wrong legitimate session. |
| Frontend workflows | `frontend/src/app.ts`, `views/home.ts`, `views/transactions.ts`, `views/inventory.ts`, `views/settings.ts` | Four routes; capture remains on Home. Ledger/review/manual forms and settings are implemented. Dedicated Counter, Customers and Expenses routes are not registered. |
| Realtime | `backend/ws/`, `frontend/src/ws.ts`, filtered-update/resync tests | Shop partitioning, reconnect and stale snapshot/event handling have coverage. Real hosted/mobile interruptions remain unverified. |
| Browser voice loop | `frontend/src/audio.ts`, home view and `backend/validate/` | A functional integration exists, including final-result gating, rolling speech buffer, configurable language and restart backoff. Existence does not establish noisy-shop accuracy or iOS/Android reliability. |
| Operations | migration parity checker, backup scripts, readiness handlers, CI workflow | Useful preparation exists. Current deployment, restricted-role schema and restoration were not accessed during this audit. |

## Findings that change the readiness assessment

### 1. Manual retries can duplicate a recorded movement — high priority

`frontend/src/api.ts:createManualTransaction` sends no stable request ID. `frontend/src/offline_queue.ts:syncOfflineQueue` repeats that call. `backend/api/transactions.go:handleTransactionsManual` generates a fresh transaction ID and a fresh `man_<id>_<time>` idempotency key for every request.

If the server commits a manual sale and its response is lost, retrying the queued entry is another sale. Storage-level idempotency cannot deduplicate different keys. The existing queue test models a failed transport followed by success; it does not model commit followed by acknowledgement loss.

Required repair: generate one ID before the initial manual POST, persist/reuse it for retries, and enforce the same shop-scoped fingerprint/replay semantics server-side. Acceptance: one receipt and one stock change after a lost response, retry, reload and concurrent replay.

An isolated in-process API probe submitted the same two-pack sale twice against opening stock 20. Both requests returned 201 with different transaction IDs; stock became **16**, rather than 18 if the second request were a retry of the first. This proves the endpoint lacks a way to distinguish that retry; it does not claim a field duplicate was observed.

### 2. Local queue replay is not bound to the signed-in shop — high priority

Queue entries contain `shop_id`, but the queue database/key is global to the origin and `syncOfflineQueue` iterates all entries without checking the active identity. Logout reloads the page without an account-aware replay boundary. A runtime probe queued spoken entries for two different shops and observed both posted by one sync, with neither original shop included in the request body.

The server correctly derives the destination shop from the current authenticated session. That does not protect a transcript queued under an earlier account: it can be matched/recorded against the new account's catalogue, or create an inappropriate review item. Manual product IDs may fail cross-shop lookup, but that is not a complete queue isolation policy.

Required repair: scope reads, badges and replay to the active identity; preserve other accounts' entries; recheck identity across in-flight sync and reject/quarantine legacy unscoped entries. Validate with account A → logout → account B → reconnect, including multiple tabs.

Repaired on branch `feat/p4-8-queue-shop-isolation` (base `71787e7`): queue reads (`getQueuedCaptures`) and counter badges (`getQueuedCount`) bind to the active authenticated shop; `syncOfflineQueue` replays only captures matching the active shop while preserving other shops' pending data in local storage; legacy unscoped entries missing `shop_id` are quarantined rather than submitted under arbitrary accounts; and in-flight identity checks immediately abort replay if the user signs out or switches accounts mid-sync. Covered by unit tests 8–12 in `frontend/test/offline_queue.test.mjs`. Multi-account physical-device acceptance remains open.

### 3. Manual fallback is only partially available offline — high priority

Opening the manual form in `frontend/src/views/transactions.ts` always fetches inventory. On failure the product selector contains “Failed to load products”; submission requires a selected product. Thus a form loaded while online can queue a later failed POST, but opening the form after an outage is not a complete fallback. Initial app authentication and route templates also require network access; no offline app-shell/catalogue implementation was found in the reviewed path.

Queue persistence falls back from IndexedDB to localStorage and finally memory. A memory-only queue cannot survive browser termination; user messaging must reflect the actual storage guarantee. Verify browser eviction/quota, transaction-commit failure and reload recovery on physical devices.

### 4. P4.2 still turns unrelated closing phrases into review work — high priority

`detectClosingBoundary` accepts “thank you” alone. `assembleCaptureTranscript` returns that phrase if it finds no preceding anchors. The listener still submits it; `backend/validate/engine.go` deliberately creates an “Unintelligible speech” review line when parsing yields no lines.

A busy counter can accumulate reviews unrelated to transactions. Add no-anchor suppression and a diagnostic counter without hiding legitimate partial sales. Test ten stray closings with zero reviews and genuine sales interspersed.

### 5. Several parsing problems remain in current source — high priority

- `ParseSpokenSegment` parses numbers before catalogue matching; `cleanProductMention` strips number words and numeric content. Number-bearing brands require catalogue-aware parsing, then real-transcript evaluation.
- Unit extraction iterates `knownUnits`, a Go map, taking the first match. Mixed units can be assigned inconsistently rather than attached to their own quantities/lines.
- `DetectCancellation` uses substring matching across the whole transcript. Ordinary instructions such as “drop that bag there…” can cancel a sale.
- `MatchProduct` uses exact/alias/spelling similarity; phonetic matching and a measured correction-to-alias workflow are absent.
- Quantity defaults to one when amount/product are present, and the engine usually assigns quantity confidence 0.98. That number is a heuristic, not measured certainty.
- Browser recognition still requests one alternative. Alternative transcripts and recognizer confidence are not used to resolve ambiguity.

Repair and regression-test demonstrable errors, but choose broader parser/LLM/STT changes using P1/P2 data. An LLM, phonetic library or particular language default is not automatically required if measured results support a simpler solution.

Observed results from an isolated Go probe of the actual parser/engine:

| Input / experiment | Observed result | Interpretation |
|---|---|---|
| “Two Five Alive, one thousand naira” | Product `Alive`, quantity 1, amount ₦2,500 | Product, quantity and amount all wrong. |
| “Two Seven Up, one thousand naira” | Product `Up`, quantity 2, amount ₦1,000 | Number word removed from product. |
| “Two 7Up, one thousand naira” | Product `7Up`, quantity 2, amount ₦1,000 | This particular numeric spelling passed; do not generalize the defect to every spelling. Keep it in regression/field coverage. |
| “Two Three Crowns, one thousand naira” | Product `Crowns`, quantity 2, amount ₦1,000 | Number word removed from product. |
| “Milo, four thousand naira” | Quantity 1, `QuantityFound=true` | Inferred quantity is not distinguished from explicit speech. |
| “Drop that bag there, two Milo four thousand naira thank you” | Cancellation detected as `drop that` | Ordinary instructions trigger cancellation. |
| Same mixed carton/pack segment parsed 200 times | `carton` 64 times, `pack` 136 times | Unit choice varied within one run. Counts are diagnostic observations, not field probabilities. |
| “Thank you” with no anchors | One `Unintelligible speech` review line | Confirms P4.2's review-noise path in the engine. |

### 6. Privacy implementation and instructions do not yet agree — release gate

P0b is merged: the browser does not create new snippet recordings, uploads/queues strip audio, and capture intake clears audio and rejects detected credit attribution. This is stronger than the stale “unmerged candidate” wording in the README/roadmap.

It is not a complete P6 implementation. Legacy transaction audio remains selectable and playable; a transcript/audio purge endpoint exists but no scheduled policy enforcement or audio opt-in was found. Staff training and legacy runbook sections still promise that audio is never uploaded, which does not describe the current browser recognition path. The purge handler takes a JSON request body; the runbook's query-string retention example does not control that handler's cutoff.

For the first trial, keep application raw-audio retention disabled, make processing/retention notices truthful, decide and verify legacy-data handling, and confirm the deployed build follows the policy. Optional review-only recording should wait for its own consent/access/retention design and acceptance.

### 7. Recognition/device reliability remains unmeasured — product gate

P4.1, P4.3, P4.4, P4.7, P4.8 and P4.9 improvements are present in main. None establishes microphone performance on real phones. Recognition still shares microphone resources with a getUserMedia analyser; backgrounding, calls, permission changes, repeated starts, wake locks and readback feedback require physical checks. Voice readback includes phrases such as “Stock received”; verify it cannot be recaptured as another transaction.

The roadmap's 10-second payment pause and exactly-one-sale checks should be extended to consecutive sales in one recognition session, restarted sessions and a customer uttering the closing phrase. P2 must determine engine/language accuracy, and P3 must determine ambient versus push-to-talk suitability.

## Reconciliation of the three planning documents

| Document | What it tells us | Correction or interpretation |
|---|---|---|
| `realProductpath.md` | P0–P7 define the actual field-facing priorities and acceptance | Correct P0b and P4.4 merge status; keep device/deployment acceptance open; remove the unsupported manual “exactly once” claim; add queue isolation/offline availability and the field register. |
| `frontreadme.md` | 7 checked features: F01, F02, F05, F06, F08, F34, F36; 38 unchecked | Opening paragraph mentions only F01/F02, while later evidence records more. F03 remains unchecked and only four routes exist; the final directive mentions F04. Do not infer F03 completion or count all redesign tasks as MVP blockers. The dated owner directive allows existing redesign work without waiving field gates. |
| `README.md` | 167 top-level checked tasks, 21 unchecked (88.8% checked) | Repeated foundations, parked scope and stale statements make this a poor completion metric. The MVP-specific checklist has only 3/7 checked; even those statements must be qualified by the manual replay and offline gaps. |
| `TRIAL_REPORT.md` | Explicitly synthetic backend benchmark | Its 100% accuracy and millisecond latencies exclude real microphone/STT/noise/operator performance. They cannot establish clean capture, field latency or willingness to pay. |
| `TODO.md`, staff guide and runbook | Historical implementation notes and operating instructions | “Complete” milestones and broad audio/offline promises are not release evidence. Correct the operating instructions before handing them to staff. |

Additional README drift: it still reports twelve migrations while the current dialects each contain 13 files, versions 1–12 and 14; version 13 is absent and is not needed merely to make numbering contiguous. Apply only the canonical migration 0014 via the documented administrator release sequence. The old architecture table and hardware-oriented instructions should not imply that Rust hardware integration blocks the phone-first pilot. No new migration is needed for this audit.

## What is required before field use

1. Begin P1 arrangements now: owner discussion, consent/notice, secure ordinary-phone recording, ≥3 hours of ground truth, protocol training comparison and willingness-to-pay interview. This can proceed while engineering repairs are made.
2. Repair P4.2, manual retry identity, queue shop binding and actual offline manual availability. Address demonstrated P5 parsing errors before relying on automatically confirmed inventory movements. Test failure after server commit, not just failure before receipt.
3. Verify the actual hosted commit/schema, restricted role, HTTPS/assets, manual/review/stock flows and backup/restore. Correct staff notices and keep new raw snippets disabled.
4. Run physical-device checks on two Android phones and one iPhone. Record unsupported cases rather than promising general compatibility. Use an isolated test shop for destructive/error scenarios.
5. Perform P2 recognition evaluation and P3 mode comparison on real evidence. Agree proposed targets with the owner before scoring the pilot.
6. Run P7 for two weeks alongside independent records, with daily reconciliation and investigation of every unexplained discrepancy. Expand only after agreed gates pass.

The [roadmap field register](realProductpath.md#real-world-testing-register--19-october-2026) specifies the tests per feature. Capture ≥90%, clean capture ≥85%, zero duplicates, false confirmations ≤1%, protocol compliance ≥85%, lower correction effort than manual entry and willingness to pay are **proposals**, not achieved results or agreed commitments. Record numerator/denominator and sample size for every rate.

Do not delay P1 for customer-credit pages, expenses/profit analytics, Rust hardware, cohort dashboards, a framework rewrite or the complete frontend redesign. Do not treat removing these from the pilot UI as removal of their APIs.

## Verification record and limits

- Frontend: `npm ci --no-audit --no-fund`, `npm run typecheck`, and `npm test` in the isolated audit worktree passed; **100 tests passed, zero failed**. The test script runs TypeScript compilation. Tests use simulated DOM/recognition/fetch responses; several fixtures emit caught overview-loading errors despite the passing assertions. This is not physical-browser or hosted workflow evidence.
- Coordination: migration version/dialect parity passed; eight coordination tests passed. These checks do not verify deployed schema or field outcomes.
- Focused frontend runtime probes reproduced all-shop spoken queue replay and manual retry requests without a stable idempotency key. Transport was mocked; no real account was accessed or altered.
- Isolated Go parser/engine/manual API probes passed execution and reproduced the findings documented above, using a temporary synthetic SQLite shop; no real account or data was used.
- Backend: `GOCACHE=/tmp/listenledger-audit-go-cache GOTOOLCHAIN=go1.25.0 TEST_POSTGRES_URL= DATABASE_URL= go test -race ./...` passed across all packages. The first sandbox run could not bind localhost for the TLS test; the approved rerun passed. External database URLs were explicitly disabled, so live PostgreSQL integration tests were skipped; PostgreSQL/hosted acceptance is not established by this local run.
- GitHub connector inspection confirmed remote `main` equals `4337c78f8527e9ccd45f756a2ed7a1cf1838dd77` and there were no open PRs at inspection. `gh` is unavailable, so connector inspection replaced the failed CLI discovery. No physical microphones, shop records, production credentials or live hosting state were inspected.
- Exact-baseline GitHub [CI run 39215978833](https://github.com/eeyung1/listenledger/actions/runs/39215978833) completed successfully. That validates the audited source under its CI workflow, not current deployment or field performance.
- This is a targeted implementation/readiness audit, not an exhaustive security certification. Parked subsystems received scope/status review, not a complete independent revalidation.

Only `realProductpath.md` and this report are product-repository edits. Shared coordination logs are maintained separately in the primary checkout. No application logic, database schema, deployment, merge or field-acceptance checkbox was changed by the audit.
