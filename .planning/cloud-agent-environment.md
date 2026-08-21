# Cloud Agent environment setup plan

Goal: make `gclean` fully usable in Cursor Cloud Agents — build, vet, lint, test,
and the end-to-end fixture cleanup flow all runnable on a fresh boot.

## Discovery

- Language/toolchain: Go `1.26.4` (see `go.mod`), pure-Go SQLite (`modernc.org/sqlite`),
  no CGO required for tests. `go` is present on the base image (auto-fetches the
  pinned toolchain on first use).
- Task runner: `just` (preferred, per `AGENTS.md`); `Makefile` mirrors it.
- Lint: `scripts/lint-email-literals.sh` (bash, required by CI) + optional
  `golangci-lint` (justfile/Makefile skip gracefully when absent). No
  `.golangci.yml` — default linters.
- CI (`.github/workflows/lint-emails.yml`) only runs the email-literal lint.
- Product: a CLI (`cmd/gclean`). No servers, databases, or daemons to run — the
  end-to-end dev flow is driven by `--fixtures` against `testdata/fixtures/messages.json`.

## Base image

Cursor default image. It already provides `go`, `git`, `curl`, and `cargo`.
Egress is unrestricted, so the pinned Go toolchain and the two extra CLIs download
at install time. No custom Dockerfile is warranted.

## Lifecycle split

- `install` (`.cursor/install.sh`, idempotent):
  1. `go mod download` — prime the module cache.
  2. Install `just` (task runner) if missing.
  3. Install `golangci-lint` (documented `just check` gate) if missing.
  4. `go build ./...` — warm the build cache and fail fast on breakage.
- `start` / `terminals`: none. `gclean` is a CLI with no long-running services.

## Validation

- `just check` → vet + build + golangci-lint (0 issues) + email lint + tests.
- E2E fixture flow: `scan` (40 msgs) → `stats` → `dry-run` → `clean --yes`
  (25 → Trash) → `undo` (25 restored).
- Run `install` twice (and once after removing the tools) to prove idempotence.
- Snapshot → draft build → fresh-agent verification.
