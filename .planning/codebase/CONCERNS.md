# CONCERNS.md — gclean technical concerns

## Safety / correctness (highest priority)

- **Real-Gmail local reconciliation is still being hardened.** `clean`,
  `undo`, and `purge` mutate Gmail and SQLite/undo-cache as separate
  operations with no transaction spanning both. A partial or interrupted run
  can leave local state out of sync with Gmail. AGENTS.md and both
  `.planning/*.md` plans call this out as the next safety layer. **Do not run
  destructive real-account flows until reconciliation + live validation are
  complete.**
- **`IsContact` is never set on the real path** — `mapGmailMessage` hard-codes
  `IsContact: false` (`internal/gmailclient/real.go`). The planner's
  contacts keep-rule is a no-op against real data; only fixture data
  exercises it. People API enrichment is a documented gap
  (`internal/gmailclient/real.go`, `.planning/real-gmail-test-plan.md`).
- **`REPLIED` label is never produced on the real path** — `Protect()` checks
  for a `REPLIED` label (`internal/engine/protector.go`), but the real
  `ListMessages` doesn't fetch thread-level reply metadata. Replied-to
  messages lose that protection signal live.
- **Read path has no retry** — `ListMessages` (`internal/gmailclient/real.go`)
  does a single attempt per page; a 429/5xx aborts the whole scan. Mutation
  paths retry, reads don't. Noted in `.planning/live-account-mutation-test-plan.md`.
- **`undo` failure semantics are unverified live** — `RestoreFromTrash`
  aborts the whole batch on the first per-message error; whether Gmail
  rejects an untrash for a message not in Trash is unspecified. TC-05 in the
  live plan flags this as needing a finding/decision (warn vs. hard error).

## Email-obfuscation landmine (recurring)

- Cloudflare's source-pass silently rewrites `local@domain` literals to
  `[email protected]` (no `@`) in source **and** committed data files.
  Defense is `defang.MkEmail` + `scripts/lint-email-literals.sh` +
  `TestMessagesJSON_HasNoPlaceholder`. Historical corruption hit the fixture
  (`testdata/fixtures/messages.json`), TUI tests, and CLI tests —
  documented extensively in `.plans/implement-notes.md`. Any new code that
  touches email-shaped strings must use runtime assembly.
- The lint currently excludes `testdata/` and comment lines; the README/AGENTS
  example strings and `.planning` docs are not linted (they're prose, but a
  corrupted example in docs misleads).

## Security / privacy

- **`mail.google.com` full-access scope is requested** (`oauth.go`) solely
  because Google's backend rejects `batchDelete`/`delete` with `gmail.modify`
  (googleapis/google-api-python-client#2710). Full access is a sensitive
  scope; the consent screen says so. Revisit if Google ever relaxes this.
- **Destructive-by-design surface**: `purge` is permanent and
  irreversible. Protected by `--yes` only; there is no confirmation prompt
  beyond the flag. A stray `gclean purge --yes` on the wrong account is
  unrecoverable.
- **Tokens/creds are stored with restrictive perms** (0600 token, 0600 undo
  cache/selection, 0700 dirs) — good. `credentials.json` is expected to be
  user-managed.
- **FakeClient rejects symlinked fixture paths** (`fake.go`) to stop
  `--fixtures` from reading arbitrary symlink targets. Dev/test-only input.
- No secrets are committed; `.plans/implement-notes.md` mandates sanitized
  snippets.

## Performance / scale

- **`scan` fetches all messages** (`ListMessages("", 0)`) — paginates 500 at
  a time with one `get` per message (20 quota units each). A multi-decade
  mailbox will be slow and quota-bound. No `--query` flag exists on `scan`
  yet (a UX improvement noted in `.planning/real-gmail-test-plan.md`).
- **`Aggregations()` does one full table scan** per stats/sender/TUI call
  (`internal/storage/stats.go`) and `AllClassified` re-reads every row for
  dry-run/clean planning. Fine for the 40-message fixture; unbounded on a
  real mailbox.
- **`TrashMessages`/`RestoreFromTrash` are sequential per-ID calls** with
  retry — a large cohort is slow and quota-heavy. `EmptyTrash` batches
  (1,000/page) but falls back to per-message `delete` (10 units each) when
  scope blocks `batchDelete`.
- **`gclean dev` watch mode polls on a 2s timer** — fine for a dev tool, but
  it re-runs a full scan+stats+dry-run (full DB rewrite) on every fixture
  edit.

## Tech debt / code quality

- **Duplicated helpers**: `humanBytes` exists in `internal/cli/cli.go` and
  again in `internal/tui/app.go`; `truncate` in `internal/cli/insights.go`
  and `internal/tui/app.go`; `mkT` (tui test) duplicates `defang.MkEmail`.
  `.plans/implement-notes.md` suggests lifting shared helpers into a
  `internal/util/` package when a second consumer appears.
- **`internal/engine/pipeline.go` duplicates the StoredMessage construction**
  between `fetchAndClassify` and `applyTrash` (two near-identical
  `StoredMessage{...}` literals).
- **`models.StatsReport`/aggregations live partly in storage and partly in
  models** — the `Aggregations` consolidation (stats.go) addressed
  duplicate table scans, but the report type straddles `models` and
  `storage`.
- **No test files for `internal/config`, `internal/defang`, `internal/models`**
  (thin/leaf packages — acceptable, but `config.Compile` error paths are
  untested).
- **`gclean dev` watch mode is untested** (deliberately; only one-shot mode
  is covered).
- **`gclean rules` is show-only** — no editor; the README roadmap lists a
  `gclean rules` editor as future work.
- A couple of docstrings reference PRD §N (e.g. §5/§6/§9/§12/§15/§16/§17);
  the PRD itself isn't in the repo, so those references are opaque to new
  readers (mitigated by AGENTS.md and the README).

## Fragile seams

- **`engine.Plan`'s step order is the safety-critical seam** — any logic
  change there is the only thing deciding what gets trashed. Protected by
  `findByID`-based planner tests, but no property/fuzz tests exist.
- **`TestBuild_Help` registration lock** — adding a subcommand without
  updating the substring list breaks CI (intentional guard).
- **`retryDelay` is a package-level var** swapped by tests
  (`real_test.go`) — a concurrency hazard if tests ever run in parallel
  (they currently don't race).
- **Fixture diversity threshold** (`minDistinctFixtureSenders = 30`) —
  removing a sender from the corpus trips the test by design; growing the
  corpus requires bumping it.

## Roadmap items (from README)

- Reconcile local SQLite + undo-cache after partial/interrupted real Gmail
  mutations (the top safety item).
- People-API `IsContact` enrichment on scan.
- Per-message rate-limited batcher for `clean`.
- `gclean rules` editor; `gclean report` analytics export.
- `--query` flag on `scan` to narrow real-mailbox scans.
