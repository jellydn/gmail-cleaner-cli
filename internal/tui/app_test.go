package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"gclean/internal/models"
)

// mkT joins a local part and domain at runtime with "@" so the literal in
// source cannot be rewritten by Cloudflare's email obfuscation. (Same root
// cause / same defense as the helper in internal/engine/*_test.go; if/when
// the project's MkEmail now lives in the internal/defang package
// can be deleted in favour of an import.)
func mkT(local, domain string) string { return local + "@" + domain }

// mustRun promotes the bubbletea interface return of Update into our concrete
// Model so field accessors (selected, cursor) resolve correctly in tests.
// Returning tea.Model at this level hides the concrete fields.
func mustRun(t *testing.T, fn func() (tea.Model, tea.Cmd)) Model {
	t.Helper()
	out, _ := fn()
	if out == nil {
		t.Fatal("Update returned nil model")
	}
	m, ok := out.(Model)
	if !ok {
		t.Fatalf("Update returned %T, expected Model", out)
	}
	return m
}

func TestNewModel_PreSelectsSendersWithDeleteCount(t *testing.T) {
	safeties := []models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 5, DeleteBytes: 1000},
		{Email: mkT("b", "y.com"), DeleteCount: 0, DeleteBytes: 0},
		{Email: mkT("c", "z.com"), DeleteCount: 1, DeleteBytes: 100},
	}
	m := NewModel(safeties)
	if !m.selected[mkT("a", "x.com")] {
		t.Error("a had DeleteCount>0; should be pre-selected")
	}
	if m.selected[mkT("b", "y.com")] {
		t.Error("b had DeleteCount=0; should NOT be pre-selected")
	}
	if !m.selected[mkT("c", "z.com")] {
		t.Error("c had DeleteCount>0; should be pre-selected")
	}
	if len(m.rows) != 3 {
		t.Errorf("rows=%d, want 3", len(m.rows))
	}
}

func TestUpdate_SpaceTogglesSender(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 1},
		{Email: mkT("b", "x.com"), DeleteCount: 1},
	})
	if !m.selected[mkT("a", "x.com")] {
		t.Fatal("pre-selected expected for a@x.com")
	}
	next := mustRun(t, func() (tea.Model, tea.Cmd) { return m.Update(tea.KeyPressMsg{Code: tea.KeySpace}) })
	if next.selected[mkT("a", "x.com")] {
		t.Error("space should toggle off a@x.com")
	}
}

func TestUpdate_J_K_MoveCursor(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 1},
		{Email: mkT("b", "x.com"), DeleteCount: 1},
		{Email: mkT("c", "x.com"), DeleteCount: 1},
	})
	if m.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.cursor)
	}

	next := mustRun(t, func() (tea.Model, tea.Cmd) { return m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) })
	if next.cursor != 1 {
		t.Errorf("down: cursor=%d want 1", next.cursor)
	}
	next = mustRun(t, func() (tea.Model, tea.Cmd) { return next.Update(tea.KeyPressMsg{Code: tea.KeyDown}) })
	if next.cursor != 2 {
		t.Errorf("down: cursor=%d want 2", next.cursor)
	}
	// Clamp at bottom.
	next = mustRun(t, func() (tea.Model, tea.Cmd) { return next.Update(tea.KeyPressMsg{Code: tea.KeyDown}) })
	if next.cursor != 2 {
		t.Errorf("down at bottom: cursor=%d want 2 (clamped)", next.cursor)
	}
	// Up moves back.
	next = mustRun(t, func() (tea.Model, tea.Cmd) { return next.Update(tea.KeyPressMsg{Code: tea.KeyUp}) })
	if next.cursor != 1 {
		t.Errorf("up: cursor=%d want 1", next.cursor)
	}
	// Clamp at top.
	next = mustRun(t, func() (tea.Model, tea.Cmd) { return next.Update(tea.KeyPressMsg{Code: tea.KeyUp}) })
	next = mustRun(t, func() (tea.Model, tea.Cmd) { return next.Update(tea.KeyPressMsg{Code: tea.KeyUp}) })
	if next.cursor != 0 {
		t.Errorf("up at top: cursor=%d want 0 (clamped)", next.cursor)
	}
}

func TestUpdate_Q_QuitsWithoutCommit(t *testing.T) {
	m := NewModel([]models.SenderSafety{{Email: mkT("a", "x.com"), DeleteCount: 1}})
	next := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.KeyPressMsg{Code: 'q'})
	})
	if !next.Quitted() {
		t.Error("q should set Quitted()=true")
	}
	if next.Committed() {
		t.Error("q should NOT commit")
	}
}

func TestUpdate_EnterCommitsAndIncludesSelection(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 3, DeleteBytes: 1500},
		{Email: mkT("b", "x.com"), DeleteCount: 0, DeleteBytes: 0},
		{Email: mkT("c", "x.com"), DeleteCount: 7, DeleteBytes: 4200},
	})
	next := mustRun(t, func() (tea.Model, tea.Cmd) { return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) })
	if !next.Committed() {
		t.Fatal("enter should set Committed()=true")
	}
	senders, msgs, bytes := next.SelectionStats()
	if senders != 2 {
		t.Errorf("selected senders=%d want 2", senders)
	}
	if msgs != 10 {
		t.Errorf("selected msgs=%d want 10", msgs)
	}
	if bytes != 5700 {
		t.Errorf("selected bytes=%d want 5700", bytes)
	}
	sel := next.Selection()
	if len(sel) != 2 || sel[0] != mkT("a", "x.com") || sel[1] != mkT("c", "x.com") {
		t.Errorf("Selection()=%v", sel)
	}
}

