// Package format holds small rendering helpers shared by the CLI and the
// TUI. They were previously duplicated verbatim in internal/cli and
// internal/tui; keeping one copy means a formatting fix (units, width
// handling, i18n) lands in exactly one place.
package format

import "fmt"

// HumanBytes formats an int64 byte count for human display.
// 1024-base, KMGTPE units. Used by stats, dry-run, attachments, sender,
// demo, and the TUI confirmation summary — almost every output that
// prints a size.
func HumanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(k), 0
	for n2 := n / k; n2 >= k; n2 /= k {
		div *= k
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), units[exp])
}

// Truncate shortens a string and adds an ellipsis on overflow. Used for
// rendering subjects and sender addresses in fixed-width tabwriter columns
// and the TUI list.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
