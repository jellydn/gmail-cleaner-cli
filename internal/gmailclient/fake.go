package gmailclient

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"gclean/internal/models"
)

// FakeClient serves messages from a JSON fixture file on disk. Used for local
// dev, tests, and the §5 scan/dry-run/clean flow when no credentials.json
// exists.
//
// Trash/Restore/EmptyTrash mutate in-memory state only. The fixture file is
// the source of truth and is not modified. This lets us demo the full
// pipeline without an OAuth round-trip.
type FakeClient struct {
	mu      sync.Mutex
	msgs    []*models.Message
	trashed map[string]bool
}

// NewFakeClient loads a fixture JSON file (an array of Gmail-shaped messages).
//
// The path is validated before opening: it must be a regular file (not a
// directory) and must not be a symlink. os.Open follows symlinks, so without
// this guard `--fixtures` could be pointed at an arbitrary symlinked target
// outside the intended tree (CONCERNS.md #10). This is dev/test-only input,
// so rejecting symlinks is acceptable.
func NewFakeClient(path string) (*FakeClient, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("fake: stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("fake: fixtures path %s must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("fake: fixtures path %s must be a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("fake: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("fake: read %s: %w", path, err)
	}
	var msgs []*models.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("fake: parse %s: %w", path, err)
	}
	for i := range msgs {
		if msgs[i].Headers == nil {
			msgs[i].Headers = map[string]string{}
		}
	}
	return &FakeClient{msgs: msgs, trashed: map[string]bool{}}, nil
}

// NewFakeClientFromMessages builds an in-memory fake without disk I/O.
func NewFakeClientFromMessages(msgs []*models.Message) *FakeClient {
	cp := make([]*models.Message, len(msgs))
	copy(cp, msgs)
	return &FakeClient{msgs: cp, trashed: map[string]bool{}}
}

func (f *FakeClient) ListMessages(query string, max int) ([]*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*models.Message, 0, len(f.msgs))
	for _, m := range f.msgs {
		if f.trashed[m.ID] {
			continue
		}
		if query != "" {
			ok, err := matchQuery(m, query)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, m)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out, nil
}

func (f *FakeClient) TrashMessages(ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.trashed[id] = true
	}
	return nil
}

func (f *FakeClient) EmptyTrash() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trashed = map[string]bool{}
	return nil
}

func (f *FakeClient) RestoreFromTrash(ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		delete(f.trashed, id)
	}
	return nil
}

// TrashedIDs exposes currently trashed IDs (used by undo to know what to
// restore). Not part of the Client interface — used by tests and CLI.
func (f *FakeClient) TrashedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.trashed))
	for id := range f.trashed {
		out = append(out, id)
	}
	return out
}

// matchQuery applies the fake's query contract. Terms are ANDed, matching
// Gmail's search semantics. The supported token subset is documented below;
// any other token is an error so a query the fake cannot faithfully honor
// fails loudly instead of silently returning wrong results (the previous
// implementation ORed tokens and fell back to a subject substring for
// unknown ones, which could drift from real Gmail behavior).
func matchQuery(m *models.Message, q string) (bool, error) {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return true, nil
	}
	for _, t := range tokens {
		ok, err := matchToken(m, t)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// matchToken evaluates one query token.
//
// Supported tokens:
//   - from:<substring>    — case-insensitive substring of the From address
//   - subject:<substring> — case-insensitive substring of the subject
//   - label:<name>        — exact label match
//   - category:<name>     — Gmail category (promotions, social, ...)
//   - has:<header-substr> — case-insensitive substring of a header key
//     (aligned with the engine DSL's has: predicate)
//
// Unsupported tokens return an error.
func matchToken(m *models.Message, t string) (bool, error) {
	switch {
	case strings.HasPrefix(t, "from:"):
		return strings.Contains(strings.ToLower(m.Sender.Email), strings.ToLower(strings.TrimPrefix(t, "from:"))), nil
	case strings.HasPrefix(t, "subject:"):
		return strings.Contains(strings.ToLower(m.Subject), strings.ToLower(strings.TrimPrefix(t, "subject:"))), nil
	case strings.HasPrefix(t, "label:"):
		want := strings.TrimPrefix(t, "label:")
		for _, l := range m.Labels {
			if l == want {
				return true, nil
			}
		}
		return false, nil
	case strings.HasPrefix(t, "category:"):
		want := "CATEGORY_" + strings.ToUpper(strings.TrimPrefix(t, "category:"))
		for _, l := range m.Labels {
			if l == want {
				return true, nil
			}
		}
		return false, nil
	case strings.HasPrefix(t, "has:"):
		needle := strings.ToLower(strings.TrimPrefix(t, "has:"))
		for k := range m.Headers {
			if strings.Contains(strings.ToLower(k), needle) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("fake: unsupported query token %q (supported: from:, subject:, label:, category:, has:)", t)
	}
}
