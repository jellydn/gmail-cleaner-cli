# TESTING.md — gclean testing practices

## Framework & commands

- Standard library `testing`. No third-party test framework.
- Run everything: `go test ./...` (pure-Go SQLite, no CGO).
- Package-scoped: `go test ./internal/engine/`.
- Single integration test: `go test -run TestScanCommand_DevFixturePipeline ./internal/cli/`.
- Default gate: `just check` (vet + build + lint + test); quick gate:
  `just check-quick`.
- The repo is currently green: `go build ./...`, `go vet ./...`,
  `go test ./...`, and `./scripts/lint-email-literals.sh` all pass.

## Test layers

| Layer | Location | Style |
| ----- | -------- | ----- |
| Engine unit tests | `internal/engine/*_test.go` | Pure, table-driven, no I/O, no clocks |
| Gmail client tests | `internal/gmailclient/*_test.go` | `httptest` servers + stub retry delay |
| Storage tests | `internal/storage/*_test.go` | `t.TempDir()` files, atomic-write assertions |
| TUI tests | `internal/tui/app_test.go` | Headless `Update`/`View` against the concrete `Model` |
| CLI integration | `internal/cli/cli_test.go` | Full command runs via `Build()` + `SetArgs()` + `Execute()` |

## Key patterns

- **`cli.Build(stdout, stderr io.Writer)`** injects `bytes.Buffer` writers so
  tests capture output without real streams (`internal/cli/cli_test.go`).
- **Temp state**: `t.TempDir()` + `t.Setenv("GCLEAN_DB_PATH", ...)` (and
  `GCLEAN_UNDO_CACHE`, `GCLEAN_SELECTION_PATH`, `GCLEAN_CONFIG_PATH` as
  needed) isolate each test from the real `~/.config/gclean`.
- **Synthetic fixtures**: tests that assert on email identity build JSON
  fixtures at runtime with `defang.MkEmail` and write them to a temp dir,
  then drive the full `scan --fixtures` pipeline
  (`TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders`,
  `TestDevCommand_OneShotMode_RendersPipeline`). This dodges the
  email-obfuscation risk of the on-disk fixture.
- **`findByID`** (`internal/engine/planner_test.go`): planner tests look up
  decisions by message ID, never by positional index — `Plan()` sorts by
  size DESC, so positional assertions silently break.
- **`stubRetryDelay`** (`internal/gmailclient/real_test.go`): replaces the
  package-level `retryDelay` var with a zero/stub function so retry tests
  are fast and deterministic; the real `defaultRetryDelay` is still invoked
  to record computed waits.
- **`rewriteHostTransport`** (`real_test.go`): rewrites request hosts to an
  `httptest.Server` so `RealClient` mutation paths are exercised without
  network.
- **`mkT`/`stripANSI`** (`internal/tui/app_test.go`): `mkT` joins addresses
  at runtime; `stripANSI` removes lipgloss escape codes so view assertions
  match visible text.
- **Bare key constants**: TUI tests wrap keys as `tea.KeyPressMsg{Code:
  tea.KeyDown}` — passing the bare `tea.KeyType` constant falls through the
  type switch and silently does nothing.

## What's covered

- Classifier: each signal tier (noreply prefixes, vendor domains, header
  signals, categories) plus `extractDomain`/`headerValue` helpers.
- Protector: starred (on/off), recent window, contact, whitelist, sent-by-user.
- Evaluator: rule parsing (incl. comma tolerance + invalid input), duration
  and byte-size parsing, rule matching (incl. empty-rule-never-matches).
- Planner: TUI selection exclusion, §15 non-junk refusal, keep-beats-delete,
  ignored domain protection, protection-beats-rules, archive.
- Gmail client: fake list/trash/restore; real retry on 429, Retry-After
  (seconds + HTTP-date + cap), exponential backoff bounds, untrash endpoint,
  EmptyTrash 403 fallback to per-message delete, EmptyTrash batch pagination.
- Storage: undo-cache atomic round-trip + overwrite protection + tamper
  detection; selection round-trip + legacy format + missing = unrestricted.
- TUI: pre-selection, space toggle, j/k cursor + clamping, q/ctrl+c quit,
  enter commit + stats, a/n select-all/clear, window resize, empty-list
  guards, view rendering.
- CLI: `--help` registration lock, fixture pipeline (scan→stats→dry-run→
  clean), `--yes` gate, selection limits dry-run+clean, demo output,
  synthetic-fixture sender output, fixture placeholder regression lock,
  dev one-shot pipeline.

## Not covered (deliberately or known gaps)

- `gclean dev` **watch mode** is untested (timing/signals); only one-shot
  mode is tested. The loop body is identical, so coverage is considered
  sufficient.
- Real-Gmail end-to-end (read + mutation) is not in CI — it's documented as
  runbooks in `.planning/real-gmail-test-plan.md` and
  `.planning/live-account-mutation-test-plan.md`.
- `internal/config`, `internal/defang`, `internal/models` have no test files
  (thin/leaf packages).
- `TestMessagesJSON_HasNoPlaceholder` guards the fixture corpus against
  re-obfuscation (bytes-level placeholder scan + ≥30 distinct senders).
