package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Archive describes the pull requests kept for one closed calendar year: those last
// updated in that year, as collected after it ended. Any later change to one of them
// moves its update date out of the year, so the rest never go stale.
type Archive struct {
	Repository  string    `json:"repository"`
	Year        int       `json:"year"`
	Fingerprint string    `json:"fingerprint"`
	Month       string    `json:"month"`
	CollectedAt time.Time `json:"collected_at"`
}

const (
	archiveMeta = "archive.json"
	archiveData = "prs.jsonl.gz"
)

// ReadArchive reads an archive written by WriteArchive. The error wraps fs.ErrNotExist
// when dir holds no archive.
func ReadArchive(dir string) (Archive, []PullRequest, error) {
	var a Archive
	b, err := os.ReadFile(filepath.Join(dir, archiveMeta))
	if err != nil {
		return a, nil, err
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return a, nil, fmt.Errorf("read %s: %w", filepath.Join(dir, archiveMeta), err)
	}
	prs, err := ReadFile(filepath.Join(dir, archiveData))
	return a, prs, err
}

// WriteArchive writes the archive's description and pull requests into dir, creating it.
func WriteArchive(dir string, a Archive, prs []PullRequest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := WriteFile(filepath.Join(dir, archiveData), prs); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, archiveMeta), append(b, '\n'), 0o644)
}
