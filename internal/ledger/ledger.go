// Package ledger turns pull request activity into counted contributions,
// recording a reason for every activity it does not count.
package ledger

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

// Reason explains why an activity was not counted.
type Reason string

// Reasons an activity is left out of the counted units.
const (
	OpenedNotMerged     Reason = "opened_not_merged"
	OpenedMaintenance   Reason = "opened_maintenance"
	BranchCommits       Reason = "branch_commits"
	MergeCommits        Reason = "merge_commits"
	MaintenanceCommits  Reason = "maintenance_commits"
	OwnPRReviews        Reason = "own_pr_reviews"
	MaintenanceReviews  Reason = "maintenance_reviews"
	OwnPRComments       Reason = "own_pr_comments"
	MaintenanceComments Reason = "maintenance_comments"
	BotCommands         Reason = "bot_commands"
	MaintenanceMerges   Reason = "maintenance_merges"
	ClosedByMerge       Reason = "closed_by_merge"
	SelfClosed          Reason = "self_closed"
	MaintenanceCloses   Reason = "maintenance_closes"
	ForcePushes         Reason = "force_pushes"
	ReviewRequests      Reason = "review_requests"
	MergedElsewhere     Reason = "merged_elsewhere"
	BotMerges           Reason = "bot_merges"
)

var reasonInfo = map[Reason]struct {
	label       string
	maintenance bool
}{
	OpenedNotMerged:     {label: "Opened, not merged (still open or closed)"},
	OpenedMaintenance:   {label: "Opened backport or dependency PR"},
	BranchCommits:       {label: "PR-branch commits (replaced by one credit per merged PR)"},
	MergeCommits:        {label: "Merge commits on PR branches"},
	MaintenanceCommits:  {label: "Commits in backport, dependency or bot PRs"},
	OwnPRReviews:        {label: "Author replies counted as reviews"},
	MaintenanceReviews:  {label: "Reviews on backport, dependency or bot PRs", maintenance: true},
	OwnPRComments:       {label: "Author comments on their own PR"},
	MaintenanceComments: {label: "Comments on backport, dependency or bot PRs"},
	BotCommands:         {label: "Bot commands"},
	MaintenanceMerges:   {label: "Merges of backport, dependency or bot PRs", maintenance: true},
	ClosedByMerge:       {label: `"Closed" event fired by every merge`},
	SelfClosed:          {label: "Authors closing their own PR"},
	MaintenanceCloses:   {label: "Closing backport, dependency or bot PRs", maintenance: true},
	ForcePushes:         {label: "Force-pushes (rebases re-push commits)"},
	ReviewRequests:      {label: "Review requests"},
	MergedElsewhere:     {label: "Merged into a branch other than the default (stacked PRs)"},
	BotMerges:           {label: "Merged by a bot with no human merge command or approval"},
}

// Label is the human-readable description of the reason.
func (r Reason) Label() string { return reasonInfo[r].label }

// Maintenance reports whether activity with this reason is credited to the maintenance column.
func (r Reason) Maintenance() bool { return reasonInfo[r].maintenance }

// Person holds one contributor's counted units.
type Person struct {
	Login                string  `json:"login"`
	Landed               int     `json:"landed"`
	Reviewed             int     `json:"reviewed"`
	ReviewedWithFeedback int     `json:"reviewed_with_feedback"`
	Commented            int     `json:"commented"`
	Merged               int     `json:"merged"`
	Triaged              int     `json:"triaged"`
	Maintenance          int     `json:"maintenance"`
	Total                int     `json:"total"`
	AuthoringWeight      float64 `json:"authoring_weight"`
	ReviewingWeight      float64 `json:"reviewing_weight"`
}

// Dropped is how many activities were left out for one reason.
type Dropped struct {
	Reason      Reason `json:"reason"`
	Label       string `json:"label"`
	Count       int    `json:"count"`
	Maintenance bool   `json:"maintenance"`
}

// Totals sums the counted units over everyone.
type Totals struct {
	Landed               int     `json:"landed"`
	Reviewed             int     `json:"reviewed"`
	ReviewedWithFeedback int     `json:"reviewed_with_feedback"`
	Commented            int     `json:"commented"`
	Merged               int     `json:"merged"`
	Triaged              int     `json:"triaged"`
	Maintenance          int     `json:"maintenance"`
	AuthoringWeight      float64 `json:"authoring_weight"`
	ReviewingWeight      float64 `json:"reviewing_weight"`
}

