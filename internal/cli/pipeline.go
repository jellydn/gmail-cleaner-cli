package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"gclean/internal/config"
	"gclean/internal/engine"
	"gclean/internal/format"
	"gclean/internal/gmailclient"
	"gclean/internal/storage"
)

// pipeline.go — gclean scan / stats / dry-run / clean / purge / undo.
// Plus the undo-cache path resolution. These six subcommands are the
// "action" surface — every other file is either config (meta.go),
// reporting (insights.go), or auth (auth.go).
//
// The heavy lifting (fetch → classify → upsert → plan → verdict → trash)
// now lives in engine.Pipeline as composable stages; each handler below is a
// thin adapter that opens the store, resolves the client + config, and runs
// the slice of stages it needs.

// buildPipeline wires an engine.Pipeline from already-resolved CLI inputs.
// The caller owns store open/close and client/cache resolution.
func buildPipeline(store *storage.Store, client engine.Gmailer, doc config.Document, out, errOut io.Writer, cachePath string) (engine.Pipeline, error) {
	cc, err := doc.CompileFull()
	if err != nil {
		return engine.Pipeline{}, err
	}
	return engine.Pipeline{
		Store:         store,
		Client:        client,
		Keep:          cc.Keep,
		Rules:         cc.Rules,
		Out:           out,
		ErrOut:        errOut,
		CachePath:     cachePath,
		SelectionPath: selectionPath(),
	}, nil
}

// --- Scan / Stats -----------------------------------------------------

func newScanCmd(out, errOut io.Writer) *cobra.Command {
	var fixtures string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Pull messages from Gmail, classify them, persist to local SQLite",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			client, err := resolveClient(fixtures, credentialsPath())
			if err != nil {
				return err
			}
			doc, err := config.Load()
			if err != nil {
				return err
			}
			p, err := buildPipeline(store, client, doc, out, errOut, "")
			if err != nil {
				return err
			}
			if err := p.Run(p.ScanStages()...); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Scanned %d messages.\n", p.Scanned())
			_, _ = fmt.Fprintln(out, "Next: `gclean dry-run` to preview, or `gclean stats` for storage analytics.")
			return nil
		},
	}
	cmd.Flags().StringVar(&fixtures, "fixtures", "", "Path to a JSON fixtures file (dev/test mode)")
	return cmd
}

func newStatsCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show storage analytics from the local SQLite store",
		Long:  "Mirrors PRD §5 example output: total messages, estimated storage, potential reclaim, largest sender, newsletter/notification counts, attachments, by-category and by-year breakdown.",
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
			rep := agg.Report

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintf(tw, "Total messages\t%d\n", rep.TotalMessages)
			_, _ = fmt.Fprintf(tw, "Estimated storage\t%s\n", format.HumanBytes(rep.EstimatedStorage))
			_, _ = fmt.Fprintf(tw, "Potential reclaim\t%s\n", format.HumanBytes(rep.PotentialReclaim))
			_, _ = fmt.Fprintf(tw, "Largest sender\t%s (%s, %d msgs)\n", rep.LargestSender.Email, format.HumanBytes(rep.LargestSender.Bytes), rep.LargestSender.Count)
			_, _ = fmt.Fprintf(tw, "Newsletters\t%d\n", rep.NewsletterCount)
			_, _ = fmt.Fprintf(tw, "Notifications\t%d\n", rep.NotificationCount)
			_, _ = fmt.Fprintf(tw, "Attachments >10MB\t%d\n", rep.AttachmentsOver10MB)
			_ = tw.Flush()

			if len(rep.ByCategory) > 0 {
				_, _ = fmt.Fprintln(out, "\nBy category:")
				keys := make([]string, 0, len(rep.ByCategory))
				for k := range rep.ByCategory {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					_, _ = fmt.Fprintf(out, "  %s\t%d\n", k, rep.ByCategory[k])
				}
			}
			if len(rep.ByYear) > 0 {
				_, _ = fmt.Fprintln(out, "\nBy year:")
				years := make([]int, 0, len(rep.ByYear))
				for y := range rep.ByYear {
					years = append(years, y)
				}
				sort.Ints(years)
				for _, y := range years {
					_, _ = fmt.Fprintf(out, "  %d\t%d\n", y, rep.ByYear[y])
				}
			}
			return nil
		},
	}
}

// --- Dry-run / Clean / Purge / Undo -----------------------------------

func newDryRunCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "dry-run",
		Short: "Show what `gclean clean` would do. Changes nothing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			// dry-run needs no real client; pass a no-op so the function
			// signature stays uniform with clean.
			client, _ := resolveClient("", credentialsPath())
			doc, err := config.Load()
			if err != nil {
				return err
			}
			p, err := buildPipeline(store, client, doc, out, errOut, "")
			if err != nil {
				return err
			}
			if err := p.Run(p.PlanStages()...); err != nil {
				return err
			}
			rep := p.Report()
			_, _ = fmt.Fprintln(out, "──────────────────────────")
			_, _ = fmt.Fprintf(out, "Safe to delete\t%d messages\n", rep.DeleteCount)
			_, _ = fmt.Fprintf(out, "Recover\t%s\n", format.HumanBytes(rep.RecoverBytes))
			_, _ = fmt.Fprintf(out, "Will keep\t%d (contacts, starred, important, replied, recent, ignored)\n", rep.KeepCount)
			_, _ = fmt.Fprintf(out, "Will archive\t%d\n", rep.ArchiveCount)
			_, _ = fmt.Fprintln(out, "──────────────────────────")
			_, _ = fmt.Fprintln(out, "Nothing changes.")
			if len(rep.SampleDeletes) > 0 {
				_, _ = fmt.Fprintln(out, "\nSample deletes:")
				for _, s := range rep.SampleDeletes {
					_, _ = fmt.Fprintf(out, "  %s\n", s)
				}
			}
			if len(rep.DeleteBySender) > 0 {
				_, _ = fmt.Fprintln(out, "\nTop delete senders:")
				pairs := topN(rep.DeleteBySender, 10)
				for _, pr := range pairs {
					_, _ = fmt.Fprintf(out, "  %s\t%d msgs\n", pr.k, pr.v)
				}
			}
			if len(rep.RecoverByReason) > 0 {
				_, _ = fmt.Fprintln(out, "\nRecover by reason:")
				pairs := topN(rep.RecoverByReason, 10)
				for _, pr := range pairs {
					_, _ = fmt.Fprintf(out, "  %s\t%d msgs\n", pr.k, pr.v)
				}
			}
			return nil
		},
	}
}

func newCleanCmd(out, errOut io.Writer) *cobra.Command {
	var fixtures string
	var yes bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Move junk mail to Gmail Trash (recoverable for 30 days)",
		Long:  "Runs the planner and moves every VerdictDelete message to Trash via the Gmail API. Refuses to run without --yes. Records the originals so `gclean undo` can restore.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				_, _ = fmt.Fprintln(errOut, "Refusing to clean without --yes. Run `gclean dry-run` first, then re-run with --yes.")
				return errors.New("confirmation required")
			}
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			client, err := resolveClient(fixtures, credentialsPath())
			if err != nil {
				return err
			}
			doc, err := config.Load()
			if err != nil {
				return err
			}
			cache, _ := defaultCache()
			p, err := buildPipeline(store, client, doc, out, errOut, cache)
			if err != nil {
				return err
			}
			if err := p.Run(p.PlanStages()...); err != nil {
				return err
			}
			if err := p.Run(p.ApplyStages()...); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Moved %d messages to Trash.\n", len(p.TrashedIDs()))
			_, _ = fmt.Fprintln(out, "Recoverable within 30 days via `gclean undo`. Permanent via `gclean purge`.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation before moving mail to Trash")
	cmd.Flags().StringVar(&fixtures, "fixtures", "", "Path to a JSON fixtures file (dev/test mode)")
	return cmd
}

func newPurgeCmd(out, errOut io.Writer) *cobra.Command {
	var fixtures string
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Empty Gmail Trash permanently. Cannot be undone by Gmail.",
		Long:  "PR §15: requires explicit --yes. This is the ONLY gclean operation that Gmail's server-side Undo cannot reverse. Use after a clean to make the deletion permanent and recover the storage.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				_, _ = fmt.Fprintln(errOut, "Refusing to purge without --yes. Re-run with --yes to confirm.")
				return errors.New("confirmation required")
			}
			cache, _ := defaultCache()
			records, _ := storage.LoadUndoCache(cache) // best-effort; may be absent
			client, err := resolveClient(fixtures, credentialsPath())
			if err != nil {
				return err
			}
			if err := purgeWithReconcile(client, records, cache); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "Trash emptied. Storage reclaimed from Gmail's side.")
			_ = os.Remove(cache)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation before emptying Trash")
	cmd.Flags().StringVar(&fixtures, "fixtures", "", "Path to a JSON fixtures file (dev/test mode)")
	return cmd
}

