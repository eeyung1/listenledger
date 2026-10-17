# Real Product Path

This document sets the delivery priority for ListenLedger. It comes from a codebase review on **16 October 2026** at `db85661`, which was merged to `main` as #42. Line references were checked against `main` at `72af068`. Until the gates below are passed, it takes precedence over the milestone roadmap in [README.md](README.md) and [frontreadme.md](frontreadme.md).

## The only question that matters

> **Can a microphone + software reliably turn a structured real-world sales conversation into an accurate inventory transaction without manual data entry?** — Blueprint §24

**Status: unanswered.** No real counter audio has been recorded or evaluated. No shop has used the system. `TRIAL_REPORT.md` scores 20 hand-written sentences and is not evidence. Everything below exists to answer this question as fast and cheaply as possible.

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
- [ ] P0b release: attach real-device evidence, verify exact-head CI, merge and verify the deployed build (working rule 2).
- [ ] Verify P0b on a real device and deployed build, then close the parent P0 gate.

P0a is a UI-only increment: hidden controls are not an API authorization boundary. Spoken credit attribution is rejected in the P0b candidate; it is not disabled in the live P0a build merely by hiding the credit book. No P1–P7 evidence or capture accuracy is claimed. Automated checks and desktop-browser inspection validate this interface change only.


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

### P4.2 Every "thank you" creates a review item — critical

- **Problem:** anyone saying "thank you", customers included, triggers a capture (`detectClosingBoundary`, `audio.ts:175-231`). Captures with no anchors are held for review as "Unintelligible speech" (`backend/validate/engine.go:204-211`).
- **Fix:** do not submit captures that contain no anchors. On the server, log closing-phrase-only captures as a metric instead of creating review items.
- [ ] **Acceptance:** 10 stray "thank you"s in normal conversation create 0 review items.

### P4.3 One sale may be recorded twice — critical

- **Problem:** boundary detection runs on interim results with no `isFinal` check (`audio.ts:807`). Chrome sends an interim result ending in "thank you" before the final one. The online POST sends no `capture_id` or `Idempotency-Key` (`frontend/src/views/home.ts:927`), and the server mints a new ID every time (`backend/api/captures.go:46-52`). Only the offline queue path is idempotent (`frontend/src/offline_queue.ts:398`).
- **Fix:** act only on final results. Generate the capture ID in the browser at detection time and send it as both `capture_id` and `Idempotency-Key`. Ignore a second boundary for the same result.
- [ ] **Acceptance:** one spoken sale produces exactly one ledger entry and one stock movement on Android Chrome and iOS Safari.

### P4.4 Recognition language is American English

- **Problem:** `recognition.lang = "en-US"` (`audio.ts:781`). Voice readback is also `en-US` (`audio.ts:450`).
- **Fix:** make the recognition language a shop setting, defaulting to the P2 winner (likely `en-NG`).
- [ ] **Acceptance:** the language is configurable, and the default is backed by P2 results.

### P4.5 Recognizer confidence and alternatives are thrown away

- **Problem:** `maxAlternatives = 1` (`audio.ts:782`), and the recognizer's confidence score is never sent to the server.
- **Fix:** send the top 3–5 alternatives with their confidence scores. Let the matcher try each alternative against the catalogue, and store the scores for analysis.

### P4.6 Microphone contention on mobile (unverified risk)

- **Problem:** `getUserMedia`, the analyser and MediaRecorder run at the same time as SpeechRecognition (`audio.ts:890-950`). On some Android devices these compete for the microphone.
- [ ] Verify on real devices. If they conflict, remove the analyser and recorder from the capture path.

### P4.7 Restart loop

- **Problem:** recognition restarts every 150 ms after it ends (`audio.ts:870-885`), including after network errors. Some Android devices may beep on each restart.
- **Fix:** add backoff, and show a clear "not listening" state when recognition keeps failing.

### P4.8 Offline voice capture is not really offline

- **Problem:** Chrome's default Web Speech recognition runs on Google's servers. Without a network connection there is no transcript to queue, so the IndexedDB queue only covers failures between recognition and POST.
- **Fix:** use on-device recognition where the chosen engine supports it. Otherwise, state plainly in the UI that voice capture needs a connection, and keep manual entry as the offline fallback.

### P4.9 Features that do not improve accuracy

- `SpeechGrammarList` "grammar boosting" (`audio.ts:761-775`) has no effect in Chrome.
- Adaptive VAD (`audio.ts:1012-1028`) only drives UI badges. `adaptiveSilenceTimeoutMs` is computed and never used to segment or flush speech.
- The spectral visualizer is decoration.
- [ ] Remove these, or relabel them as UI-only, so they are not mistaken for accuracy work.

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
  - Since #42, the app stores up to ~15 seconds of base64 counter audio, including customers' voices, on each transaction (`audio.ts:555-575`, `audio.ts:917-944`, playback in `frontend/src/views/transactions.ts`). This conflicts with Blueprint §17 (discard unnecessary raw audio) and the open items in README §10.
  - Saved snippets are probably unplayable after ~15 seconds of listening, because `audioChunks.shift()` (`audio.ts:935-937`) drops the first WebM chunk, which holds the container header.
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
| P4.1 lost sales on pause, P4.2 review flood, P4.3 missing capture ID | Code reading; confirm on a real device |
| P4.4 `en-US`, P4.5 single alternative, P4.9 inert features | Code reading |
| P4.6 mic contention, P4.7 restart beeps | Unverified risk; test on devices |
| P5.1 brand names, P5.2 unit order | Traced by hand through `parser.go`; add failing tests first |
| P5.3–P5.5 | Code reading |
| P6 unplayable snippets | Code reading plus known MediaRecorder/WebM behaviour; confirm by playback after 20+ seconds of listening |
| Edge mock fail-open (P0) | Code reading |

Update this table when a finding is confirmed, fixed or disproved.


## P0b release evidence (pending)

P0a merged as PR #47 (`a0adf3299b1cba77da2f1fe06bec479d908f9f24`); Render deploy `dep-db9qp0flk1mc73c7nu2g` is live and `/readyz` returned 200 with the database connected. The live Settings template contains the parked panels and corrected privacy wording.

P0b remains a candidate, not deployed. No physical-device or real-shop evidence has been supplied. This change does not solve P4 recognition reliability, erase existing stored audio, or implement the P6 retention policy. Legacy local queue snippets are not uploaded by the new client, but pre-existing local data is not purged.

Before merging P0b, run the candidate against an isolated test shop on a real phone:

1. Record device/browser version, candidate commit and test time; screen-record Start Listening → a test sale → Stop Listening. Verify transcript capture and microphone release.
2. Inspect that test capture's network request: no `audio_data`. Confirm its server row has empty `audio_data`, and its receipt/stock movement is correct.
3. Queue a test transcript, reconnect, and verify it syncs without audio. Repeat with a legacy queued item containing `audio_data`.
4. Submit a legacy client payload containing a snippet; verify it is discarded while the transcript is retained.
5. Try a spoken credit phrase and explicit customer attribution; verify an explanatory rejection and no ledger or stock change.
6. Attach the screen recording (without customer data) and measured observations to the PR. Leave the P0 parent unchecked until this evidence and deployment verification are complete.
