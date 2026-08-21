# STRUCTURE.md — gclean directory layout

```
.
├── cmd/gclean/main.go          Entry point: slog setup + cli.Build().ExecuteContext
├── internal/
│   ├── cli/                    Cobra command tree (thin handlers)
│   │   ├── cli.go              Build() root + resolveClient/storePath/credentialsPath/humanBytes
│   │   ├── auth.go             login / logout (OAuth browser flow)
│   │   ├── pipeline.go         scan / stats / dry-run / clean / purge / undo + undo-cache path
│   │   ├── insights.go         sender / attachments / newsletters / receipts + saveSelection
│   │   ├── meta.go             rules / config / tui
│   │   ├── demo.go             demo (self-contained TUI preview)
│   │   ├── dev.go              dev (polling watch-mode pipeline runner)
│   │   └── cli_test.go         integration tests (566 lines, largest test file)
│   ├── config/
│   │   ├── config.go           Document type, DefaultPath, Load, Parse, Compile/CompileFull
│   │   └── yaml.go             yaml.v3 wrapper (parse indirection)
│   ├── defang/
│   │   └── defang.go           MkEmail / DefangSlice / DefangMap (runtime email assembly)
│   ├── engine/                 Pure, deterministic decision logic
│   │   ├── classifier.go       Junk signal ranking (noreply > vendor > headers > categories)
│   │   ├── protector.go        §6 keep profile (labels, contact, recent, whitelist)
│   │   ├── evaluator.go        Rules DSL: ParseRule, Predicate, Matches, ParseDuration, ParseByteSize
│   │   ├── planner.go          Plan() verdicts + RuleConfig + safety §15
│   │   ├── pipeline.go         Pipeline + Stage seam (scan→plan→apply)
│   │   └── *_test.go           classifier/protector/evaluator/planner tests
│   ├── gmailclient/            Gmail boundary seam
│   │   ├── client.go           Client interface (List/Trash/EmptyTrash/Restore)
│   │   ├── fake.go             FakeClient — fixture-driven, in-memory trash
│   │   ├── real.go             RealClient — OAuth, retrying, paginating
│   │   ├── oauth.go            OAuth config, token IO, AuthCodeServer, OpenBrowser
│   │   └── *_test.go           fake / oauth / real tests (httptest-driven)
│   ├── models/
│   │   └── models.go           Cross-package types, Reason* codes, Verdict enum, reports
│   ├── storage/                Local persistence
│   │   ├── sqlite.go           Schema, Store, Upsert, verdicts, trash/restore
│   │   ├── stats.go            Aggregations() one-scan rollups + LargestAttachments
│   │   ├── undocache.go        Versioned + checksummed undo cache
│   │   ├── selection.go        TUI sender cohort (atomic write, legacy compat)
│   │   ├── sendersafety.go     SenderSafety type (TUI input)
│   │   └── *_test.go           undocache / selection tests
│   └── tui/
│       ├── app.go              Bubble Tea Model (headless-testable)
│       └── app_test.go         Update/View tests with mkT + stripANSI helpers
├── testdata/fixtures/
│   ├── messages.json           40-message Gmail-shaped fixture corpus
│   └── messages.README.md      Fixture ground-truth doc
├── scripts/
│   └── lint-email-literals.sh  Reject raw email literals in non-test source
├── .github/workflows/
│   └── lint-emails.yml         CI: email-literal lint on push/PR
├── .planning/                  Agent planning docs (real-Gmail + live-mutation test plans)
├── .plans/
│   └── implement-notes.md      AI-agent handoff trail (dated entries)
├── go.mod / go.sum             Module + deps
├── justfile / Makefile         Task runners (just preferred, make mirrors)
├── .pre-commit-config.yaml     Pre-commit hooks (vet/build/golangci/email-lint)
├── renovate.json               Renovate dependency automation
├── AGENTS.md                   Agent guide (dev commands, safety invariants, env vars)
└── README.md                   Status + dev workflow
```

## Naming conventions

- **Packages**: single-word lowercase (`engine`, `storage`, `models`,
  `config`, `defang`, `tui`). The `cli` package holds many files split by
  domain (auth/pipeline/insights/meta/demo/dev).
- **Files**: snake_case (`newScanCmd` lives in `pipeline.go`, not a
  per-command file). Test files are `*_test.go` beside their subject.
- **Constructors**: `newXxxCmd(out, errOut io.Writer) *cobra.Command` for
  every subcommand.
- **Types**: exported types carry doc comments; `models.Reason*` constants
  are stable, append-only strings (never reorder).
- **Env vars**: `GCLEAN_*` prefix, resolved via small helpers
  (`storePath`, `credentialsPath`, `defaultCache`, `selectionPath`,
  `tokenPath`).

## Key locations to remember

- Safety-critical planner: `internal/engine/planner.go` (step order +
  §15 non-junk refusal).
- Pipeline seam: `internal/engine/pipeline.go` (Stage boundary; Apply is the
  only Gmail-mutating stage).
- Gmail adapter + retry: `internal/gmailclient/real.go`.
- Schema + rollups: `internal/storage/sqlite.go`, `internal/storage/stats.go`.
- Command registration lock: `TestBuild_Help` in `internal/cli/cli_test.go`.
- Obfuscation defense: `internal/defang/defang.go` + `scripts/lint-email-literals.sh`.
