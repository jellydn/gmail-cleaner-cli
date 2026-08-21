# ARCHITECTURE.md — gclean architecture

## Overview

gclean is a Go CLI that reclaims Gmail storage safely. The core idea is a
**local-first, safe-by-default pipeline**: pull message *metadata* from
Gmail, classify and plan against local rules, and only ever move the
"delete cohort" to Trash (recoverable), with an undo cache so the operation
is reversible. The hard safety rule (PRD §15): **never delete a non-junk
message even if a delete rule matches**.

## Layering / dependency direction

```
cmd/gclean (main, slog)
   └── internal/cli        (Cobra command tree — thin handlers)
        ├── internal/engine    (classifier, protector, planner, DSL evaluator, pipeline)
        ├── internal/storage   (SQLite + undo cache + selection + aggregations)
        ├── internal/config    (YAML → engine.RuleConfig)   [config → engine only]
        ├── internal/gmailclient (Client interface + Fake + Real + OAuth)
        ├── internal/models    (cross-package types)
        ├── internal/tui       (Bubble Tea UI)
        └── internal/defang    (MkEmail runtime address assembly)
```

Key invariants:
- `internal/engine` is **pure and deterministic** — no I/O, no clocks beyond
  what's passed in. It declares its own narrow `Gmailer` interface
  (`internal/engine/pipeline.go`) so it never imports `gmailclient`.
- `internal/config` imports `engine` (to build `RuleConfig`/`KeepConfig`),
  but `engine` never imports `config` — one-way dependency
  (`internal/config/config.go`).
- Everything depends on `internal/models` for shared types; field names
  mirror the Gmail API JSON shape so one struct decodes real and fixture
  responses.

## The pipeline seam (`internal/engine/pipeline.go`)

The scan→plan→trash flow is `engine.Pipeline`, built from composable
`Stage` functions. The CLI owns store open/close and client/config/cache
resolution; the pipeline holds already-resolved dependencies.

- `ScanStages()` → `fetchAndClassify`: `ListMessages` → `Classify` per
  message → `Store.Upsert`.
- `PlanStages()` → `loadPlan`: read classified rows → apply TUI selection →
  `Plan()` → write verdicts back. **No Gmail I/O.**
- `ApplyStages()` → `applyTrash`: write undo cache **first** (fatal on
  error), then `TrashMessages`, then `Store.MarkTrashed`. The **only**
  Gmail-mutating stage.

## Decision flow — `engine.Plan` (`internal/engine/planner.go`)

Per-message order of operations (safety-critical seam):

1. Sender not in TUI selection → `VerdictKeep` (`selection_excluded`).
2. Domain in `ignore:` → `VerdictProtected` (`ignored_domain`).
3. `Protect()` wins (starred/important/sent/contact/replied/recent/whitelist)
   → `VerdictProtected`.
4. Config `keep:` rule match → `VerdictKeep`.
5. Config `archive:` rule match → `VerdictArchive`.
6. Config `delete:` rule match → `VerdictDelete` **only if classified junk**;
   otherwise `VerdictKeep` (`delete_rule_refused_non_junk`).
7. Default → `VerdictKeep`.

Decisions are sorted by size DESC (largest deletes first) for the report.

## Classification (`internal/engine/classifier.go`)

Signal priority: **noreply local-part prefix > known vendor domains > RFC822
header signals (List-Unsubscribe, List-ID, Precedence: bulk|list|junk,
Auto-Submitted) > Gmail categories** (PROMOTIONS/SOCIAL/UPDATES/FORUMS).
Each yields a stable `ReasonCode` string (`internal/models/models.go`).

## Protection (`internal/engine/protector.go`)

`Protect` applies the §6 keep profile: hard labels first (STARRED/IMPORTANT/
SENT), then identity (REPLIED label / IsContact), then the recent window
(`recent_days`), then the domain whitelist. First hit wins.

## Gmail client seam (`internal/gmailclient/client.go`)

`Client` interface: `ListMessages`, `TrashMessages`, `EmptyTrash`,
`RestoreFromTrash`. Two implementations:
- `FakeClient` — fixture-driven, in-memory trash state, no network
  (`fake.go`). Rejects symlinked/non-regular fixture paths.
- `RealClient` — OAuth-backed, retrying, paginating (`real.go`).

The engine uses the narrower `Gmailer` interface; the CLI resolves the
concrete client via `resolveClient` (`--fixtures` wins,
`internal/cli/cli.go`).

## Storage (`internal/storage`)

- `sqlite.go` — single `messages` table + indexes; `Upsert` (idempotent by
  ID), `SetVerdict`, `AllClassified`, `MarkTrashed` (transactional delete),
  `RestoreTrashed` (transactional re-insert).
- `stats.go` — `Aggregations()` does **one** table scan producing the
  `StatsReport`, per-sender volume ranking, and per-sender safety split
  (capped at 200 rows for the TUI).
- `undocache.go` — versioned, checksummed, atomically-written undo records.
- `selection.go` — TUI sender cohort, atomically-written, legacy-format
  compatible.

## CLI command surface (`internal/cli/`)

- `auth.go` — `login`, `logout`.
- `pipeline.go` — `scan`, `stats`, `dry-run`, `clean`, `purge`, `undo`
  (thin adapters over `engine.Pipeline`).
- `insights.go` — `sender`, `attachments`, `newsletters`, `receipts` +
  `saveSelection`.
- `meta.go` — `rules`, `config`, `tui`.
- `demo.go` — `demo` (self-contained TUI preview, no I/O).
- `dev.go` — `dev` (polling watch-mode pipeline runner for fixture dev).

`Build(stdout, stderr io.Writer)` injects test buffers; every subcommand is
registered there, and `TestBuild_Help` locks the registration.

## TUI (`internal/tui/app.go`)

Bubble Tea `Model` with headless-testable `Update` (no tea runtime needed).
Pre-selects senders with ≥1 delete candidate; commits write the selection
file via `saveSelection`.

## Entry point

`cmd/gclean/main.go`: slog TextHandler → `cli.Build(nil, nil).ExecuteContext`
→ prints `error: ...` to stderr and exits 1 on failure.