func TestUpdate_CtrlCEqualsQuit(t *testing.T) {
	m := NewModel([]models.SenderSafety{{Email: mkT("a", "x.com"), DeleteCount: 1}})
	next := mustRun(t, func() (tea.Model, tea.Cmd) { return m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) })
	if !next.Quitted() {
		t.Error("ctrl+c should quit")
	}
}

func TestUpdate_A_SelectsAllJunk(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 5, DeleteBytes: 1000},
		{Email: mkT("b", "y.com"), DeleteCount: 0, DeleteBytes: 0},
		{Email: mkT("c", "z.com"), DeleteCount: 2, DeleteBytes: 100},
	})
	clr := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.KeyPressMsg{Code: 'n'})
	})
	if len(clr.selected) != 0 {
		t.Fatalf("n should clear; got %v", clr.selected)
	}
	all := mustRun(t, func() (tea.Model, tea.Cmd) {
		return clr.Update(tea.KeyPressMsg{Code: 'a'})
	})
	wantSelected := []string{mkT("a", "x.com"), mkT("c", "z.com")}
	for _, em := range wantSelected {
		if !all.selected[em] {
			t.Errorf("a should have selected %q; map: %v", em, all.selected)
		}
	}
	if all.selected[mkT("b", "y.com")] {
		t.Error("a should NOT select senders with no DeleteCount (b@y.com has DeleteCount=0)")
	}
}

func TestUpdate_N_ClearsSelection(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 1},
		{Email: mkT("b", "x.com"), DeleteCount: 1},
	})
	if len(m.selected) != 2 {
		t.Fatal("expected pre-selected")
	}
	clr := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.KeyPressMsg{Code: 'n'})
	})
	if len(clr.selected) != 0 {
		t.Errorf("n should clear: %v", clr.selected)
	}
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := NewModel(nil)
	next := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	})
	if next.width != 120 || next.height != 40 {
		t.Errorf("WindowSizeMsg: got %dx%d, want 120x40", next.width, next.height)
	}
}

func TestUpdate_EmptyRowsGuards(t *testing.T) {
	m := NewModel(nil)
	next := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.KeyPressMsg{Code: 'a'})
	})
	if next.committed || next.quit {
		t.Errorf("empty list: non-quit keys should be inert, got %+v", next)
	}
	q := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.KeyPressMsg{Code: 'q'})
	})
	if !q.Quitted() {
		t.Error("empty list: q should still quit")
	}
}

// stripANSI removes lipgloss/ANSI terminal escape sequences so that the test
// can assert on the *visible* text rather than the rendered-with-color form.
// Without this, lipgloss-injected escape codes (e.g. around the title and
// per-row cursor highlight) make the rendered string contain "a\x1b[36mx",
// "[\x1b[32m✓\x1b[0m]", etc, and substring checks against plain "a@x" or
// "[ ]" silently miss.
func stripANSI(s string) string {
	// ESC = 0x1B; CSI sequences end with a letter in @-~ (here typically m).
	var b strings.Builder
	skip := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if skip > 0 {
			skip--
			continue
		}
		if c == 0x1B {
			// Look ahead for CSI: ESC [ ... <letter>
			if i+1 < len(s) && s[i+1] == '[' {
				skip = 2 // consume ESC and [
				for j := i + 2; j < len(s); j++ {
					bj := s[j]
					if (bj >= '@' && bj <= '~') || bj == '\n' {
						skip = j - i // mark GC of the entire CSI run after ESC[
						break
					}
				}
				continue
			}
			// Non-CSI escape (rare in lipgloss) — drop ESC alone.
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func TestView_MainViewContainsHeaderAndHelpers(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 7, DeleteBytes: 1024 * 1024 * 5},
		{Email: mkT("b", "y.com"), DeleteCount: 0},
	})
	v := stripANSI(m.View().Content)
	for _, want := range []string{"gclean tui", mkT("a", "x.com"), mkT("b", "y.com"), "[✓]", "[ ]", "EXPERIMENTAL", "space toggle"} {
		if !strings.Contains(v, want) {
			t.Errorf("View missing %q\nbody:\n%s", want, v)
		}
	}
}

func TestView_CancelledMessage(t *testing.T) {
	m := NewModel([]models.SenderSafety{{Email: mkT("a", "x.com"), DeleteCount: 1}})
	q := mustRun(t, func() (tea.Model, tea.Cmd) {
		return m.Update(tea.KeyPressMsg{Code: 'q'})
	})
	if !strings.Contains(q.View().Content, "cancelled") {
		t.Errorf("q View should include 'cancelled': %s", q.View().Content)
	}
}

func TestView_CommittedSummary(t *testing.T) {
	m := NewModel([]models.SenderSafety{
		{Email: mkT("a", "x.com"), DeleteCount: 9, DeleteBytes: 2048},
	})
	c := mustRun(t, func() (tea.Model, tea.Cmd) { return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) })
	v := c.View().Content
	if !strings.Contains(v, "selection confirmed") {
		t.Errorf("committed View should include 'selection confirmed': %s", v)
	}
	if !strings.Contains(v, "9") {
		t.Errorf("committed View should include message count: %s", v)
	}
}
