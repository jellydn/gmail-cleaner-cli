package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// UndoCache persists pre-trash message records so `gclean undo` can restore
// them. It lives in storage (not cli) so the engine pipeline can write it as
// a stage without importing os/env. The CLI still owns the *path* (via
// GCLEAN_UNDO_CACHE); the engine passes that path in.
const undoCacheVersion = 1

type undoCache struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Records  []StoredMessage `json:"records"`
}

// checksumRecords hashes the canonical JSON of the records so a partial write
// or external tampering is detected before the records are re-inserted.
func checksumRecords(recs []StoredMessage) (string, error) {
	payload, err := json.Marshal(recs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// SaveUndoCache writes the pre-trash records to path with an integrity tag.
// It writes and syncs a temporary file before renaming it into place so a
// crash cannot leave a partially-written cache at the canonical path. It
// refuses to overwrite a non-empty existing cache.
func SaveUndoCache(path string, recs []StoredMessage) error {
	return writeUndoCache(path, recs, false)
}

// ReplaceUndoCache overwrites an existing undo cache. It is used to trim the
// records to the subset that actually reached Trash after a partial mutation
// (or that remain in Trash after a partial restore/purge), so `gclean undo`
// only ever touches the messages that really need it. The write is still
// atomic.
func ReplaceUndoCache(path string, recs []StoredMessage) error {
	return writeUndoCache(path, recs, true)
}

func writeUndoCache(path string, recs []StoredMessage, overwrite bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !overwrite {
		if info, err := os.Stat(path); err == nil {
			if info.Size() > 0 {
				return fmt.Errorf("undo cache already exists at %s; run `gclean undo` or `gclean purge` first", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	sum, err := checksumRecords(recs)
	if err != nil {
		return err
	}
	c := undoCache{Version: undoCacheVersion, Checksum: sum, Records: recs}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".undo-cache-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

// LoadUndoCache reads pre-trash records, verifying the integrity tag. A
// missing file is not an error (nothing to undo). A checksum mismatch or
// unsupported version is — a corrupt cache must not silently re-upsert
// strange rows.
func LoadUndoCache(path string) ([]StoredMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c undoCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Checksum != "" {
		if c.Version != undoCacheVersion {
			return nil, fmt.Errorf("undo cache version %d unsupported (want %d)", c.Version, undoCacheVersion)
		}
		want, err := checksumRecords(c.Records)
		if err != nil {
			return nil, err
		}
		if want != c.Checksum {
			return nil, errors.New("undo cache checksum mismatch: file may be corrupt or partially written")
		}
	}
	return c.Records, nil
}
