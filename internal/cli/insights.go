package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"gclean/internal/format"
	"gclean/internal/models"
	"gclean/internal/storage"
)

// insights.go — gclean sender / attachments / newsletters / receipts +
// the TUI-selection save. Pure-read operations on the local SQLite store;
// none of these mutate state in Gmail or on disk beyond the TUI's selection
// file (the only "save" is saveSelection, which writes
// ~/.config/gclean/tui-selection.json when the user commits a TUI choice).

// saveSelection writes the TUI's commit-time selection through the storage
// boundary so the same format is loaded by the planning pipeline.
func saveSelection(emails []string) error {
	return storage.SaveSelection(selectionPath(), emails)
}

// sliceControl drives newsletters/receipts: print one row per classified
// message whose ReasonCode matches any of `reasons`.
func sliceControl(out io.Writer, reasons []string) error {
	store, err := storage.Open(storePath())
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	rows, err := store.AllClassified()
	if err != nil {
		return err
	}
	for _, c := range rows {
		for _, r := range reasons {
			if c.ReasonCode == r {
				_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", c.Message.ID, c.Message.Sender.Email, format.Truncate(c.Message.Subject, 60), format.HumanBytes(c.Message.Size))
				break
			}
		}
	}
	return nil
}

// --- Subcommand constructors -------------------------------------------

func newSenderCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "sender [address]",
		Short: "Per-sender insights: count, storage, safe-to-delete split",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			agg, err := store.Aggregations()
			if err != nil {
				return err
			}
			senders := agg.BySender
			if len(senders) > 50 {
				senders = senders[:50]
			}
			filter := ""
			if len(args) > 0 {
				filter = args[0]
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "SENDER\tMESSAGES\tSTORAGE")
			for _, s := range senders {
				if filter != "" && !strings.Contains(strings.ToLower(s.Email), strings.ToLower(filter)) {
					continue
				}
				_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n", s.Email, s.Count, format.HumanBytes(s.Bytes))
			}
			_ = tw.Flush()
			return nil
		},
	}
}

func newAttachmentsCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "attachments",
		Short: "List the largest messages (likely attachment-heavy)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			minBytes := int64(1) << 20 // 1MB threshold
			rows, err := store.LargestAttachments(minBytes, 50)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tSENDER\tSUBJECT\tSIZE\tDATE")
			for _, r := range rows {
				dateStr := r.Date
				if len(dateStr) > 10 {
					dateStr = dateStr[:10]
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.SenderEmail, format.Truncate(r.Subject, 60), format.HumanBytes(r.Size), dateStr)
			}
			_ = tw.Flush()
			return nil
		},
	}
}

func newNewslettersCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "newsletters",
		Short: "List all classified newsletter/mailing-list messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sliceControl(out, []string{models.ReasonNewsletter, models.ReasonMailingList})
		},
	}
}

func newReceiptsCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "receipts",
		Short: "List all messages auto-classified as receipts/invoices",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sliceControl(out, []string{models.ReasonStripe, models.ReasonAWSBilling})
		},
	}
}
