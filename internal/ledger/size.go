package ledger

import (
	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/glob"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

// BucketCount is a size bucket with the number of landed pull requests in it.
type BucketCount struct {
	config.Bucket
	Landed int `json:"landed"`
}

// SizeSummary describes the sizes of the human pull requests that landed on the default
// branch in the window. ChangedLines are all their added and deleted lines, ExcludedLines
// those in generated, vendored or lock files, CountedLines the weighted lines that set the
// buckets. Unsized counts landed pull requests whose file list was not collected in full.
type SizeSummary struct {
	Buckets                []BucketCount `json:"buckets"`
	ChangedLines           int           `json:"changed_lines"`
	ExcludedLines          int           `json:"excluded_lines"`
	CountedLines           float64       `json:"counted_lines"`
	Unsized                int           `json:"unsized"`
	ReviewFeedbackMultiple float64       `json:"review_feedback_multiplier"`
}

type prSize struct {
	counted  float64
	changed  int
	excluded int
	bucket   int
}

type sizer struct {
	cfg     config.Size
	exclude glob.Set
	tests   glob.Set
	attrs   glob.Attributes
}

func newSizer(cfg config.Size) sizer {
	s := sizer{cfg: cfg}
	s.exclude, _ = glob.CompileAll(append(append([]string{}, cfg.Exclude...), cfg.ExcludeExtra...))
	s.tests, _ = glob.CompileAll(cfg.TestPatterns)
	if cfg.UseGitAttributes {
		s.attrs = glob.ParseGitAttributes(cfg.GitAttributes)
	}
	return s
}

func (s sizer) excluded(path string) bool {
	return s.exclude.Match(path) || s.attrs.Excluded(path)
}

// size measures a pull request, or reports false when its files were not collected.
func (s sizer) size(pr store.PullRequest) (prSize, bool) {
	if pr.Files == nil {
		return prSize{}, false
	}
	var z prSize
	for _, f := range pr.Files.Nodes {
		z.changed += f.Additions + f.Deletions
		lines, excluded := s.fileLines(f)
		if excluded {
			z.excluded += f.Additions + f.Deletions
			continue
		}
		z.counted += lines
	}
	z.bucket = len(s.cfg.Buckets) - 1
	for i, b := range s.cfg.Buckets {
		if b.Max != nil && z.counted <= *b.Max {
			z.bucket = i
			break
		}
	}
	return z, true
}

// fileLines returns a file's counted lines, or reports that the file is excluded.
func (s sizer) fileLines(f store.File) (float64, bool) {
	if s.excluded(f.Path) {
		return 0, true
	}
	lines := float64(f.Additions) + s.cfg.DeletionsWeight*float64(f.Deletions)
	if s.tests.Match(f.Path) {
		lines *= s.cfg.TestWeight
	}
	return lines, false
}

func (s sizer) weight(z prSize) float64 { return s.cfg.Buckets[z.bucket].Weight }
