// Command oss-chronicle counts the contributions behind an open source repository,
// leaving out activity that does not reflect new work and saying why.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/github"
	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
	"github.com/ppapapetrou76/oss-chronicle/internal/page"
	"github.com/ppapapetrou76/oss-chronicle/internal/report"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

const usage = `Usage: oss-chronicle COMMAND [flags]

Commands:
  run       Collect from GitHub, build the ledger and write the summary (what the GitHub Action runs).
  collect   Fetch pull request activity from GitHub into a JSON Lines file.
  compute   Build the ledger from a collected JSON Lines file.
  page      Build the static web page from a ledger.json.
  archive-key
            Print the Actions cache key for last year's archive, or nothing when no archive applies.

The GitHub token is read from GITHUB_TOKEN or GH_TOKEN. Run "oss-chronicle COMMAND -h" for flags.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "oss-chronicle:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, now time.Time) error {
	if len(args) == 0 {
		return errors.New("expected a command\n\n" + usage)
	}
	switch args[0] {
	case "run":
		return runAll(args[1:], stdout, now)
	case "collect":
		return runCollect(args[1:], stdout, now)
	case "compute":
		return runCompute(args[1:], stdout, now)
	case "page":
		return runPage(args[1:], stdout, now)
	case "archive-key":
		return runArchiveKey(args[1:], stdout, now)
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

type common struct {
	cfgPath, from, to, periods string
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.cfgPath, "config", ".github/oss-chronicle.yaml", "config file; defaults apply when it does not exist")
	fs.StringVar(&c.from, "from", "", "first day counted (YYYY-MM-DD), overrides the config window")
	fs.StringVar(&c.to, "to", "", "last day counted (YYYY-MM-DD, or now for today), overrides the config window")
	fs.StringVar(&c.periods, "periods", "", "periods to compute, such as 30d,90d,last-year, replacing the config's list; none for the window alone")
}

func (c *common) load(now time.Time) (config.Config, time.Time, time.Time, error) {
	cfg, err := config.Load(c.cfgPath)
	if err != nil {
		return cfg, time.Time{}, time.Time{}, err
	}
	if c.from != "" {
		cfg.Window.From = c.from
	}
	if c.to != "" {
		cfg.Window.To = c.to
	}
	if c.periods != "" {
		if cfg.Periods, err = config.ParsePeriodIDs(c.periods); err != nil {
			return cfg, time.Time{}, time.Time{}, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, time.Time{}, time.Time{}, err
	}
	from, to, err := cfg.Window.Range(now)
	return cfg, from, to, err
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// resolveRepository sets the repository to count: the one the workflow runs in.
func resolveRepository(cfg *config.Config) error {
	cfg.Repository = os.Getenv("GITHUB_REPOSITORY")
	if cfg.Repository == "" {
		return errors.New("no repository: run inside GitHub Actions, or set GITHUB_REPOSITORY=owner/name to run locally")
	}
	if !repoPattern.MatchString(cfg.Repository) {
		return fmt.Errorf("GITHUB_REPOSITORY %q must look like owner/name", cfg.Repository)
	}
	return nil
}

func collect(cfg *config.Config, from time.Time, log io.Writer) ([]store.PullRequest, store.Meta, error) {
	var meta store.Meta
	if err := resolveRepository(cfg); err != nil {
		return nil, meta, err
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return nil, meta, errors.New("no GitHub token: set GITHUB_TOKEN or GH_TOKEN")
	}
	c := github.NewClient(token)
	c.Log = log
	start := time.Now()
	col, err := c.Collect(context.Background(), cfg.Repository, from)
	if err != nil {
		return nil, meta, err
	}
	meta = store.Meta{Repository: cfg.Repository, DefaultBranch: col.DefaultBranch, GitAttributes: col.GitAttributes, Codeowners: col.Codeowners,
		CollectedAt: start.UTC(), CollectedFrom: from.Format(config.DateLayout)}
	applyMeta(cfg, meta)
	fmt.Fprintf(log, "collected %d pull requests from %s in %s (%d requests, %d rate-limit points)\n",
		len(col.PullRequests), cfg.Repository, time.Since(start).Round(time.Second), col.Requests, col.Cost)
	return col.PullRequests, meta, nil
}

// applyMeta fills what the repository says about itself into the config, without
// overriding a default branch the config sets.
func applyMeta(cfg *config.Config, meta store.Meta) {
	if cfg.Repository == "" {
		cfg.Repository = meta.Repository
	}
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = meta.DefaultBranch
	}
	cfg.Size.GitAttributes = meta.GitAttributes
	cfg.Components.Codeowners = meta.Codeowners
}

const archiveUsage = "directory that keeps last year's pull requests between runs, so only the rest is fetched from GitHub; refreshed monthly"

func runAll(args []string, stdout io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c common
	c.register(fs)
	outDir := fs.String("out-dir", "oss-chronicle", "directory for prs.jsonl.gz, meta.json, ledger.json, periods.json, summary.md, the csv/ tables and the site/ web page")
	top := fs.Int("top", 25, "people listed in the summary")
	archiveDir := fs.String("archive", "", archiveUsage)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, _, _, err := c.load(now)
	if err != nil {
		return err
	}
	plan, def, err := cfg.Plan(now)
	if err != nil {
		return err
	}
	prs, meta, archived, err := collectArchived(&cfg, plan, *archiveDir, now, os.Stderr)
	if err != nil {
		return err
	}
	periods := ledger.ComputePeriods(prs, cfg, plan, def)
	gen := generator()
	for _, p := range periods {
		p.Ledger.Generator = &gen
	}
	res := *periods[def].Ledger

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	paths := map[string]string{
		"data":    filepath.Join(*outDir, "prs.jsonl.gz"),
		"meta":    filepath.Join(*outDir, "meta.json"),
		"ledger":  filepath.Join(*outDir, "ledger.json"),
		"periods": filepath.Join(*outDir, "periods.json"),
		"summary": filepath.Join(*outDir, "summary.md"),
		"site":    filepath.Join(*outDir, "site"),
		"csv":     filepath.Join(*outDir, "csv"),
	}
	if err := store.WriteFile(paths["data"], prs); err != nil {
		return err
	}
	if err := store.WriteMeta(paths["meta"], meta); err != nil {
		return err
	}
	if err := writeFile(paths["ledger"], func(w io.Writer) error { return writeJSON(w, res) }); err != nil {
		return err
	}
	if err := writeFile(paths["periods"], func(w io.Writer) error { return writeJSON(w, periods) }); err != nil {
		return err
	}
	serverURL := githubServer()
	if err := page.WriteSite(paths["site"], res, periods, serverURL, now); err != nil {
		return err
	}
	if err := writeCSV(paths["csv"], res, periods); err != nil {
		return err
	}
	render := func(w io.Writer) error { return report.Markdown(w, res, *top, serverURL) }
	if err := writeFile(paths["summary"], render); err != nil {
		return err
	}
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if err := appendFile(p, render); err != nil {
			return err
		}
	}
	if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
		err := appendFile(p, func(w io.Writer) error {
			for _, k := range []string{"data", "meta", "ledger", "periods", "summary", "site", "csv"} {
				if _, err := fmt.Fprintf(w, "%s=%s\n", k, paths[k]); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, "archive=%s\n", archived); err != nil {
				return err
			}
			if archived == archiveWritten {
				year, _, _ := archivePlan(plan, now)
				_, err := fmt.Fprintf(w, "archive-key=%s\n", archiveKey(cfg.Repository, year, now))
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "%s %s..%s: %d landed, %d reviewed, %d comments, %d merges, %d triage closes, %d maintenance; wrote %s\n",
		res.Repository, res.From, res.To, res.Totals.Landed, res.Totals.Reviewed, res.Totals.Commented, res.Totals.Merged, res.Totals.Triaged, res.Totals.Maintenance, *outDir)
	return nil
}

func runCollect(args []string, stdout io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	var c common
	c.register(fs)
	out := fs.String("out", "prs.jsonl.gz", "output file (JSON Lines, gzipped when it ends in .gz)")
	metaOut := fs.String("meta", "meta.json", "where to write the repository details compute needs")
	archiveDir := fs.String("archive", "", archiveUsage)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, _, _, err := c.load(now)
	if err != nil {
		return err
	}
	plan, _, err := cfg.Plan(now)
	if err != nil {
		return err
	}
	prs, meta, _, err := collectArchived(&cfg, plan, *archiveDir, now, os.Stderr)
	if err != nil {
		return err
	}
	if err := store.WriteFile(*out, prs); err != nil {
		return err
	}
	if err := store.WriteMeta(*metaOut, meta); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %d pull requests to %s (default branch %s)\n", len(prs), *out, cfg.DefaultBranch)
	return nil
}

func runCompute(args []string, stdout io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("compute", flag.ContinueOnError)
	var c common
	c.register(fs)
	data := fs.String("data", "", "pull request data file (JSON Lines, .gz allowed)")
	out := fs.String("out", "", "write the ledger JSON here instead of standard output")
	metaPath := fs.String("meta", "", "meta.json written by collect or run, giving the default branch, .gitattributes and CODEOWNERS (default: meta.json next to --data)")
	csvDir := fs.String("csv", "", "also write the CSV tables into this directory")
	periodsOut := fs.String("periods-out", "", "also compute every configured period and write them here as JSON, like periods.json from run")
	branch := fs.String("default-branch", "", "default branch, overrides the config and meta; inferred from the data when none sets it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" {
		return errors.New("--data is required")
	}
	cfg, from, to, err := c.load(now)
	if err != nil {
		return err
	}
	if *branch != "" {
		cfg.DefaultBranch = *branch
	}
	if *metaPath == "" {
		if p := filepath.Join(filepath.Dir(*data), "meta.json"); fileExists(p) {
			*metaPath = p
		}
	}
	var meta store.Meta
	if *metaPath == "" {
		fmt.Fprintln(os.Stderr, "warning: no meta.json; the default branch is inferred and .gitattributes rules are not applied")
	} else {
		meta, err = store.ReadMeta(*metaPath)
		if err != nil {
			return err
		}
		applyMeta(&cfg, meta)
	}
	if cfg.Repository == "" {
		cfg.Repository = os.Getenv("GITHUB_REPOSITORY")
	}
	prs, err := store.ReadFile(*data)
	if err != nil {
		return err
	}
	gen := generator()
	res := ledger.Compute(prs, cfg, from, to)
	res.Generator = &gen
	var periods []ledger.PeriodLedger
	if *periodsOut != "" {
		plan, def, err := cfg.Plan(now)
		if err != nil {
			return err
		}
		plan, def = covered(plan, def, meta, os.Stderr)
		periods = ledger.ComputePeriods(prs, cfg, plan, def)
		for _, p := range periods {
			p.Ledger.Generator = &gen
		}
		if err := writeFile(*periodsOut, func(w io.Writer) error { return writeJSON(w, periods) }); err != nil {
			return err
		}
	}
	if *csvDir != "" {
		if err := writeCSV(*csvDir, res, periods); err != nil {
			return err
		}
	}
	if *out == "" {
		return writeJSON(stdout, res)
	}
	return writeFile(*out, func(w io.Writer) error { return writeJSON(w, res) })
}

func runPage(args []string, stdout io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("page", flag.ContinueOnError)
	ledgerPath := fs.String("ledger", "ledger.json", "ledger written by run or compute")
	out := fs.String("out", "site", "directory for index.html and ledger.json")
	periodsPath := fs.String("periods", "", "periods.json written by run or compute --periods-out, to let the page switch between periods")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var periods []ledger.PeriodLedger
	if *periodsPath != "" {
		b, err := os.ReadFile(*periodsPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &periods); err != nil {
			return fmt.Errorf("read %s: %w", *periodsPath, err)
		}
	}
	b, err := os.ReadFile(*ledgerPath)
	if err != nil {
		return err
	}
	var res ledger.Result
	if err := json.Unmarshal(b, &res); err != nil {
		return fmt.Errorf("read %s: %w", *ledgerPath, err)
	}
	if err := page.WriteSite(*out, res, periods, githubServer(), now); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s\n", filepath.Join(*out, "index.html"))
	return nil
}

// earliest is the first day any period covers, which is how far back collection must go.
func earliest(periods []config.Period) time.Time {
	first := periods[0].From
	for _, p := range periods[1:] {
		if p.From.Before(first) {
			first = p.From
		}
	}
	return first
}

// covered drops the periods the data does not fully cover, warning about each: those that
// start before meta.CollectedFrom or end after the day it was collected. The default period
// is always kept, with a warning when it is not covered. When meta.json does not record
// collected_from, coverage is unknown and every period is kept, with a warning.
func covered(periods []config.Period, def int, meta store.Meta, log io.Writer) ([]config.Period, int) {
	start, err := time.Parse(config.DateLayout, meta.CollectedFrom)
	if err != nil {
		fmt.Fprintln(log, "warning: meta.json does not say how far back the data goes; periods it does not cover will be incomplete")
		return periods, def
	}
	end := meta.CollectedAt.UTC().Truncate(24 * time.Hour)
	gap := func(p config.Period) string {
		switch {
		case p.From.Before(start):
			return "the data starts on " + start.Format(config.DateLayout)
		case !end.IsZero() && p.To.After(end):
			return "the data was collected on " + end.Format(config.DateLayout)
		}
		return ""
	}
	var kept []config.Period
	newDef := 0
	for i, p := range periods {
		g := gap(p)
		switch {
		case i == def:
			newDef = len(kept)
			if g != "" {
				fmt.Fprintf(log, "warning: the default period (%s to %s) is incomplete: %s\n", p.From.Format(config.DateLayout), p.To.Format(config.DateLayout), g)
			}
		case g != "":
			fmt.Fprintf(log, "warning: skipping %s (%s to %s): %s\n", p.ID, p.From.Format(config.DateLayout), p.To.Format(config.DateLayout), g)
			continue
		}
		kept = append(kept, p)
	}
	return kept, newDef
}

// writeCSV writes the default ledger's tables into dir and each other period's into
// dir/periods/ID.
func writeCSV(dir string, res ledger.Result, periods []ledger.PeriodLedger) error {
	if err := report.WriteCSV(dir, res); err != nil {
		return err
	}
	for _, p := range periods {
		if p.Default || p.Ledger == nil {
			continue
		}
		if err := report.WriteCSV(filepath.Join(dir, "periods", p.ID), *p.Ledger); err != nil {
			return err
		}
	}
	return nil
}

func generator() ledger.Generator {
	return ledger.GeneratorFromActionPath(os.Getenv("OSS_CHRONICLE_ACTION_PATH"))
}

// githubServer is the GitHub web address links point to: GITHUB_SERVER_URL on Actions
// runners, which also covers GitHub Enterprise Server, or github.com.
func githubServer() string {
	if s := os.Getenv("GITHUB_SERVER_URL"); s != "" {
		return s
	}
	return page.DefaultServer
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeFile(path string, write func(io.Writer) error) error {
	return openAndWrite(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, write)
}

func appendFile(path string, write func(io.Writer) error) error {
	return openAndWrite(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, write)
}

func openAndWrite(path string, flags int, write func(io.Writer) error) (err error) {
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return write(f)
}
