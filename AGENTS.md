# Shared repository work

Read `multi-agent-orchestrator/coordination.md`, `resolution.md`, then
`agentmeeting.md` before work. These are shared files in the primary checkout,
not independent copies in a worktree. Do not run the unrelated orchestrator.

## Claims and isolation

- Product edits require an isolated git worktree/branch. Never switch branches,
  stage, reset, or commit in the shared primary checkout. Existing shared edits
  belong to their authors; do not sweep them into a commit.
- Before editing, acquire an atomic claim with the primary checkout's
  `scripts/agent_coordination.py`. It serializes changes using an OS lock shared
  by all worktrees. A prose standup or an earlier read is not a claim.
- Example (replace paths and identity):
  `python3 /home/eeyung/listenledger/scripts/agent_coordination.py claim --repo . --agent Codex --feature P4.4 --worktree /tmp/codex-p44 --scope backend/api/settings.go --scope frontend/src/audio.ts --migration 15`
- Reserve every file/directory you will edit. Overlapping scopes and duplicate
  feature claims fail. Reserve a migration number before creating either SQL
  file. SQLite/PostgreSQL intentionally share one reserved version; separate
  features must not. Reservations survive release and are never recycled.
- Keep the returned token. Run `check --repo . --agent NAME --token TOKEN
  --worktree PATH --scope FILE` before each edit/commit cycle, and `heartbeat`
  each cycle. Use `release` only after writing the resolution/handover.
- Never edit another agent's claim row, steal a claim on a timer, or bypass a
  failed claim. Explicit owner-authorized handover is required; record it in
  the logs and release the old token before acquiring the replacement.
- Existing pre-tool claims remain binding. Before starting, check standups and
  the backlog as well as the atomic table. For any unregistered active agent,
  ask it to register its scopes; do not infer that its files are free.
- Shared log writes must also use the tool's `locked(shared_root(...))` context
  and `atomic_write` helper; re-read inside the lock, preserve other rows, and
  append corrections rather than rewriting historical entries. Do not replace
  a shared log from a stale in-memory snapshot.
- Only Emmanuel merges to main. Stage explicit owned paths and inspect the
  staged diff. Run `python3 scripts/agent_coordination.py migrations` before
  committing migrations; CI repeats it. A passing check is not field evidence.

P4.4's canonical implementation is PR #56 (`56166be`); Codex's former local
implementation is superseded. Do not implement P4.4 again or create another
migration 0014. P2 still selects the measured default; current default is en-US.

## Implementation routing — 20 October 2026

Read [realProductpath.md](realProductpath.md) before selecting work. This routing
snapshot was checked against `origin/main` at `7d143dc` and P5.4 branch head
`2b12af0`, then reconciled with `22a32b5` after PRs #73 and #75–#79 merged.
Refresh remote refs and the shared logs before claiming anything.
A row below is a direction to remaining work, not a reservation or permission
to take another agent's scopes. The roadmap's feature freeze and evidence gates
still apply.

### Completed code: verify instead of implementing again

- P0a/P0b: pilot UI parking, disabled new audio retention and credit-intake guards.
- P4.1–P4.4: rolling transcripts, no-anchor suppression, final-result capture
  idempotency, and configurable recognition language.
- P4.7–P4.9: restart backoff, offline voice messaging, manual retry idempotency,
  queue/shop isolation, offline catalogue caching, removal of inert accuracy
  features, and the readback recapture guard.
- P5.1–P5.3: catalogue-first number-brand parsing, deterministic unit selection,
  and cancellation boundary fixes.
- P5.5 is **partially complete**: inferred quantities receive lower confidence.
- P6 is **partially complete**: staff/runbook privacy wording is corrected, the
  manual purge endpoint honours the documented query parameters, scheduled
  retention merged in PR #77, and legacy audio is fetched on demand instead of
  riding in ordinary ledger and WebSocket payloads (PR #75).
- P5.4 phonetic matching and correction-to-alias saving are merged (PR #73,
  `c0b85b3`, from branch `2b12af0`); manual CI and field calibration remain open.

These statements describe implementation only. Real-device, field, deployment,
and current-revision CI acceptance remain separate.

### Remaining work and dependencies

| Roadmap item | Work to complete | Assignment constraint |
|---|---|---|
| P4.5 | Carry 3–5 recognizer alternatives and confidence through capture/queue/API, evaluate catalogue matches, and retain scores for analysis. | Existing active owner: `DeepSeek-20261019-p45`, token `af390fdfdf534215a42be13f859f08f2`. Coordinate with that owner; no takeover without explicit handover. |
| P5.5 | Incorporate P4.5 confidence and calibrate review thresholds against labelled real outcomes. | Depends on P4.5 and P1/P2 data; do not redo the merged inferred-quantity fix or overlap the P4.5 claim. |
| P6 | Verify and document deletion under the agreed retention policy, legacy server/browser audio handling, and the deployed payload behaviour. | Highest independent code work to scope after checking current claims. Scheduled enforcement merged in PR #77 and legacy audio is no longer carried in ordinary ledger/WebSocket payloads (PR #75); per-shop policy choice, legacy browser queue handling and deployed verification remain open. Reserve migrations before creating them. |
| P6, conditional | Per-shop opt-in, review-only recording, separate audio storage and controlled retrieval. | Only if the owner chooses to re-enable recording. Keep new audio retention disabled until the policy and controls are ready. |
| P3 | A real hold-to-talk experiment flow, followed by comparison with ambient capture. | Follow the roadmap's P1/P2 evidence order and owner experiment plan. Start/Stop listening is not a completed push-to-talk workflow. |
| P5.6, optional | Compare catalogue-ID-constrained LLM extraction against the rules on the same real transcript set. | Requires P1/P2 data; adoption needs a measured win. Do not make this an unconditional MVP dependency. |

P1 field collection, P2 engine/language measurement, P3 mode selection and P7
the controlled pilot remain open. P4.6 microphone contention needs physical
device evidence before deciding whether a code change is necessary. Other
implemented capture features still require the roadmap's phone and release
checks. Do not turn missing evidence into another implementation task.

### Branch handover while GitHub CI is unavailable

Continue work in claimed isolated branches and push completed feature work for
review. Report the branch/PR, full pushed SHA, checks actually run, failures,
checks not run, and **manual CI pending** until the owner verifies that revision
on the prepared machine. Do not repeatedly retry a known billing failure or
report blocked GitHub checks as green.

The manual procedure is currently in
`/tmp/listenledger-ci-jobs-guide/CI_jobs.md` on this workspace, local and unpushed;
transfer it before testing on another machine. No runner registration is needed.
New commits require renewed verification. Manual results do not alter GitHub
check badges or bypass branch rules. Emmanuel alone accepts evidence and merges;
phone/shop evidence remains required where the roadmap calls for it.