// Result is the ledger for one repository and window. Rules are the counting rules the run
// applied, and Generator the oss-chronicle version that computed it, when known.
type Result struct {
	Repository    string           `json:"repository"`
	DefaultBranch string           `json:"default_branch"`
	From          string           `json:"from"`
	To            string           `json:"to"`
	PullRequests  int              `json:"pull_requests"`
	Totals        Totals           `json:"totals"`
	Size          SizeSummary      `json:"size"`
	People        []Person         `json:"people"`
	Components    []ComponentStats `json:"components"`
	Dropped       []Dropped        `json:"dropped"`
	Rules         []Rule           `json:"rules,omitempty"`
	Generator     *Generator       `json:"generator,omitempty"`
}

type reviewKey struct {
	login  string
	number int
}

type tally struct {
	c       classifier
	sizer   sizer
	weights map[int]float64
	size    SizeSummary
	mapper  *mapper
	comps   map[string]*componentTally
	primary map[int]string
	from    time.Time
	to      time.Time
	people  map[string]*Person
	reviews map[reviewKey]bool
	dropped map[Reason]int
	optOut  map[string]bool
}

// Compute builds the ledger for activity dated from..to, both inclusive (UTC dates).
func Compute(prs []store.PullRequest, cfg config.Config, from, to time.Time) Result {
	optOut := map[string]bool{}
	for _, l := range cfg.Publish.OptOut {
		optOut[strings.ToLower(l)] = true
	}
	t := tally{
		c:       newClassifier(cfg, prs),
		sizer:   newSizer(cfg.Size),
		weights: map[int]float64{},
		size:    SizeSummary{Buckets: make([]BucketCount, len(cfg.Size.Buckets))},
		mapper:  newMapper(cfg.Components, optOut),
		comps:   map[string]*componentTally{},
		primary: map[int]string{},
		from:    from.UTC().Truncate(24 * time.Hour),
		to:      to.UTC().Truncate(24 * time.Hour),
		people:  map[string]*Person{},
		reviews: map[reviewKey]bool{},
		dropped: map[Reason]int{},
		optOut:  optOut,
	}
	active := 0
	for _, pr := range prs {
		t.add(pr)
		if t.active(pr) {
			active++
		}
	}
	return t.result(cfg, active)
}

func (t *tally) inWindow(ts *time.Time) bool {
	if ts == nil || ts.IsZero() {
		return false
	}
	d := ts.UTC().Truncate(24 * time.Hour)
	return !d.Before(t.from) && !d.After(t.to)
}

func (t *tally) person(login string) *Person {
	p, ok := t.people[login]
	if !ok {
		p = &Person{Login: login}
		t.people[login] = p
	}
	return p
}

func (t *tally) drop(r Reason, login string) {
	t.dropped[r]++
	if r.Maintenance() {
		t.person(login).Maintenance++
	}
}

type landing struct {
	at    *time.Time
	actor *store.Actor
	event int
}

// landingOf finds how a pull request landed: through the merge button, or closed by a
// commit pushed to the base branch. event is the timeline index of the landing event, or -1.
func landingOf(pr store.PullRequest) landing {
	for i, e := range pr.TimelineItems.Nodes {
		if e.Typename == store.MergedEvent {
			at := pr.MergedAt
			if at == nil {
				at = e.CreatedAt
			}
			return landing{at: at, actor: e.Actor, event: i}
		}
	}
	if pr.State == "MERGED" {
		return landing{at: pr.MergedAt, event: -1}
	}
	if pr.State == "CLOSED" {
		for i := len(pr.TimelineItems.Nodes) - 1; i >= 0; i-- {
			if e := pr.TimelineItems.Nodes[i]; e.ClosedByCommit() {
				return landing{at: e.CreatedAt, actor: e.Actor, event: i}
			}
		}
	}
	return landing{event: -1}
}

