package page

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
	"github.com/ppapapetrou76/oss-chronicle/internal/report"
)

func TestRenderEmbedsLedgerSafely(t *testing.T) {
	hostile := `</script><script>alert(1)</script> & "quotes"`
	res := ledger.Result{
		Repository: "o/r", From: "2026-07-10", To: "2026-10-08",
		People:     []ledger.Person{{Login: "alice", Total: 3}},
		Components: []ledger.ComponentStats{{Name: "UI", Waiting: []ledger.WaitingPR{{Number: 7, Title: hostile}}}},
	}
	other := res
	periods := []ledger.PeriodLedger{
		{ID: "90d", Label: "Last 90 days", From: res.From, To: res.To, Default: true, Ledger: &res},
		{ID: "30d", Label: hostile, From: "2026-09-09", To: res.To, Ledger: &other},
	}
	var b strings.Builder
	if err := Render(&b, res, periods, "https://github.example/", time.Date(2026, 10, 9, 8, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if n := strings.Count(out, "<script"); n != 3 || strings.Contains(out, "alert(1)</script>") {
		t.Fatalf("a title escaped a data block: %d script tags, raw payload present: %v", n, strings.Contains(out, "alert(1)</script>"))
	}
	pm := regexp.MustCompile(`(?s)<script type="application/json" id="periods-data">(.*?)</script>`).FindStringSubmatch(out)
	if pm == nil {
		t.Fatal("no periods block")
	}
	var embedded []ledger.PeriodLedger
	if err := json.Unmarshal([]byte(pm[1]), &embedded); err != nil {
		t.Fatalf("periods block is not JSON: %v", err)
	}
	if len(embedded) != 2 || embedded[0].Ledger != nil || embedded[1].Label != hostile || embedded[1].Ledger == nil {
		t.Fatalf("periods = %+v, want the default without its ledger (it is in ledger-data) and the other with it", embedded)
	}
	if w := embedded[1].Ledger.Components[0].Waiting; w != nil || len(other.Components[0].Waiting) != 1 {
		t.Errorf("embedded waiting = %v, want it left out (the page takes it from the default) without changing the caller's ledger", w)
	}
	m := regexp.MustCompile(`(?s)<script type="application/json" id="ledger-data">(.*?)</script>`).FindStringSubmatch(out)
	if m == nil {
		t.Fatal("no data block")
	}
	var got ledger.Result
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatalf("data block is not JSON: %v\n%s", err, m[1])
	}
	if got.Components[0].Waiting[0].Title != hostile || got.People[0].Login != "alice" {
		t.Errorf("round trip = %+v", got)
	}
	for _, want := range []string{
		`<title>o/r contribution ledger</title>`,
		`const SERVER = "https://github.example";`,
		"2026-10-09 08:30 UTC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if regexp.MustCompile(`<(script|link)[^>]+(src|href)=`).MatchString(out) {
		t.Error("page loads an external resource")
	}
}

func TestWriteSite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "site")
	if err := WriteSite(dir, ledger.Result{Repository: "o/r"}, nil, "https://github.com", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "ledger.json"} {
		if fi, err := os.Stat(filepath.Join(dir, f)); err != nil || fi.Size() == 0 {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestServerURLMustBeHTTP(t *testing.T) {
	for in, want := range map[string]string{
		"https://ghe.example.com/":   "https://ghe.example.com",
		"http://localhost:8080":      "http://localhost:8080",
		"javascript:alert(1)//":      DefaultServer,
		`https://x"; alert(1); "`:    DefaultServer,
		"":                           DefaultServer,
		"https://ghe.example.com?q=": DefaultServer,
	} {
		if got := server(in); got != want {
			t.Errorf("server(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderEscapesHostileDataBlock(t *testing.T) {
	res := ledger.Result{Repository: "o/r<!--", Components: []ledger.ComponentStats{{Name: "a b</script>"}}}
	var b strings.Builder
	if err := Render(&b, res, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	block := out[strings.Index(out, `id="ledger-data">`):]
	block = block[:strings.Index(block, "</script>")]
	if strings.Contains(block, "<!--") || strings.Contains(block, " ") || strings.Contains(block, "</") {
		t.Errorf("data block not escaped: %q", block)
	}
}

func TestRenderEmptyLedger(t *testing.T) {
	var b strings.Builder
	if err := Render(&b, ledger.Result{}, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"people":null`, `"components":null`, `"dropped":null`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("empty ledger missing %s", want)
		}
	}
}

// TestPageScript checks the page's JavaScript parses and that its duration and percentage
// formatting match the Markdown summary. It needs node, which GitHub runners have.
func TestPageScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	var page strings.Builder
	if err := Render(&page, ledger.Result{Repository: "o/r"}, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)<script>(.*)</script>`).FindStringSubmatch(page.String())
	if m == nil {
		t.Fatal("no script in page")
	}
	script := filepath.Join(t.TempDir(), "page.js")
	if err := os.WriteFile(script, []byte(m[1]), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", script).CombinedOutput(); err != nil {
		t.Fatalf("page script does not parse: %v\n%s", err, out)
	}
	var defs []string
	for _, name := range []string{"fmt", "num", "pct", "dur"} {
		d := regexp.MustCompile(`(?m)^const ` + name + ` = .*$`).FindString(m[1])
		if d == "" {
			t.Fatalf("no %s in page script", name)
		}
		defs = append(defs, d)
	}
	probe := strings.Join(defs, "\n") + `
const out = [];
for (let i = 0; i <= 50000; i++) out.push(dur(i / 10));
out.push(dur(null), pct(0.528), pct(1), num(1234567), num(32.5));
console.log(JSON.stringify(out));`
	cmd := exec.Command(node, "-e", probe)
	got, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var js []string
	if err := json.Unmarshal(got, &js); err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := 0; i <= 50000; i++ {
		h := float64(i) / 10
		want = append(want, report.Duration(&h))
	}
	want = append(want, "–", "52.8%", "100%", "1,234,567", "32.5")
	mismatches := 0
	for i := range want {
		if js[i] != want[i] {
			if mismatches++; mismatches <= 5 {
				t.Errorf("value %d: page %q, summary %q", i, js[i], want[i])
			}
		}
	}
}

// TestPagePeriodSelection runs the page's period selection in node: the ?period= query picks
// the ledger, unknown or default ids fall back to the default, and the waiting lists come
// from the default ledger.
func TestPagePeriodSelection(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	def := ledger.Result{Repository: "o/r", Totals: ledger.Totals{Landed: 90},
		Components: []ledger.ComponentStats{{Name: "UI", Waiting: []ledger.WaitingPR{{Number: 7, Title: "w"}}}}}
	month := ledger.Result{Repository: "o/r", Totals: ledger.Totals{Landed: 30}, Components: []ledger.ComponentStats{{Name: "UI"}}}
	periods := []ledger.PeriodLedger{{ID: "90d", Default: true, Ledger: &def}, {ID: "30d", Ledger: &month}}
	var page strings.Builder
	if err := Render(&page, def, periods, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	block := func(id string) string {
		m := regexp.MustCompile(`(?s)<script type="application/json" id="` + id + `">(.*?)</script>`).FindStringSubmatch(page.String())
		if m == nil {
			t.Fatalf("no %s block", id)
		}
		return m[1]
	}
	m := regexp.MustCompile(`(?s)"use strict";\n(.*?)\nconst SERVER`).FindStringSubmatch(page.String())
	if m == nil {
		t.Fatal("no selection code in page script")
	}
	ledgerData, _ := json.Marshal(block("ledger-data"))
	periodsData, _ := json.Marshal(block("periods-data"))
	for query, want := range map[string]string{"": "90 1", "?period=30d": "30 1", "?period=90d": "90 1", "?period=bogus": "90 1"} {
		q, _ := json.Marshal(query)
		probe := `const data = {"ledger-data": ` + string(ledgerData) + `, "periods-data": ` + string(periodsData) + `};
const document = {getElementById: id => ({textContent: data[id]})};
const location = {search: ` + string(q) + `};
` + m[1] + `
console.log(L.totals.landed + " " + L.components[0].waiting.length);`
		out, err := exec.Command(node, "-e", probe).CombinedOutput()
		if err != nil {
			t.Fatalf("%q: %v\n%s", query, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("query %q: landed and waiting = %q, want %q", query, got, want)
		}
	}
}
