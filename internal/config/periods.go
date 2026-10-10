package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Period IDs for the calendar periods; rolling periods are written as a number of days, "30d".
const (
	LastMonth   = "last-month"
	LastQuarter = "last-quarter"
	LastYear    = "last-year"
)

// Now is the to date of a range, or of the window, that ends on the window's last day:
// today, unless the window's to is a date.
const Now = "now"

// DefaultPeriods are the periods a run computes when the config does not list any.
var DefaultPeriods = []string{"7d", "30d", "90d", "180d", "365d", LastMonth, LastQuarter, LastYear}

const maxPeriodDays = 3660

var (
	rollingPeriod = regexp.MustCompile(`^([1-9][0-9]*)d$`)
	rangeID       = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)
	deviceName    = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)
)

var errNotStarted = errors.New("starts after the window's last day")

// PeriodSpec is one entry of the periods list: a predefined period such as "30d" or
// "last-year", written as its ID alone, or a named date range when From is set. A range
// whose To is empty or Now ends on the window's last day; one still in progress ends there
// too, labelled "(so far)", and one that has not started is left out.
type PeriodSpec struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
	From  string `yaml:"from"`
	To    string `yaml:"to"`
}

// PeriodIDs turns predefined period IDs into specs.
func PeriodIDs(ids ...string) []PeriodSpec {
	specs := make([]PeriodSpec, len(ids))
	for i, id := range ids {
		specs[i] = PeriodSpec{ID: id}
	}
	return specs
}

// UnmarshalYAML reads a period ID, or a range written as a mapping with id, label, from and to.
func (s *PeriodSpec) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = PeriodSpec{ID: n.Value}
		return nil
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			switch k := n.Content[i].Value; k {
			case "id", "label", "from", "to":
			default:
				return fmt.Errorf("line %d: periods: unknown key %q in a range; want id, label, from and to", n.Content[i].Line, k)
			}
		}
		type plain PeriodSpec
		if err := n.Decode((*plain)(s)); err != nil {
			return err
		}
		if s.From == "" {
			return fmt.Errorf("line %d: periods: a range needs from", n.Line)
		}
		return nil
	}
	return fmt.Errorf("line %d: periods: want a period such as 30d, or a range with id, from and to", n.Line)
}

// Resolve turns the spec into dates, with to the window's last day.
func (s PeriodSpec) Resolve(to time.Time) (Period, error) {
	if s.From == "" {
		return Resolve(s.ID, to)
	}
	to = to.UTC().Truncate(24 * time.Hour)
	switch {
	case !rangeID.MatchString(s.ID) || deviceName.MatchString(s.ID):
		return Period{}, fmt.Errorf("range %q: the id must be up to 64 lowercase letters, digits, dots, dashes or underscores, starting and ending with a letter or digit", s.ID)
	case s.ID == "window" || rollingPeriod.MatchString(s.ID) || s.ID == LastMonth || s.ID == LastQuarter || s.ID == LastYear:
		return Period{}, fmt.Errorf("range %q: the id is reserved for a predefined period", s.ID)
	}
	from, err := time.Parse(DateLayout, s.From)
	if err != nil {
		return Period{}, fmt.Errorf("range %q: from %q: want YYYY-MM-DD", s.ID, s.From)
	}
	end, label := to, "Since "+s.From
	if s.To != "" && s.To != Now {
		if end, err = time.Parse(DateLayout, s.To); err != nil {
			return Period{}, fmt.Errorf("range %q: to %q: want YYYY-MM-DD or %s", s.ID, s.To, Now)
		}
		if from.After(end) {
			return Period{}, fmt.Errorf("range %q: from %s is after to %s", s.ID, s.From, s.To)
		}
		label = s.From + " to " + s.To
	}
	if from.After(to) {
		return Period{}, fmt.Errorf("range %q: %w", s.ID, errNotStarted)
	}
	if strings.TrimSpace(s.Label) != "" {
		label = strings.TrimSpace(s.Label)
	}
	if end.After(to) {
		end, label = to, label+" (so far)"
	}
	return Period{ID: s.ID, Label: label, From: from, To: end}, nil
}

