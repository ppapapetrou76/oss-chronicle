package ledger

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

func TestPullRequestsCountsOnlyActivityInTheWindow(t *testing.T) {
	res, _, _ := compute(t,
		pr(1, "alice"),
		pr(2, "alice", func(p *store.PullRequest) { p.CreatedAt = before }),
		pr(3, "alice", func(p *store.PullRequest) { p.CreatedAt = before }, review("bob", "APPROVED", inside, 0, "")),
		pr(4, "alice", func(p *store.PullRequest) { p.CreatedAt = before }, comment("bot[bot]", "ping", inside)),
		pr(5, "alice", func(p *store.PullRequest) { p.CreatedAt = before }, comment("bob", "late", day(25))),
	)
	if res.PullRequests != 3 {
		t.Errorf("pull requests = %d, want 3: opened (1), reviewed (3) and commented on by a bot (4) in the window", res.PullRequests)
	}
}

func TestComputePeriodsMatchesCollectingEachPeriod(t *testing.T) {
	to := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	prs := synthetic(to, 800)
	cfg := withComponents(
		config.Component{Name: "UI", Paths: []string{"/ui/"}},
		config.Component{Name: "Docs", Paths: []string{"/docs/"}},
		config.Component{Name: "Core", Paths: []string{"**"}})
	var periods []config.Period
	for _, id := range []string{"7d", "30d", "90d", config.LastMonth, config.LastQuarter} {
		p, err := config.Resolve(id, to)
		if err != nil {
			t.Fatal(err)
		}
		periods = append(periods, p)
	}
	got := ComputePeriods(prs, cfg, periods, 2)
	if len(got) != len(periods) || !got[2].Default || got[0].Default {
		t.Fatalf("periods = %d, default flags %v %v", len(got), got[0].Default, got[2].Default)
	}
	waiting := func(r *Result) map[string][]WaitingPR {
		m := map[string][]WaitingPR{}
		for _, c := range r.Components {
			m[c.Name] = c.Waiting
		}
		return m
	}
	withoutWaiting := func(r Result) []byte {
		var comps []ComponentStats
		for _, c := range r.Components {
			if c.Touched+c.Opened+c.Reviews > 0 {
				c.Waiting = nil
				comps = append(comps, c)
			}
		}
		r.Components = comps
		return must(json.Marshal(r))
	}
	for i, p := range periods {
		var direct []store.PullRequest
		for _, pr := range prs {
			if !pr.UpdatedAt.Before(p.From) {
				direct = append(direct, pr)
			}
		}
		want := Compute(direct, cfg, p.From, p.To)
		if string(withoutWaiting(*got[i].Ledger)) != string(withoutWaiting(want)) {
			t.Errorf("%s: ledger from the whole collection differs from one collected for the period alone (%d of %d PRs)", p.ID, len(direct), len(prs))
		}
		if got[i].From != p.From.Format(config.DateLayout) || got[i].To != p.To.Format(config.DateLayout) || got[i].Label != p.Label {
			t.Errorf("%s: header = %+v", p.ID, got[i])
		}
		for name, w := range waiting(got[i].Ledger) {
			if other, ok := waiting(got[2].Ledger)[name]; ok && !reflect.DeepEqual(w, other) {
				t.Errorf("%s: waiting list of %s differs from the default period's", p.ID, name)
			}
		}
	}
	if a, b := got[0].Ledger.Totals.Landed, got[1].Ledger.Totals.Landed; a == 0 || a >= b || b >= got[2].Ledger.Totals.Landed {
		t.Errorf("landed 7d %d, 30d %d, 90d %d: want each period to hold more than the shorter one", a, b, got[2].Ledger.Totals.Landed)
	}
}

