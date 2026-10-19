# Real Product Path

This document sets the delivery priority for ListenLedger. It comes from a codebase review on **16 October 2026** at `db85661`, which was merged to `main` as #42. Line references were checked against `main` at `72af068`. Until the gates below are passed, it takes precedence over the milestone roadmap in [README.md](README.md) and [frontreadme.md](frontreadme.md).

**Readiness audit — 19 October 2026, source baseline `4337c78`:** approximately **55% ready / 45% remaining** to a field-validated MVP, with a judgment range of **40–50% remaining**. This is an evidence-weighted readiness estimate, not measured development effort or a delivery-date forecast. See [the detailed codebase audit](MVP_READINESS_AUDIT.md) for scoring, code findings and verification limits, and [the field acceptance register](#real-world-testing-register--19-october-2026) below for every remaining field gate. Original problem descriptions and old line numbers below describe the earlier review; dated implementation notes and this audit take precedence. No field gate is closed by a merge or automated test.

## The only question that matters

> **Can a microphone + software reliably turn a structured real-world sales conversation into an accurate inventory transaction without manual data entry?** — Blueprint §24

**Status: unanswered in the available evidence.** This checkout contains no completed field-session evidence rows, labelled real-counter dataset or documented shop pilot results. This does not establish that no unreported testing has occurred elsewhere. `TRIAL_REPORT.md` scores 20 synthetic interactions and is not field evidence. Everything below exists to answer this question as fast and cheaply as possible.

## Working rules while this document is open

1. **Feature freeze.** No new milestones outside this document.
2. **Real-world evidence before merge.** A PR that touches capture, parsing or matching must include something from outside the test suite: a real recording, a screen capture from a real device, or measured results on real transcripts.
3. **Synthetic tests are regression guards, not evidence.** A passing CI run on hand-written sentences does not count as proof of accuracy.
4. **Measure before building.** If a priority asks for data, collect the data before writing code for the next priority.
5. Keep implementation, CI validation and field evidence separate (unchanged from the README).

## Priority order

| # | Priority | Type | Gate to move on |
|---|---|---|---|
| P0 | Freeze and park scope | Decision | Parked features listed and hidden from pilot UI |
| P1 | Field truth test in a real shop | No code | Labelled ground truth from ≥3 hours of real counter audio |
| P2 | Speech-to-text bake-off on real recordings | Mostly scripts | Engine chosen on measured accuracy |
| P3 | Decide capture mode: ambient vs push-to-talk | Field test | Mode chosen on measured capture and correction rates |
| P4 | Fix the browser capture pipeline | Code | Acceptance checks pass on real Android and iOS devices |
| P5 | Fix the understanding layer | Code | Accuracy measured on the P1/P2 real transcript set |
| P6 | Privacy and raw-audio policy | Decision + code | Retention, notice and deletion implemented |
| P7 | Controlled pilot | Field | Metrics meet the agreed targets (see below) |

---

## P0 — Freeze and park scope

These are not needed to answer the core question. Stop extending them. Hide them in the pilot build if they would distract or confuse the shop.

| Feature | Location | Action |
|---|---|---|
| Customer credit book ("Gbese") and spoken credit attribution | `backend/api/customers.go`, `frontend/src/views/customers.ts`, `ExtractCustomer` in `backend/validate/parser.go` | Park. README lists it as beyond the first MVP. |
| Expenses and net profit | `backend/api/expenses.go` | Park |
| Pilot reconciliation engine and multi-shop cohort framework | `backend/api/reconciliation.go`, `backend/eval/` | Park. Use a spreadsheet for pilot reconciliation. |
| Owner target gates agreement and cashier training simulator | `backend/eval/`, onboarding | Park. Agree targets with a real owner on paper. |
| Rust edge runner | `edge/` | Park. The phone is the device for now. **Never run it against a real backend in its current state:** it defaults to synthetic audio (`edge/src/main.rs:59-74`) and a mock recognizer that returns "Two Milo, four thousand naira. Thank you." for any input (`edge/src/stt.rs:33-39,73`). |
| PostgreSQL dual-dialect, transfer tooling, Render/Supabase | `backend/storage/`, `render.yaml`, `DATABASE_MIGRATION.md` | Keep only what is needed to host one pilot. No further work. |
| Spectral visualizer, SNR/VAD badges, grammar boosting | `frontend/src/audio.ts`, `frontend/src/views/home.ts` | Leave inert or remove. Do not count them as accuracy work (see P4.9). |
| Raw audio snippet storage (merged in #42) | `frontend/src/audio.ts`, `frontend/src/views/transactions.ts` | Disable until the P6 decisions are implemented. |

- [ ] Parked features listed above are agreed and hidden from the pilot build where they would confuse users.

### P0 implementation checklist

- [x] P0a: Park credit-book/expense actions and KPIs, edge-device UI, training/target-gate onboarding, and reconciliation/cohort panels in the pilot interface. Preserve existing data and backend capabilities.
- [x] P0a: Replace inaccurate privacy claims with current browser-processing and retention behaviour; explain the transcript queue's connectivity limits.
- [x] P0a: Align README and frontend priorities with this document's feature freeze.
- [x] P0b implementation on `fix/p0-disable-audio-retention`: disable browser snippet recording, strip uploads and new queue entries, discard audio at server intake, and reject detected credit attribution before any ledger write.
- [x] P0b source integration: merged in PR #48 (`1208fb7`); source at `4337c78` discards incoming audio and rejects detected customer/credit attribution before ledger writes.
- [ ] P0b release acceptance: attach real-device evidence, verify exact-head CI and verify the deployed build (working rule 2). Merge history alone does not satisfy this gate.
- [ ] Verify P0b on a real device and deployed build, then close the parent P0 gate.

P0a is a UI-only increment: hidden controls are not an API authorization boundary. Spoken credit attribution is rejected in merged P0b source; the currently deployed commit and behaviour must still be verified. No P1–P7 evidence or capture accuracy is claimed. Automated checks and desktop-browser inspection validate implementation only.


---

## P1 — Field truth test (no new code)

**Goal:** find out what real counter conversations sound like before engineering further.

- [ ] Get the owner's explicit consent and put up a visible notice for customers that audio is being recorded for a trial.
- [ ] Using an ordinary phone voice recorder (not the app), record 3–4 hours of real trading at one counter, including at least one busy period.
- [ ] Keep recordings **out of git**. Store them encrypted and delete them after labelling.
- [ ] Hand-label every sale: time, products, quantities, units, amount, who spoke the price, language used.
- [ ] Measure:
  - [ ] sales per hour
  - [ ] % of sales where product, quantity and amount are all spoken naturally
  - [ ] language mix (English, Pidgin, Yoruba, Igbo, Hausa, mixed)
  - [ ] occurrences of "thank you" **outside** a sale boundary, and who said it
  - [ ] typical pause between the price phrase and the closing phrase
  - [ ] background noise conditions (generator, traffic, music)
- [ ] Train one cashier on the protocol (Product + Quantity + Amount + closing phrase) for one day, then record and label again. Measure protocol compliance and any slowdown or customer friction.
- [ ] Interview the owner:
  - [ ] How are sales and stock recorded today, and what goes wrong?
  - [ ] Is the main pain **effort** (no time to record) or **trust** (staff under-reporting sales)? Voice capture relies on staff choosing to speak each sale, so it does not solve a trust problem on its own.
  - [ ] What do they pay for today (POS app, notebook, bookkeeper)?
  - [ ] Would they pay for this, and how much?

**Deliverable:** a labelled ground-truth sheet (CSV), plus a one-page summary of the measurements above.

---

## P2 — Speech-to-text bake-off on real recordings

**Goal:** pick the speech engine using measured accuracy on P1 audio, not assumptions.

- [ ] Transcribe the P1 recordings with each candidate:
  - [ ] Chrome Web Speech with `en-US` (current baseline)
  - [ ] Chrome Web Speech with `en-NG`
  - [ ] Whisper (small, medium and large-v3)
  - [ ] A cloud STT service with Nigerian English support
- [ ] Run each set of transcripts through the existing parser and matcher (`backend/validate`) and score against the P1 labels:
  - [ ] product accuracy
  - [ ] quantity accuracy
  - [ ] amount accuracy
  - [ ] full-transaction accuracy
  - [ ] latency, cost per hour, and whether it works offline
- [ ] Save the real transcripts (text only, anonymised) as the new regression set in the repo. This replaces hand-written sentences as the main accuracy benchmark.

**Gate:** choose an engine and recognition language based on the numbers.

---

## P3 — Decide capture mode: ambient vs push-to-talk

The blueprint assumes ambient listening with no button. That assumption carries most of the technical risk: segmentation, stray closing phrases, privacy, battery, and mobile browser restarts. Test it rather than assume it.

- [ ] Run half a day in **ambient** mode and half a day in **push-to-talk** mode (hold a button, say "two Milo, four thousand").
- [ ] Compare:
  - [ ] % of real sales captured
  - [ ] % confirmed correctly without correction
  - [ ] seconds of cashier effort per sale
  - [ ] cashier and owner preference
- [ ] Record the decision and the evidence in this file.

**Recommendation:** start with push-to-talk unless ambient mode proves itself in this test.

---

## P4 — Fix the browser capture pipeline

These issues were found by reading the code. Each fix needs an acceptance check on **real devices** (at least two Android phones running Chrome and one iPhone running Safari).

### P4.1 Sales lost when there is a pause before "thank you" — critical

- **Problem:** `onresult` only gathers text from `event.resultIndex` onward (`frontend/src/audio.ts:794`). The protocol has a natural pause while the customer pays, which splits the speech into separate results or sessions (restart at `audio.ts:870-885`). The capture fires with the transcript "Thank you" alone, and the product, quantity and amount are never sent.
- **Fix:** keep a rolling, timestamped buffer of final results across results and sessions. When a closing phrase arrives, look back over a window (for example 60 seconds) for the most recent utterance containing anchors. Alternatively, detect anchor-bearing utterances directly and treat the closing phrase as optional confirmation.
- [ ] **Acceptance:** on a real phone, saying "Two Milo, four thousand naira", waiting 10 seconds, then saying "Thank you" records exactly one correct sale.
- [x] Implementation on branch `feat/p4-1-rolling-transcript-buffer`: `onresult` now reads the full session transcript, keeps a bounded rolling buffer of finalized utterances across results and restarts, and looks back (default 60s) for the most recent anchor-bearing utterance when the closing phrase arrives alone. Covered by automated unit and listener regression tests. Merged to main via PR #49 (`93c2497`). Real-device acceptance above still gates closing the item; no phone evidence yet.

### P4.2 Every "thank you" creates a review item — critical

- **Problem:** anyone saying "thank you", customers included, triggers a capture (`detectClosingBoundary`, `audio.ts:175-231`). Captures with no anchors are held for review as "Unintelligible speech" (`backend/validate/engine.go:204-211`).
- **Fix:** do not submit captures that contain no anchors. On the server, log closing-phrase-only captures as a metric instead of creating review items.
- [ ] **Acceptance:** 10 stray "thank you"s in normal conversation create 0 review items.

### P4.3 One sale may be recorded twice — critical

- **Problem:** boundary detection runs on interim results with no `isFinal` check (`audio.ts:807`). Chrome sends an interim result ending in "thank you" before the final one. The online POST sends no `capture_id` or `Idempotency-Key` (`frontend/src/views/home.ts:927`), and the server mints a new ID every time (`backend/api/captures.go:46-52`). Only the offline queue path is idempotent (`frontend/src/offline_queue.ts:398`).
- **Fix:** act only on final results. Generate the capture ID in the browser at detection time and send it as both `capture_id` and `Idempotency-Key`. Ignore a second boundary for the same result.
- [ ] **Acceptance:** one spoken sale produces exactly one ledger entry and one stock movement on Android Chrome and iOS Safari.
- [x] Implementation on branch `feat/p4-3-final-results-idempotent-capture`: boundary detection now runs only when the changed result is final, a replayed final result (same result position + results length in one session) is ignored, the browser mints the capture id at detection time (`audio.ts` `BoundaryCaptureCandidate.captureId`), and the online POST sends it as `capture_id` and `Idempotency-Key` while the offline queue reuses it on sync. Server replay tested end-to-end (`TestCaptures_ReplayedCaptureIDRecordsOnce`); listener tests cover interim-then-final and replayed-final. Merged to main via PR #51 (`bd1a918`). Real-device acceptance above still gates closing the item.

### P4.4 Recognition language is American English

- [x] Configurable language implementation: canonical PR #56 (`56166be`, opencode), merged as `19e3287`, persists `recognition_language`, exposes a Settings input, and wires the counter listener/readback. Codex's duplicate local implementation is withdrawn; keep only PR #56's migration 0014 per dialect. Default stays en-US pending P2 evidence. Source integration is complete; deployed schema and real-device acceptance remain unverified by this audit.

- **Problem:** `recognition.lang = "en-US"` (`audio.ts:781`). Voice readback is also `en-US` (`audio.ts:450`).
- **Fix:** make the recognition language a shop setting, defaulting to the P2 winner (likely `en-NG`).
- [ ] **Acceptance:** the language is configurable, and the default is backed by P2 results.
- [x] Implementation: `recognition_language` is a per-shop setting (migration `0014_recognition_language`, `storage.UpdateShopSettingsParams`, `PATCH /api/settings`), exposed in the settings form and validated as a BCP-47-style tag. `CounterSpeechListener` accepts a `recognitionLanguage` option plus `setRecognitionLanguage()`, and the home counter listener and voice readback use the shop's language. The default keeps the pre-P4.4 `en-US` baseline; the "default backed by P2 results" acceptance stays open until the P2 bake-off runs. Covered by backend and frontend tests.

### P4.5 Recognizer confidence and alternatives are thrown away

- **Problem:** `maxAlternatives = 1` (`audio.ts:782`), and the recognizer's confidence score is never sent to the server.
- **Fix:** send the top 3–5 alternatives with their confidence scores. Let the matcher try each alternative against the catalogue, and store the scores for analysis.

### P4.6 Microphone contention on mobile (unverified risk)

- **Problem:** `getUserMedia`, the analyser and MediaRecorder run at the same time as SpeechRecognition (`audio.ts:890-950`). On some Android devices these compete for the microphone.
- [ ] Verify on real devices. If they conflict, remove the analyser and recorder from the capture path.

### P4.7 Restart loop

- **Problem:** recognition restarts every 150 ms after it ends (`audio.ts:870-885`), including after network errors. Some Android devices may beep on each restart.
- **Fix:** add backoff, and show a clear "not listening" state when recognition keeps failing.
- [x] Implementation on branch `p47-restart-backoff`: repeated recognition failures back off exponentially (capped) instead of looping every ~150 ms, and a plainly surfaced "not listening" state is shown when recognition keeps failing (dock + toast) so the cashier is never left believing the mic is live. Covered by listener regression tests (backoff progression and the failure-state transition). Merged to main via PR #50 (`6839f74`).

### P4.8 Offline voice capture is not really offline

- **Problem:** Chrome's default Web Speech recognition runs on Google's servers. Without a network connection there is no transcript to queue, so the IndexedDB queue only covers failures between recognition and POST.
- **Fix:** use on-device recognition where the chosen engine supports it. Otherwise, state plainly in the UI that voice capture needs a connection, and keep manual entry as the offline fallback.
- [x] Implementation on branch `feat/p4-8-honest-offline-voice-capture`: the counter dock now states plainly when offline ("Voice capture needs a connection — speech is recognized in the cloud; use manual counter entry while offline; pending captures sync when reconnected"), the mic toggle reads "Voice Offline" and refuses to start a doomed recognition session offline, an outage while listening stops the listener instead of feeding the P4.7 backoff, and reconnecting restores the idle dock and resumes offline sync. Manual counter entry in the Ledger remains the offline fallback. Covered by a mounted-view connectivity test. Merged to main via PR #52 (`6cc3d51`).
- [x] Fallback queue implementation, merged in PR #55 (`b31b58f`): a failed manual POST can be queued with `kind: "manual-transaction"` and replayed into `/api/transactions/manual`. Test 6 in `offline_queue.test.mjs` covers a failed request followed by one successful replay.
- [x] Fallback correctness (branch `fix/p48-manual-retry-idempotency`, base `febb43e`): the manual form mints one `request_id` before the initial POST (`frontend/src/views/transactions.ts` via `generateManualRequestId`) and sends it in the body and as the `Idempotency-Key` header (`frontend/src/api.ts`); the offline queue stores the payload verbatim and replays that same id (`frontend/src/offline_queue.ts`); and `POST /api/transactions/manual` requires the id (body `request_id`, or the `Idempotency-Key` header for parity with `/api/captures`), uses it as the shop-scoped idempotency key, and fingerprints only caller-controlled fields so a committed entry whose response was lost replays as one ledger entry and one stock movement (`backend/api/transactions.go`, existing durable replay in `backend/storage/ledger.go`). A replayed write is returned as HTTP 200 and does not re-broadcast inventory/transaction events; a reused id with different content is HTTP 409. Covered by `backend/api/manual_idempotency_test.go` (committed-request/lost-response replay, event suppression, conflict, missing-id, header fallback) and `offline_queue.test.mjs` tests 6–7 plus the manual fallback in `ui_acceptance.test.mjs`. Real-device acknowledgement-loss and concurrent-replay acceptance on the row below remains open.
- [x] Offline availability (branch `feat/p4-8-offline-catalogue`, base `20e246e`): product catalogues are persisted per-shop into durable browser storage (`frontend/src/offline_catalogue.ts` with IndexedDB `listenledger_catalogue_db`, localStorage `listenledger_catalogue_${shopId}`, and in-memory fallbacks); `showManualEntryModal` in `frontend/src/views/transactions.ts` immediately pre-populates products from local storage on opening, renders an offline catalogue badge (`📦 Offline Catalogue (N cached)`), and falls back seamlessly when the network fetch fails; `home.ts` and `inventory.ts` hydrate the catalogue cache during active browsing; review corrections fallback to cached catalogue when offline; and offline manual submissions queue atomically on device with stable `request_id`s and replay on reconnect. Covered by tests 1–9 in `frontend/test/offline_catalogue.test.mjs`. Offline catalogue field acceptance on the row below remains open.
- [ ] Offline availability field acceptance: verify opening manual counter entry during an active cellular dropout on Android Chrome in Lagos market pilot stall, selecting from cached catalogue, queuing on-device, and replaying upon Wi-Fi/4G reconnection.
- [x] Queue isolation (branch `feat/p4-8-queue-shop-isolation`, base `71787e7`): scoped queue reads (`getQueuedCaptures`) and counter badges (`getQueuedCount`) strictly bind to the active authenticated `shop_id`; `syncOfflineQueue` replays only captures belonging to the active shop while preserving other shops' pending data in local storage; legacy unscoped entries missing `shop_id` are quarantined rather than posted into arbitrary sessions; and active identity is dynamically verified across in-flight sync loops to halt replay immediately on account switch or logout. Covered by unit tests 8–12 in `frontend/test/offline_queue.test.mjs`. Multi-account physical-device acceptance on the row below remains open.
- [ ] Queue isolation multi-account field acceptance: verify account A → logout → account B across network drop and multi-tab reconnect on physical devices without cross-account capture replay.

### P4.9 Features that do not improve accuracy

- `SpeechGrammarList` "grammar boosting" (`audio.ts:761-775`) has no effect in Chrome.
- Adaptive VAD (`audio.ts:1012-1028`) only drives UI badges. `adaptiveSilenceTimeoutMs` is computed and never used to segment or flush speech.
- The spectral visualizer is decoration.
- [x] Implementation on branch `feat/p4-9-remove-inert-accuracy-features`: removed `SpeechGrammarList` grammar boosting and all `catalogueWords` plumbing that existed only to feed it, removed the dead `adaptiveSilenceTimeoutMs` metric (computed, never used to segment or flush speech), and removed the decorative spectral visualizer (bars, `calculateSpectralBands`, `onSpectralBands`, `getSpectralBands`). The surviving ambient VAD/SNR/signal badges are relabelled as explicit UI-only indicators ("🎙 Voice Active (UI)", "🟢 Mic Signal Clear (UI)", tooltips state they do not affect recognition or accuracy). The dormant raw-audio recorder path is intentionally untouched because P6 will re-enable recording under per-shop opt-in. Covered by listener tests 8/17/19.
- [ ] Real-device sanity: the counter still listens and records sales with the decorative widgets removed.

---

## P5 — Fix the understanding layer

Measure every change against the **real transcript set from P1/P2**, not hand-written sentences.

### P5.1 Number words inside brand names

- **Problem:** "Two Five Alive, one thousand naira" is traced as amount ₦2,500 (the "two-five" shorthand, `backend/validate/parser.go:219-226`), quantity defaulted to 1 (`parser.go:323-330`), and product "Alive". Seven Up and Three Crowns are affected the same way, because number words are stripped from product mentions (`parser.go:537-539`).
- **Fix:** match catalogue names and aliases first (longest match wins) and remove them from the text before parsing numbers.
- [ ] **Acceptance:** tests pass for Five Alive, Seven Up, 7Up and Three Crowns, including with a quantity and an amount.

### P5.2 Unit chosen in random order

- **Problem:** the unit is found by iterating a Go map (`parser.go:115-123`). Go map order is random, so a sentence with two unit words ("cartons … packs") gives an unpredictable unit.
- **Fix:** scan in text order and attach each unit to the quantity it follows.

### P5.3 Cancellation is too eager

- **Problem:** cancellation phrases match anywhere in the transcript (`parser.go:741-768`). "Drop that bag there, two Milo…" cancels the sale.
- **Fix:** treat a cancellation phrase as a cancellation only when it is a standalone or final clause near the boundary.

### P5.4 Matching uses spelling, not sound

- **Problem:** fuzzy matching is Levenshtein distance on spelling (`backend/validate/matcher.go:171-194`). Speech-to-text errors are phonetic: "peek" vs "Peak" scores 0.5, so it does not match.
- **Fix:**
  - [ ] Add phonetic keys (for example Double Metaphone) for product names and aliases.
  - [ ] When an owner corrects a review item (for example "peek" → Peak), offer to save the misheard text as an alias.

### P5.5 Confidence scores are heuristic

- **Problem:** product confidence is a fixed score per match type (exact name 0.98, alias 0.95, fuzzy 0.80–0.90). Quantity confidence is a constant 0.98 (`backend/validate/engine.go:246`), even when the quantity was defaulted to 1 by the parser.
- **Fix:** lower confidence when a value was defaulted or inferred, and include the recognizer confidence from P4.5.

### P5.6 Evaluate LLM extraction against the rules

- [ ] Compare the regex parser against an LLM extraction step: shop catalogue in the prompt, strict JSON output that must reference catalogue product IDs.
- [ ] Adopt it only if it measurably wins on the real transcript set. Weigh latency, cost per sale and connectivity.
- [ ] Keep the existing validation and review gate after it either way.

---

## P6 — Privacy and raw audio

- **Problem:**
  - Historical #42 behaviour retained base64 counter audio. Merged P0b now disables new browser recording, strips new queue/upload audio and discards incoming snippets. This is implemented source behaviour, not proof of the currently deployed release.
  - Legacy audio can remain in transaction rows and old browser queues. Transaction reads still select `audio_data`, and UI playback still exists. An explicit transcript/audio purge endpoint exists, but this audit found no scheduled retention enforcement or per-shop audio opt-in. P0b is not a purge of legacy data.
  - The earlier WebM-header/playback concern applies to old recordings or a future re-enabled recorder; do not treat it as a current active recorder defect. Any future recording implementation needs a fresh playback test after 20+ seconds.
  - `STAFF_TRAINING.md` and legacy `RUNBOOK.md` sections still say audio is never uploaded or processed in the cloud. Reconcile those instructions with the actual browser speech service before giving them to a shop. Disabling application snippet storage does not establish on-device speech processing.
- **Decision for the pilot:**
  - [ ] Audio retention is opt-in per shop, and only for review-required captures.
  - [ ] Short retention (for example 7 days), with deletion enforced automatically.
  - [ ] Audio is stored separately from transaction rows and excluded from ledger list responses and WebSocket events. Check whether `audio_data` is currently returned in those payloads; if so, remove it.
  - [ ] A visible notice at the counter tells customers that audio may be processed.
  - [ ] Owner and staff instructions match what the software actually does.

---

## P7 — Controlled pilot

- [ ] One shop, one counter, one trained cashier, running alongside the shop's existing records for two weeks.
- [ ] Daily reconciliation of ledger vs. actual sales and stock, kept in a spreadsheet. Investigate every difference.
- [ ] Record the metrics below in the evidence log.
- [ ] Expand to 1–2 more shops only after the targets are met.

## Success metrics and kill criteria

Agree the targets with the pilot owner **before** collecting results. The numbers below are proposals.

| Metric | Definition | Proposed target | Kill / pivot signal |
|---|---|---|---|
| Capture rate | % of real sales that reach the ledger (confirmed or review) | ≥ 90% | < 75% after three iterations |
| Clean capture rate | % of real sales confirmed correctly with no correction | ≥ 85% | < 70% after three iterations |
| False confirmations | Confirmed transactions that did not happen, including duplicates | 0 duplicates, ≤ 1% false | Any unresolved duplicates |
| Correction effort | Owner seconds per sale spent on review vs. typing the sale manually | Clearly less than manual entry (H6) | Equal to or more than manual entry |
| Protocol compliance | % of sales where the cashier speaks all anchors | ≥ 85% | < 60% after training |
| Willingness to pay | Owner commits to a price after the pilot | Yes | No |

**If the kill signals are hit:** pivot to a tap-first POS, with voice as an optional extra. The ledger core is reused either way.

## What to keep

These parts are solid and should be reused whatever the capture mode:

- Atomic ledger and stock movements with rollback.
- Idempotent writes with payload fingerprints (once the live capture path actually uses them, see P4.3).
- The review, correct-and-confirm, reject and reversal flow with its audit trail.
- Shop isolation and authentication.
- Catalogue with aliases and packaging conversions.

## Evidence log

Fill in one row per field session. Link to the labelled ground-truth sheet, not to raw audio.

| Date | Shop | Hours recorded | Real sales (ground truth) | Engine / mode | Capture rate | Clean capture rate | Duplicates | Notes |
|---|---|---|---|---|---|---|---|---|
| | | | | | | | | |

## Status of findings

| Finding | How it was established |
|---|---|
| P4.1 pause handling and P4.3 capture IDs | Fixes merged and regression-tested; physical-device acceptance still open |
| P4.2 review flood | Bare “Thank you” reproduced as a review-required engine result in the 19 October synthetic probe; suppression still missing |
| P4.4 language and P4.9 inert features | Configurable language and removal implemented; en-US baseline remains pending P2; device sanity still open |
| P4.5 single alternative | Still present in current source; evaluation support and field measurement pending |
| P4.6 mic contention, P4.7 restart beeps | Recorder removed and restart backoff merged; analyser/recognizer coexistence and audible device behaviour remain unverified |
| P4.8 fallback safety and availability | Manual retry duplication reproduced against a temporary API/store; all-shop replay reproduced with mocked transport; network-dependent catalogue selection confirmed by source reading |
| P5.1 brand names, P5.2 unit order | Reproduced with current parser on 19 October: Five Alive/Seven Up/Three Crowns lose number words; one 7Up example passes; repeated mixed-unit input varies |
| P5.3 cancellation | “Drop that bag there…” reproduced as cancellation in the current parser |
| P5.4–P5.5 matching/confidence | Current code uses spelling similarity and heuristic confidence; default-to-one reproduced; real-transcript calibration pending |
| P6 snippets and policy | New recording disabled; legacy audio and future recorder playback still need checking; notices and retention enforcement remain open |
| Edge mock fail-open (P0) | Code reading |

Update this table when a finding is confirmed, fixed or disproved.


## P0b release evidence (pending)

P0a merged as PR #47 (`a0adf3299b1cba77da2f1fe06bec479d908f9f24`); Render deploy `dep-db9qp0flk1mc73c7nu2g` is live and `/readyz` returned 200 with the database connected. The live Settings template contains the parked panels and corrected privacy wording.

**19 October correction:** P0b is merged in source through PR #48 (`1208fb7`). Current deployment and physical-device acceptance were not verified in this audit; no corresponding evidence is present in this register. This change does not solve P4 recognition reliability, erase existing stored audio, or implement the P6 retention policy. Legacy local queue snippets are not uploaded by the new client, but pre-existing local data is not purged. The preceding P0a deployment record is historical, not current release verification.

Before closing P0b release acceptance, run the actual release against an isolated test shop on a real phone:

1. Record device/browser version, candidate commit and test time; screen-record Start Listening → a test sale → Stop Listening. Verify transcript capture and microphone release.
2. Inspect that test capture's network request: no `audio_data`. Confirm its server row has empty `audio_data`, and its receipt/stock movement is correct.
3. Queue a test transcript, reconnect, and verify it syncs without audio. Repeat with a legacy queued item containing `audio_data`.
4. Submit a legacy client payload containing a snippet; verify it is discarded while the transcript is retained.
5. Try a spoken credit phrase and explicit customer attribution; verify an explanatory rejection and no ledger or stock change.
6. Attach the screen recording (without customer data) and measured observations to the PR. Leave the P0 parent unchecked until this evidence and deployment verification are complete.

## Real-world testing register — 19 October 2026

**Every row below remains open.** Implementation and synthetic regression results are separate from acceptance. Use at least two physical Android phones with Chrome and one iPhone with Safari for browser capture checks; record exact OS/browser versions. Restrict any supported-device claim to devices that pass.

| Feature / gate | Current source status | Required real-world test and evidence |
|---|---|---|
| P0 pilot scope and safe intake | Parked UI and P0b guards merged | On the deployed build, confirm parked actions are hidden, credit capture explains rejection with no movement, microphone stops, and fresh/legacy-client requests retain no audio. Record release SHA and observed payload/row results. |
| P1 natural counter speech and training | No labelled real dataset in checkout | Consent and notice; record ≥3 hours including a busy period; label products, quantities, units, amount, speaker, language and timing. Compare natural speech with one trained cashier; measure compliance, friction and willingness to pay. Keep recordings encrypted and outside Git. |
| P2 recognition engine and language | Web Speech; configurable language; en-US baseline | Compare en-US, en-NG, Whisper candidates and an appropriate cloud candidate against the same P1 ground truth. Measure full-sale and per-anchor accuracy, false captures, latency, connectivity and cost. Choose the default only from measured results. |
| P3 ambient versus push-to-talk | No measured choice; current listener is continuous | Compare each mode for one trading day using the same catalogue/protocol. Measure capture, clean capture, correction effort and cashier preference. A Start/Stop toggle alone is not evidence of a completed push-to-talk workflow. |
| P4.1 delayed closing phrase | Rolling buffer merged | Speak a sale, wait 10 seconds, then close it; verify exactly one correct receipt. Include recognizer restarts, consecutive customers and pauses near the look-back limit. |
| P4.2 stray closing phrases | Missing no-anchor suppression; server creates review items | After fixing the guard, 10 unrelated “thank you” phrases create zero review items; also test customer speech, adjacent sales and background conversations. |
| P4.3 spoken capture deduplication | Final-result gate and stable capture ID merged | Interim/final/repeated results, session restarts and an interrupted acknowledgement must produce one receipt and one stock movement per sale on each device. Include successive sales in one recognition session. |
| P4.4 language persistence | Canonical implementation merged | Change language, reload/restart, verify recognizer and readback use the saved tag on actual browsers. Attach P2 results before changing en-US default. Verify migration 0014 in the deployed schema. |
| P4.5 alternatives and recognizer confidence | Still one alternative; confidence not propagated | Implement evaluation support, then compare catalogue matching with alternative hypotheses on real misrecognitions. Confirm confidence affects review safely; do not treat heuristic scores as accuracy percentages. |
| P4.6 microphone resources | Recognizer plus getUserMedia/analyser remain; recorder removed | Start/stop repeatedly; deny/revoke permission; interrupt with a call, background the tab and lock/unlock the screen. Verify capture, truthful status, mic release and absence of resource contention. |
| P4.7 restart and error recovery | Backoff and failure state merged | Test flaky network and repeated recognition failures for a full counter session. Observe beeps, time to recovery, battery/heat and clear “not listening” feedback. |
| P4.8 offline voice messaging | Implemented; recognition still needs connectivity | Disconnect before start and mid-session; cashier must understand that queued transcripts are not offline recognition. Reconnect without creating duplicate captures. |
| P4.8 manual fallback and durable queue | Queue exists; manual retry idempotency fixed in code + tests (P4.8-manual-retry); shop binding and offline catalogue gaps remain | Fix remaining gaps first; then open the form after an outage, queue entries, kill/reopen the browser, lose a server acknowledgement and reconnect. Exactly one movement per entry, no lost durable work, truthful volatile-storage warnings, and no replay into another account/shop. |
| P4.9 simplified capture UI | Inert grammar/spectrum features removed | Verify real sales still capture and UI-only signal indicators are not understood as proof of recognition quality. |
| Voice readback, wake lock and navigation cleanup | Implemented controls; physical behaviour unverified | With readback enabled, verify it cannot trigger a second sale/restock. Navigate away, background/foreground and stop; check streams, recognizer, wake lock and timers are released or recovered as promised. |
| P5.1 number-bearing product names | Catalogue-first parsing missing | Measure Five Alive, Seven Up, 7Up and Three Crowns on real audio/transcripts, with quantity and price variants. Verify exact product, quantity and amount after repair. |
| P5.2 units and multi-line restocks | Unit selection iterates a Go map | After deterministic parsing, speak mixed carton/pack/bottle receipts; verify each line, conversion, supplier, receipt total and purchase cost. No silent partial receipt or sales-revenue inflation. |
| P5.3 cancellation | Server matches cancellation substrings anywhere | Separate genuine cancellations from “drop that bag there…” and other ordinary speech. Verify cancelled/unrelated phrases never remove a genuine sale silently. |
| P5.4 phonetic matching and aliases | Spelling matcher exists; phonetic learning missing | Evaluate real mishearings such as “peek”/Peak and similar catalogue names; test an owner's correction/alias workflow if added. Measure wrong confident matches as well as successful matches. |
| P5.5 confidence and review thresholds | Fixed heuristic scores; defaulted quantity can score highly | Label correct/incorrect outcomes and calibrate review thresholds. Check omitted quantities and ambiguous anchors. Measure false confirmations and review effort, not just aggregate parser accuracy. |
| P5.6 optional LLM extraction | Comparative experiment pending | Compare rules with strict catalogue-ID JSON extraction on the same real set, including latency/cost/offline effects. Adoption is conditional on a measured win, not an MVP requirement by itself. |
| Owner onboarding, catalogue and review | APIs/UI and automated coverage exist | A new owner loads 20–50 products/opening stock, sets packaging/aliases, corrects/rejects/confirms a capture, records manual sale/restock and reverses an error without developer help. Compare resulting stock/revenue with independent records. |
| Realtime, account isolation and recovery | Backend foundations and regression tests exist | Use two real accounts/devices; test login/recovery/logout, stale sessions, disconnect/reconnect, filters and daily totals. Check local queue isolation separately from server authorization. |
| P6 notices, retention, deletion and access | New snippet retention disabled; legacy data/policy remain | Agree the pilot policy, give staff truthful instructions, show notice, exercise deletion/access controls and prove legacy audio is handled. If retention stays disabled, do not re-enable it merely to complete optional opt-in recording work. |
| Hosted operation and recovery | Deployment/backup tooling exists; current live state unverified | Verify exact deployed SHA, HTTPS/assets, readiness, schema 0014, restricted role, restart persistence, backup restoration and outage recovery in an isolated environment. Do not equate a historical /readyz 200 with this acceptance. |
| P7 two-week controlled pilot | No completed field evidence rows | One shop/counter/cashier beside existing records; daily sales/stock reconciliation, all discrepancies investigated, metrics and willingness to pay recorded. Expand only after agreed targets pass. |

### Starting field testing versus completing the MVP

- **P1 observation/recording:** no additional application code is required. Start once the owner, notice, recording arrangement, secure storage and labelling plan are ready; none is evidenced complete here. This is field research, not a claim that the app is safe to depend on.
- **Supervised app counter trial:** approximately **30% readiness work remains (25–35% judgment range)**. Use four equal readiness areas: core app availability 90%, safe capture/recovery 50%, privacy/release verification 50%, trial protocol/evidence preparation 90% for the documented plan only. Average readiness is 70%; actual owner agreement and staff preparation remain hard gates. Fix the high-impact capture/queue defects, reconcile notices, verify the release and run the device checks first. Keep independent records authoritative throughout.
- **Field-validated MVP:** approximately **45% remains (40–50% judgment range)** under the detailed audit's weighted model. A score cannot override P1–P7 acceptance. No physical field gate is evidenced closed in this checkout.
- Frontend redesign has **7 of 45 items checked (15.6%)**. Its **84.4% remaining** describes that redesign only; dedicated Counter navigation and parked Customer/Expense pages are not prerequisites for P1. Continue authorized redesign work without substituting it for evidence collection.

### Evidence to attach before marking a row accepted

Record date/session ID, owner/cashier preparation, deployed commit, device/OS/browser, engine/language/mode, catalogue version, noise/connectivity conditions, ground-truth counts, failures and corrective actions. Report capture rate, clean capture rate, amount/unit accuracy, false confirmations, duplicates, correction time and protocol compliance with numerator/denominator. Use the proposed targets above only after agreeing them with the owner. Link anonymized labels and measured summaries, never raw customer audio in Git. A fix requires a new run of the failed scenario.
