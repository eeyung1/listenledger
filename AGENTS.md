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
