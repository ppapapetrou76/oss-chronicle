// Package config loads and validates the .github/oss-chronicle.yaml file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ppapapetrou76/oss-chronicle/internal/glob"
)

// DateLayout is the format of window dates in the config file and on the command line.
const DateLayout = "2006-01-02"

// Config is the full configuration for one repository. Repository is the repository
// being counted, which is always the one the workflow runs in, so the config file cannot
// set it. An empty DefaultBranch is inferred as the branch most pull requests target.
type Config struct {
	Repository    string       `yaml:"-"`
	DefaultBranch string       `yaml:"default_branch"`
	Window        Window       `yaml:"window"`
	Periods       []PeriodSpec `yaml:"periods"`
	PullRequests  PullRequests `yaml:"pull_requests"`
	Bots          Bots         `yaml:"bots"`
	Comments      Comments     `yaml:"comments"`
	Size          Size         `yaml:"size"`
	Components    Components   `yaml:"components"`
	Publish       Publish      `yaml:"publish"`
}

// Size controls how pull requests are weighted by the lines they change.
//
// Counted lines are additions plus DeletionsWeight times deletions, over files that are
// neither matched by Exclude or ExcludeExtra nor marked generated, vendored or binary in
// the repository's .gitattributes (when UseGitAttributes is set). Setting Exclude replaces
// the default list; ExcludeExtra adds to it. Lines in files matching TestPatterns are
// multiplied by TestWeight. A pull request takes the weight of the first bucket whose Max
// it does not exceed; the last bucket has no Max.
type Size struct {
	Buckets          []Bucket `yaml:"buckets"`
	Exclude          []string `yaml:"exclude"`
	ExcludeExtra     []string `yaml:"exclude_extra"`
	UseGitAttributes bool     `yaml:"use_gitattributes"`
	DeletionsWeight  float64  `yaml:"deletions_weight"`
	TestPatterns     []string `yaml:"test_patterns"`
	TestWeight       float64  `yaml:"test_weight"`
	ReviewFeedback   float64  `yaml:"review_feedback_multiplier"`
	// GitAttributes is the repository's .gitattributes text, set by the collector.
	GitAttributes string `yaml:"-"`
}

// Components groups files into the areas of the project.
//
// Map entries are tried in order and the first whose Paths match a file names its
// component. Files no entry matches take their CODEOWNERS owners when UseCodeowners is
// set, then their top-level directory. Each pull request belongs to the component where
// most of its counted lines changed.
type Components struct {
	Map           []Component `yaml:"map"`
	UseCodeowners bool        `yaml:"use_codeowners"`
	// Codeowners is the repository's CODEOWNERS text, set by the collector.
	Codeowners string `yaml:"-"`
}

// Component names an area of the project and the paths that belong to it.
type Component struct {
	Name  string   `yaml:"name"`
	Paths []string `yaml:"paths"`
}

// Bucket is one size class. Max is nil for the last, open-ended bucket.
type Bucket struct {
	Name   string   `yaml:"name" json:"name"`
	Max    *float64 `yaml:"max" json:"max,omitempty"`
	Weight float64  `yaml:"weight" json:"weight"`
}