func (t *tally) add(pr store.PullRequest) {
	kind := t.c.kind(pr)
	human := kind == Human
	author := pr.AuthorLogin()
	land := landingOf(pr)
	landed := land.at != nil
	toDefault := pr.BaseRefName == t.c.defaultBranch
	z, sized := t.sizer.size(pr)
	if human && sized {
		t.weights[pr.Number] = t.sizer.weight(z)
	}
	if human {
		t.addComponent(pr, land, toDefault)
	}

	if t.inWindow(&pr.CreatedAt) && !t.c.isBot(pr.Author) {
		switch {
		case human && landed:
		case human:
			t.drop(OpenedNotMerged, author)
		default:
			t.drop(OpenedMaintenance, author)
		}
	}

	for _, n := range pr.Commits.Nodes {
		c := n.Commit
		if !t.inWindow(&c.AuthoredDate) || c.Author == nil || t.c.isBot(c.Author.User) {
			continue
		}
		login := c.Author.User.Login
		switch {
		case c.Parents.TotalCount > 1:
			t.drop(MergeCommits, login)
		case !human:
			t.drop(MaintenanceCommits, login)
		default:
			t.drop(BranchCommits, login)
		}
	}

	for _, r := range pr.Reviews.Nodes {
		if !t.inWindow(r.SubmittedAt) || t.c.isBot(r.Author) {
			continue
		}
		login := r.Author.Login
		switch {
		case login == author:
			t.drop(OwnPRReviews, login)
		case !human:
			t.drop(MaintenanceReviews, login)
		default:
			t.review(login, pr.Number, hasFeedback(r))
		}
	}

	for i, e := range pr.TimelineItems.Nodes {
		if !t.inWindow(e.CreatedAt) {
			continue
		}
		if i == land.event {
			t.merge(pr, land, human, toDefault)
			continue
		}
		if t.c.isBotItem(e) {
			continue
		}
		login := e.Login()
		switch e.Typename {
		case store.IssueComment:
			switch {
			case login == author:
				t.drop(OwnPRComments, login)
			case !human && t.c.isApproval(e.Body):
				t.drop(MaintenanceReviews, login)
			case !human:
				t.drop(MaintenanceComments, login)
			case t.c.isApproval(e.Body):
				t.review(login, pr.Number, false)
			case t.c.isMergeCommand(e.Body), t.c.isBotCommand(e.Body):
				t.drop(BotCommands, login)
			default:
				t.person(login).Commented++
			}
		case store.ClosedEvent:
			switch {
			case landed:
				t.drop(ClosedByMerge, login)
			case login == author:
				t.drop(SelfClosed, login)
			case !human:
				t.drop(MaintenanceCloses, login)
			default:
				t.person(login).Triaged++
			}
		case store.HeadRefForcePushedEvent:
			t.drop(ForcePushes, login)
		case store.ReviewRequestedEvent:
			t.drop(ReviewRequests, login)
		}
	}

	if human && landed && t.inWindow(land.at) && !t.c.isBot(pr.Author) {
		if toDefault {
			p := t.person(author)
			p.Landed++
			if sized {
				p.AuthoringWeight += t.sizer.weight(z)
				t.size.Buckets[z.bucket].Landed++
				t.size.CountedLines += z.counted
				t.size.ChangedLines += z.changed
				t.size.ExcludedLines += z.excluded
			} else {
				t.size.Unsized++
			}
		} else {
			t.drop(MergedElsewhere, author)
		}
	}
}

// active reports whether anything happened on the pull request in the window: it was
// opened or landed, or it has a commit, review or timeline event dated in the window.
func (t *tally) active(pr store.PullRequest) bool {
	if t.inWindow(&pr.CreatedAt) || t.inWindow(landingOf(pr).at) {
		return true
	}
	for _, n := range pr.Commits.Nodes {
		if t.inWindow(&n.Commit.AuthoredDate) {
			return true
		}
	}
	for _, r := range pr.Reviews.Nodes {
		if t.inWindow(r.SubmittedAt) {
			return true
		}
	}
	for _, e := range pr.TimelineItems.Nodes {
		if t.inWindow(e.CreatedAt) {
			return true
		}
	}
	return false
}

func (t *tally) review(login string, number int, feedback bool) {
	k := reviewKey{login: login, number: number}
	t.reviews[k] = t.reviews[k] || feedback
}

// merge credits the landing event. When a bot performed it, the credit goes to the
// last person who issued a merge command before it, as in Prow, bors and merge-bot workflows.
// With no merge command, it goes to the last person other than the author who approved
// before it: Prow approves an approver's own pull request itself and merges it on someone
// else's /lgtm or approving review.
func (t *tally) merge(pr store.PullRequest, land landing, human, toDefault bool) {
	if land.actor == nil {
		return
	}
	login := land.actor.Login
	if t.c.isBot(land.actor) {
		login = t.lastMergeCommand(pr, land.at)
		if login == "" {
			login = t.lastApproval(pr, land.at)
		}
		if login == "" {
			t.drop(BotMerges, "")
			return
		}
	}
	switch {
	case !human:
		t.drop(MaintenanceMerges, login)
	case !toDefault:
		t.drop(MergedElsewhere, login)
	default:
		t.person(login).Merged++
	}
}

