// Package gmailclient is the boundary between gclean and Gmail. The rest of
// the codebase must depend on the Client interface, not on RealClient or
// FakeClient concretely. Swap implementations at wiring time.
package gmailclient

import "gclean/internal/models"

// Client is the Gmail-interface seam used by every gclean command.
// Implementations:
//
//   - FakeClient   — fixture-driven, used for tests and local dev (no network)
//   - RealClient   — talks to actual Gmail over HTTPS+OAuth for reads and
//     batched, retrying mutations
type Client interface {
	// ListMessages returns messages matching `query` (same syntax as the
	// Gmail web search bar) up to `max`. max==0 means "all".
	ListMessages(query string, max int) ([]*models.Message, error)

	// TrashMessages moves the given message IDs to Gmail's Trash.
	// Trash is recoverable for 30 days server-side. This is intentionally
	// different from permanent delete.
	TrashMessages(ids []string) error

	// EmptyTrash permanently deletes everything currently in Trash.
	// This is the ONE gclean operation that Gmail's own Undo cannot
	// reverse. We require explicit confirmation at the CLI layer.
	EmptyTrash() error

	// RestoreFromTrash undoes a TrashMessages call where possible
	// (within Gmail's 30-day window). Best-effort.
	RestoreFromTrash(ids []string) error

	// InTrash returns the subset of ids currently in Gmail's Trash. It is
	// the source of truth for reconciling local state after a partial
	// mutation: a message trashed server-side whose local mark failed (or
	// vice versa) can be detected, so the undo cache and the SQLite store
	// never silently drift from Gmail.
	InTrash(ids []string) ([]string, error)
}
