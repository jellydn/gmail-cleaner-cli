// Package cli wires gclean's Cobra command tree. Subcommand handlers live
// in per-group files:
//
//	auth.go      — login / logout                   (OAuth browser flow)
//	pipeline.go  — scan / stats / dry-run / clean / purge / undo
//	              as thin adapters over engine.Pipeline (undo-cache path only)
//	insights.go  — sender / attachments / newsletters / receipts
//	              + tui-selection.saveSelection
//	meta.go      — rules / config / tui             (experimental TUI in meta.go)
//	demo.go      — demo (self-contained preview of tui output)
//	dev.go       — dev (develop mode with file watching)
//
// cli.go itself owns the root command (Build) and the cross-cutting helpers
// every other file needs (path resolution, client resolution). Handlers are
// intentionally thin — heavy logic lives in internal/engine,
// internal/storage, and internal/gmailclient. Output formatting helpers
// (humanBytes, truncate) live in internal/format.
package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"gclean/internal/gmailclient"
)

// Build returns the root command. Pass nil for stdout/stderr to use the
// process streams; tests pass temp files.
//
// The AddCommand list is intentionally exhaustive — every subcommand from
// PRD §5/§9 is referenced here. A future contributor who adds a new
// subcommand MUST also extend TestBuild_Help's substring list in
// cli_test.go so the registration is locked.
func Build(stdout, stderr io.Writer) *cobra.Command {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	root := &cobra.Command{
		Use:           "gclean",
		Short:         "Reclaim Gmail storage safely",
		Long:          "Gmail Clean CLI — preserve important conversations, identify low-value mail, recover storage.\nDry-run by default. Trash before purge. Undo supported.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Order stays aligned with PRD §9; TestBuild_Help substring list is
	// unordered so adding/reordering here doesn't break the test, only
	// dropping does.
	root.AddCommand(
		newLoginCmd(stdout, stderr),
		newLogoutCmd(stdout, stderr),
		newScanCmd(stdout, stderr),
		newStatsCmd(stdout, stderr),
		newDryRunCmd(stdout, stderr),
		newCleanCmd(stdout, stderr),
		newPurgeCmd(stdout, stderr),
		newUndoCmd(stdout, stderr),
		newRulesCmd(stdout, stderr),
		newConfigCmd(stdout, stderr),
		newSenderCmd(stdout, stderr),
		newAttachmentsCmd(stdout, stderr),
		newNewslettersCmd(stdout, stderr),
		newReceiptsCmd(stdout, stderr),
		newTuiCmd(stdout, stderr),
		newDemoCmd(stdout, stderr),
		newDevCmd(stdout, stderr),
	)
	root.SetOut(stdout)
	root.SetErr(stderr)
	for _, sub := range root.Commands() {
		sub.SetOut(stdout)
		sub.SetErr(stderr)
	}
	return root
}

// resolveClient returns the right Gmail Client. --fixtures wins so the
// dev/test path always uses FakeClient; otherwise RealClient is attempted.
func resolveClient(fixturesPath, credsPath string) (gmailclient.Client, error) {
	if fixturesPath != "" {
		return gmailclient.NewFakeClient(fixturesPath)
	}
	return gmailclient.NewRealClient(credsPath)
}

// storePath returns the SQLite DB path honoring GCLEAN_DB_PATH.
func storePath() string {
	if p := os.Getenv("GCLEAN_DB_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gclean", "gclean.db")
}

// credentialsPath returns the credentials.json path honoring
// GCLEAN_CREDENTIALS_PATH.
func credentialsPath() string {
	if p := os.Getenv("GCLEAN_CREDENTIALS_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gclean", "credentials.json")
}