// Period is a date range a ledger is computed for, both ends inclusive (UTC dates).
type Period struct {
	ID    string
	Label string
	From  time.Time
	To    time.Time
}

// Resolve turns a period ID into dates. Rolling periods end on to; calendar periods are the
// last full month, quarter or year before the one to falls in.
func Resolve(id string, to time.Time) (Period, error) {
	to = to.UTC().Truncate(24 * time.Hour)
	if m := rollingPeriod.FindStringSubmatch(id); m != nil {
		days, err := strconv.Atoi(m[1])
		if err != nil || days > maxPeriodDays {
			return Period{}, fmt.Errorf("period %q: at most %d days", id, maxPeriodDays)
		}
		label := "Last " + strconv.Itoa(days) + " days"
		if days == 1 {
			label = "Last day"
		}
		return Period{ID: id, Label: label, From: to.AddDate(0, 0, -(days - 1)), To: to}, nil
	}
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	switch id {
	case LastMonth:
		from := day(to.Year(), to.Month()-1, 1)
		return Period{ID: id, Label: "Last month (" + from.Format("January 2006") + ")", From: from, To: from.AddDate(0, 1, -1)}, nil
	case LastQuarter:
		quarterStart := to.Month() - (to.Month()-1)%3
		from := day(to.Year(), quarterStart-3, 1)
		q := (int(from.Month())-1)/3 + 1
		return Period{ID: id, Label: fmt.Sprintf("Last quarter (Q%d %d)", q, from.Year()), From: from, To: from.AddDate(0, 3, -1)}, nil
	case LastYear:
		from := day(to.Year()-1, time.January, 1)
		return Period{ID: id, Label: "Last year (" + strconv.Itoa(from.Year()) + ")", From: from, To: from.AddDate(1, 0, -1)}, nil
	}
	return Period{}, fmt.Errorf("period %q: want a number of days such as 30d, or %s, %s or %s", id, LastMonth, LastQuarter, LastYear)
}

// ResolvePeriods resolves every period in specs, in order, leaving out ranges that start
// after to.
func ResolvePeriods(specs []PeriodSpec, to time.Time) ([]Period, error) {
	out := make([]Period, 0, len(specs))
	for _, s := range specs {
		p, err := s.Resolve(to)
		if errors.Is(err, errNotStarted) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ParsePeriodIDs reads a list of predefined period IDs separated by commas or spaces, as
// the periods input and --periods give it. "none" is the empty list.
func ParsePeriodIDs(s string) ([]PeriodSpec, error) {
	ids := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	switch {
	case len(ids) == 0:
		return nil, fmt.Errorf("periods %q lists no periods; use none for the window alone", s)
	case len(ids) == 1 && ids[0] == "none":
		return []PeriodSpec{}, nil
	}
	return PeriodIDs(ids...), nil
}

func validatePeriods(specs []PeriodSpec, to time.Time) []error {
	var errs []error
	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s.ID] {
			errs = append(errs, fmt.Errorf("periods: %q is listed twice", s.ID))
		}
		seen[s.ID] = true
		if _, err := s.Resolve(to); err != nil && !errors.Is(err, errNotStarted) {
			errs = append(errs, fmt.Errorf("periods: %w", err))
		}
	}
	return errs
}

// Plan returns the periods a run computes and the index of the default one, which is the
// window: the period the run summary, ledger.json and the CSV tables describe. When no
// listed period has the window's dates, the window is added first. now is used as Range uses it.
func (c Config) Plan(now time.Time) ([]Period, int, error) {
	from, to, err := c.Window.Range(now)
	if err != nil {
		return nil, 0, err
	}
	periods, err := ResolvePeriods(c.Periods, to)
	if err != nil {
		return nil, 0, err
	}
	for i, p := range periods {
		if p.From.Equal(from) && p.To.Equal(to) {
			return periods, i, nil
		}
	}
	window := Period{ID: "window", Label: from.Format(DateLayout) + " to " + to.Format(DateLayout), From: from, To: to}
	if c.Window.From == "" && c.Window.Days > 0 {
		if p, err := Resolve(strconv.Itoa(c.Window.Days)+"d", to); err == nil {
			window = p
		}
	}
	return append([]Period{window}, periods...), 0, nil
}
