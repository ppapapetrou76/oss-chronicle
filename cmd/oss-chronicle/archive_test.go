package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/github"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

func TestArchivePlan(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(config.DateLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, tc := range []struct {
		name    string
		periods []string
		to, now string
		year    int
		fresh   string
	}{
		{name: "october", periods: config.DefaultPeriods, to: "2026-10-09", now: "2026-10-09", year: 2025, fresh: "2025-10-10"},
		{name: "first day of the year saves one day", periods: config.DefaultPeriods, to: "2026-01-01", now: "2026-01-01", year: 2025, fresh: "2025-01-02"},
		{name: "last day of the year", periods: config.DefaultPeriods, to: "2026-12-31", now: "2026-12-31", year: 2025, fresh: "2026-01-01"},
		{name: "leap year keeps January 1 fresh", periods: config.DefaultPeriods, to: "2028-12-31", now: "2028-12-31", year: 2027, fresh: "2028-01-01"},
		{name: "only last year", periods: []string{config.LastYear}, to: "2026-10-09", now: "2026-10-09", year: 2025, fresh: "2026-01-01"},
		{name: "no last year", periods: []string{"30d", "365d"}, to: "2026-10-09", now: "2026-10-09"},
		{name: "another period covers last year", periods: []string{"730d", config.LastYear}, to: "2026-10-09", now: "2026-10-09"},
		{name: "year not over", periods: config.DefaultPeriods, to: "2027-03-01", now: "2026-10-09"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := config.ResolvePeriods(config.PeriodIDs(tc.periods...), day(tc.to))
			if err != nil {
				t.Fatal(err)
			}
			year, fresh, ok := archivePlan(plan, day(tc.now).Add(15*time.Hour))
			if ok != (tc.year != 0) || year != tc.year {
				t.Fatalf("archivePlan = %d, %s, %t; want year %d", year, fresh.Format(config.DateLayout), ok, tc.year)
			}
			if ok && !fresh.Equal(day(tc.fresh)) {
				t.Errorf("fresh = %s, want %s", fresh.Format(config.DateLayout), tc.fresh)
			}
		})
	}
}

func TestMergeArchivedPrefersFreshCopies(t *testing.T) {
	fresh := []store.PullRequest{{Number: 3, Title: "new"}, {Number: 1}}
	archived := []store.PullRequest{{Number: 3, Title: "old"}, {Number: 2}}
	var got []string
	for _, pr := range mergeArchived(fresh, archived) {
		got = append(got, fmt.Sprintf("%d%s", pr.Number, pr.Title))
	}
	if strings.Join(got, ",") != "3new,1,2" {
		t.Errorf("merged = %v", got)
	}
}

