package report

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
)

// CSVFiles are the files WriteCSV writes, in order.
var CSVFiles = []string{"people.csv", "components.csv", "person_component.csv", "waiting.csv", "left_out.csv"}

// WriteCSV writes the ledger as CSV tables into dir, creating it if needed:
//   - people.csv: one row per person, in ledger order.
//   - components.csv: one row per component.
//   - person_component.csv: PRs landed and reviewed per person and component, the
//     person × component grid in long form, most activity first.
//   - waiting.csv: open PRs waiting for a first response, oldest first.
//   - left_out.csv: activity not counted, by reason.
//
// Text that a spreadsheet would run as a formula is prefixed with a single quote.
func WriteCSV(dir string, res ledger.Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tables := map[string]func() [][]string{
		"people.csv":           func() [][]string { return peopleRows(res) },
		"components.csv":       func() [][]string { return componentRows(res) },
		"person_component.csv": func() [][]string { return gridRows(res) },
		"waiting.csv":          func() [][]string { return waitingRows(res) },
		"left_out.csv":         func() [][]string { return leftOutRows(res) },
	}
	for _, name := range CSVFiles {
		if err := writeTable(filepath.Join(dir, name), tables[name]()); err != nil {
			return err
		}
	}
	return nil
}

func writeTable(path string, rows [][]string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return writeRows(f, rows)
}

func writeRows(w io.Writer, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	return cw.Error()
}

func peopleRows(res ledger.Result) [][]string {
	rows := [][]string{{"rank", "login", "total", "landed", "reviewed", "reviewed_with_feedback", "commented", "merged", "triaged", "maintenance", "authoring_weight", "reviewing_weight"}}
	for i, p := range res.People {
		rows = append(rows, []string{itoa(i + 1), text(p.Login), itoa(p.Total), itoa(p.Landed), itoa(p.Reviewed), itoa(p.ReviewedWithFeedback),
			itoa(p.Commented), itoa(p.Merged), itoa(p.Triaged), itoa(p.Maintenance), dec(p.AuthoringWeight), dec(p.ReviewingWeight)})
	}
	return rows
}

func componentRows(res ledger.Result) [][]string {
	rows := [][]string{{"component", "landed", "touched", "authors", "median_time_to_merge_hours", "opened", "median_first_response_hours",
		"no_response", "waiting", "reviews", "reviewers", "top_reviewer", "top_reviewer_share", "reviewers_for_half"}}
	for _, c := range res.Components {
		rows = append(rows, []string{text(c.Name), itoa(c.Landed), itoa(c.Touched), itoa(c.Authors), optional(c.MedianTimeToMergeHours), itoa(c.Opened),
			optional(c.MedianFirstResponseHours), itoa(c.NoResponse), itoa(len(c.Waiting)), itoa(c.Reviews), itoa(c.ReviewerCount),
			text(c.TopReviewer), dec(c.TopReviewerShare), itoa(c.ReviewersForHalf)})
	}
	return rows
}

func gridRows(res ledger.Result) [][]string {
	rows := [][]string{{"component", "login", "landed", "reviews"}}
	for _, c := range res.Components {
		cells := map[string]*[2]int{}
		cell := func(login string) *[2]int {
			if cells[login] == nil {
				cells[login] = &[2]int{}
			}
			return cells[login]
		}
		for _, r := range c.Reviewers {
			cell(r.Login)[1] = r.Reviews
		}
		for _, a := range c.LandedBy {
			cell(a.Login)[0] = a.Landed
		}
		logins := make([]string, 0, len(cells))
		for login := range cells {
			logins = append(logins, login)
		}
		sort.Slice(logins, func(i, j int) bool {
			a, b := cells[logins[i]], cells[logins[j]]
			if a[0]+a[1] != b[0]+b[1] {
				return a[0]+a[1] > b[0]+b[1]
			}
			return logins[i] < logins[j]
		})
		for _, login := range logins {
			v := cells[login]
			rows = append(rows, []string{text(c.Name), text(login), itoa(v[0]), itoa(v[1])})
		}
	}
	return rows
}

func waitingRows(res ledger.Result) [][]string {
	rows := [][]string{{"number", "title", "author", "created_at", "component"}}
	for _, w := range allWaiting(res) {
		rows = append(rows, []string{itoa(w.Number), text(w.Title), text(w.Author), w.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), text(w.Component)})
	}
	return rows
}

func leftOutRows(res ledger.Result) [][]string {
	rows := [][]string{{"reason", "label", "count", "credited_as_maintenance"}}
	for _, d := range res.Dropped {
		rows = append(rows, []string{string(d.Reason), text(d.Label), itoa(d.Count), strconv.FormatBool(d.Maintenance)})
	}
	return rows
}

func itoa(n int) string { return strconv.Itoa(n) }

func optional(f *float64) string {
	if f == nil {
		return ""
	}
	return dec(*f)
}

// formulaStart are the first characters that make a spreadsheet treat a cell as a formula,
// including the full-width forms some locales accept (OWASP CSV injection guidance).
const formulaStart = "=+-@\t\r\n＝＋－＠"

// text guards a cell against formula injection by giving such text a leading quote.
func text(s string) string {
	if r, _ := utf8.DecodeRuneInString(s); s != "" && strings.ContainsRune(formulaStart, r) {
		return "'" + s
	}
	return s
}
