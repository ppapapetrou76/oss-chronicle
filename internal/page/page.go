// Package page renders a ledger as a self-contained static web page, ready to publish
// with GitHub Pages.
package page

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/ledger"
)

//go:embed index.html.tmpl
var source string

var tmpl = template.Must(template.New("page").Parse(source))

// DefaultServer is where links point when no valid GitHub web address is given.
const DefaultServer = "https://github.com"

// Render writes the page for res, the default ledger. periods, when there are any, let the
// reader switch to another period; the page then shows the one named in its ?period= query.
// serverURL is the GitHub web address links point to; any value that is not an http or https
// URL falls back to DefaultServer. The ledgers are embedded as JSON and drawn in the browser;
// the page loads nothing else.
func Render(w io.Writer, res ledger.Result, periods []ledger.PeriodLedger, serverURL string, generated time.Time) error {
	embedded := make([]ledger.PeriodLedger, len(periods))
	for i, p := range periods {
		switch {
		case p.Default:
			p.Ledger = nil
		case p.Ledger != nil:
			p.Ledger = withoutWaiting(*p.Ledger)
		}
		embedded[i] = p
	}
	return tmpl.Execute(w, struct {
		Ledger    ledger.Result
		Periods   []ledger.PeriodLedger
		Units     []ledger.Unit
		ServerURL string
		Generated string
	}{res, embedded, ledger.Units, server(serverURL), generated.UTC().Format("2006-01-02 15:04 UTC")})
}

// withoutWaiting copies res without its waiting lists, which are the same in every period,
// and without its rules and generator, which the page shows only from the default ledger.
func withoutWaiting(res ledger.Result) *ledger.Result {
	res.Rules, res.Generator = nil, nil
	comps := make([]ledger.ComponentStats, len(res.Components))
	for i, c := range res.Components {
		c.Waiting = nil
		comps[i] = c
	}
	res.Components = comps
	return &res
}

func server(s string) string {
	u, err := url.Parse(strings.TrimSuffix(s, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return DefaultServer
	}
	return u.String()
}

// WriteSite writes index.html and ledger.json into dir, creating it if needed, and
// periods.json when there are periods, removing one left from an earlier build when there
// are none.
func WriteSite(dir string, res ledger.Result, periods []ledger.PeriodLedger, serverURL string, generated time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := Render(&b, res, periods, serverURL, generated); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), b.Bytes(), 0o644); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "ledger.json"), res); err != nil {
		return err
	}
	if len(periods) == 0 {
		if err := os.Remove(filepath.Join(dir, "periods.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeJSON(filepath.Join(dir, "periods.json"), periods)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
