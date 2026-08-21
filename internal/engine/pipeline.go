package engine

import (
	"fmt"
	"strings"

	"gclean/internal/models"
	"gclean/internal/storage"
)

// Pipeline is the scan→plan→trash seam. Previously the 6-step flow
// (open store → fetch → classify → upsert → plan → set verdict →
// trash → write undo cache) lived inline across runScan + planAndApply in
// internal/cli, duplicated by scan / dry-run / clean / undo handlers. The
// CLI now builds a Pipeline and runs a slice of stages; each stage is
// independently testable.
//
// The Pipeline holds already-resolved dependencies (store, client, config).
// It does no env/path/file discovery itself — the CLI owns that so the
// engine stays deterministic and pure (its documented invariant).
type Pipeline struct {
	Store  *storage.Store
	Client Gmailer
	Keep   KeepConfig
	Rules  RuleConfig
	Out    Writer
	ErrOut Writer
	// CachePath is the undo-cache file the Apply stage writes to. Empty
	// disables caching (some callers, e.g. dry-run, don't trash).
	CachePath     string
	SelectionPath string

	// stage-populated state, read by the CLI to render output.
	scanned        int
	decisions      []models.Decision
	report         models.DryRunReport
	trashedIDs     []string
	trashedRecords []storage.StoredMessage
}

// Gmailer is the subset of gmailclient.Client the pipeline needs. Declaring
// it here keeps the engine package free of the gmailclient import graph and
// makes the stage boundary explicit.
type Gmailer interface {
	ListMessages(query string, max int) ([]*models.Message, error)
	TrashMessages(ids []string) error
	RestoreFromTrash(ids []string) error
}

// Writer is the minimal output sink (io.Writer) the stages print to.
type Writer interface {
	Write(p []byte) (int, error)
}

// Stage is one step of the pipeline. Each stage mutates the shared Pipeline
// state and returns an error to abort. Keeping stages small is what makes
// the deletion test meaningful: "would deleting this stage concentrate
// complexity or merely move it?" — a stage concentrates.
type Stage func(p *Pipeline) error

// Run executes stages in order, stopping at the first error.
func (p *Pipeline) Run(stages ...Stage) error {
	for _, s := range stages {
		if err := s(p); err != nil {
			return err
		}
	}
	return nil
}

// ScanStages is the full ingest path: fetch + classify + persist.
// Used by `scan`. (Store open/close is owned by the CLI.)
func (p *Pipeline) ScanStages() []Stage {
	return []Stage{p.fetchAndClassify}
}

// PlanStages loads classified rows, runs the planner, and writes verdicts
// back. NO Gmail interaction. Used by `dry-run` and as the first half of
// `clean`.
func (p *Pipeline) PlanStages() []Stage {
	return []Stage{p.loadPlan}
}

// ApplyStages trashes the delete cohort and writes the undo cache. Must run
// after PlanStages. Used by `clean`.
func (p *Pipeline) ApplyStages() []Stage {
	return []Stage{p.applyTrash}
}

// fetchAndClassify pulls messages, classifies each, and upserts to SQLite.
func (p *Pipeline) fetchAndClassify(pl *Pipeline) error {
	msgs, err := pl.Client.ListMessages("", 0)
	if err != nil {
		return fmt.Errorf("list messages: %w", err)
	}
	for _, m := range msgs {
		c := Classify(m)
		if err := pl.Store.Upsert(storage.FromClassified(&c, models.VerdictKeep)); err != nil {
			return fmt.Errorf("persist %s: %w", m.ID, err)
		}
	}
	pl.scanned = len(msgs)
	return nil
}

// loadPlan runs the planner and persists verdicts. It never touches Gmail.
func (p *Pipeline) loadPlan(pl *Pipeline) error {
	classified, err := pl.Store.AllClassified()
	if err != nil {
		return err
	}
	selected, err := loadSelectedSenders(pl.SelectionPath)
	if err != nil {
		return err
	}
	decisions, rep := Plan(PlanInputs{
		Messages:        classified,
		Config:          pl.Rules,
		Keep:            pl.Keep,
		SelectedSenders: selected,
	})
	for _, d := range decisions {
		reasons := strings.Join(d.Reasons, ";")
		if err := pl.Store.SetVerdict(d.Message.ID, int(d.Verdict), reasons, d.Verdict == models.VerdictProtected); err != nil {
			return fmt.Errorf("set verdict %s: %w", d.Message.ID, err)
		}
	}
	pl.decisions = decisions
	pl.report = rep
	return nil
}

// applyTrash moves the delete cohort to Trash and stashes the originals for
// undo. It is the ONLY stage that performs Gmail mutation.
func (p *Pipeline) applyTrash(pl *Pipeline) error {
	ids := []string{}
	toTrash := []storage.StoredMessage{}
	for _, d := range pl.decisions {
		if d.Verdict != models.VerdictDelete {
			continue
		}
		ids = append(ids, d.Message.ID)
		toTrash = append(toTrash, storage.FromClassified(d.Classified, models.VerdictDelete))
	}
	if len(ids) > 0 {
		if pl.CachePath != "" {
			if err := storage.SaveUndoCache(pl.CachePath, toTrash); err != nil {
				return fmt.Errorf("save undo cache: %w", err)
			}
		}
		if err := pl.Client.TrashMessages(ids); err != nil {
			return fmt.Errorf("trash: %w", err)
		}
		if err := pl.Store.MarkTrashed(ids); err != nil {
			return fmt.Errorf("mark trashed: %w", err)
		}
	}
	pl.trashedIDs = ids
	pl.trashedRecords = toTrash
	return nil
}

// Exported accessors for the CLI to render output after a run.
func (p *Pipeline) Scanned() int                            { return p.scanned }
func (p *Pipeline) Report() models.DryRunReport             { return p.report }
func (p *Pipeline) TrashedIDs() []string                    { return p.trashedIDs }
func (p *Pipeline) TrashedRecords() []storage.StoredMessage { return p.trashedRecords }

func loadSelectedSenders(path string) (map[string]struct{}, error) {
	if path == "" {
		return nil, nil
	}
	senders, err := storage.LoadSelection(path)
	if err != nil {
		return nil, fmt.Errorf("load sender selection: %w", err)
	}
	if len(senders) == 0 {
		return nil, nil
	}
	selected := make(map[string]struct{}, len(senders))
	for _, sender := range senders {
		selected[sender] = struct{}{}
	}
	return selected, nil
}
