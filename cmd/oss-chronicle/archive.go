package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/github"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

const monthLayout = "2006-01"

// archivePlan says whether the last-year period can come from an archive: its year, and
// the first day the rest must be collected from. ok is false when there is no last-year
// period, the year has not ended by now, or the other periods reach back to its start.
// The rest is collected from no later than January 1 after the year, so every pull
// request changed since the archive was collected is fetched again.
func archivePlan(plan []config.Period, now time.Time) (year int, fresh time.Time, ok bool) {
	i := -1
	for j, p := range plan {
		if p.ID == config.LastYear {
			i = j
		}
	}
	if i < 0 || !now.UTC().Truncate(24*time.Hour).After(plan[i].To) {
		return 0, time.Time{}, false
	}
	fresh = plan[i].To.AddDate(0, 0, 1)
	for j, p := range plan {
		if j != i && p.From.Before(fresh) {
			fresh = p.From
		}
	}
	if !fresh.After(plan[i].From) {
		return 0, time.Time{}, false
	}
	return plan[i].From.Year(), fresh, true
}

// archiveKey names the archive in the Actions cache. The month in it makes each month's
// first run collect everything again, which picks up what an update date does not show,
// such as renamed accounts.
func archiveKey(repo string, year int, now time.Time) string {
	return fmt.Sprintf("oss-chronicle-%s-%d-%s-%s", strings.ToLower(strings.ReplaceAll(repo, "/", "_")), year, github.Fingerprint(), now.UTC().Format(monthLayout))
}

// Archive states collectArchived reports.
const (
	archiveUsed    = "used"
	archiveWritten = "written"
)

// collectArchived collects like collect, keeping last year's pull requests in dir between
// runs when archivePlan allows it. It reports archiveUsed when it took them from dir,
// archiveWritten when it collected everything and stored them there, and "" otherwise.
func collectArchived(cfg *config.Config, plan []config.Period, dir string, now time.Time, log io.Writer) ([]store.PullRequest, store.Meta, string, error) {
	from := earliest(plan)
	year, fresh, ok := archivePlan(plan, now)
	if dir == "" || !ok {
		prs, meta, err := collect(cfg, from, log)
		return prs, meta, "", err
	}
	if err := resolveRepository(cfg); err != nil {
		return nil, store.Meta{}, "", err
	}
	a, archived, err := store.ReadArchive(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(log, "no archive for %d yet; collecting everything\n", year)
	case err != nil:
		fmt.Fprintf(log, "warning: ignoring the archive in %s: %v\n", dir, err)
	case !strings.EqualFold(a.Repository, cfg.Repository) || a.Year != year || a.Fingerprint != github.Fingerprint() || a.Month != now.UTC().Format(monthLayout):
		fmt.Fprintf(log, "the archive in %s is for %s %d (%s, %s); collecting everything\n", dir, a.Repository, a.Year, a.Fingerprint, a.Month)
	default:
		prs, meta, err := collect(cfg, fresh, log)
		if err != nil {
			return nil, meta, "", err
		}
		meta.CollectedFrom = from.Format(config.DateLayout)
		merged := mergeArchived(prs, archived)
		fmt.Fprintf(log, "added %d pull requests last updated in %d from the archive of %s\n",
			len(merged)-len(prs), year, a.CollectedAt.Format(config.DateLayout))
		return merged, meta, archiveUsed, nil
	}
	prs, meta, err := collect(cfg, from, log)
	if err != nil {
		return nil, meta, "", err
	}
	var keep []store.PullRequest
	for _, pr := range prs {
		if pr.UpdatedAt.UTC().Year() == year {
			keep = append(keep, pr)
		}
	}
	a = store.Archive{Repository: cfg.Repository, Year: year, Fingerprint: github.Fingerprint(), Month: now.UTC().Format(monthLayout), CollectedAt: meta.CollectedAt}
	if err := store.WriteArchive(dir, a, keep); err != nil {
		fmt.Fprintf(log, "warning: could not write the archive: %v\n", err)
		return prs, meta, "", nil
	}
	fmt.Fprintf(log, "archived %d pull requests last updated in %d\n", len(keep), year)
	return prs, meta, archiveWritten, nil
}

// mergeArchived adds the archived pull requests that fresh does not have, after the fresh ones.
func mergeArchived(fresh, archived []store.PullRequest) []store.PullRequest {
	seen := make(map[int]bool, len(fresh))
	for _, pr := range fresh {
		seen[pr.Number] = true
	}
	for _, pr := range archived {
		if !seen[pr.Number] {
			fresh = append(fresh, pr)
		}
	}
	return fresh
}

func runArchiveKey(args []string, stdout io.Writer, now time.Time) error {
	flags := flag.NewFlagSet("archive-key", flag.ContinueOnError)
	var c common
	c.register(flags)
	if err := flags.Parse(args); err != nil {
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
	year, _, ok := archivePlan(plan, now)
	if !ok {
		return nil
	}
	if err := resolveRepository(&cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, archiveKey(cfg.Repository, year, now))
	return err
}
