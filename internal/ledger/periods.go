package ledger

import (
	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

// PeriodLedger is the ledger for one reporting period. Default marks the period the run
// summary, ledger.json and the CSV tables describe.
type PeriodLedger struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Default bool    `json:"default"`
	Ledger  *Result `json:"ledger,omitempty"`
}

// ComputePeriods builds one ledger per period from the same pull requests, which must cover
// every period: everything updated since the earliest period starts. The lists of pull
// requests waiting for a first response describe the data as collected, so they are the
// same in every period.
func ComputePeriods(prs []store.PullRequest, cfg config.Config, periods []config.Period, def int) []PeriodLedger {
	out := make([]PeriodLedger, len(periods))
	for i, p := range periods {
		res := Compute(prs, cfg, p.From, p.To)
		out[i] = PeriodLedger{ID: p.ID, Label: p.Label, From: res.From, To: res.To, Default: i == def, Ledger: &res}
	}
	return out
}
