# ListenLedger Frontend Redesign — Implementation Tracker

Created: 16 October 2026
Baseline: `ea37895b6532c2c464ff4d10c2fdc3b5610dcea5` (main, PR #38).

## Purpose and status

This is the execution checklist for the agreed frontend redesign. Counter is the main audio listening dashboard staff keep open during trading. Home is the owner's business summary.

F01 (PR #40) and F02 (PR #43) are implemented. Other redesign features remain pending, including the dedicated Counter route. The feature freeze in [realProductpath.md](realProductpath.md) takes precedence: execute P0 and the evidence gates before resuming this checklist.

The baseline has four page routes (Home, Transactions, Inventory, Settings), customer management in dialogs, and audio capture on Home. PR #38 introduced an IndexedDB offline capture queue; preserve and inspect it rather than replacing it or assuming offline persistence is absent. Its real-device reliability still needs acceptance testing.

## How to use this checklist

- Implement one numbered feature at a time on a feature branch.
- Tick an item only when its implementation and relevant verification are complete; record the commit/PR and evidence below.
- Keep physical microphone tests, browser checks, automated tests, and live acceptance distinct. If a required check is blocked, leave that acceptance item unchecked and state why.
- Push the branch, wait for backend, frontend, edge, and container CI to pass, review the diff, then merge according to the user's authorization.
- Update this file in every implementation PR. Update README and any existing project instruction/status document when behaviour changes.
- Do not add a Codex co-author trailer to commits.
- Never mark an entire phase complete just because its layout exists.

## Design and scope

Use the existing TypeScript, HTML-template, CSS frontend and real APIs. No framework rewrite, fabricated business data, or decorative nonfunctional controls.

Visual direction: warm off-white background, white surfaces, deep teal primary actions, readable dark text, restrained warning/error colours, consistent line icons, generous spacing, and clear money formatting. Use one component system across all pages.

Desktop navigation: left sidebar plus a compact shop/account top bar.
Mobile navigation: Home, Counter, Stock, Customers, More. More exposes Transactions, Expenses, and Settings. The Counter entry is prominent without obscuring content.

| Page | Purpose | Planned route |
| --- | --- | --- |
| Home | Daily business summary and alerts | #/home |
| Counter | Main microphone and capture workspace | #/counter |
| Transactions | Receipt history and Needs review tab | #/transactions |
| Inventory | Catalogue and stock management | #/inventory |
| Customers | Customer directory, balances, and history | #/customers |
| Expenses | Expense history and entry | #/expenses |
| Settings | Shop, voice, account, optional devices, advanced pilot tools | #/settings |

Preserve existing routes and supported workflows. Only expose searches/filters that are implemented against the API or accurately scoped loaded data.

## Phase 1 — Shared visual system and navigation

- [x] F01. Define reusable colour, spacing, typography, radius, and focus tokens; unify button/input/card/badge/dialog styles.
- [x] F02. Build the desktop sidebar and shop/account top bar with active navigation and accessible labels.
- [ ] F03. Build mobile bottom navigation and More menu with safe-area spacing, usable close behaviour, and no content obstruction.
- [ ] F04. Register the new page routes and lifecycle handlers; preserve old links, browser back/forward, page titles, and unknown-route handling. Do not ship dead navigation entries.
- [ ] F05. Standardize loading, empty, validation, failed-save, and disconnected states with useful recovery actions.
- [ ] F06. Verify shared shell at 360, 390, 768, and 1440px, including keyboard navigation and no page-level horizontal overflow.

## Phase 2 — Home and Counter

### Home

- [ ] F07. Present daily sales, daily expenses, outstanding customer debt, and review count with explicit time scopes.
- [ ] F08. Rename the existing sales-minus-expenses metric to "Sales less expenses" wherever cost of goods sold is excluded.
- [ ] F09. Show recent activity, actionable review/low-stock alerts, and a prominent Open counter action.
- [ ] F10. Collapse onboarding into a compact progress banner; remove microphone operation from Home once Counter is functional.

### Counter — main audio listening dashboard

- [ ] F11. Move the working capture flow into Counter with a prominent Start/Stop listening control.
- [ ] F12. Show Stopped, Requesting permission, Listening, Processing, and Error states, readable transcript, and audio-level feedback.
- [ ] F13. Show captured receipts with items, quantities, amounts, customer/supplier attribution, and recent captures.
- [ ] F14. Distinguish Saved, Needs review, Cancelled, Queued on this device, Syncing, and Failed outcomes; show Saved only after server confirmation.
- [ ] F15. Preserve manual entry, voice feedback, screen-awake indicators, configured closing phrases, multi-item captures, cancellation, and customer-credit attribution.
- [ ] F16. Audit final/interim recognition and transcript accumulation; prevent duplicate submissions and preserve stable capture/idempotency IDs on retry.
- [ ] F17. Preserve and validate the existing IndexedDB queue: reload recovery, retry/error visibility, account/shop isolation, and explicit local-queue versus server-saved status.
- [ ] F18. Stop recognition on leaving Counter and release microphone streams, wake locks, timers, and listeners; explain this behaviour. Do not delete queued captures during page cleanup.
- [ ] F19. Handle unsupported browsers, denied permissions, interruptions, and network failures; prevent spoken feedback from producing another capture.
- [ ] F20. Add meaningful regression coverage for changed audio/capture behaviour and complete controlled browser acceptance. Record physical microphone testing separately.

## Phase 3 — Customers and Expenses

- [ ] F21. Promote Customer Credit Book to a dedicated page with search, balances, and clear loading/error/empty states.
- [ ] F22. Provide customer detail/history navigation with registration, editing, repayment, credit entry, and write-off actions.
- [ ] F23. Clearly distinguish debt-book entries from sales that also change inventory; verify supported customer/transaction links and refresh after mutations.
- [ ] F24. Build the Expenses page with supported filters, totals, entry, and existing expense actions.
- [ ] F25. Verify customer and expense workflows on desktop/mobile with controlled test accounts and no fake entries in real shop accounts.

## Phase 4 — Transactions and Inventory

- [ ] F26. Redesign transaction history around receipts, supported filters, pagination, and CSV export.
- [ ] F27. Provide receipt details with items, totals, status, transcript, customer/supplier, and supported actions.
- [ ] F28. Add the Needs review tab and navigation count; highlight uncertain fields and explain confirm/reject consequences.
- [ ] F29. Preserve manual entry, correction, rejection, reversal, audit details, and live updates.
- [ ] F30. Redesign Inventory with product search, supported stock filters, Add product, desktop table, and readable mobile layout.
- [ ] F31. Keep aliases, packaging, stock adjustments, editing, import/template, and archival behaviour accessible through product details or appropriate forms.
- [ ] F32. Verify transaction and inventory mutations, review decisions, pagination/filter behaviour, and WebSocket refresh.

## Phase 5 — Settings, authentication, and onboarding

- [ ] F33. Organize settings into Shop details, Voice settings, Account, and Optional devices.
- [ ] F34. Move technical thresholds, pilot audits, and reconciliation tools into Advanced/Pilot without removing functionality.
- [ ] F35. Explain that browser microphones do not require external-device pairing; make privacy statements match actual speech-processing behaviour.
- [ ] F36. Replace the authentication mode dropdown with clear Sign in/Create account/Recover account flows.
- [ ] F37. Preserve recovery-code, session, logout, and validation behaviour; focus onboarding on shop details, products, microphone test, and first capture.
- [ ] F38. Verify accessible forms/dialogs, focus management, keyboard dismissal, and usable mobile layouts.

## Phase 6 — Release acceptance

- [ ] F39. Verify all routes, back/forward, asynchronous cleanup, and protection against stale responses.
- [ ] F40. Verify 360, 390, 768, and 1440px layouts; capture desktop/mobile screenshots.
- [ ] F41. Run type checks, production build, relevant frontend regressions, and all required CI jobs.
- [ ] F42. Verify live Render startup and frontend assets after authorized deployment.
- [ ] F43. Complete controlled hosted checks for login, Counter, transactions/review, inventory, customers, expenses, and realtime reconnect.
- [ ] F44. Record real-device microphone acceptance, separately from simulated speech events and automated tests.
- [ ] F45. Update README and this tracker with verified completion, evidence, and remaining limitations.

## Engineering guardrails

- Preserve authentication, shop isolation, API contracts, integer minor-unit money formatting, and WebSocket behaviour.
- Clean up per-view resources without losing durable queued work. Do not let an old response overwrite a new route or search.
- Do not mask backend failures with optimistic success messages or promise atomicity the backend does not provide.
- Recheck previously identified voice-credit consistency concerns against latest main before treating them as current defects; track unrelated backend work separately.
- Keep migrations and deployment configuration outside this frontend scope unless a justified dependency requires them.
- If schema changes are essential, apply and verify migrations before deploying dependent code. Missing migrations previously blocked production startup.
- Browser-specific speech support, cloud processing, offline recognition, and audio privacy must be described accurately after verification.

## Implementation evidence log

Add one row per completed feature or coherent PR. Include exact CI links and state which browser/device checks were actually performed.

| Feature IDs | PR / commit | Automated verification | Browser / live verification | Remaining limitations |
| --- | --- | --- | --- | --- |
| Planning document | [PR #39](https://github.com/eeyung1/listenledger/pull/39) | All four CI jobs passed before merge | Not applicable; no UI change | Plan established |
| F01 | [PR #40](https://github.com/eeyung1/listenledger/pull/40) | Typecheck/build and 63 existing frontend tests passed; exact-head repository CI required before merge | Chromium rendered-template checks at 360/390/768/1440px for four current pages and Customer Book dialog; viewport containment, 44px button sizing, button alias parity, reduced motion, screenshots | Empty/intercepted test responses; not full authenticated or physical microphone acceptance. New shell, per-page layouts, modal focus lifecycle, and real-device checks remain pending. |
| F02 | PR #43 | Typecheck/build and all 73 frontend tests passed (including 5 shell nav tests); repository CI required before merge | Shell navigation unit and DOM rendering tests verify sidebar and mobile stand-in nav | Mobile bottom navigation (F03) and route additions (F04) to follow in subsequent PRs |

## Next action

Do not proceed to F03 during the product feature freeze. Next: finish P0 in realProductpath.md, then collect P1 field evidence. Resume this redesign only when the product gates permit it. Tick only completed and verified items.