func newUndoCmd(out, errOut io.Writer) *cobra.Command {
	var fixtures string
	cmd := &cobra.Command{
		Use:   "undo",
		Short: "Restore messages from Trash and re-insert into the local store",
		RunE: func(cmd *cobra.Command, args []string) error {
			cache, err := defaultCache()
			if err != nil {
				return err
			}
			records, err := storage.LoadUndoCache(cache)
			if err != nil {
				return err
			}
			if len(records) == 0 {
				_, _ = fmt.Fprintln(out, "Nothing to undo.")
				return nil
			}
			client, err := resolveClient(fixtures, credentialsPath())
			if err != nil {
				return err
			}
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			if err := undoWithReconcile(client, store, records, cache); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Restored %d messages from Trash.\n", len(records))
			_ = os.Remove(cache)
			return nil
		},
	}
	cmd.Flags().StringVar(&fixtures, "fixtures", "", "Path to a JSON fixtures file (dev/test mode)")
	return cmd
}

// --- undo cache path ---------------------------------------------------

// undoWithReconcile restores records from Trash, reconciling a partial
// failure so the local store and undo cache reflect Gmail's actual state:
// only the messages actually restored are re-inserted, and the cache is
// trimmed to the ones still in Trash so `gclean undo` can be retried.
func undoWithReconcile(client gmailclient.Client, store *storage.Store, records []storage.StoredMessage, cachePath string) error {
	ids := recordIDs(records)
	if err := client.RestoreFromTrash(ids); err != nil {
		still, inErr := client.InTrash(ids)
		if inErr != nil {
			return fmt.Errorf("restore: %w (reconcile failed: %v)", err, inErr)
		}
		restored := subtractIDs(ids, still)
		if err := store.RestoreTrashed(filterRecords(records, restored)); err != nil {
			return fmt.Errorf("restore: %w (reconcile re-insert failed: %v)", err, err)
		}
		if err := storage.ReplaceUndoCache(cachePath, filterRecords(records, still)); err != nil {
			return fmt.Errorf("restore: %w (reconcile cache rewrite failed: %v)", err, err)
		}
		return fmt.Errorf("restore partially applied: %d of %d messages restored: %w", len(restored), len(ids), err)
	}
	if err := store.RestoreTrashed(records); err != nil {
		return err
	}
	return nil
}

// purgeWithReconcile empties Trash, keeping (and trimming) the undo cache to
// the messages still in Trash on a partial failure so `gclean undo` can still
// recover them. A full success returns nil and the caller deletes the cache.
func purgeWithReconcile(client gmailclient.Client, records []storage.StoredMessage, cachePath string) error {
	if err := client.EmptyTrash(); err != nil {
		if len(records) > 0 {
			still, inErr := client.InTrash(recordIDs(records))
			if inErr != nil {
				return fmt.Errorf("purge: %w (reconcile failed: %v)", err, inErr)
			}
			if len(still) > 0 {
				if err2 := storage.ReplaceUndoCache(cachePath, filterRecords(records, still)); err2 != nil {
					return fmt.Errorf("purge: %w (reconcile cache rewrite failed: %v)", err, err2)
				}
				return fmt.Errorf("purge partially applied: %d messages remain in Trash: %w", len(still), err)
			}
		}
		return err
	}
	return nil
}

// recordIDs extracts the message IDs from undo-cache records.
func recordIDs(records []storage.StoredMessage) []string {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	return ids
}

// filterRecords returns the records whose ID is in ids, preserving order.
func filterRecords(records []storage.StoredMessage, ids []string) []storage.StoredMessage {
	keep := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		keep[id] = struct{}{}
	}
	out := make([]storage.StoredMessage, 0, len(ids))
	for _, r := range records {
		if _, ok := keep[r.ID]; ok {
			out = append(out, r)
		}
	}
	return out
}

// subtractIDs returns all minus remove, preserving order.
func subtractIDs(all, remove []string) []string {
	rm := make(map[string]struct{}, len(remove))
	for _, id := range remove {
		rm[id] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, id := range all {
		if _, ok := rm[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

func selectionPath() string {
	if p := os.Getenv("GCLEAN_SELECTION_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gclean", "tui-selection.json")
}

func defaultCache() (string, error) {
	if p := os.Getenv("GCLEAN_UNDO_CACHE"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gclean", "undo-cache.json"), nil
}

// --- top-N rendering (used by dry-run to show leading senders + reasons) ---

type kv struct {
	k string
	v int64
}

func topN(m map[string]int64, n int) []kv {
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return pairs
}