func fakeNode(number int, updated, title string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"state":"MERGED","createdAt":%q,"updatedAt":%q,"mergedAt":%q,"baseRefName":"trunk","headRefName":"b%d",
"author":{"login":"alice","__typename":"User"},
"files":{"pageInfo":{"hasNextPage":false},"nodes":[]},
"commits":{"pageInfo":{"hasNextPage":false},"nodes":[]},
"reviews":{"pageInfo":{"hasNextPage":false},"nodes":[]},
"timelineItems":{"pageInfo":{"hasNextPage":false},"nodes":[{"__typename":"MergedEvent","createdAt":%q,"actor":{"login":"bob","__typename":"User"}}]}}`,
		number, title, updated, updated, updated, number, updated)
}

func TestRunKeepsLastYearInAnArchive(t *testing.T) {
	var mu sync.Mutex
	var nodes []string
	serve := func(n ...string) {
		mu.Lock()
		defer mu.Unlock()
		nodes = n
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "gitattributes:") {
			io.WriteString(w, `{"data":{"repository":{"defaultBranchRef":{"name":"trunk"}}}}`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, `{"data":{"rateLimit":{"cost":1,"remaining":4999},"repository":{"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[%s]}}}}`, strings.Join(nodes, ","))
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("GITHUB_GRAPHQL_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	archive := filepath.Join(dir, "archive")

	runAt := func(name string, now time.Time) (string, []store.PullRequest, store.Meta) {
		t.Helper()
		outputs := filepath.Join(dir, name+".outputs")
		t.Setenv("GITHUB_OUTPUT", outputs)
		out := filepath.Join(dir, name)
		args := []string{"run", "--config", filepath.Join(dir, "none.yaml"), "--out-dir", out, "--archive", archive}
		if err := run(args, &bytes.Buffer{}, now); err != nil {
			t.Fatal(err)
		}
		prs, err := store.ReadFile(filepath.Join(out, "prs.jsonl.gz"))
		if err != nil {
			t.Fatal(err)
		}
		meta, err := store.ReadMeta(filepath.Join(out, "meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(outputs)
		return string(b), prs, meta
	}
	numbers := func(prs []store.PullRequest) string {
		var s []string
		for _, pr := range prs {
			s = append(s, fmt.Sprintf("%d:%s", pr.Number, pr.Title))
		}
		return strings.Join(s, ",")
	}
	october := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	serve(fakeNode(3, "2026-09-01T08:00:00Z", "recent"), fakeNode(2, "2025-11-01T08:00:00Z", "late 2025"), fakeNode(1, "2025-03-01T08:00:00Z", "early 2025"))
	outputs, prs, _ := runAt("first", october)
	if !strings.Contains(outputs, "archive=written\n") || !strings.Contains(outputs, "archive-key=oss-chronicle-o_r-2025-") || numbers(prs) != "3:recent,2:late 2025,1:early 2025" {
		t.Fatalf("first run: outputs %q, prs %s", outputs, numbers(prs))
	}
	a, archived, err := store.ReadArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	if a.Repository != "o/r" || a.Year != 2025 || a.Month != "2026-10" || a.Fingerprint != github.Fingerprint() || numbers(archived) != "2:late 2025,1:early 2025" {
		t.Fatalf("archive = %+v with %s", a, numbers(archived))
	}

	serve(fakeNode(3, "2026-10-09T08:00:00Z", "edited"), fakeNode(2, "2025-11-01T08:00:00Z", "late 2025"), fakeNode(1, "2025-03-01T08:00:00Z", "not fetched"))
	outputs, prs, meta := runAt("second", october.Add(24*time.Hour))
	if !strings.Contains(outputs, "archive=used\n") || strings.Contains(outputs, "archive-key=") || numbers(prs) != "3:edited,2:late 2025,1:early 2025" {
		t.Errorf("second run: outputs %q, prs %s", outputs, numbers(prs))
	}
	if meta.CollectedFrom != "2025-01-01" {
		t.Errorf("collected_from = %q", meta.CollectedFrom)
	}

	var key bytes.Buffer
	if err := run([]string{"archive-key", "--config", filepath.Join(dir, "none.yaml")}, &key, october); err != nil {
		t.Fatal(err)
	}
	if want := "oss-chronicle-o_r-2025-" + github.Fingerprint() + "-2026-10\n"; key.String() != want {
		t.Errorf("archive-key = %q, want %q", key.String(), want)
	}

	outputs, prs, _ = runAt("november", time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC))
	if a, _, _ := store.ReadArchive(archive); !strings.Contains(outputs, "archive=written\n") || a.Month != "2026-11" || numbers(prs) != "3:edited,2:late 2025,1:not fetched" {
		t.Errorf("november run: outputs %q, archive month %s, prs %s", outputs, a.Month, numbers(prs))
	}
}

func TestArchiveKeyIsEmptyWithoutLastYear(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(cfg, []byte("periods: [30d, 365d]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"archive-key", "--config", cfg}, &out, time.Now()); err != nil || out.Len() != 0 {
		t.Errorf("archive-key = %q, err %v", out.String(), err)
	}
}

func TestPeriodsFlagReplacesTheConfigList(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(cfg, []byte("periods: [last-year, {id: old, from: 2020-01-01}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, periods := range []string{"30d,365d", "none"} {
		var out bytes.Buffer
		if err := run([]string{"archive-key", "--config", cfg, "--periods", periods}, &out, time.Now()); err != nil || out.Len() != 0 {
			t.Errorf("--periods %s: archive-key = %q, err %v", periods, out.String(), err)
		}
	}
	var key bytes.Buffer
	if err := run([]string{"archive-key", "--config", cfg, "--periods", "90d,last-year"}, &key, time.Now()); err != nil || key.Len() == 0 {
		t.Errorf("--periods without the config's 2020 range: archive-key = %q, err %v", key.String(), err)
	}
	for _, bad := range []string{"30d,last-week", " "} {
		if err := run([]string{"archive-key", "--config", cfg, "--periods", bad}, &bytes.Buffer{}, time.Now()); err == nil {
			t.Errorf("--periods %q: want an error", bad)
		}
	}
}