func synthetic(to time.Time, n int) []store.PullRequest {
	r := rand.New(rand.NewPCG(1, 2))
	people := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi"}
	person := func() string { return people[r.IntN(len(people))] }
	states := []string{"APPROVED", "COMMENTED", "CHANGES_REQUESTED"}
	bodies := []string{"looks good", "/lgtm", "/retest", "can you rebase?", "/approve"}
	paths := []string{"ui/app.tsx", "docs/guide.md", "server/api.go", "server/api_test.go", "go.sum", "controller/sync.go"}
	start := to.AddDate(-1, 0, 0)
	span := int(to.Sub(start).Hours())
	var prs []store.PullRequest
	for i := range n {
		created := start.Add(time.Duration(r.IntN(span)) * time.Hour)
		when := func() time.Time { return created.Add(time.Duration(r.IntN(24*30)) * time.Hour) }
		p := store.PullRequest{Number: i + 1, Title: "fix: change", State: "OPEN", CreatedAt: created, Author: actor(person()), BaseRefName: "main", HeadRefName: fmt.Sprintf("feature-%d", i)}
		switch r.IntN(10) {
		case 0:
			p.Author = &store.Actor{Login: "dependabot", Typename: "Bot"}
			p.Title = "chore(deps): bump a library"
		case 1:
			p.HeadRefName = fmt.Sprintf("cherry-pick-%d", i)
			p.BaseRefName = "release-1.0"
		case 2:
			p.Author = &store.Actor{Login: "page-updater", Typename: "Bot"}
		case 3:
			p.BaseRefName = fmt.Sprintf("feature-%d", r.IntN(i+1))
		}
		var files []store.File
		for range 1 + r.IntN(3) {
			files = append(files, store.File{Path: paths[r.IntN(len(paths))], Additions: r.IntN(300), Deletions: r.IntN(100)})
		}
		p.Files = &store.Files{Nodes: files}
		author := p.AuthorLogin()
		for range r.IntN(4) {
			p.Commits.Nodes = append(p.Commits.Nodes, store.CommitNode{Commit: store.Commit{AuthoredDate: when(), Parents: store.Count{TotalCount: 1 + r.IntN(2)}, Author: &store.CommitUser{User: actor(person())}}})
		}
		for range r.IntN(4) {
			p.Reviews.Nodes = append(p.Reviews.Nodes, store.Review{Author: actor(person()), State: states[r.IntN(len(states))], SubmittedAt: at(when()), Comments: store.Count{TotalCount: r.IntN(3)}})
		}
		for range r.IntN(4) {
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.IssueComment, CreatedAt: at(when()), Author: actor(person()), Body: bodies[r.IntN(len(bodies))]})
		}
		if r.IntN(3) == 0 {
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.ReviewRequestedEvent, CreatedAt: at(when()), Actor: actor(author)})
		}
		if r.IntN(4) == 0 {
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.HeadRefForcePushedEvent, CreatedAt: at(when()), Actor: actor(author)})
		}
		end := when()
		switch r.IntN(5) {
		case 0, 1, 2:
			merger := actor(person())
			if r.IntN(3) == 0 {
				merger = &store.Actor{Login: "merge-queue", Typename: "Bot"}
			}
			p.State, p.MergedAt = "MERGED", at(end)
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes,
				store.TimelineItem{Typename: store.MergedEvent, CreatedAt: at(end), Actor: merger},
				store.TimelineItem{Typename: store.ClosedEvent, CreatedAt: at(end), Actor: merger})
		case 3:
			p.State = "CLOSED"
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.ClosedEvent, CreatedAt: at(end), Actor: actor(person())})
		}
		p.UpdatedAt = created
		for _, c := range p.Commits.Nodes {
			p.UpdatedAt = later(p.UpdatedAt, c.Commit.AuthoredDate)
		}
		for _, rv := range p.Reviews.Nodes {
			p.UpdatedAt = later(p.UpdatedAt, *rv.SubmittedAt)
		}
		for _, e := range p.TimelineItems.Nodes {
			p.UpdatedAt = later(p.UpdatedAt, *e.CreatedAt)
		}
		prs = append(prs, p)
	}
	return prs
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
