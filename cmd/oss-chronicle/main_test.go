package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

func TestComputeWritesLedgerJSON(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("OSS_CHRONICLE_ACTION_PATH", "/home/runner/work/_actions/me/fork/v3")
	dir := t.TempDir()
	data := filepath.Join(dir, "prs.jsonl")
	line := `{"number":1,"title":"fix: x","state":"MERGED","createdAt":"2026-07-15T08:00:00Z","mergedAt":"2026-07-16T08:00:00Z","author":{"login":"alice"},"baseRefName":"main","headRefName":"fix","timelineItems":{"nodes":[{"__typename":"MergedEvent","createdAt":"2026-07-16T08:00:00Z","actor":{"login":"bob"}}]}}` + "\n"
	if err := os.WriteFile(data, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	csvDir := filepath.Join(dir, "tables")
	err := run([]string{"compute", "--data", data, "--config", filepath.Join(dir, "none.yaml"), "--from", "2026-07-10", "--to", "2026-07-20", "--csv", csvDir}, &out, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var res ledger.Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Repository != "o/r" || res.Totals.Landed != 1 || res.Totals.Merged != 1 {
		t.Errorf("ledger = %s %+v", res.Repository, res.Totals)
	}
	if res.Generator == nil || *res.Generator != (ledger.Generator{Repository: "me/fork", Ref: "v3"}) || len(res.Rules) == 0 {
		t.Errorf("generator = %+v, %d rules", res.Generator, len(res.Rules))
	}
	people, err := os.ReadFile(filepath.Join(csvDir, "people.csv"))
	if err != nil || !strings.Contains(string(people), "\n1,alice,1,1,") {
		t.Errorf("people.csv = %q, err %v", people, err)
	}
}

func TestComputeRequiresData(t *testing.T) {
	err := run([]string{"compute"}, &bytes.Buffer{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "--data") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownCommandShowsUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}} {
		err := run(args, &bytes.Buffer{}, time.Now())
		if err == nil || !strings.Contains(err.Error(), "Usage") {
			t.Fatalf("run(%v) err = %v", args, err)
		}
	}
}

const fakePage = `{"data":{"rateLimit":{"cost":1,"remaining":4999},"repository":{"defaultBranchRef":{"name":"trunk"},"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[
{"number":1,"title":"fix: x","state":"MERGED","createdAt":"2026-07-15T08:00:00Z","updatedAt":"2026-07-16T08:00:00Z","mergedAt":"2026-07-16T08:00:00Z","baseRefName":"trunk","headRefName":"fix",
"author":{"login":"alice","__typename":"User"},
"files":{"pageInfo":{"hasNextPage":false},"nodes":[{"path":"a.go","additions":3,"deletions":1}]},
"commits":{"pageInfo":{"hasNextPage":false},"nodes":[]},
"reviews":{"pageInfo":{"hasNextPage":false},"nodes":[{"author":{"login":"carol","__typename":"User"},"state":"APPROVED","submittedAt":"2026-07-16T07:00:00Z","body":"","comments":{"totalCount":0}}]},
"timelineItems":{"pageInfo":{"hasNextPage":false},"nodes":[{"__typename":"MergedEvent","createdAt":"2026-07-16T08:00:00Z","actor":{"login":"bob","__typename":"User"}}]}}]}}}}`

func TestRunCollectsComputesAndWritesActionFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "gitattributes:") {
			io.WriteString(w, `{"data":{"repository":{"defaultBranchRef":{"name":"trunk"},"gitattributes":null}}}`)
			return
		}
		io.WriteString(w, fakePage)
	}))
	defer srv.Close()
	dir := t.TempDir()
	stepSummary := filepath.Join(dir, "step-summary.md")
	outputs := filepath.Join(dir, "outputs")
	t.Setenv("GITHUB_GRAPHQL_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_STEP_SUMMARY", stepSummary)
	t.Setenv("GITHUB_OUTPUT", outputs)
	t.Setenv("GITHUB_SERVER_URL", "https://ghe.example.com")

	outDir := filepath.Join(dir, "out")
	var stdout bytes.Buffer
	err := run([]string{"run", "--config", filepath.Join(dir, "none.yaml"), "--from", "2026-07-10", "--to", "2026-07-20", "--out-dir", outDir}, &stdout, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(outDir, "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	var res ledger.Result
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	want := ledger.Totals{Landed: 1, Reviewed: 1, Merged: 1, AuthoringWeight: 0.5, ReviewingWeight: 0.5}
	if res.Repository != "o/r" || res.DefaultBranch != "trunk" || res.Totals != want {
		t.Errorf("ledger = %s %s %+v", res.Repository, res.DefaultBranch, res.Totals)
	}
	prs, err := store.ReadFile(filepath.Join(outDir, "prs.jsonl.gz"))
	if err != nil || len(prs) != 1 {
		t.Errorf("collected data = %d PRs, err %v", len(prs), err)
	}
	meta, err := store.ReadMeta(filepath.Join(outDir, "meta.json"))
	if err != nil || meta.DefaultBranch != "trunk" || meta.Repository != "o/r" {
		t.Errorf("meta = %+v, err %v", meta, err)
	}
	summary, _ := os.ReadFile(stepSummary)
	if !strings.Contains(string(summary), "[alice](https://ghe.example.com/alice)") {
		t.Errorf("step summary:\n%s", summary)
	}
	out, _ := os.ReadFile(outputs)
	if !strings.Contains(string(out), "ledger="+filepath.Join(outDir, "ledger.json")) {
		t.Errorf("outputs = %q", out)
	}
	if !strings.Contains(stdout.String(), "1 landed") {
		t.Errorf("stdout = %q", stdout.String())
	}
	html, err := os.ReadFile(filepath.Join(outDir, "site", "index.html"))
	if err != nil || !strings.Contains(string(html), `"https://ghe.example.com"`) {
		t.Errorf("site page: %v", err)
	}
	for _, k := range []string{"site", "csv"} {
		if !strings.Contains(string(out), k+"="+filepath.Join(outDir, k)) {
			t.Errorf("outputs missing %s: %q", k, out)
		}
	}
	people, err := os.ReadFile(filepath.Join(outDir, "csv", "people.csv"))
	if err != nil || !strings.Contains(string(people), "\n1,alice,") {
		t.Errorf("people.csv = %q, err %v", people, err)
	}

	if meta.CollectedFrom != "2025-01-01" {
		t.Errorf("collected from %q, want 2025-01-01: last year before the window's end", meta.CollectedFrom)
	}
	var periods []ledger.PeriodLedger
	pb, err := os.ReadFile(filepath.Join(outDir, "periods.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(pb, &periods); err != nil {
		t.Fatalf("periods.json: %v", err)
	}
	var ids []string
	for _, p := range periods {
		ids = append(ids, p.ID)
		if p.Ledger == nil {
			t.Errorf("%s has no ledger in periods.json", p.ID)
		}
	}
	if strings.Join(ids, ",") != "window,7d,30d,90d,180d,365d,last-month,last-quarter,last-year" || !periods[0].Default {
		t.Errorf("periods = %v, default first %v", ids, periods[0].Default)
	}
	if !strings.Contains(string(out), "periods="+filepath.Join(outDir, "periods.json")) {
		t.Errorf("outputs missing periods: %q", out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "csv", "periods", "last-year", "people.csv")); err != nil {
		t.Errorf("per-period CSV: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "csv", "periods", "window")); err == nil {
		t.Error("the default period's CSV is written twice")
	}

	site := filepath.Join(dir, "rebuilt")
	stdout.Reset()
	if err := run([]string{"page", "--ledger", filepath.Join(outDir, "ledger.json"), "--periods", filepath.Join(outDir, "periods.json"), "--out", site}, &stdout, time.Now()); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(filepath.Join(site, "index.html"))
	if err != nil || !strings.Contains(string(rebuilt), "o/r contribution ledger") || !strings.Contains(string(rebuilt), `"id":"last-quarter"`) {
		t.Errorf("page command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(site, "periods.json")); err != nil {
		t.Errorf("rebuilt site periods.json: %v", err)
	}
}

func TestCoveredSkipsPeriodsTheDataDoesNotReach(t *testing.T) {
	to := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	plan, err := config.ResolvePeriods(config.PeriodIDs("7d", "90d", "last-year"), to)
	if err != nil {
		t.Fatal(err)
	}
	collected := time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC)
	var log bytes.Buffer
	kept, def := covered(plan, 1, store.Meta{CollectedFrom: "2026-07-13", CollectedAt: collected}, &log)
	if len(kept) != 2 || kept[def].ID != "90d" || kept[1].ID != "90d" {
		t.Errorf("kept %v, default %d", kept, def)
	}
	if !strings.Contains(log.String(), "skipping last-year") || !strings.Contains(log.String(), "default period (2026-07-12 to 2026-10-09) is incomplete") {
		t.Errorf("log = %q", log.String())
	}
	log.Reset()
	if kept, _ := covered(plan, 1, store.Meta{}, &log); len(kept) != 3 || !strings.Contains(log.String(), "does not say") {
		t.Errorf("unknown coverage: kept %d, log %q", len(kept), log.String())
	}
	log.Reset()
	if kept, _ := covered(plan, 1, store.Meta{CollectedFrom: "2025-01-01", CollectedAt: collected}, &log); len(kept) != 3 || log.Len() != 0 {
		t.Errorf("full coverage: kept %d, log %q", len(kept), log.String())
	}
	log.Reset()
	early := store.Meta{CollectedFrom: "2025-01-01", CollectedAt: collected.AddDate(0, 0, -3)}
	if kept, _ := covered(plan, 1, early, &log); len(kept) != 2 || !strings.Contains(log.String(), "skipping 7d") || !strings.Contains(log.String(), "collected on 2026-10-06") {
		t.Errorf("collected before the periods end: kept %d, log %q", len(kept), log.String())
	}
}

func TestRunNeedsTokenAndRepository(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_REPOSITORY", "")
	cfg := filepath.Join(dir, "none.yaml")
	err := run([]string{"run", "--config", cfg}, &bytes.Buffer{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no repository") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("GITHUB_REPOSITORY", "not a repo")
	err = run([]string{"run", "--config", cfg}, &bytes.Buffer{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "owner/name") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	err = run([]string{"run", "--config", cfg}, &bytes.Buffer{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no GitHub token") {
		t.Errorf("err = %v", err)
	}
}

func TestOnlyTheCurrentRepositoryIsCounted(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(cfg, []byte("repository: other/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"run", "--config", cfg}, &bytes.Buffer{}, time.Now()); err == nil || !strings.Contains(err.Error(), "field repository not found") {
		t.Errorf("repository in the config: err = %v", err)
	}
	if err := run([]string{"run", "--repo", "other/repo"}, &bytes.Buffer{}, time.Now()); err == nil || !strings.Contains(err.Error(), "-repo") {
		t.Errorf("--repo: err = %v", err)
	}
}

func TestComputeUsesMetaForDefaultBranchAndGitAttributes(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "someone/else")
	dir := t.TempDir()
	data := filepath.Join(dir, "prs.jsonl")
	line := `{"number":1,"title":"feat: x","state":"MERGED","createdAt":"2026-07-15T08:00:00Z","mergedAt":"2026-07-16T08:00:00Z","author":{"login":"alice"},"baseRefName":"develop","headRefName":"x",` +
		`"files":{"nodes":[{"path":"api/gen/client.go","additions":5000,"deletions":0},{"path":"api/handler.go","additions":20,"deletions":5}]},` +
		`"timelineItems":{"nodes":[{"__typename":"MergedEvent","createdAt":"2026-07-16T08:00:00Z","actor":{"login":"bob"}}]}}` + "\n"
	other := strings.Replace(strings.Replace(line, `"number":1`, `"number":2`, 1), `"develop"`, `"release-1.0"`, 1)
	if err := os.WriteFile(data, []byte(line+other+other), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "meta.json")
	if err := store.WriteMeta(meta, store.Meta{Repository: "o/r", DefaultBranch: "develop", GitAttributes: "api/gen/** linguist-generated\n"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--meta", meta}, nil} {
		var out bytes.Buffer
		args = append([]string{"compute", "--data", data, "--config", filepath.Join(dir, "none.yaml"), "--from", "2026-07-10", "--to", "2026-07-20"}, args...)
		if err := run(args, &out, time.Now()); err != nil {
			t.Fatal(err)
		}
		var res ledger.Result
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		checkMetaApplied(t, res)
	}
}

func checkMetaApplied(t *testing.T, res ledger.Result) {
	t.Helper()
	if res.DefaultBranch != "develop" || res.Repository != "o/r" || res.Totals.Landed != 1 {
		t.Errorf("result = %s %s %+v", res.Repository, res.DefaultBranch, res.Totals)
	}
	if res.Size.ExcludedLines != 5000 || res.Size.CountedLines != 25 || res.Totals.AuthoringWeight != 1 {
		t.Errorf("size = %+v, authoring weight %v", res.Size, res.Totals.AuthoringWeight)
	}
}

func TestPageCommandErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	for name, args := range map[string][]string{
		"missing ledger": {"page", "--ledger", filepath.Join(dir, "absent.json"), "--out", dir},
		"invalid ledger": {"page", "--ledger", bad, "--out", dir},
	} {
		if err := run(args, io.Discard, time.Now()); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestComputeWritesRangePeriods(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "prs.jsonl")
	line := `{"number":1,"title":"fix: x","state":"MERGED","createdAt":"2026-03-15T08:00:00Z","updatedAt":"2026-03-16T08:00:00Z","mergedAt":"2026-03-16T08:00:00Z","author":{"login":"alice"},"baseRefName":"main","headRefName":"fix","timelineItems":{"nodes":[{"__typename":"MergedEvent","createdAt":"2026-03-16T08:00:00Z","actor":{"login":"bob"}}]}}` + "\n"
	meta := `{"repository":"o/r","default_branch":"main","collected_at":"2026-10-09T08:00:00Z","collected_from":"2025-01-01"}`
	cfg := "periods:\n  - 30d\n  - {id: v1.0, label: Release 1.0, from: 2026-03-01, to: 2026-03-31}\n  - {id: this-year, from: 2026-01-01, to: now}\n"
	for name, body := range map[string]string{"prs.jsonl": line, "meta.json": meta, "c.yaml": cfg} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "periods.json")
	err := run([]string{"compute", "--data", data, "--config", filepath.Join(dir, "c.yaml"), "--to", "now", "--periods-out", out, "--csv", filepath.Join(dir, "csv")}, &bytes.Buffer{}, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var periods []ledger.PeriodLedger
	if err := json.Unmarshal(b, &periods); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range periods {
		got[p.ID] = fmt.Sprintf("%s %s..%s landed %d", p.Label, p.From, p.To, p.Ledger.Totals.Landed)
	}
	want := map[string]string{
		"90d":       "Last 90 days 2026-07-12..2026-10-09 landed 0",
		"30d":       "Last 30 days 2026-09-10..2026-10-09 landed 0",
		"v1.0":      "Release 1.0 2026-03-01..2026-03-31 landed 1",
		"this-year": "Since 2026-01-01 2026-01-01..2026-10-09 landed 1",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("periods = %v\nwant %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "csv", "periods", "v1.0", "people.csv")); err != nil {
		t.Error(err)
	}
}
