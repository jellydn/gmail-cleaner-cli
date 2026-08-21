// Build any email-shaped assertion via `defang.MkEmail(local, domain)`, not a
// literal — see TestDemoCommand_RendersExpectedOutput for the pattern.
// Otherwise the test file itself becomes a Cloudflare-obfuscation attack
// surface.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gclean/internal/config"
	"gclean/internal/defang"
	"gclean/internal/engine"
	"gclean/internal/models"
	"gclean/internal/storage"
)

func TestBuild_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := Build(&out, &errOut)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help should not error: %v", err)
	}
	body := out.String()
	for _, want := range []string{"login", "logout", "scan", "stats", "dry-run", "clean", "undo", "purge", "dev"} {
		if !strings.Contains(body, want) {
			t.Errorf("--help missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestScanCommand_DevFixturePipeline(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GCLEAN_DB_PATH", tmp+"/gclean.db")
	t.Setenv("GCLEAN_UNDO_CACHE", tmp+"/undo-cache.json")

	var out bytes.Buffer
	cmd := Build(&out, &out)
	cmd.SetArgs([]string{"scan", "--fixtures", "../../testdata/fixtures/messages.json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !strings.Contains(out.String(), "Scanned") {
		t.Errorf("scan output missing 'Scanned': %s", out.String())
	}

	out.Reset()
	cmd = Build(&out, &out)
	cmd.SetArgs([]string{"stats"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats: %v", err)
	}
	body := out.String()
	for _, sec := range []string{"Total messages", "Estimated storage", "Potential reclaim", "By category"} {
		if !strings.Contains(body, sec) {
			t.Errorf("stats missing section %q\n%s", sec, body)
		}
	}

	out.Reset()
	cmd = Build(&out, &out)
	cmd.SetArgs([]string{"dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	body = out.String()
	for _, sec := range []string{"Safe to delete", "Recover", "Will keep", "Nothing changes"} {
		if !strings.Contains(body, sec) {
			t.Errorf("dry-run missing section %q\n%s", sec, body)
		}
	}

	out.Reset()
	cmd = Build(&out, &out)
	cmd.SetArgs([]string{"clean", "--yes", "--fixtures", "../../testdata/fixtures/messages.json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if !strings.Contains(out.String(), "Moved 0 messages") && !strings.Contains(out.String(), "Moved") {
		t.Errorf("clean output should report count: %s", out.String())
	}
}

func TestSelectionLimitsDryRunAndClean(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GCLEAN_DB_PATH", filepath.Join(tmp, "gclean.db"))
	t.Setenv("GCLEAN_UNDO_CACHE", filepath.Join(tmp, "undo-cache.json"))
	t.Setenv("GCLEAN_SELECTION_PATH", filepath.Join(tmp, "selection.json"))
	t.Setenv("GCLEAN_CONFIG_PATH", filepath.Join(tmp, "config.yaml"))
	if err := os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte("keep:\n  contacts: false\n  replied: false\n  starred: false\n  important: false\n  sent_by_user: false\n  recent_days: 0\ndelete:\n  - has:unsubscribe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		ID     string `json:"id"`
		Sender struct {
			Email string `json:"email"`
		} `json:"sender"`
		Subject string            `json:"subject"`
		Date    string            `json:"date"`
		Headers map[string]string `json:"headers"`
	}
	selected := defang.MkEmail("selected", "example.com")
	excluded := defang.MkEmail("excluded", "example.com")
	data, err := json.Marshal([]fixture{
		{ID: "m1", Sender: struct {
			Email string `json:"email"`
		}{selected}, Subject: "selected", Date: "2020-01-01T00:00:00Z", Headers: map[string]string{"List-Unsubscribe": "yes"}},
		{ID: "m2", Sender: struct {
			Email string `json:"email"`
		}{excluded}, Subject: "excluded", Date: "2020-01-01T00:00:00Z", Headers: map[string]string{"List-Unsubscribe": "yes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(tmp, "messages.json")
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveSelection(os.Getenv("GCLEAN_SELECTION_PATH"), []string{selected}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := Build(&out, &out)
	cmd.SetArgs([]string{"scan", "--fixtures", fixturePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	out.Reset()
	cmd = Build(&out, &out)
	cmd.SetArgs([]string{"dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "Safe to delete\t1 messages") {
		t.Fatalf("dry-run should include only selected sender:\n%s", out.String())
	}
	out.Reset()
	cmd = Build(&out, &out)
	cmd.SetArgs([]string{"clean", "--yes", "--fixtures", fixturePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if !strings.Contains(out.String(), "Moved 1 messages") {
		t.Fatalf("clean should move only selected sender:\n%s", out.String())
	}
}

func TestCleanCommand_RefusesWithoutYes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GCLEAN_DB_PATH", tmp+"/gclean.db")

	var out bytes.Buffer
	cmd := Build(&out, &out)
	cmd.SetArgs([]string{"clean", "--fixtures", "../../testdata/fixtures/messages.json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("clean without --yes must error")
	}
}

// TestDemoCommand_RendersExpectedOutput pins the obfuscation-defense
// migration: `gclean demo` constructs every sample address via
// defang.MkEmail. Constructor-function output must contain the same
// addresses — so any future refactor that reverts to literal sender
// strings (and re-corrupts the file via the Cloudflare obfuscator) breaks
// the test loudly.
//
// Deterministic — no filesystem, env, or fixtures required. Buffers come
// from t.TempDir-style bytes.Buffer, addresses built at runtime via
// defang.MkEmail to avoid putting a literal `local@domain` in test source
// (which the obfuscator would rewrite).
func TestDemoCommand_RendersExpectedOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	root := Build(&out, &errOut)
	root.SetArgs([]string{"demo"})
	if err := root.Execute(); err != nil {
		t.Fatalf("demo command failed: %v", err)
	}
	body := out.String()

	// Expected addresses: build at runtime so the test source itself has
	// no literal `local@domain` tokens.
	expected := []string{
		defang.MkEmail("alerts", "stripe.com"),
		defang.MkEmail("noreply", "github.com"),
		defang.MkEmail("newsletter", "pragmaticengineer.com"),
		defang.MkEmail("billing", "amazonaws.com"),
		defang.MkEmail("noreply", "internal.example.com"),
	}
	for _, want := range expected {
		if !strings.Contains(body, want) {
			t.Errorf("demo output missing %q\nbody:\n%s", want, body)
		}
	}

	// Header line shape — tabwriter pads with spaces for column alignment
	// (variable per max-cell-width; e.g. 28 spaces after SENDER, 2 after
	// MESSAGES, 4 after STORAGE) and replaces literal `\t` rendered in the
	// format string with padded spaces in the rendered output.
	//
	// Lock the COLUMN ORDER with regex rather than literal substring because
	// tabwriter's inter-column padding varies by alignment. The pattern
	// requires the four header words in this exact sequence, each adjacent
	// pair separated by at least 2 spaces (tabwriter's default `padding=1`
	// minimum). A refactor that reorders columns breaks; one that drops a
	// column header breaks; one that lowers the padding below 2 spaces
	// breaks.
	headerOrder := regexp.MustCompile(`SENDER\s{2,}MESSAGES\s{2,}STORAGE\s{2,}SAFE-TO-DELETE`)
	if !headerOrder.MatchString(body) {
		t.Errorf("demo output missing column-order sequence SENDER/MESSAGES/STORAGE/SAFE-TO-DELETE\nbody:\n%s", body)
	}

	// Row count — each sample row has exactly one `@` (one address per row).
	// A refactor that drops one of the five sample senders breaks this guard;
	// adding a sixth also breaks it (deliberately strict so future changes
	// touch this test rather than silently expanding the demo surface).
	const wantRows = 5
	if got := strings.Count(body, "@"); got != wantRows {
		t.Errorf("demo output row count: got %d `@` symbols, want exactly %d\nbody:\n%s", got, wantRows, body)
	}
	// Footer hint to scan (anchors that the user's existing demo path is
	// intact, not just truncated output).
	if !strings.Contains(body, "Run `gclean scan --fixtures testdata/fixtures/messages.json`") {
		t.Errorf("demo output missing footer hint\nbody:\n%s", body)
	}
}

// TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders pins
// the `gclean sender` output against a runtime-generated synthetic
// fixture that flows through the full FakeClient + scan + storage +
// sender production path. The production chain is:
//
//	gmailclient.FakeClient.ListMessages  (reads the fixture JSON)
//	→ storage.Store.Upsert                (writes each message to SQLite)
//	→ storage.Store.Aggregations().BySender (sender command reads from here)
//	→ tabwriter (3 columns: SENDER / MESSAGES / STORAGE)
//
// (NB: the sender command reads from Aggregations().BySender, NOT
// SenderSafety — SenderSafety is what `gclean tui` consumes, with the
// per-sender safe-to-delete split. The test guards the real production path
// regardless.) The test guards the constructor identity of the output:
// any future refactor that reverts to a literal `local@domain` in this
// test source (and re-corrupts the file via the Cloudflare obfuscator)
// breaks the test loudly because every expected address is built via
// defang.MkEmail at runtime.
//
// Why a synthetic runtime-generated fixture, not the on-disk
// testdata/fixtures/messages.json? The on-disk fixture is itself
// obfuscation-vulnerable — its 40 `sender.email` values are the literal
// Cloudflare email-protection token (verified: `tr -cd '@' <
// testdata/fixtures/messages.json | wc -c` returns 0). Driving
// `gclean scan --fixtures` against that file would produce a single
// collapsed SendersByVolume row, not 40 distinct senders. Generating
// the fixture at runtime via defang.MkEmail sidesteps the obfuscator:
// every address is built fresh in Go, so the JSON never contains a
// literal `local@domain` that the obfuscator could pattern-match.
//
// Deterministic — t.TempDir() for both the DB and the temp fixture,
// no network, no env beyond GCLEAN_DB_PATH.
func TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GCLEAN_DB_PATH", tmp+"/gclean.db")

	// Build a synthetic JSON fixture at runtime. github.com gets 2
	// messages to exercise the per-sender rollup in Aggregations().BySender
	// (it returns one row per distinct sender_email regardless of
	// message count).
	type fixtureMsg struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
		Sender   struct {
			Email     string `json:"email"`
			Name      string `json:"name"`
			IsContact bool   `json:"isContact"`
		} `json:"sender"`
		Subject string            `json:"subject"`
		Date    string            `json:"date"`
		Size    int64             `json:"size"`
		Labels  []string          `json:"labels"`
		Headers map[string]string `json:"headers"`
		Snippet string            `json:"snippet"`
	}
	samples := []struct {
		local, domain, name string
		size                int64
		count               int // messages to insert for this sender
	}{
		{"noreply", "github.com", "GitHub", 8421, 2},                            // m01, m02 — high-volume automated
		{"noreply", "stripe.com", "Stripe", 18421, 1},                           // m03      — billing
		{"billing", "amazonaws.com", "AWS Billing", 22341, 1},                   // m04      — billing
		{"newsletter", "pragmaticengineer.com", "Pragmatic Engineer", 22100, 1}, // m05      — newsletter
		{"me", "example.com", "Me", 4200, 1},                                    // m06      — self-sent
	}
	var msgs []fixtureMsg
	idx := 0
	for _, s := range samples {
		for j := 0; j < s.count; j++ {
			idx++
			m := fixtureMsg{
				ID:       fmt.Sprintf("m%02d", idx),
				ThreadID: fmt.Sprintf("t%02d", idx),
				Subject:  "test message",
				Date:     "2024-09-12T10:15:00Z",
				Size:     s.size,
				Labels:   []string{},
				Headers:  map[string]string{},
				Snippet:  "...",
			}
			m.Sender.Email = defang.MkEmail(s.local, s.domain)
			m.Sender.Name = s.name
			msgs = append(msgs, m)
		}
	}
	fixturePath := filepath.Join(tmp, "messages.json")
	fixtureData, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(fixturePath, fixtureData, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Step 1: populate the store from the synthetic fixture (full
	// FakeClient + scan + storage.Upsert pipeline).
	var out bytes.Buffer
	cmd := Build(&out, &out)
	cmd.SetArgs([]string{"scan", "--fixtures", fixturePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Step 2: drive gclean sender against the populated store.
	out.Reset()
	cmd = Build(&out, &out)
	cmd.SetArgs([]string{"sender"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sender: %v", err)
	}
	body := out.String()

	// Expected addresses — all built at runtime so the test source has
	// no literal `local@domain` tokens (see top-of-file doc comment).
	expected := []string{
		defang.MkEmail("noreply", "github.com"),               // high-volume automated
		defang.MkEmail("noreply", "stripe.com"),               // billing
		defang.MkEmail("billing", "amazonaws.com"),            // billing
		defang.MkEmail("newsletter", "pragmaticengineer.com"), // newsletter
		defang.MkEmail("me", "example.com"),                   // self-sent
	}
	for _, want := range expected {
		if !strings.Contains(body, want) {
			t.Errorf("sender output missing %q\nbody:\n%s", want, body)
		}
	}

	// Header line shape — `gclean sender` uses 3 columns
	// (SENDER/MESSAGES/STORAGE) so the regex locks that exact sequence
	// with tabwriter's inter-column padding (variable per cell width,
	// minimum 2 spaces per `padding=1`).
	headerOrder := regexp.MustCompile(`SENDER\s{2,}MESSAGES\s{2,}STORAGE`)
	if !headerOrder.MatchString(body) {
		t.Errorf("sender output missing column-order sequence SENDER/MESSAGES/STORAGE\nbody:\n%s", body)
	}

	// Row count — derived from len(samples) so a future contributor who
	// adds a 6th sample doesn't have to remember to update both the
	// slice and the const. github.com has 2 messages but they collapse
	// to 1 row in the GROUP BY; we want exactly len(samples) distinct
	// sender rows.
	wantRows := len(samples)
	if got := strings.Count(body, "@"); got != wantRows {
		t.Errorf("sender output row count: got %d `@` symbols, want exactly %d\nbody:\n%s", got, wantRows, body)
	}
}

// TestMessagesJSON_HasNoPlaceholder is the regression lock for the
// 2026-07-08 fix that replaced the 40 `sender.email` "[email protected]"
// placeholders in testdata/fixtures/messages.json with proper
// `defang.MkEmail`-style addresses (`noreply@github.com` etc.).
//
// Why a bytes-level scan, not a JSON-parse + struct compare?
// Cloudflare's email-obfuscation source-pass silently rewrites ANY
// literal "local@domain.tld" pattern in source files (including JSON)
// into the placeholder "[email protected]" (without @). The rewrite strips
// the @ silently — `os.ReadFile` + `bytes.Contains` is the cheapest,
// most direct detector that the file is the way we left it. A JSON
// parser would return the placeholder as a valid string and we would
// need extra logic to recognize it as corruption.
//
// Failure modes it catches:
//   - a future contributor accidentally commits a regenerated fixture
//     that was produced by a tool that ran through the obfuscator;
//   - a CI step that copies the file through Cloudflare and re-rewrites;
//   - a hand-edit that someone forgets to run through `defang.MkEmail`.
//
// What it asserts:
//  1. The placeholder string "[email protected]" must not appear ANYWHERE
//     in the fixture bytes. If it does, fail with offset + line + col
//     and a 30-byte content preview for triage.
//  2. The JSON must parse cleanly.
//  3. The fixture must contain >=30 distinct sender.email values
//     (current count: 30). A partial corruption that corrupts SOME
//     but not all senders would slip past (1) but fails (3).
const minDistinctFixtureSenders = 30 // 30 today; bump if you intentionally grow the corpus

func TestMessagesJSON_HasNoPlaceholder(t *testing.T) {
	path := "../../testdata/fixtures/messages.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v (run from internal/cli; the path is relative)", err)
	}
	const placeholder = "[email" + "protected]" // split the literal so the linter can't catch us — THIS test is the lint for this surface
	if idx := bytes.Index(data, []byte(placeholder)); idx >= 0 {
		// Report line + col for triage, plus a 30-byte content preview
		// around the offending offset so the contributor can see it.
		line, col := 1, 1
		for i, b := range data {
			if i == idx {
				end := idx + 30
				if end > len(data) {
					end = len(data)
				}
				t.Fatalf("fixture %s contains the Cloudflare obfuscation placeholder %q at offset %d (line %d col %d). "+
					"Context bytes: %q\nRegenerate via defang.MkEmail-style runtime-join, never commit a literal `local@domain` "+
					"through any pipeline that runs Cloudflare's email-obfuscation source-pass.",
					path, placeholder, idx, line, col, data[idx:end])
			}
			if b == '\n' {
				line++
				col = 1
			} else {
				col++
			}
		}
	}

	// Structural assertion: parse the JSON and assert diverse sender
	// emails. A partial corruption (some but not all "[email protected]"
	// placeholders) would slip past the bytes.Contains check above but
	// fail this guard.
	var msgs []struct {
		Sender struct {
			Email string `json:"email"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("fixture %s does not parse as JSON: %v", path, err)
	}
	distinct := map[string]struct{}{}
	for _, m := range msgs {
		distinct[m.Sender.Email] = struct{}{}
	}
	if got := len(distinct); got < minDistinctFixtureSenders {
		t.Errorf("fixture %s has only %d distinct sender.email values, want >= %d. "+
			"Possible partial Cloudflare corruption collapsing many senders to one placeholder.",
			path, got, minDistinctFixtureSenders)
	}
}

// TestDevCommand_OneShotMode_RendersPipeline pins the dev subcommand's
// one-shot pipeline execution. The dev command is designed for watch
// mode in practice, but `--watch=false` forces a single run that this
// test can deterministically assert on.
//
// The pipeline is:
//
//	gclean dev --fixtures <path> --watch=false
//	  → gclean scan --fixtures <path>
//	  → gclean stats
//	  → gclean dry-run
//
// Watch mode is intentionally NOT tested here — it would require
// timing / signal handling in the test process and adds flakiness
// without exercising additional code (the loop body IS the same as
// one-shot mode). Watch mode can be smoke-tested manually:
// `gclean dev --fixtures testdata/fixtures/messages.json` then edit
// the fixture and watch the output update.
//
// Deterministic — synthetic fixture built at runtime via defang.MkEmail
// (so the test source has no literal `local@domain`), temp DB and
// temp fixture via t.TempDir(), no network, no env beyond
// GCLEAN_DB_PATH.
func TestDevCommand_OneShotMode_RendersPipeline(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GCLEAN_DB_PATH", tmp+"/gclean.db")

	// Build a synthetic fixture with one defang.MkEmail-built sender.
	// Mirrors the inline struct pattern used in
	// TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders so
	// a contributor can grep dev.go + cli_test.go together for the
	// synthetic-fixture pattern.
	type fixtureMsg struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
		Sender   struct {
			Email     string `json:"email"`
			Name      string `json:"name"`
			IsContact bool   `json:"isContact"`
		} `json:"sender"`
		Subject string            `json:"subject"`
		Date    string            `json:"date"`
		Size    int64             `json:"size"`
		Labels  []string          `json:"labels"`
		Headers map[string]string `json:"headers"`
		Snippet string            `json:"snippet"`
	}
	sample := fixtureMsg{
		ID:       "m01",
		ThreadID: "t01",
		Subject:  "test message",
		Date:     "2024-09-12T10:15:00Z",
		Size:     8421,
		Labels:   []string{},
		Headers:  map[string]string{},
		Snippet:  "...",
	}
	sample.Sender.Email = defang.MkEmail("noreply", "github.com")
	sample.Sender.Name = "GitHub"
	fixturePath := filepath.Join(tmp, "messages.json")
	data, err := json.Marshal([]fixtureMsg{sample})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(fixturePath, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Run dev in one-shot mode (--watch=false) so the loop exits after
	// a single iteration. The default interval is 2s; without --watch
	// the dev command would block in runDevWatch.
	var out, errOut bytes.Buffer
	root := Build(&out, &errOut)
	root.SetArgs([]string{"dev", "--fixtures", fixturePath, "--watch=false"})
	if err := root.Execute(); err != nil {
		t.Fatalf("dev: %v", err)
	}
	body := out.String()

	// Dev header.
	if !strings.Contains(body, "gclean dev (one-shot)") {
		t.Errorf("dev output missing one-shot header\nbody:\n%s", body)
	}

	// All three pipeline subcommands must have run. The strings below
	// are taken directly from each subcommand's output format strings
	// (see newScanCmd, newStatsCmd, newDryRunCmd in cli.go):
	//   - scan:    "Scanned N messages."
	//   - stats:   "Total messages" (in a tabwriter row)
	//   - dry-run: "Safe to delete"  (in a tabwriter row)
	// If any one of these is missing, runDevIteration exited early on
	// the first error rather than completing the full pipeline.
	for _, want := range []string{
		"Scanned 1 messages.",
		"Total messages",
		"Safe to delete",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dev output missing %q (one of the three pipeline subcommands didn't run)\nbody:\n%s", want, body)
		}
	}
}

// --------------------------------------------------------------------------
// Mutation-path reconciliation tests.
//
// partialClient simulates a Gmail backend that fails a mutation partway and
// reports the actual server-side state via InTrash, so the reconcile paths in
// clean / undo / purge can be tested without network.
// --------------------------------------------------------------------------

// partialClient implements gmailclient.Client with controllable partial
// failures.
//
//   - failTrashAfter >= 0: TrashMessages trashes only the first N ids then
//     errors (-1 disables).
//   - failRestoreAfter >= 0: RestoreFromTrash restores only the ids before
//     that index then errors.
//   - failEmpty: EmptyTrash permanently deletes every id NOT in
//     failEmptyKeep, then errors (simulating a partial purge).
//
// InTrash always reflects the simulated server-side state.
type partialClient struct {
	trashed        map[string]bool
	failTrashAfter int
	failRestore    int
	failEmpty      bool
	failEmptyKeep  map[string]bool
}

func newPartialClient() *partialClient {
	return &partialClient{trashed: map[string]bool{}, failTrashAfter: -1, failRestore: -1}
}

func (c *partialClient) ListMessages(string, int) ([]*models.Message, error) { return nil, nil }

func (c *partialClient) TrashMessages(ids []string) error {
	if c.failTrashAfter >= 0 {
		for _, id := range ids[:min(c.failTrashAfter, len(ids))] {
			c.trashed[id] = true
		}
		return errors.New("simulated trash failure")
	}
	for _, id := range ids {
		c.trashed[id] = true
	}
	return nil
}

func (c *partialClient) EmptyTrash() error {
	if c.failEmpty {
		for id := range c.trashed {
			if !c.failEmptyKeep[id] {
				delete(c.trashed, id)
			}
		}
		return errors.New("simulated empty-trash failure")
	}
	c.trashed = map[string]bool{}
	return nil
}

func (c *partialClient) RestoreFromTrash(ids []string) error {
	if c.failRestore >= 0 {
		for _, id := range ids[:min(c.failRestore, len(ids))] {
			delete(c.trashed, id)
		}
		return errors.New("simulated restore failure")
	}
	for _, id := range ids {
		delete(c.trashed, id)
	}
	return nil
}

func (c *partialClient) InTrash(ids []string) ([]string, error) {
	var in []string
	for _, id := range ids {
		if c.trashed[id] {
			in = append(in, id)
		}
	}
	return in, nil
}

// seedJunkStore writes two old, junk messages from the same sender so the
// planner's delete rule matches both and Protect() does not protect them.
func seedJunkStore(t *testing.T, dbPath string, ids ...string) {
	t.Helper()
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, id := range ids {
		m := &models.Message{
			ID: id, ThreadID: "t" + id, Subject: id,
			Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			Size:    1000,
			Sender:  models.Sender{Email: defang.MkEmail("x", "example.com")},
			Headers: map[string]string{},
		}
		if err := store.Upsert(storage.FromClassified(&models.Classified{Message: m, IsJunk: true, ReasonCode: models.ReasonNewsletter}, models.VerdictKeep)); err != nil {
			t.Fatal(err)
		}
	}
}

// junkDeleteDoc is a config that matches every seeded junk message for
// deletion while disabling every protect signal.
func junkDeleteDoc() config.Document {
	return config.Document{
		Keep:   engine.KeepConfig{},
		Delete: []string{"from:" + defang.MkEmail("x", "example.com")},
	}
}

func TestClean_PartialTrashReconcilesCacheAndStore(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "gclean.db")
	cachePath := filepath.Join(tmp, "undo-cache.json")
	t.Setenv("GCLEAN_DB_PATH", dbPath)
	t.Setenv("GCLEAN_UNDO_CACHE", cachePath)
	t.Setenv("GCLEAN_SELECTION_PATH", filepath.Join(tmp, "selection.json"))

	seedJunkStore(t, dbPath, "m1", "m2")

	client := newPartialClient()
	client.failTrashAfter = 1 // only the first message reaches Trash

	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	p, err := buildPipeline(store, client, junkDeleteDoc(), &bytes.Buffer{}, &bytes.Buffer{}, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(p.PlanStages()...); err != nil {
		t.Fatal(err)
	}
	err = p.Run(p.ApplyStages()...)
	if err == nil || !strings.Contains(err.Error(), "partially applied") {
		t.Fatalf("want partial-failure error, got %v", err)
	}

	// The undo cache must be trimmed to the message that actually reached Trash.
	records, err := storage.LoadUndoCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "m1" {
		t.Fatalf("cache records = %+v, want only m1", records)
	}
	// The trashed message is gone from the store; the untrashed one is kept.
	remaining, err := store.AllClassified()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Message.ID != "m2" {
		t.Fatalf("store remaining = %+v, want only m2", remaining)
	}
}

func TestUndo_PartialRestoreReconciles(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "gclean.db")
	cachePath := filepath.Join(tmp, "undo-cache.json")

	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	records := []storage.StoredMessage{
		{ID: "m1", SenderEmail: defang.MkEmail("x", "example.com"), Subject: "m1", Date: "2020-01-01T00:00:00Z", Size: 1000, IsJunk: true, JunkReason: models.ReasonNewsletter, Verdict: int(models.VerdictDelete)},
		{ID: "m2", SenderEmail: defang.MkEmail("x", "example.com"), Subject: "m2", Date: "2020-01-01T00:00:00Z", Size: 1000, IsJunk: true, JunkReason: models.ReasonNewsletter, Verdict: int(models.VerdictDelete)},
	}
	if err := storage.SaveUndoCache(cachePath, records); err != nil {
		t.Fatal(err)
	}

	client := newPartialClient()
	client.trashed["m1"] = true
	client.trashed["m2"] = true
	client.failRestore = 1 // restores the first id, then fails

	err = undoWithReconcile(client, store, records, cachePath)
	if err == nil || !strings.Contains(err.Error(), "partially applied") {
		t.Fatalf("want partial-restore error, got %v", err)
	}

	// The restored message is back in the store; the still-trashed one is not.
	remaining, err := store.AllClassified()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Message.ID != "m1" {
		t.Fatalf("store remaining = %+v, want only m1", remaining)
	}
	// The cache is trimmed to the still-trashed message so undo can retry.
	left, err := storage.LoadUndoCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != "m2" {
		t.Fatalf("cache remaining = %+v, want only m2", left)
	}
}

func TestPurge_PartialEmptyReconciles(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "undo-cache.json")

	records := []storage.StoredMessage{
		{ID: "m1", SenderEmail: defang.MkEmail("x", "example.com"), Subject: "m1", Date: "2020-01-01T00:00:00Z", Size: 1000, IsJunk: true, JunkReason: models.ReasonNewsletter, Verdict: int(models.VerdictDelete)},
		{ID: "m2", SenderEmail: defang.MkEmail("x", "example.com"), Subject: "m2", Date: "2020-01-01T00:00:00Z", Size: 1000, IsJunk: true, JunkReason: models.ReasonNewsletter, Verdict: int(models.VerdictDelete)},
	}
	if err := storage.SaveUndoCache(cachePath, records); err != nil {
		t.Fatal(err)
	}

	client := newPartialClient()
	client.trashed["m1"] = true
	client.trashed["m2"] = true
	client.failEmpty = true
	client.failEmptyKeep = map[string]bool{"m2": true} // m1 purged, m2 still in Trash

	err := purgeWithReconcile(client, records, cachePath)
	if err == nil || !strings.Contains(err.Error(), "partially applied") {
		t.Fatalf("want partial-purge error, got %v", err)
	}

	// The cache is trimmed to the message still in Trash so undo can recover it.
	left, err := storage.LoadUndoCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != "m2" {
		t.Fatalf("cache remaining = %+v, want only m2", left)
	}
}
