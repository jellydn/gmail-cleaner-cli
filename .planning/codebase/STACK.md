# STACK.md — gclean technology stack

## Language & runtime

- **Go 1.26.4** (`go.mod`). Single-module repo, module name `gclean`.
- No CGO: SQLite is pure-Go via `modernc.org/sqlite`, so builds are fully
  cross-compilable and CI-friendly.
- CLI framework: `github.com/spf13/cobra v1.10.2` (`internal/cli/`).
- Logging: standard library `log/slog` (TextHandler to stderr), set up in
  `cmd/gclean/main.go`, used by the real Gmail client's retry/backoff paths.

## Direct dependencies (`go.mod`)

| Module | Version | Purpose |
| ------ | ------- | ------- |
| `github.com/spf13/cobra` | v1.10.2 | Command tree for every subcommand |
| `gopkg.in/yaml.v3` | v3.0.1 | Config file parsing (`internal/config/`) |
| `modernc.org/sqlite` | v1.56.0 | Pure-Go SQLite driver (`internal/storage/`) |
| `golang.org/x/oauth2` | v0.36.0 | OAuth2 token flow (`internal/gmailclient/oauth.go`) |
| `google.golang.org/api` | v0.293.0 | Gmail API client (`internal/gmailclient/real.go`) |
| `charm.land/bubbletea/v2` | v2.0.8 | TUI framework (`internal/tui/app.go`) |
| `charm.land/lipgloss/v2` | v2.0.6 | TUI styling (`internal/tui/app.go`) |

Notable indirect deps: `google.golang.org/grpc`, `golang.org/x/net`,
`modernc.org/libc`, OpenTelemetry SDK (pulled in by the Google API client),
`cloud.google.com/go/auth` (OAuth transport).

## Deliberately absent

- **No Viper** — config is parsed with `yaml.v3` directly. Viper would add
  30+ transitive deps for a single YAML file. Documented in
  `internal/config/config.go` and the README; swapping it in is a 1-file
  change.
- **No fsnotify** — `gclean dev` watch mode polls mtimes on a 2s interval
  instead of using a filesystem watcher (intentional, see
  `internal/cli/dev.go`).
- **No body fetching** — the local store is metadata-only (privacy default,
  `internal/models/models.go`).

## Build & config tooling

- **`just`** is the preferred task runner (`justfile`); a **`make`** mirror
  exists (`Makefile`). Recipes: `check` (vet+build+lint+test),
  `check-quick`, `e2e` (fixture-driven end-to-end), `lint-emails`.
- **Renovate** (`renovate.json`) manages dependency PRs (recent commits show
  automated `fix(deps)` bumps).
- **GitHub Actions** (`./.github/workflows/lint-emails.yml`) runs the
  email-literal lint on push/PR.
- **pre-commit** (`.pre-commit-config.yaml`) runs `go vet`, `go build`,
  `golangci-lint`, plus the email-literal lint as a local hook.
- `golangci-lint` is optional in the lint recipes (skipped with a notice if
  not installed).

## Configuration & runtime state (env-var driven)

| Env var | Default | Purpose |
| ------- | ------- | ------- |
| `GCLEAN_DB_PATH` | `~/.config/gclean/gclean.db` | SQLite DB path (`internal/cli/cli.go`) |
| `GCLEAN_CREDENTIALS_PATH` | `~/.config/gclean/credentials.json` | Gmail OAuth client creds |
| `GCLEAN_TOKEN_PATH` | `~/.config/gclean/token.json` | Persisted OAuth token (`internal/gmailclient/oauth.go`) |
| `GCLEAN_SELECTION_PATH` | `~/.config/gclean/tui-selection.json` | TUI sender cohort (`internal/cli/pipeline.go`) |
| `GCLEAN_CONFIG_PATH` | `~/.config/gclean/config.yaml` (honors `XDG_CONFIG_HOME`) | YAML rule config (`internal/config/config.go`) |
| `GCLEAN_UNDO_CACHE` | `~/.config/gclean/undo-cache.json` | Pre-trash undo records (`internal/cli/pipeline.go`) |

Config file is auto-created on first run with defaults (`.internal/config/config.go`).

## Data files

- `testdata/fixtures/messages.json` — 40-message Gmail-shaped fixture corpus
  used by `--fixtures` and tests. Sibling `messages.README.md` documents its
  structural requirements.
- `testdata/fixtures/messages.README.md` — ground-truth doc for the fixture
  (JSON can't hold comments).