// Window is the period activity is counted for. Days applies when From is empty. The
// window ends at To, or today when To is empty or Now.
type Window struct {
	Days int    `yaml:"days"`
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// PullRequests holds the rules that tell maintenance pull requests apart from feature and fix work.
type PullRequests struct {
	Backport     Backport     `yaml:"backport"`
	Dependencies Dependencies `yaml:"dependencies"`
}

// Backport identifies backport pull requests by head branch prefix, or by a base branch
// matching one of the BaseBranches globs. Pull requests into any other branch that is not
// the default branch, such as stacked pull requests, are treated as feature work that has
// not landed yet.
type Backport struct {
	HeadPrefixes []string `yaml:"head_prefixes"`
	BaseBranches []string `yaml:"base_branches"`
}

// Dependencies identifies dependency-bump pull requests by author or title.
type Dependencies struct {
	Authors       []string `yaml:"authors"`
	TitlePrefixes []string `yaml:"title_prefixes"`
}

// Bots identifies automated accounts. GitHub App accounts are always bots;
// Patterns match anywhere in the login, ignoring case, and Logins match exactly.
type Bots struct {
	Patterns []string `yaml:"patterns"`
	Logins   []string `yaml:"logins"`
}

// Comments holds rules for pull request conversation comments. A command matches when a
// line of the comment starts with it, ignoring case; lines that cancel a command are skipped.
// ApprovalCommands count as a review, for projects that approve by comment (Prow, bors).
// MergeCommands credit the merge to the person who issued them when a bot performs it.
type Comments struct {
	BotCommandPrefixes []string `yaml:"bot_command_prefixes"`
	ApprovalCommands   []string `yaml:"approval_commands"`
	MergeCommands      []string `yaml:"merge_commands"`
}

// Publish controls what appears in the output.
type Publish struct {
	OptOut []string `yaml:"opt_out"`
}

// Default returns the configuration used when no file is present.
func Default() Config {
	limit := func(n float64) *float64 { return &n }
	return Config{
		Window:  Window{Days: 90},
		Periods: PeriodIDs(DefaultPeriods...),
		Size: Size{
			Buckets: []Bucket{
				{Name: "XS", Max: limit(10), Weight: 0.5},
				{Name: "S", Max: limit(50), Weight: 1},
				{Name: "M", Max: limit(250), Weight: 2},
				{Name: "L", Weight: 3},
			},
			Exclude: []string{
				"go.sum", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb",
				"Cargo.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "Gemfile.lock", "composer.lock", "mix.lock",
				"vendor/**", "**/vendor/**", "node_modules/**", "**/node_modules/**",
				"*.pb.go", "*.pb.gw.go", "*_pb2.py", "*_pb2_grpc.py", "zz_generated*",
				"*.min.js", "*.min.css", "*.snap", "__snapshots__/**", "**/__snapshots__/**",
			},
			UseGitAttributes: true,
			DeletionsWeight:  1,
			TestPatterns:     []string{"*_test.go", "*_test.py", "test_*.py", "*.test.*", "*.spec.*", "test/**", "tests/**", "**/test/**", "**/tests/**", "**/testdata/**"},
			TestWeight:       1,
			ReviewFeedback:   2,
		},
		PullRequests: PullRequests{
			Backport: Backport{
				HeadPrefixes: []string{"cherry-pick", "backport", "automated-cherry-pick-of-", "mergify/bp/"},
				BaseBranches: []string{"release-*", "release/*", "release_*", "stable-*", "stable/*", "*-stable", "v[0-9]*"},
			},
			Dependencies: Dependencies{
				Authors:       []string{"dependabot", "renovate"},
				TitlePrefixes: []string{"chore(deps", "fix(deps", "build(deps"},
			},
		},
		Bots: Bots{
			Patterns: []string{"[bot]", "-bot", "-robot", "mergebot", "dependabot", "renovate", "github-actions", "copilot", "codecov", "coderabbit", "sonarqube", "netlify", "snyk"},
			Logins:   []string{"bors", "homu"},
		},
		Comments: Comments{
			BotCommandPrefixes: []string{"/", "@dependabot", "@renovate"},
			ApprovalCommands:   []string{"/lgtm", "/approve", "@bors r+", "@bors r=", "bors r+", "bors r="},
			MergeCommands: []string{"/approve", "/merge", "@bors r+", "@bors r=", "bors r+", "bors r=", "@bors merge", "bors merge",
				"@mergifyio queue", "@mergifyio merge", "@pytorchbot merge", "@pytorchmergebot merge"},
		},
	}
}

// Load reads a config file over the defaults. A missing file returns the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, cfg.Validate()
	}
	if err != nil {
		return Config{}, err
	}
	if err := Parse(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes YAML into cfg, rejecting unknown keys, then validates the result.
// Fields absent from the YAML keep the values cfg already holds.
func Parse(b []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return cfg.Validate()
}

// Validate reports every problem in the config at once.
func (c Config) Validate() error {
	var errs []error
	if c.DefaultBranch != strings.TrimSpace(c.DefaultBranch) {
		errs = append(errs, fmt.Errorf("default_branch %q has surrounding spaces", c.DefaultBranch))
	}
	errs = append(errs, c.Size.validate()...)
	errs = append(errs, c.Components.validate()...)
	for _, g := range c.PullRequests.Backport.BaseBranches {
		if _, err := path.Match(g, ""); err != nil {
			errs = append(errs, fmt.Errorf("pull_requests.backport.base_branches: bad pattern %q", g))
		}
	}
	_, to, err := c.Window.Range(time.Now())
	if err != nil {
		errs = append(errs, err)
		to = time.Now()
	}
	errs = append(errs, validatePeriods(c.Periods, to)...)
	return errors.Join(errs...)
}

func (s Size) validate() []error {
	var errs []error
	if len(s.Buckets) == 0 {
		errs = append(errs, errors.New("size.buckets must not be empty"))
	}
	prev := -1.0
	for i, b := range s.Buckets {
		last := i == len(s.Buckets)-1
		switch {
		case b.Weight <= 0:
			errs = append(errs, fmt.Errorf("size.buckets[%d]: weight must be positive", i))
		case last && b.Max != nil:
			errs = append(errs, fmt.Errorf("size.buckets[%d]: the last bucket must not have a max", i))
		case !last && b.Max == nil:
			errs = append(errs, fmt.Errorf("size.buckets[%d]: only the last bucket may omit max", i))
		case !last && *b.Max <= prev:
			errs = append(errs, fmt.Errorf("size.buckets[%d]: max must be larger than the previous bucket's", i))
		}
		if b.Max != nil {
			prev = *b.Max
		}
	}
	if s.DeletionsWeight < 0 || s.TestWeight < 0 || s.ReviewFeedback < 0 {
		errs = append(errs, errors.New("size weights and multipliers must not be negative"))
	}
	if _, err := glob.CompileAll(s.Exclude); err != nil {
		errs = append(errs, fmt.Errorf("size.exclude: %w", err))
	}
	if _, err := glob.CompileAll(s.ExcludeExtra); err != nil {
		errs = append(errs, fmt.Errorf("size.exclude_extra: %w", err))
	}
	if _, err := glob.CompileAll(s.TestPatterns); err != nil {
		errs = append(errs, fmt.Errorf("size.test_patterns: %w", err))
	}
	return errs
}

func (c Components) validate() []error {
	var errs []error
	seen := map[string]bool{}
	for i, m := range c.Map {
		name := strings.TrimSpace(m.Name)
		switch {
		case name == "":
			errs = append(errs, fmt.Errorf("components.map[%d]: name must not be empty", i))
		case seen[name]:
			errs = append(errs, fmt.Errorf("components.map[%d]: duplicate name %q", i, name))
		}
		seen[name] = true
		if len(m.Paths) == 0 {
			errs = append(errs, fmt.Errorf("components.map[%d]: paths must not be empty", i))
		}
		if _, err := glob.CompileAll(m.Paths); err != nil {
			errs = append(errs, fmt.Errorf("components.map[%d]: %w", i, err))
		}
	}
	return errs
}

// Range resolves the window to inclusive start and end dates, at midnight UTC.
func (w Window) Range(now time.Time) (from, to time.Time, err error) {
	to = now.UTC().Truncate(24 * time.Hour)
	if w.To != "" && w.To != Now {
		if to, err = time.Parse(DateLayout, w.To); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("window.to %q: want YYYY-MM-DD or %s", w.To, Now)
		}
	}
	switch {
	case w.From != "":
		if from, err = time.Parse(DateLayout, w.From); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("window.from %q: want YYYY-MM-DD", w.From)
		}
	case w.Days > 0:
		from = to.AddDate(0, 0, -(w.Days - 1))
	default:
		return time.Time{}, time.Time{}, errors.New("window needs days, or from and to")
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("window.from %s is after window.to %s", from.Format(DateLayout), to.Format(DateLayout))
	}
	return from, to, nil
}
