# INTEGRATIONS.md — gclean external integrations

## Gmail API (the primary integration)

- **Client library**: `google.golang.org/api/gmail/v1` via
  `gmail.NewService` (`internal/gmailclient/real.go`).
- **Auth**: OAuth2 desktop flow, `golang.org/x/oauth2`. `gclean login`
  starts a browser flow with a localhost callback server on an **allocated**
  loopback port (`internal/gmailclient/oauth.go`). Token persisted to
  `~/.config/gclean/token.json` (`$GCLEAN_TOKEN_PATH`) with mode 0600.
- **Scopes requested** (`internal/gmailclient/oauth.go`):
  - `gmail.readonly`
  - `gmail.modify`
  - `mail.google.com` (full access) — required because Google's backend
    rejects `batchDelete`/`delete` with `gmail.modify` alone
    (googleapis/google-api-python-client#2710).
- **Read path** (`RealClient.ListMessages`): paginates
  `users.messages.list` at 500/page, then fetches each message with
  `Format("metadata")` requesting headers `From, To, Cc, Subject, Date,
  List-Unsubscribe, List-ID, Precedence, Auto-Submitted` (the four
  classification headers are explicit).
- **Mutation path** (`internal/gmailclient/real.go`):
  - `TrashMessages` — per-ID `users.messages.trash`, retried.
  - `RestoreFromTrash` — per-ID `users.messages.untrash`, retried.
  - `EmptyTrash` — paginates `LabelIds("TRASH")` at 1,000/page, then
    `batchDelete` in chunks of up to 1,000; falls back to per-message
    `delete` on a 403 scope-insufficiency error.
- **Retry policy** (`retryMutation`): 3 attempts max; retries on
  `googleapi.Error` 429 / ≥500 and transport `*url.Error`. Wait honors the
  server's `Retry-After` header (delta-seconds or HTTP-date, capped at 60s)
  else jittered exponential backoff (1s doubling, ≤32s cap, ≤1s jitter).
- **Quota model** (from `.planning/live-account-mutation-test-plan.md`):
  ~6,000 units/min/user — `list`=5, `get`=20, `trash`=20, `untrash`=5,
  `batchDelete`=50, `insert`=25. The read path has **no retry** yet.

## Google People API

- **Not yet integrated.** `IsContact` enrichment is a documented gap:
  `mapGmailMessage` hard-codes `IsContact: false`
  (`internal/gmailclient/real.go`), so the planner's contacts keep-rule is a
  no-op against real data. The People API dependency is available
  (`cloud.google.com/go/auth` + `google.golang.org/api`) but not wired.
  Tracked in `.planning/real-gmail-test-plan.md` (Phase 1c).

## Google Cloud OAuth

- Requires a manually-created GCP project with the Gmail API enabled and a
  **Desktop app** OAuth client; `credentials.json` is dropped at
  `~/.config/gclean/credentials.json`. Setup steps are printed by
  `gclean login` (`internal/cli/auth.go`).

## Local persistence (not cloud)

- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO) — schema in
  `internal/storage/sqlite.go`. Metadata only; never message bodies.
- **Undo cache** — JSON file with version + SHA-256 checksum, written
  atomically (temp file + rename + dir sync), mode 0600
  (`internal/storage/undocache.go`).
- **TUI selection** — JSON file (`tui-selection.json`) with the selected
  sender cohort, written atomically (`internal/storage/selection.go`).

## No other third-party services

- No analytics, monitoring, error tracking, or payment services.
- No webhooks; no email sending.
- Renovate bot is the only CI automation beyond GitHub Actions itself.
