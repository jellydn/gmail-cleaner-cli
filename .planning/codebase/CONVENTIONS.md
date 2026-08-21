# CONVENTIONS.md — gclean coding conventions

## Code style

- Standard `gofmt`; the repo is gofmt-clean. `go vet ./...` is part of the
  default gate (`just check`).
- Go 1.26 idioms. Note: Go 1.26 promoted `maplit` ("duplicate key in map
  literal") from vet warning to **build failure** — prefer slice-of-structs
  case tables over map literals in tests (see `.plans/implement-notes.md`).
- `golangci-lint` is run when installed but is optional in the lint recipes.
- Doc comments on all exported identifiers, especially in `internal/models`
  and the pure `internal/engine` package.

## Package-level conventions

- **`internal/engine` is pure** — no I/O, no clocks except passed-in
  arguments. This is a documented invariant (`internal/engine/classifier.go`
  package doc) so the decision logic is unit-testable against fixtures.
- **`internal/config` → `engine` only**; `engine` never imports `config`.
- **`internal/gmailclient` is the only package that touches Gmail.** The
  rest of the codebase depends on the `Client` interface
  (`internal/gmailclient/client.go`), never on `RealClient`/`FakeClient`
  concretely.
- **The engine declares its own narrow `Gmailer` interface** rather than
  importing `gmailclient` (`internal/engine/pipeline.go`).

## The email-literal rule (CRITICAL)

Raw `local@domain` literals are **forbidden in non-test `*.go`/`*.json`**.
CI (`scripts/lint-email-literals.sh`) and pre-commit reject them because
Cloudflare's source-pass silently rewrites such literals to `[email protected]`
(no `@`), breaking domain extraction and equality checks. Always assemble at
runtime:

```go
addr := defang.MkEmail("noreply", "example.com") // "noreply@example.com"
```

- `defang.MkEmail` lives in `internal/defang` (non-test) so production
  fixture loaders and demo commands can use it too.
- Tests use the same pattern (`defang.MkEmail`, or a local `mkT`/`mk` join).
- **Data fixtures on disk are also vulnerable**: tests that assert on
  email identity build synthetic fixtures at runtime via `MkEmail` rather
  than trusting `testdata/fixtures/messages.json`
  (`TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders`).
- The lint allows `*_test.go`, `testdata/`, `vendor/`, `.git`, `.plans`, and
  comment lines.

## Command & handler conventions

- Every subcommand is `newXxxCmd(out, errOut io.Writer) *cobra.Command`,
  registered in `Build()` (`internal/cli/cli.go`). Adding a subcommand MUST
  also extend `TestBuild_Help`'s substring list.
- Handlers are thin: they open the store, resolve client + config, build an
  `engine.Pipeline`, and run the stage slice they need. Heavy logic lives in
  `engine`/`storage`/`gmailclient`.
- `--yes` is required before `clean`/`purge` mutate state; the gate is
  enforced at the top of the handler (`internal/cli/pipeline.go`).
- `--fixtures PATH` wins over real Gmail in `resolveClient` so dev/test
  always uses `FakeClient`.

## Error handling

- Errors are wrapped with `%w` and contextual prefixes
  (`fmt.Errorf("trash: %w", err)`).
- Cobra runs with `SilenceUsage: true` and `SilenceErrors: true`; the entry
  point prints `error: ...` to stderr and exits 1
  (`cmd/gclean/main.go`).
- Human-readable "refusing" messages go to stderr *and* the command returns
  a sentinel error (e.g. `errors.New("confirmation required")`).
- The engine returns typed errors with stable strings where tests assert on
  them (e.g. `ErrCredentialsMissing`, `"checksum mismatch"`).

## Concurrency & state

- `FakeClient` guards its in-memory trash map with a `sync.Mutex`
  (`internal/gmailclient/fake.go`).
- Undo cache and selection files are written **atomically**: temp file +
  `fsync` + rename + directory sync (`internal/storage/undocache.go`,
  `internal/storage/selection.go`). Never write the canonical path directly.
- The undo cache refuses to overwrite a non-empty existing cache (`undo
  cache already exists ...`).

## Safety invariants (from AGENTS.md)

- `--yes` required before `clean`/`purge` modify state.
- Planner refuses to delete a non-junk message even if a delete rule matches
  (`internal/engine/planner.go`).
- `clean` moves to Trash (recoverable); only `purge` empties Trash
  permanently.
- Undo cache preserves pre-trash records and is written **before** any Gmail
  mutation.

## Documentation & handoff

- Non-obvious decisions land in `.plans/implement-notes.md` as dated,
  categorized entries (blocker/issue/finding/learning), appended (newest at
  bottom), never overwritten.
- Planning docs live in `.planning/` (e.g. real-Gmail and live-mutation test
  plans).
- `AGENTS.md` is the canonical agent guide and is kept in sync with the
  README.