func (t *tally) lastMergeCommand(pr store.PullRequest, before *time.Time) string {
	login := ""
	var latest time.Time
	for _, e := range pr.TimelineItems.Nodes {
		if e.Typename != store.IssueComment || e.CreatedAt == nil || e.CreatedAt.After(*before) || t.c.isBotItem(e) {
			continue
		}
		if t.c.isMergeCommand(e.Body) && !e.CreatedAt.Before(latest) {
			login, latest = e.Login(), *e.CreatedAt
		}
	}
	return login
}

func (t *tally) lastApproval(pr store.PullRequest, before *time.Time) string {
	author := pr.AuthorLogin()
	login := ""
	var latest time.Time
	consider := func(at *time.Time, by string) {
		if at == nil || at.After(*before) || by == "" || by == author || at.Before(latest) {
			return
		}
		login, latest = by, *at
	}
	for _, r := range pr.Reviews.Nodes {
		if r.State == "APPROVED" && !t.c.isBot(r.Author) {
			consider(r.SubmittedAt, r.Author.Login)
		}
	}
	for _, e := range pr.TimelineItems.Nodes {
		if e.Typename == store.IssueComment && !t.c.isBotItem(e) && t.c.isApproval(e.Body) {
			consider(e.CreatedAt, e.Login())
		}
	}
	return login
}

func hasFeedback(r store.Review) bool {
	return r.State == "CHANGES_REQUESTED" || r.Comments.TotalCount > 0 || strings.TrimSpace(r.Body) != ""
}

func (t *tally) result(cfg config.Config, n int) Result {
	for k, deep := range t.reviews {
		p := t.person(k.login)
		p.Reviewed++
		w := t.weights[k.number]
		if deep {
			p.ReviewedWithFeedback++
			w *= cfg.Size.ReviewFeedback
		}
		p.ReviewingWeight += w
	}

	res := Result{
		Repository:    cfg.Repository,
		DefaultBranch: t.c.defaultBranch,
		Rules:         rulesOf(cfg),
		From:          t.from.Format(config.DateLayout),
		To:            t.to.Format(config.DateLayout),
		PullRequests:  n,
		Size:          t.size,
	}
	for i, b := range cfg.Size.Buckets {
		res.Size.Buckets[i].Bucket = b
	}
	res.Size.CountedLines = round2(res.Size.CountedLines)
	res.Size.ReviewFeedbackMultiple = cfg.Size.ReviewFeedback
	for _, p := range t.people {
		p.Total = p.Landed + p.Reviewed + p.Commented + p.Merged + p.Triaged
		p.AuthoringWeight, p.ReviewingWeight = round2(p.AuthoringWeight), round2(p.ReviewingWeight)
		res.Totals.AuthoringWeight += p.AuthoringWeight
		res.Totals.ReviewingWeight += p.ReviewingWeight
		res.Totals.Landed += p.Landed
		res.Totals.Reviewed += p.Reviewed
		res.Totals.ReviewedWithFeedback += p.ReviewedWithFeedback
		res.Totals.Commented += p.Commented
		res.Totals.Merged += p.Merged
		res.Totals.Triaged += p.Triaged
		res.Totals.Maintenance += p.Maintenance
		if (p.Total > 0 || p.Maintenance > 0) && !t.optOut[strings.ToLower(p.Login)] {
			res.People = append(res.People, *p)
		}
	}
	res.Components = t.componentStats(t.optOut)
	res.Totals.AuthoringWeight, res.Totals.ReviewingWeight = round2(res.Totals.AuthoringWeight), round2(res.Totals.ReviewingWeight)
	sort.Slice(res.People, func(i, j int) bool {
		a, b := res.People[i], res.People[j]
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Login < b.Login
	})

	for r, c := range t.dropped {
		res.Dropped = append(res.Dropped, Dropped{Reason: r, Label: r.Label(), Count: c, Maintenance: r.Maintenance()})
	}
	sort.Slice(res.Dropped, func(i, j int) bool {
		a, b := res.Dropped[i], res.Dropped[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Reason < b.Reason
	})
	return res
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
