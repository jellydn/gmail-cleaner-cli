package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"gclean/internal/defang"
	"gclean/internal/format"
	"gclean/internal/models"

	"github.com/spf13/cobra"
)

// newDemoCmd prints a self-contained preview of what `gclean tui` will
// surface once a scan has populated the local store. The sample rows are
// models.SenderSafety values, the EXACT type `gclean tui` and any future
// fixture-loader consume — so a contributor who reads demo.go can see a
// one-to-one preview of the production shape.
//
// NOTE: This preview mirrors the TUI's per-sender shape, NOT `gclean sender`
// (which uses `store.Aggregations().BySender` returning a simpler Email/Count/Bytes
// struct). If a future contributor wants a `gclean sender` preview they
// should add a separate subcommand or extend this one with view-mode flags.
//
// Two defenses are load-bearing here:
//
//  1. Every address is constructed at runtime via defang.MkEmail, so no
//     literal `local@domain` pattern exists in source. This defeats the
//     Cloudflare source-pass obfuscation that has previously rewritten test
//     literals into `[email protected]` placeholders.
//
//  2. The data type is the real production struct, so future readers can
//     pattern-match demo.go against models.SenderSafety line-for-line.
func newDemoCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "demo",
		Short: "Print a self-contained preview of `gclean tui` output (no scan required)",
		Long: "Constructs a small set of sample models.SenderSafety rows at runtime\n" +
			"via defang.MkEmail and renders them in the same shape as `gclean tui`.\n" +
			"Useful for:\n" +
			"  - new contributors checking the CLI on a fresh box\n" +
			"  - sanity-checking that a coworker's terminal renders the columns\n" +
			"  - demonstrating the obfuscation-defense + production-shape pattern\n" +
			"\nNo SQLite access, no Gmail auth, no fixtures file required.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Every field populated so the demo row is structurally identical
			// to what storage.Aggregations().SendersSafe returns from SQLite. Reasons come
			// from models.ReasonXxx so a contributor can grep demo.go against
			// models.SenderSafety to see the contract.
			//
			// DeleteBytes is sized proportionally to DeleteCount/TotalCount so
			// total/delete/kept bytes add up consistently (TotalBytes =
			// DeleteBytes + bytes-kept).
			//
			// KeepCount = TotalCount - DeleteCount is a per-row simplification
			// of the real SQL aggregation (which sums VerdictKeep +
			// VerdictProtected). The demo rows have no protected cohort, so
			// the two formulas coincide here — but a future contributor using
			// this template should note the approximation.
			samples := []models.SenderSafety{
				{
					Email:       defang.MkEmail("alerts", "stripe.com"),
					TotalCount:  14,
					TotalBytes:  2_310_000,
					DeleteCount: 14,
					DeleteBytes: 2_310_000,
					KeepCount:   0,
					Reasons:     []string{models.ReasonStripe},
				},
				{
					Email:       defang.MkEmail("noreply", "github.com"),
					TotalCount:  38,
					TotalBytes:  780_000,
					DeleteCount: 30,
					DeleteBytes: 615_789,
					KeepCount:   38 - 30,
					Reasons:     []string{models.ReasonGitHub},
				},
				{
					Email:       defang.MkEmail("newsletter", "pragmaticengineer.com"),
					TotalCount:  12,
					TotalBytes:  540_000,
					DeleteCount: 11,
					DeleteBytes: 495_000,
					KeepCount:   12 - 11,
					Reasons:     []string{models.ReasonNewsletter},
				},
				{
					Email:       defang.MkEmail("billing", "amazonaws.com"),
					TotalCount:  6,
					TotalBytes:  211_000,
					DeleteCount: 5,
					DeleteBytes: 175_833,
					KeepCount:   6 - 5,
					Reasons:     []string{models.ReasonAWSBilling},
				},
				{
					Email:       defang.MkEmail("noreply", "internal.example.com"),
					TotalCount:  4,
					TotalBytes:  88_000,
					DeleteCount: 4,
					DeleteBytes: 88_000,
					KeepCount:   0,
					Reasons:     []string{models.ReasonNewsletter},
				},
			}
			_, _ = fmt.Fprintln(out, "gclean demo: sample models.SenderSafety preview (no scan has been run yet)")
			_, _ = fmt.Fprintln(out, "Each row was constructed at runtime via defang.MkEmail —")
			_, _ = fmt.Fprintln(out, "the obfuscation-defense is load-bearing in production code, not just in tests.")
			_, _ = fmt.Fprintln(out)
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "SENDER\tMESSAGES\tSTORAGE\tSAFE-TO-DELETE")
			for _, s := range samples {
				_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%d\n", s.Email, s.TotalCount, format.HumanBytes(s.TotalBytes), s.DeleteCount)
			}
			_ = tw.Flush()
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, "Run `gclean scan --fixtures testdata/fixtures/messages.json` to see real data.")
			return nil
		},
	}
}
