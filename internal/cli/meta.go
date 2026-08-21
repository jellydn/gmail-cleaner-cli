package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gclean/internal/config"
	"gclean/internal/engine"
	"gclean/internal/format"
	"gclean/internal/storage"
	"gclean/internal/tui"
)

// meta.go — gclean rules / config / tui. These commands are about
// inspecting or modifying the configuration and the experimental TUI;
// none of them touch Gmail's API or move mail. The TUI selection is
// persisted to ~/.config/gclean/tui-selection.json via the saveSelection
// helper in insights.go (the helper straddles both files by design).

// --- Rules / Config -----------------------------------------------------

func newRulesCmd(out, errOut io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Show the parsed rule set (from config.yaml)",
		RunE: func(cmd *cobra.Command, args []string) error {
			doc, err := config.Load()
			if err != nil {
				return err
			}
			rc, err := doc.Compile()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "Keep:")
			printKeep(out, doc.Keep)
			_, _ = fmt.Fprintln(out, "\nDelete:")
			printRules(out, "delete", rc.Delete)
			_, _ = fmt.Fprintln(out, "\nArchive:")
			printRules(out, "archive", rc.Archive)
			if len(rc.Ignore) > 0 {
				_, _ = fmt.Fprintln(out, "\nIgnore domains:")
				for _, d := range rc.Ignore {
					_, _ = fmt.Fprintf(out, "  %s\n", d)
				}
			}
			return nil
		},
	}
	return cmd
}

func printKeep(out io.Writer, k engine.KeepConfig) {
	_, _ = fmt.Fprintf(out, "  contacts\t%v\n", k.Contacts)
	_, _ = fmt.Fprintf(out, "  replied\t%v\n", k.Replied)
	_, _ = fmt.Fprintf(out, "  starred\t%v\n", k.Starred)
	_, _ = fmt.Fprintf(out, "  important\t%v\n", k.Important)
	_, _ = fmt.Fprintf(out, "  sent_by_user\t%v\n", k.SentByUser)
	_, _ = fmt.Fprintf(out, "  recent_days\t%d\n", k.RecentDays)
}

func printRules(out io.Writer, action string, rs []engine.Rule) {
	if len(rs) == 0 {
		_, _ = fmt.Fprintf(out, "  (none)\n")
		return
	}
	for _, r := range rs {
		if len(r.Predicates) == 0 {
			_, _ = fmt.Fprintf(out, "  - %s: (empty)\n", action)
			continue
		}
		parts := make([]string, 0, len(r.Predicates))
		for _, p := range r.Predicates {
			parts = append(parts, p.Key+":"+p.Value)
		}
		_, _ = fmt.Fprintf(out, "  - %s: %s\n", action, strings.Join(parts, " "))
	}
}

func newConfigCmd(out, errOut io.Writer) *cobra.Command {
	var op string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or show the config file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.DefaultPath()
			if err != nil {
				return err
			}
			switch op {
			case "path", "":
				_, _ = fmt.Fprintln(out, p)
			case "show":
				_, _ = fmt.Fprintf(out, "Config: %s\n", p)
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, string(data))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&op, "op", "path", "One of: path | show")
	return cmd
}

// --- Bubble Tea TUI -----------------------------------------------------

func newTuiCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "EXPERIMENTAL interactive Bubble Tea UI: checkbox list of senders",
		Long: "Experimental: opens an interactive checkbox list of senders with their safe-to-delete counts.\n" +
			"Reads from local SQLite. Run `gclean scan` and `gclean dry-run` first.\n" +
			"Keys: \u2191/k \u2193/j move \u00b7 space toggle \u00b7 a select all junk \u00b7 n clear \u00b7 enter commit \u00b7 q quit.\n" +
			"On commit, writes ~/.config/gclean/tui-selection.json.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(errOut, "note: `gclean tui` is experimental; keymaps and visuals may shift.")
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			agg, err := store.Aggregations()
			if err != nil {
				return err
			}
			safeties := agg.SendersSafe
			if len(safeties) == 0 {
				_, _ = fmt.Fprintln(errOut, "store is empty \u2014 run `gclean scan --fixtures testdata/fixtures/messages.json` first.")
				return errors.New("empty store")
			}
			final, err := tui.Run(tui.NewModel(safeties))
			if err != nil {
				return err
			}
			if final.Quitted() {
				_, _ = fmt.Fprintln(out, "gclean tui: cancelled.")
				return nil
			}
			sel := final.Selection()
			if len(sel) == 0 {
				_, _ = fmt.Fprintln(out, "gclean tui: no senders selected \u2014 nothing saved.")
				return nil
			}
			if err := saveSelection(sel); err != nil {
				return fmt.Errorf("save selection: %w", err)
			}
			senders, msgs, bytes := final.SelectionStats()
			_, _ = fmt.Fprintf(out, "\nSelection confirmed: %d senders \u00b7 %d messages \u00b7 %s recoverable.\nSaved to ~/.config/gclean/tui-selection.json.\n",
				senders, msgs, format.HumanBytes(bytes))
			_, _ = fmt.Fprintln(out, "Run `gclean dry-run` to review the selected cohort, then `gclean clean --yes` to apply it.")
			return nil
		},
	}
}
