package report

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
)

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestWriteCSV(t *testing.T) {
	ttm := 30.5
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	res := ledger.Result{
		People: []ledger.Person{{Login: "alice", Total: 5, Landed: 2, Reviewed: 3, AuthoringWeight: 2.5, ReviewingWeight: 4}, {Login: "bob", Total: 1, Commented: 1}},
		Components: []ledger.ComponentStats{
			{Name: "UI", Landed: 2, Authors: 1, MedianTimeToMergeHours: &ttm, Reviews: 3, ReviewerCount: 2, TopReviewer: "bob", TopReviewerShare: 0.667, ReviewersForHalf: 1,
				Reviewers: []ledger.ComponentReviewer{{Login: "bob", Reviews: 2}, {Login: "alice", Reviews: 1}},
				LandedBy:  []ledger.ComponentAuthor{{Login: "alice", Landed: 2}, {Login: "dan", Landed: 3}},
				Waiting:   []ledger.WaitingPR{{Number: 9, Title: "=HYPERLINK(\"x\"), \"quoted\"", Author: "carol", CreatedAt: day(3)}}},
			{Name: "@org/docs", Waiting: []ledger.WaitingPR{{Number: 4, Title: "docs: fix, typo", CreatedAt: day(3)}, {Number: 2, Title: "-1 more", CreatedAt: day(5)}}},
		},
		Dropped: []ledger.Dropped{{Reason: ledger.ForcePushes, Label: "Force-pushes", Count: 7}, {Reason: ledger.MaintenanceMerges, Label: "Merges", Count: 1, Maintenance: true}},
	}
	dir := filepath.Join(t.TempDir(), "csv")
	if err := WriteCSV(dir, res); err != nil {
		t.Fatal(err)
	}
	tests := map[string][][]string{
		"people.csv": {
			{"rank", "login", "total", "landed", "reviewed", "reviewed_with_feedback", "commented", "merged", "triaged", "maintenance", "authoring_weight", "reviewing_weight"},
			{"1", "alice", "5", "2", "3", "0", "0", "0", "0", "0", "2.5", "4"},
			{"2", "bob", "1", "0", "0", "0", "1", "0", "0", "0", "0", "0"},
		},
		"components.csv": {
			{"component", "landed", "touched", "authors", "median_time_to_merge_hours", "opened", "median_first_response_hours", "no_response", "waiting", "reviews", "reviewers", "top_reviewer", "top_reviewer_share", "reviewers_for_half"},
			{"UI", "2", "0", "1", "30.5", "0", "", "0", "1", "3", "2", "bob", "0.667", "1"},
			{"'@org/docs", "0", "0", "0", "", "0", "", "0", "2", "0", "0", "", "0", "0"},
		},
		"person_component.csv": {
			{"component", "login", "landed", "reviews"},
			{"UI", "alice", "2", "1"},
			{"UI", "dan", "3", "0"},
			{"UI", "bob", "0", "2"},
		},
		"waiting.csv": {
			{"number", "title", "author", "created_at", "component"},
			{"4", "docs: fix, typo", "", "2026-09-03T00:00:00Z", "'@org/docs"},
			{"9", "'=HYPERLINK(\"x\"), \"quoted\"", "carol", "2026-09-03T00:00:00Z", "UI"},
			{"2", "'-1 more", "", "2026-09-05T00:00:00Z", "'@org/docs"},
		},
		"left_out.csv": {
			{"reason", "label", "count", "credited_as_maintenance"},
			{"force_pushes", "Force-pushes", "7", "false"},
			{"maintenance_merges", "Merges", "1", "true"},
		},
	}
	for name, want := range tests {
		if got := readCSV(t, filepath.Join(dir, name)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	if len(tests) != len(CSVFiles) {
		t.Errorf("test covers %d files, WriteCSV writes %d", len(tests), len(CSVFiles))
	}
}

func TestWriteCSVEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	if err := WriteCSV(dir, ledger.Result{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range CSVFiles {
		if rows := readCSV(t, filepath.Join(dir, name)); len(rows) != 1 {
			t.Errorf("%s: %d rows, want the header only", name, len(rows))
		}
	}
}

func TestFormulaGuard(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx", "\rx": "'\rx", "\n=1": "'\n=1",
		"＝1+1": "'＝1+1", "＋1": "'＋1", "－1": "'－1", "＠SUM(A1)": "'＠SUM(A1)",
		"fix: a-b": "fix: a-b", "": "", "für": "für",
	} {
		if got := text(in); got != want {
			t.Errorf("text(%q) = %q, want %q", in, got, want)
		}
	}
}
