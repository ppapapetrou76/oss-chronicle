package config

import (
	"strings"
	"testing"
	"time"
)

func date(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestResolve(t *testing.T) {
	tests := []struct {
		id, to, from, until, label string
	}{
		{"7d", "2026-10-09", "2026-10-03", "2026-10-09", "Last 7 days"},
		{"1d", "2026-10-09", "2026-10-09", "2026-10-09", "Last day"},
		{"365d", "2026-10-09", "2025-10-10", "2026-10-09", "Last 365 days"},
		{LastMonth, "2026-10-09", "2026-09-01", "2026-09-30", "Last month (September 2026)"},
		{LastMonth, "2026-01-01", "2025-12-01", "2025-12-31", "Last month (December 2025)"},
		{LastMonth, "2028-03-31", "2028-02-01", "2028-02-29", "Last month (February 2028)"},
		{LastQuarter, "2026-10-09", "2026-07-01", "2026-09-30", "Last quarter (Q3 2026)"},
		{LastQuarter, "2026-09-30", "2026-04-01", "2026-06-30", "Last quarter (Q2 2026)"},
		{LastQuarter, "2026-01-01", "2025-10-01", "2025-12-31", "Last quarter (Q4 2025)"},
		{LastQuarter, "2026-03-31", "2025-10-01", "2025-12-31", "Last quarter (Q4 2025)"},
		{LastQuarter, "2026-04-01", "2026-01-01", "2026-03-31", "Last quarter (Q1 2026)"},
		{LastYear, "2026-10-09", "2025-01-01", "2025-12-31", "Last year (2025)"},
		{LastYear, "2026-01-01", "2025-01-01", "2025-12-31", "Last year (2025)"},
	}
	for _, tt := range tests {
		t.Run(tt.id+"@"+tt.to, func(t *testing.T) {
			p, err := Resolve(tt.id, date(tt.to).Add(15*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if got := p.From.Format(DateLayout) + ".." + p.To.Format(DateLayout); got != tt.from+".."+tt.until {
				t.Errorf("range = %s, want %s..%s", got, tt.from, tt.until)
			}
			if p.Label != tt.label || p.ID != tt.id {
				t.Errorf("id, label = %q, %q, want %q, %q", p.ID, p.Label, tt.id, tt.label)
			}
		})
	}
	for _, bad := range []string{"0d", "30", "d", "3661d", "last-week", "30D", " 30d"} {
		if _, err := Resolve(bad, date("2026-10-09")); err == nil {
			t.Errorf("Resolve(%q) succeeded, want an error", bad)
		}
	}
}

func TestPeriodsValidation(t *testing.T) {
	cfg := Default()
	cfg.Periods = PeriodIDs("30d", "30d", "last-week")
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "listed twice") || !strings.Contains(err.Error(), "last-week") {
		t.Fatalf("err = %v, want the duplicate and the unknown period", err)
	}
	cfg.Periods = []PeriodSpec{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("no periods: %v", err)
	}
}

func TestParseReplacesPeriods(t *testing.T) {
	cfg := Default()
	if err := Parse([]byte("periods: [30d, last-year]\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Periods) != 2 || cfg.Periods[0] != (PeriodSpec{ID: "30d"}) || cfg.Periods[1] != (PeriodSpec{ID: LastYear}) {
		t.Errorf("periods = %v", cfg.Periods)
	}
}

func TestPlan(t *testing.T) {
	now := date("2026-10-09").Add(8 * time.Hour)
	ids := func(ps []Period) string {
		var s []string
		for _, p := range ps {
			s = append(s, p.ID)
		}
		return strings.Join(s, ",")
	}
	tests := []struct {
		name    string
		cfg     func(*Config)
		ids     string
		def     string
		defFrom string
	}{
		{"default window is a listed period", func(*Config) {}, strings.Join(DefaultPeriods, ","), "90d", "2026-07-12"},
		{"days window not listed is added first", func(c *Config) { c.Window.Days = 60 }, "60d," + strings.Join(DefaultPeriods, ","), "60d", "2026-08-11"},
		{"explicit window matching a period", func(c *Config) {
			c.Window = Window{From: "2026-09-24", To: "2026-09-30"}
			c.Periods = PeriodIDs(LastMonth, "7d")
		}, "last-month,7d", "7d", "2026-09-24"},
		{"explicit window not listed", func(c *Config) {
			c.Window = Window{From: "2026-09-15", To: "2026-10-01"}
			c.Periods = PeriodIDs("7d")
		}, "window,7d", "window", "2026-09-15"},
		{"no periods", func(c *Config) { c.Periods = nil }, "90d", "90d", "2026-07-12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.cfg(&cfg)
			ps, def, err := cfg.Plan(now)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(ps); got != tt.ids {
				t.Errorf("periods = %s, want %s", got, tt.ids)
			}
			if ps[def].ID != tt.def || ps[def].From.Format(DateLayout) != tt.defFrom {
				t.Errorf("default = %s from %s, want %s from %s", ps[def].ID, ps[def].From.Format(DateLayout), tt.def, tt.defFrom)
			}
		})
	}
}

func TestParseRanges(t *testing.T) {
	cfg := Default()
	yml := `periods:
  - 90d
  - {id: v3.2, label: "Release 3.2", from: 2026-03-01, to: 2026-06-15}
  - id: this-year
    from: 2026-01-01
    to: now
  - id: since-march
    from: 2026-03-01
`
	if err := Parse([]byte(yml), &cfg); err != nil {
		t.Fatal(err)
	}
	ps, err := ResolvePeriods(cfg.Periods, date("2026-10-09"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.ID+"|"+p.Label+"|"+p.From.Format(DateLayout)+".."+p.To.Format(DateLayout))
	}
	want := []string{
		"90d|Last 90 days|2026-07-12..2026-10-09",
		"v3.2|Release 3.2|2026-03-01..2026-06-15",
		"this-year|Since 2026-01-01|2026-01-01..2026-10-09",
		"since-march|Since 2026-03-01|2026-03-01..2026-10-09",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("periods:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if p, _ := (PeriodSpec{ID: "q", From: "2026-01-01", To: "2026-03-31"}).Resolve(date("2026-10-09")); p.Label != "2026-01-01 to 2026-03-31" {
		t.Errorf("default label = %q", p.Label)
	}
}

func TestRangeErrors(t *testing.T) {
	to := date("2026-10-09")
	for _, s := range []PeriodSpec{
		{ID: "30d", From: "2026-01-01"},
		{ID: "window", From: "2026-01-01"},
		{ID: LastYear, From: "2026-01-01"},
		{ID: "../up", From: "2026-01-01"},
		{ID: "a b", From: "2026-01-01"},
		{ID: "", From: "2026-01-01"},
		{ID: ".hidden", From: "2026-01-01"},
		{ID: "V1", From: "2026-01-01"},
		{ID: "30D", From: "2026-01-01"},
		{ID: "2d", From: "2026-01-01"},
		{ID: "con", From: "2026-01-01"},
		{ID: "lpt1.x", From: "2026-01-01"},
		{ID: "v1.", From: "2026-01-01"},
		{ID: "v1-", From: "2026-01-01"},
		{ID: "r", From: "2026-1-1"},
		{ID: "r", From: "2026-01-01", To: "today"},
		{ID: "r", From: "2026-06-01", To: "2026-05-31"},
	} {
		if _, err := s.Resolve(to); err == nil {
			t.Errorf("Resolve(%+v) succeeded, want an error", s)
		}
	}
	for _, yml := range []string{
		"periods: [{id: r, from: 2026-01-01, until: now}]\n",
		"periods: [{id: r}]\n",
		"periods: [[30d]]\n",
		"periods: [{id: r, from: 2026-01-01}, {id: r, from: 2026-02-01}]\n",
	} {
		cfg := Default()
		if err := Parse([]byte(yml), &cfg); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", yml)
		}
	}
}

func TestWindowToNow(t *testing.T) {
	now := date("2026-10-09").Add(8 * time.Hour)
	for _, w := range []Window{{Days: 30, To: Now}, {From: "2026-01-01", To: Now}} {
		_, to, err := w.Range(now)
		if err != nil || !to.Equal(date("2026-10-09")) {
			t.Errorf("%+v: to = %s, err %v", w, to.Format(DateLayout), err)
		}
	}
}

func TestPlanUsesARangeMatchingTheWindow(t *testing.T) {
	cfg := Default()
	cfg.Window = Window{From: "2026-01-01", To: Now}
	cfg.Periods = []PeriodSpec{{ID: "90d"}, {ID: "this-year", Label: "This year", From: "2026-01-01", To: Now}}
	ps, def, err := cfg.Plan(date("2026-10-09").Add(8 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[def].ID != "this-year" {
		t.Errorf("plan = %+v, default %d", ps, def)
	}
}

func TestParsePeriodIDs(t *testing.T) {
	if got, err := ParsePeriodIDs("30d, 90d\nlast-year"); err != nil || len(got) != 3 || got[2].ID != LastYear {
		t.Errorf("ParsePeriodIDs = %+v, %v", got, err)
	}
	if got, err := ParsePeriodIDs("none"); err != nil || got == nil || len(got) != 0 {
		t.Errorf("none = %#v, %v, want an empty list", got, err)
	}
	for _, blank := range []string{" ", ",", "\n"} {
		if _, err := ParsePeriodIDs(blank); err == nil {
			t.Errorf("ParsePeriodIDs(%q) succeeded, want an error", blank)
		}
	}
}

func TestRangesInProgressAndNotStarted(t *testing.T) {
	specs := []PeriodSpec{
		{ID: "v3.3", Label: "Release 3.3", From: "2026-10-01", To: "2026-12-31"},
		{ID: "q4", From: "2026-10-01", To: "2026-12-31"},
		{ID: "v3.4", From: "2027-01-01", To: "2027-03-31"},
		{ID: "tomorrow", From: "2026-10-10", To: Now},
		{ID: "30d"},
	}
	ps, err := ResolvePeriods(specs, date("2026-10-09"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.ID+"|"+p.Label+"|"+p.To.Format(DateLayout))
	}
	want := "v3.3|Release 3.3 (so far)|2026-10-09,q4|2026-10-01 to 2026-12-31 (so far)|2026-10-09,30d|Last 30 days|2026-10-09"
	if strings.Join(got, ",") != want {
		t.Errorf("periods = %s\nwant %s", strings.Join(got, ","), want)
	}
	cfg := Default()
	cfg.Periods = specs
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidateResolvesRangesAgainstTheWindow(t *testing.T) {
	cfg := Default()
	cfg.Window = Window{From: "2099-01-01", To: "2099-12-31"}
	cfg.Periods = []PeriodSpec{{ID: "h2", From: "2099-07-01", To: "2099-06-30"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "after to") {
		t.Errorf("err = %v, want from after to", err)
	}
	cfg.Periods = []PeriodSpec{{ID: "h2", From: "2099-07-01", To: Now}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("a range inside a future window: %v", err)
	}
}

func TestRangesRejectMergeKeys(t *testing.T) {
	cfg := Default()
	yml := "window: &w {days: 30}\nperiods:\n  - {<<: *w, id: a, from: 2026-01-01}\n"
	if err := Parse([]byte(yml), &cfg); err == nil || !strings.Contains(err.Error(), "<<") {
		t.Errorf("err = %v, want the merge key rejected", err)
	}
}
