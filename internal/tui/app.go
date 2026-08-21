// Package tui renders the PRD §12 interactive Bubble Tea UI. Marked
// experimental: visual style and keymaps may shift.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"gclean/internal/format"
	"gclean/internal/models"
)

// SenderRow flattens one models.SenderSafety into the render path.
type SenderRow struct {
	Email       string
	TotalCount  int64
	TotalBytes  int64
	DeleteCount int64
	DeleteBytes int64
	KeepCount   int64
	Reasons     []string
}

// Model is the bubbletea Model. State lives on this struct so Update can
// be tested without a tea runtime.
type Model struct {
	rows      []SenderRow
	cursor    int
	selected  map[string]bool // sender email → user-toggled
	width     int
	height    int
	quit      bool
	committed bool
}

// NewModel ingests sender-safety rows and pre-selects every sender with at
// least one delete candidate (matches the §12 "Safe to delete" starter set).
func NewModel(safeties []models.SenderSafety) Model {
	rows := make([]SenderRow, len(safeties))
	sel := map[string]bool{}
	for i, s := range safeties {
		rows[i] = SenderRow{
			Email:       s.Email,
			TotalCount:  s.TotalCount,
			TotalBytes:  s.TotalBytes,
			DeleteCount: s.DeleteCount,
			DeleteBytes: s.DeleteBytes,
			KeepCount:   s.KeepCount,
			Reasons:     s.Reasons,
		}
		// Pre-select: ONLY senders with at least one delete candidate.
		if s.DeleteCount > 0 {
			sel[s.Email] = true
		}
	}
	return Model{
		rows:     rows,
		selected: sel,
		width:    80,
		height:   24,
	}
}

// Committed reports whether the user pressed Enter.
func (m Model) Committed() bool { return m.committed }

// Quitted reports whether the user pressed q/ctrl+c (cancelled).
func (m Model) Quitted() bool { return m.quit && !m.committed }

// Selection returns the emails the user toggled on, in row order.
func (m Model) Selection() []string {
	out := make([]string, 0, len(m.selected))
	for _, r := range m.rows {
		if m.selected[r.Email] {
			out = append(out, r.Email)
		}
	}
	return out
}

// SelectionStats returns senders-selected, messages-eligible-for-deletion,
// and bytes-recoverable from the current selection.
func (m Model) SelectionStats() (senders int, msgs int64, bytes int64) {
	for _, r := range m.rows {
		if m.selected[r.Email] {
			senders++
			msgs += r.DeleteCount
			bytes += r.DeleteBytes
		}
	}
	return senders, msgs, bytes
}

// Init implements tea.Model. No startup commands for now.
func (m Model) Init() tea.Cmd { return nil }

// Update handles window resize, key events, and quits.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch mm := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = mm.Width
		m.height = mm.Height
		return m, nil
	case tea.KeyPressMsg:
		// Empty list: only quit keys are meaningful.
		if len(m.rows) == 0 {
			switch mm.String() {
			case "ctrl+c", "q":
				m.quit = true
				return m, tea.Quit
			}
			return m, nil
		}
		switch mm.String() {
		case "ctrl+c", "q":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
			return m, nil
		case "space":
			em := m.rows[m.cursor].Email
			m.selected[em] = !m.selected[em]
			return m, nil
		case "a":
			for _, r := range m.rows {
				if r.DeleteCount > 0 {
					m.selected[r.Email] = true
				}
			}
			return m, nil
		case "n":
			m.selected = map[string]bool{}
			return m, nil
		case "enter":
			m.committed = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View renders either the interactive list, the cancelled message, or the
// commit summary.
func (m Model) View() tea.View {
	var content string
	switch {
	case m.quit && !m.committed:
		content = "gclean tui: cancelled.\n"
	case m.committed:
		content = m.commitSummaryView()
	default:
		content = m.mainView()
	}
	return tea.NewView(content)
}

func (m Model) mainView() string {
	var sb strings.Builder

	title := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#7D56F4")).
		Padding(0, 1).
		Render("gclean tui — safe to delete")
	sb.WriteString(title)
	sb.WriteString("\n")

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	sb.WriteString(headerStyle.Render(fmt.Sprintf(
		"── %d senders · %s recoverable · EXPERIMENTAL ──",
		len(m.rows),
		format.HumanBytes(totalDeleteBytes(m.rows)),
	)))
	sb.WriteString("\n\n")

	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	for i, r := range m.rows {
		sel := "[ ]"
		if m.selected[r.Email] {
			sel = "[✓]"
		}
		cursor := "  "
		if i == m.cursor {
			cursor = "▶ "
		}
		line := fmt.Sprintf("%s%s %-40s  %5d msgs  %s",
			cursor, sel,
			format.Truncate(r.Email, 40),
			r.DeleteCount,
			format.HumanBytes(r.DeleteBytes),
		)
		if i == m.cursor {
			line = cursorStyle.Render(line)
		} else if !m.selected[r.Email] {
			line = dimStyle.Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Italic(true)
	sb.WriteString(footerStyle.Render("↑/k ↓/j move · space toggle · a select all junk · n clear · enter commit · q quit"))
	sb.WriteString("\n")
	return sb.String()
}

func (m Model) commitSummaryView() string {
	senders, msgs, bytes := m.SelectionStats()
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#04B575")).Render("selection confirmed.")
	return fmt.Sprintf("\n%s\n%d senders · %d messages · %s recoverable.\n",
		title, senders, msgs, format.HumanBytes(bytes))
}

func totalDeleteBytes(rows []SenderRow) int64 {
	var b int64
	for _, r := range rows {
		b += r.DeleteBytes
	}
	return b
}

// Run wraps tea.NewProgram. Exposed so the CLI doesn't need to import
// bubbletea directly.
func Run(m Model) (Model, error) {
	p := tea.NewProgram(m)
	fm, err := p.Run()
	if err != nil {
		return m, err
	}
	if nm, ok := fm.(Model); ok {
		return nm, nil
	}
	return m, nil
}
