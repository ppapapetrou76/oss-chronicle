package report

import (
	"strings"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
)

func TestMarkdown(t *testing.T) {
	ten, fr, ttm := 10.0, 14.66, 78.4
	res := ledger.Result{
		Repository: "o/r", DefaultBranch: "main", From: "2026-07-10", To: "2026-10-08", PullRequests: 2170,
		Totals: ledger.Totals{Landed: 1234},
		People: []ledger.Person{{Login: "alice", Total: 5, Landed: 5, AuthoringWeight: 7.5, ReviewingWeight: 2}, {Login: "bob", Total: 1}},
		Size: ledger.SizeSummary{
			Buckets:      []ledger.BucketCount{{Bucket: config.Bucket{Name: "XS", Max: &ten, Weight: 0.5}, Landed: 3}, {Bucket: config.Bucket{Name: "L", Weight: 3}, Landed: 2}},
			CountedLines: 700, ChangedLines: 1000, ExcludedLines: 250, Unsized: 1, ReviewFeedbackMultiple: 2,
		},
		Components: []ledger.ComponentStats{
			{Name: "UI", Landed: 4, Authors: 3, Reviews: 10, ReviewerCount: 3, TopReviewer: "carol", TopReviewerShare: 0.6, ReviewersForHalf: 1,
				Reviewers:                []ledger.ComponentReviewer{{Login: "carol", Reviews: 6}, {Login: "dan", Reviews: 3}},
				MedianFirstResponseHours: &fr, MedianTimeToMergeHours: &ttm,
				Waiting: []ledger.WaitingPR{{Number: 7, Title: "feat: a\nnew | [view](https://x.example) @team #9 <b>", Author: "erin", CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}}},
			{Name: "@org/a|b", Reviews: 2, ReviewerCount: 1, TopReviewerShare: 1, ReviewersForHalf: 1,
				Waiting: []ledger.WaitingPR{{Number: 3, Title: "fix: old", CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}}},
		},
		Dropped: []ledger.Dropped{
			{Reason: ledger.ForcePushes, Label: "Force-pushes", Count: 799},
			{Reason: ledger.MaintenanceMerges, Label: "Merges of backport PRs", Count: 3, Maintenance: true},
		},
		Rules: []ledger.Rule{
			{Label: "Merge commands", Values: []string{"/merge", "@bors r+", "[x](https://evil.example)"}, Custom: true},
			{Label: "Bots: login is", Values: []string{}},
			{Label: "Reviewing weight multiplier for a review with feedback", Values: []string{"2"}},
		},
		Generator: &ledger.Generator{Repository: "me/fork", Ref: "v2"},
	}
	var b strings.Builder
	if err := Markdown(&b, res, 1, "https://github.com/"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, u := range ledger.Units {
		if want := "- **" + u.Label + "**: " + u.Text + "\n"; !strings.Contains(out, want) {
			t.Errorf("summary missing the %s definition", u.Label)
		}
	}
	for _, want := range []string{
		"## Contribution ledger: o/r",
		"2,170 pull requests",
		"| 1,234 |",
		"top 1 of 2",
		"| 1 | [alice](https://github.com/alice) | **5** |",
		"| 7.5 | 2 |",
		"| XS | 0–10 | 0.5 | 3 |",
		"| L | over 10 | 3 | 2 |",
		"25% of the lines changed",
		"<details><summary>How each number is computed</summary>",
		"- **Merge commands**: /merge, @\u2060bors r+, \\[x\\]\\(https://evil.example\\) *(set by this project)*\n",
		"- **Bots: login is**: none\n",
		"- **Reviewing weight multiplier for a review with feedback**: 2\n",
		"[How it counts](https://github.com/me/fork/blob/v2/docs/how-it-counts.md)",
		"1 landed PRs have no file data",
		"| Force-pushes | 799 |",
		"Merges of backport PRs (credited as maintenance)",
		"| UI | 4 | 3 | 3 | carol (60%) | 1 | 14.7 h | 3.3 d | 1 |",
		"| @\u2060org/a\\|b | 0 | 0 | 1 | 100% | 1 | – | – | 1 |",
		"oldest 2 of 2",
		"- [#3](https://github.com/o/r/pull/3) fix: old · opened 2026-08-01 · @\u2060org/a\\|b\n" +
			"- [#7](https://github.com/o/r/pull/7) feat: a new \\| \\[view\\]\\(https://x.example\\) @\u2060team #\u20609 &lt;b&gt; by erin · opened 2026-09-01 · UI",
		"no activity since 2026-07-10",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "bob") {
		t.Error("summary lists people beyond the top limit")
	}
}

func TestNum(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1500: "-1,500"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %q, want %q", n, got, want)
		}
	}
}
