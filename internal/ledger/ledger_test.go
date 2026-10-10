package ledger

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

var (
	from   = day(10)
	to     = day(20)
	inside = day(15)
	before = day(5)
)

func day(d int) time.Time { return time.Date(2026, 7, d, 12, 0, 0, 0, time.UTC) }

func at(t time.Time) *time.Time { return &t }

func actor(login string) *store.Actor { return &store.Actor{Login: login} }

func pr(n int, author string, opts ...func(*store.PullRequest)) store.PullRequest {
	p := store.PullRequest{Number: n, Title: "fix: something", State: "OPEN", CreatedAt: inside, Author: actor(author), BaseRefName: "main", HeadRefName: "feature"}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func mergedBy(login string) func(*store.PullRequest) {
	return func(p *store.PullRequest) {
		p.State = "MERGED"
		p.MergedAt = at(inside)
		p.TimelineItems.Nodes = append(p.TimelineItems.Nodes,
			store.TimelineItem{Typename: store.MergedEvent, CreatedAt: at(inside), Actor: actor(login)},
			store.TimelineItem{Typename: store.ClosedEvent, CreatedAt: at(inside), Actor: actor(login)})
	}
}

func review(login, state string, when time.Time, comments int, body string) func(*store.PullRequest) {
	return func(p *store.PullRequest) {
		p.Reviews.Nodes = append(p.Reviews.Nodes, store.Review{Author: actor(login), State: state, SubmittedAt: at(when), Comments: store.Count{TotalCount: comments}, Body: body})
	}
}

func commit(login string, parents int) func(*store.PullRequest) {
	return func(p *store.PullRequest) {
		p.Commits.Nodes = append(p.Commits.Nodes, store.CommitNode{Commit: store.Commit{AuthoredDate: inside, Parents: store.Count{TotalCount: parents}, Author: &store.CommitUser{User: actor(login)}}})
	}
}

func event(typename, login, body string) func(*store.PullRequest) {
	return func(p *store.PullRequest) {
		item := store.TimelineItem{Typename: typename, CreatedAt: at(inside), Body: body}
		if typename == store.IssueComment {
			item.Author = actor(login)
		} else {
			item.Actor = actor(login)
		}
		p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, item)
	}
}

func compute(t *testing.T, prs ...store.PullRequest) (Result, map[string]Person, map[Reason]int) {
	t.Helper()
	cfg := config.Default()
	cfg.DefaultBranch = "main"
	return computeWith(t, cfg, prs...)
}

func comment(login, body string, when time.Time) func(*store.PullRequest) {
	return func(p *store.PullRequest) {
		p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.IssueComment, CreatedAt: at(when), Author: actor(login), Body: body})
	}
}

func botMerge(bot string) func(*store.PullRequest) {
	return func(p *store.PullRequest) {
		p.State = "MERGED"
		p.MergedAt = at(inside)
		p.TimelineItems.Nodes = append(p.TimelineItems.Nodes,
			store.TimelineItem{Typename: store.MergedEvent, CreatedAt: at(inside), Actor: &store.Actor{Login: bot, Typename: "Bot"}},
			store.TimelineItem{Typename: store.ClosedEvent, CreatedAt: at(inside), Actor: &store.Actor{Login: bot, Typename: "Bot"}})
	}
}

func computeWith(t *testing.T, cfg config.Config, prs ...store.PullRequest) (Result, map[string]Person, map[Reason]int) {
	t.Helper()
	res := Compute(prs, cfg, from, to)
	people := map[string]Person{}
	for _, p := range res.People {
		people[p.Login] = p
	}
	dropped := map[Reason]int{}
	for _, d := range res.Dropped {
		dropped[d.Reason] = d.Count
	}
	return res, people, dropped
}

func TestMergedPRCreditsAuthorAndMergerOnce(t *testing.T) {
	_, people, dropped := compute(t, pr(1, "alice", mergedBy("bob")))
	if got := people["alice"].Landed; got != 1 {
		t.Errorf("alice landed = %d, want 1", got)
	}
	if got := people["bob"].Merged; got != 1 {
		t.Errorf("bob merged = %d, want 1", got)
	}
	if dropped[ClosedByMerge] != 1 {
		t.Errorf("closed-by-merge dropped = %d, want 1", dropped[ClosedByMerge])
	}
	if people["bob"].Triaged != 0 {
		t.Errorf("merge counted as triage")
	}
}

func TestOneReviewCreditPerReviewerPerPR(t *testing.T) {
	_, people, _ := compute(t, pr(1, "alice",
		review("bob", "COMMENTED", inside, 3, ""),
		review("bob", "APPROVED", inside, 0, ""),
		review("carol", "APPROVED", inside, 0, "")))
	if p := people["bob"]; p.Reviewed != 1 || p.ReviewedWithFeedback != 1 {
		t.Errorf("bob = %+v, want 1 review with feedback", p)
	}
	if p := people["carol"]; p.Reviewed != 1 || p.ReviewedWithFeedback != 0 {
		t.Errorf("carol = %+v, want 1 review without feedback", p)
	}
}

func TestAuthorRepliesAreNotReviewsOrComments(t *testing.T) {
	_, people, dropped := compute(t, pr(1, "alice",
		review("alice", "COMMENTED", inside, 1, ""),
		event(store.IssueComment, "alice", "thanks, fixed")))
	if _, ok := people["alice"]; ok {
		t.Errorf("alice credited for replying on her own PR: %+v", people["alice"])
	}
	if dropped[OwnPRReviews] != 1 || dropped[OwnPRComments] != 1 {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestBackportActivityGoesToMaintenance(t *testing.T) {
	cherry := pr(2, "carol", func(p *store.PullRequest) { p.HeadRefName = "cherry-pick-123-release-3.6" },
		review("bob", "APPROVED", inside, 0, ""), commit("alice", 1), mergedBy("bob"))
	_, people, dropped := compute(t, cherry)
	if p := people["bob"]; p.Maintenance != 2 || p.Total != 0 {
		t.Errorf("bob = %+v, want 2 maintenance (review + merge) and no total", p)
	}
	if people["carol"].Landed != 0 {
		t.Errorf("backport credited as landed")
	}
	for _, r := range []Reason{MaintenanceReviews, MaintenanceMerges, MaintenanceCommits, OpenedMaintenance} {
		if dropped[r] != 1 {
			t.Errorf("dropped %s = %d, want 1", r, dropped[r])
		}
	}
}

func TestBotOpenedPRActivityGoesToMaintenance(t *testing.T) {
	bot := func(p *store.PullRequest) { p.Author.Typename = "Bot" }
	res, people, dropped := compute(t,
		pr(1, "page-updater", bot,
			review("bob", "APPROVED", inside, 0, ""),
			event(store.IssueComment, "carol", "Looks right"),
			mergedBy("bob")),
		pr(2, "page-updater", bot, func(p *store.PullRequest) { p.State = "CLOSED" }, event(store.ClosedEvent, "dave", "")),
		pr(3, "page-updater", bot, botMerge("merge-queue")))
	if p := people["bob"]; p.Total != 0 || p.Maintenance != 2 {
		t.Errorf("bob = %+v, want a review and a merge as maintenance", p)
	}
	if p := people["dave"]; p.Triaged != 0 || p.Maintenance != 1 || dropped[MaintenanceCloses] != 1 {
		t.Errorf("dave = %+v, maintenance closes = %d, want the close as maintenance", p, dropped[MaintenanceCloses])
	}
	if dropped[BotMerges] != 1 {
		t.Errorf("bot merges = %d, want the merge nobody commanded recorded", dropped[BotMerges])
	}
	if _, ok := people["carol"]; ok || dropped[MaintenanceComments] != 1 {
		t.Errorf("carol = %+v, maintenance comments = %d, want the comment left out", people["carol"], dropped[MaintenanceComments])
	}
	if res.Totals.Landed != 0 || dropped[OpenedMaintenance] != 0 {
		t.Errorf("landed = %d, opened maintenance = %d, want neither for a bot's PR", res.Totals.Landed, dropped[OpenedMaintenance])
	}
}

func TestMaintenanceCountsEachActionIncludingApprovalCommands(t *testing.T) {
	deps := func(p *store.PullRequest) { p.Title = "chore(deps): bump x" }
	_, people, dropped := compute(t,
		pr(1, "dependabot", deps,
			review("dave", "COMMENTED", inside, 1, ""),
			review("dave", "COMMENTED", inside, 1, ""),
			review("dave", "APPROVED", inside, 0, ""),
			comment("carol", "/lgtm", inside),
			comment("carol", "/lgtm", inside),
			comment("erin", "thanks", inside)),
		pr(2, "dependabot", deps,
			comment("frank", "/approve", inside.Add(-time.Hour)),
			botMerge("prow-bot")),
		pr(3, "grace", func(p *store.PullRequest) { p.HeadRefName = "cherry-pick-1-to-release-1.0" },
			comment("grace", "/lgtm", inside),
			comment("heidi", "/lgtm cancel", inside)))
	if p := people["dave"]; p.Maintenance != 3 || p.Reviewed != 0 {
		t.Errorf("dave = %+v, want each of the 3 reviews as maintenance", p)
	}
	if p := people["carol"]; p.Maintenance != 2 || p.Reviewed != 0 || p.Total != 0 {
		t.Errorf("carol = %+v, want each /lgtm as maintenance and nothing in the total", p)
	}
	if p := people["frank"]; p.Maintenance != 2 || p.Merged != 0 || p.Total != 0 {
		t.Errorf("frank = %+v, want /approve and the merge it commanded as maintenance", p)
	}
	for _, login := range []string{"erin", "grace", "heidi"} {
		if p, ok := people[login]; ok {
			t.Errorf("%s = %+v, want a plain comment, the author's own /lgtm and /lgtm cancel left out", login, p)
		}
	}
	if dropped[MaintenanceComments] != 2 || dropped[OwnPRComments] != 1 {
		t.Errorf("maintenance comments = %d, own PR comments = %d, want 2 and 1", dropped[MaintenanceComments], dropped[OwnPRComments])
	}
}

func TestTriageCountsEachClose(t *testing.T) {
	closed := func(p *store.PullRequest) { p.State = "CLOSED" }
	_, people, _ := compute(t,
		pr(1, "alice", closed, event(store.ClosedEvent, "bob", ""), event(store.ClosedEvent, "bob", "")),
		pr(2, "alice", event(store.ClosedEvent, "bob", ""), mergedBy("carol")))
	if got := people["bob"].Triaged; got != 2 {
		t.Errorf("bob triaged = %d, want both closes of the PR that never landed, and not the one that landed later", got)
	}
}

func TestCommitsCountByTheirOwnDate(t *testing.T) {
	dated := func(when time.Time) func(*store.PullRequest) {
		return func(p *store.PullRequest) { p.Commits.Nodes[len(p.Commits.Nodes)-1].Commit.AuthoredDate = when }
	}
	if _, _, dropped := compute(t, pr(1, "alice", commit("alice", 1), dated(before))); dropped[BranchCommits] != 0 {
		t.Errorf("commit dated before the window, on a PR opened in it: branch commits = %d, want 0", dropped[BranchCommits])
	}
	if _, _, dropped := compute(t, pr(2, "alice", func(p *store.PullRequest) { p.CreatedAt = before }, commit("alice", 1))); dropped[BranchCommits] != 1 {
		t.Errorf("commit dated in the window, on a PR opened before it: branch commits = %d, want 1", dropped[BranchCommits])
	}
}

func TestNonDefaultBaseIsBackport(t *testing.T) {
	p := pr(3, "carol", func(p *store.PullRequest) { p.BaseRefName = "release-3.6" }, mergedBy("bob"))
	_, people, _ := compute(t, p)
	if people["bob"].Maintenance != 1 || people["carol"].Landed != 0 {
		t.Errorf("people = %+v", people)
	}
}

func TestDependencyPRWithHumanFixupCommit(t *testing.T) {
	deps := pr(4, "dependabot", func(p *store.PullRequest) { p.Title = "chore(deps): bump x from 1 to 2" },
		commit("dependabot", 1), commit("alice", 1), mergedBy("bob"))
	_, people, dropped := compute(t, deps)
	if dropped[MaintenanceCommits] != 1 {
		t.Errorf("human fixup commit not dropped as maintenance: %v", dropped)
	}
	if dropped[OpenedMaintenance] != 0 {
		t.Errorf("bot-opened PR counted as opened activity")
	}
	if people["bob"].Maintenance != 1 {
		t.Errorf("bob maintenance = %d, want 1", people["bob"].Maintenance)
	}
}

func TestMergeCommitsAndBranchCommitsAreDropped(t *testing.T) {
	_, people, dropped := compute(t, pr(5, "alice", commit("alice", 1), commit("alice", 1), commit("alice", 2), mergedBy("bob")))
	if dropped[MergeCommits] != 1 || dropped[BranchCommits] != 2 {
		t.Errorf("dropped = %v", dropped)
	}
	if people["alice"].Total != 1 {
		t.Errorf("alice total = %d, want 1 (one landed PR)", people["alice"].Total)
	}
}

func TestTriageAndSelfClose(t *testing.T) {
	_, people, dropped := compute(t,
		pr(6, "alice", event(store.ClosedEvent, "bob", "")),
		pr(7, "alice", event(store.ClosedEvent, "alice", "")))
	if people["bob"].Triaged != 1 {
		t.Errorf("bob triaged = %d, want 1", people["bob"].Triaged)
	}
	if dropped[SelfClosed] != 1 || dropped[OpenedNotMerged] != 2 {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestBotCommandsAndBotAccounts(t *testing.T) {
	appBot := func(p *store.PullRequest) {
		p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.IssueComment, CreatedAt: at(inside), Author: &store.Actor{Login: "preview-env", Typename: "Bot"}})
	}
	_, people, dropped := compute(t, pr(8, "alice",
		event(store.IssueComment, "bob", "/retest"),
		event(store.IssueComment, "carol", "Looks good, one question"),
		appBot))
	if dropped[BotCommands] != 1 {
		t.Errorf("bot commands = %d, want 1", dropped[BotCommands])
	}
	if people["carol"].Commented != 1 {
		t.Errorf("carol commented = %d, want 1", people["carol"].Commented)
	}
	if _, ok := people["preview-env"]; ok {
		t.Errorf("GitHub App account counted as a person")
	}
}

func TestActivityOutsideWindowIsIgnored(t *testing.T) {
	old := pr(9, "alice", func(p *store.PullRequest) { p.CreatedAt = before },
		review("bob", "APPROVED", before, 0, ""), mergedBy("carol"))
	_, people, dropped := compute(t, old)
	if people["bob"].Reviewed != 0 {
		t.Errorf("review before the window counted")
	}
	if people["alice"].Landed != 1 {
		t.Errorf("PR merged inside the window not credited")
	}
	if dropped[OpenedNotMerged]+dropped[OpenedMaintenance] != 0 {
		t.Errorf("PR opened before the window counted: %v", dropped)
	}
}

func TestRebasesAndReviewRequestsAreDropped(t *testing.T) {
	_, _, dropped := compute(t, pr(10, "alice",
		event(store.HeadRefForcePushedEvent, "alice", ""),
		event(store.ReviewRequestedEvent, "alice", "")))
	if dropped[ForcePushes] != 1 || dropped[ReviewRequests] != 1 {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestOptOutHidesPersonButKeepsTotals(t *testing.T) {
	cfg := config.Default()
	cfg.Publish.OptOut = []string{"Alice"}
	res, people, _ := computeWith(t, cfg, pr(11, "alice", mergedBy("bob")))
	if _, ok := people["alice"]; ok {
		t.Errorf("opted-out person listed")
	}
	if res.Totals.Landed != 1 {
		t.Errorf("totals landed = %d, want 1", res.Totals.Landed)
	}
}

func TestStackedPRIsNotABackportAndHasNotLanded(t *testing.T) {
	stacked := pr(12, "alice", func(p *store.PullRequest) { p.BaseRefName = "alice/part-1" },
		review("bob", "APPROVED", inside, 0, ""), mergedBy("carol"))
	_, people, dropped := compute(t, stacked)
	if people["bob"].Reviewed != 1 || people["bob"].Maintenance != 0 {
		t.Errorf("review on stacked PR = %+v, want a counted review", people["bob"])
	}
	if people["alice"].Landed != 0 || people["carol"].Merged != 0 {
		t.Errorf("merge into a feature branch counted as landed: %+v", people)
	}
	if dropped[MergedElsewhere] != 2 {
		t.Errorf("merged elsewhere = %d, want 2 (author and merger)", dropped[MergedElsewhere])
	}
}

func TestProwApprovalsCountAsReviewsAndCreditTheMerge(t *testing.T) {
	_, people, dropped := compute(t, pr(13, "alice",
		comment("bob", "nice work\n/lgtm", before.Add(-time.Hour)),
		comment("bob", "/lgtm", inside.Add(-2*time.Hour)),
		comment("carol", "/approve", inside.Add(-time.Hour)),
		comment("dave", "/lgtm cancel", inside.Add(-time.Hour)),
		comment("erin", "/approved by whom?", inside.Add(-time.Hour)),
		botMerge("k8s-ci-robot")))
	if p := people["bob"]; p.Reviewed != 1 || p.Commented != 0 {
		t.Errorf("bob = %+v, want 1 review from /lgtm", p)
	}
	if p := people["carol"]; p.Reviewed != 1 || p.Merged != 1 {
		t.Errorf("carol = %+v, want 1 review and the merge from /approve", p)
	}
	if people["dave"].Reviewed != 0 || people["erin"].Reviewed != 0 {
		t.Errorf("cancel or lookalike counted as approval: %+v", people)
	}
	if dropped[BotCommands] != 2 || dropped[ClosedByMerge] != 0 {
		t.Errorf("dropped = %v, want the cancel and the lookalike as bot commands", dropped)
	}
}

func TestBorsApprovalCreditsTheMerge(t *testing.T) {
	_, people, _ := compute(t, pr(14, "alice", comment("bob", "@bors r+ rollup", inside), botMerge("bors")))
	if p := people["bob"]; p.Reviewed != 1 || p.Merged != 1 {
		t.Errorf("bob = %+v, want 1 review and 1 merge", p)
	}
}

func TestBotMergeWithoutCommandIsDropped(t *testing.T) {
	_, people, dropped := compute(t, pr(15, "alice",
		comment("alice", "/lgtm", inside.Add(-time.Hour)),
		review("alice", "APPROVED", inside.Add(-time.Hour), 0, ""),
		review("bob", "COMMENTED", inside.Add(-time.Hour), 1, "nit"),
		comment("carol", "/lgtm cancel", inside.Add(-time.Hour)),
		review("dave", "APPROVED", inside.Add(time.Hour), 0, ""),
		botMerge("merge-queue-app")))
	if dropped[BotMerges] != 1 || people["alice"].Landed != 1 || people["alice"].Merged != 0 {
		t.Errorf("dropped = %v, alice = %+v", dropped, people["alice"])
	}
	for _, login := range []string{"bob", "carol", "dave"} {
		if people[login].Merged != 0 {
			t.Errorf("%s got the merge without approving before it: %+v", login, people[login])
		}
	}
}

func TestBotMergeWithoutCommandGoesToLastApproval(t *testing.T) {
	_, people, dropped := compute(t,
		pr(17, "alice",
			comment("bob", "/lgtm", inside.Add(-3*time.Hour)),
			review("carol", "APPROVED", inside.Add(-2*time.Hour), 0, ""),
			comment("dave", "/lgtm", inside.Add(-time.Hour)),
			comment("alice", "/lgtm", inside.Add(-time.Minute)),
			botMerge("k8s-ci-robot")),
		pr(18, "alice",
			comment("bob", "/lgtm", inside.Add(-2*time.Hour)),
			review("carol", "APPROVED", inside.Add(-time.Hour), 0, ""),
			review("renovate", "APPROVED", inside.Add(-time.Minute), 0, ""),
			botMerge("k8s-ci-robot")),
		pr(19, "alice",
			review("carol", "APPROVED", inside.Add(-2*time.Hour), 0, ""),
			comment("bob", "/approve", before),
			botMerge("k8s-ci-robot")))
	if dropped[BotMerges] != 0 {
		t.Errorf("bot merges dropped = %d, want 0", dropped[BotMerges])
	}
	if people["dave"].Merged != 1 || people["carol"].Merged != 1 || people["bob"].Merged != 1 || people["alice"].Merged != 0 {
		t.Errorf("merges: dave %d (/lgtm on 17), carol %d (review on 18), bob %d (/approve on 19 wins over a later review), alice %d",
			people["dave"].Merged, people["carol"].Merged, people["bob"].Merged, people["alice"].Merged)
	}
}

func TestBotMergeOfMaintenancePRGoesToApproverAsMaintenance(t *testing.T) {
	_, people, dropped := compute(t, pr(20, "dependabot[bot]",
		review("bob", "APPROVED", inside.Add(-2*time.Hour), 0, ""),
		comment("github-actions", "/lgtm", inside.Add(-time.Hour)),
		botMerge("github-actions")))
	if dropped[MaintenanceMerges] != 1 || dropped[BotMerges] != 0 {
		t.Errorf("dropped = %v, want 1 maintenance merge", dropped)
	}
	if p := people["bob"]; p.Merged != 0 || p.Maintenance != 2 {
		t.Errorf("bob = %+v, want maintenance for the review and the merge", p)
	}
}

func TestClosedByCommitCountsAsLanded(t *testing.T) {
	ghstack := pr(16, "alice", comment("bob", "@pytorchbot merge -f 'flaky'", inside.Add(-time.Hour)), func(p *store.PullRequest) {
		p.State = "CLOSED"
		p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{
			Typename: store.ClosedEvent, CreatedAt: at(inside),
			Actor:  &store.Actor{Login: "pytorchmergebot", Typename: "User"},
			Closer: &store.Closer{Typename: "Commit"},
		})
	})
	_, people, dropped := compute(t, ghstack)
	if people["alice"].Landed != 1 || people["bob"].Merged != 1 {
		t.Errorf("people = %+v", people)
	}
	if dropped[OpenedNotMerged] != 0 || dropped[BotCommands] != 1 {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestDefaultBranchIsInferred(t *testing.T) {
	res, people, _ := computeWith(t, config.Default(),
		pr(17, "alice", func(p *store.PullRequest) { p.BaseRefName = "trunk" }, mergedBy("bob")),
		pr(18, "carol", func(p *store.PullRequest) { p.BaseRefName = "trunk" }),
		pr(19, "dave", func(p *store.PullRequest) { p.BaseRefName = "release-1.0" }, mergedBy("bob")))
	if res.DefaultBranch != "trunk" {
		t.Errorf("default branch = %q, want trunk", res.DefaultBranch)
	}
	if people["alice"].Landed != 1 || people["dave"].Landed != 0 || people["bob"].Maintenance != 1 {
		t.Errorf("people = %+v", people)
	}
}

func files(fs ...store.File) func(*store.PullRequest) {
	return func(p *store.PullRequest) { p.Files = &store.Files{Nodes: fs} }
}

func TestSizeBucketsAndExclusions(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultBranch = "main"
	cfg.Size.GitAttributes = "docs/cli/** linguist-generated=true\n"
	_, people, _ := computeWith(t, cfg,
		pr(20, "xs", mergedBy("m"), files(store.File{Path: "a.go", Additions: 6, Deletions: 4})),
		pr(21, "s", mergedBy("m"), files(store.File{Path: "a.go", Additions: 11})),
		pr(22, "m", mergedBy("m"), files(store.File{Path: "a.go", Additions: 200, Deletions: 50}, store.File{Path: "go.sum", Additions: 900})),
		pr(23, "l", mergedBy("m"), files(store.File{Path: "a.go", Additions: 251})),
		pr(24, "gen", mergedBy("m"), files(store.File{Path: "docs/cli/app.md", Additions: 4000}, store.File{Path: "ui/yarn.lock", Additions: 300}, store.File{Path: "cmd.go", Additions: 3})),
	)
	for login, want := range map[string]float64{"xs": 0.5, "s": 1, "m": 2, "l": 3, "gen": 0.5} {
		if got := people[login].AuthoringWeight; got != want {
			t.Errorf("%s authoring weight = %v, want %v", login, got, want)
		}
	}
}

func TestSizeSummaryCountsLandedPRs(t *testing.T) {
	res, _, _ := compute(t,
		pr(25, "a", mergedBy("m"), files(store.File{Path: "a.go", Additions: 5}, store.File{Path: "go.sum", Additions: 40})),
		pr(26, "b", mergedBy("m")),
		pr(27, "c", files(store.File{Path: "a.go", Additions: 500})))
	sz := res.Size
	if sz.Buckets[0].Landed != 1 || sz.Buckets[0].Name != "XS" || sz.Unsized != 1 || sz.CountedLines != 5 || sz.ExcludedLines != 40 || sz.ChangedLines != 45 {
		t.Errorf("size = %+v", sz)
	}
	if sz.Buckets[3].Landed != 0 {
		t.Errorf("open PR counted in size buckets")
	}
}

func TestDeletionAndTestWeights(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultBranch = "main"
	cfg.Size.DeletionsWeight = 0
	cfg.Size.TestWeight = 0.5
	_, people, _ := computeWith(t, cfg,
		pr(28, "a", mergedBy("m"), files(store.File{Path: "a.go", Additions: 40, Deletions: 400}, store.File{Path: "a_test.go", Additions: 20})))
	if got := people["a"].AuthoringWeight; got != 1 {
		t.Errorf("weight = %v, want 1 (40 + 0*400 + 0.5*20 = 50 lines, bucket S)", got)
	}
}

func TestReviewWeightUsesPRSizeAndFeedback(t *testing.T) {
	_, people, _ := compute(t,
		pr(29, "alice", files(store.File{Path: "a.go", Additions: 300}),
			review("bob", "APPROVED", inside, 0, ""),
			review("carol", "CHANGES_REQUESTED", inside, 0, "")),
		pr(30, "alice",
			review("bob", "APPROVED", inside, 0, "")),
		pr(31, "dependabot", func(p *store.PullRequest) { p.Title = "chore(deps): bump" }, files(store.File{Path: "a.go", Additions: 300}),
			review("bob", "APPROVED", inside, 0, "")))
	if got := people["bob"].ReviewingWeight; got != 3 {
		t.Errorf("bob reviewing weight = %v, want 3 (L PR; unsized and dependency PRs add nothing)", got)
	}
	if got := people["carol"].ReviewingWeight; got != 6 {
		t.Errorf("carol reviewing weight = %v, want 6 (L PR x2 for feedback)", got)
	}
	if people["bob"].Reviewed != 2 {
		t.Errorf("bob reviewed = %d, want 2", people["bob"].Reviewed)
	}
}

func TestStackedPRGetsNoAuthoringWeight(t *testing.T) {
	_, people, _ := compute(t, pr(32, "alice", func(p *store.PullRequest) { p.BaseRefName = "alice/part-1" },
		mergedBy("bob"), files(store.File{Path: "a.go", Additions: 30})))
	if people["alice"].AuthoringWeight != 0 {
		t.Errorf("stacked PR earned authoring weight")
	}
}

func TestExcludeExtraAddsToDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultBranch = "main"
	cfg.Size.ExcludeExtra = []string{"gen/**"}
	res, _, _ := computeWith(t, cfg, pr(33, "a", mergedBy("m"),
		files(store.File{Path: "gen/x.go", Additions: 100}, store.File{Path: "go.sum", Additions: 10}, store.File{Path: "a.go", Additions: 1})))
	if res.Size.ExcludedLines != 110 {
		t.Errorf("excluded = %d, want 110 (extra pattern and default go.sum)", res.Size.ExcludedLines)
	}
}

func withComponents(m ...config.Component) config.Config {
	cfg := config.Default()
	cfg.DefaultBranch = "main"
	cfg.Components.Map = m
	return cfg
}

func component(res Result, name string) ComponentStats {
	for _, c := range res.Components {
		if c.Name == name {
			return c
		}
	}
	return ComponentStats{}
}

func TestComponentMapping(t *testing.T) {
	cfg := withComponents(config.Component{Name: "UI", Paths: []string{"/ui/"}})
	cfg.Components.Codeowners = "** @org/all\n/docs/ @org/all @org/docs\n"
	m := newMapper(cfg.Components, nil)
	tests := map[string]string{
		"ui/src/app.tsx":      "UI",
		"docs/ui/page.md":     "@org/docs",
		"controller/state.go": "controller",
		"go.mod":              RootFiles,
	}
	for path, want := range tests {
		if got := m.component(path); got != want {
			t.Errorf("component(%q) = %q, want %q", path, got, want)
		}
	}
	cfg.Components.UseCodeowners = false
	if got := newMapper(cfg.Components, nil).component("docs/ui/page.md"); got != "docs" {
		t.Errorf("without CODEOWNERS docs/ui/page.md = %q, want docs", got)
	}
}

func TestOptOutOwnersAreLeftOutOfComponentNames(t *testing.T) {
	cfg := withComponents()
	cfg.Components.Codeowners = "* @bob\n/api/ @Alice @bob\n/cli/ @alice\n/ui/ @org/ui @alice\n"
	cfg.Publish.OptOut = []string{"ALICE"}
	res, _, _ := computeWith(t, cfg,
		pr(1, "carol", mergedBy("dave"), files(store.File{Path: "api/a.go", Additions: 10})),
		pr(2, "carol", mergedBy("dave"), files(store.File{Path: "cli/main.go", Additions: 10})),
		pr(3, "carol", mergedBy("dave"), files(store.File{Path: "ui/app.tsx", Additions: 10})))
	var names []string
	for _, c := range res.Components {
		names = append(names, c.Name)
		if strings.Contains(strings.ToLower(c.Name), "alice") {
			t.Errorf("component %q names an opted-out owner", c.Name)
		}
	}
	for _, want := range []string{"@bob", "cli", "@org/ui"} {
		if component(res, want).Landed != 1 {
			t.Errorf("want component %q with 1 landed PR, got components %v", want, names)
		}
	}
}

func TestPrimaryComponentIsWhereMostCountedLinesChanged(t *testing.T) {
	cfg := withComponents(config.Component{Name: "UI", Paths: []string{"/ui/"}}, config.Component{Name: "API", Paths: []string{"/server/"}})
	res, _, _ := computeWith(t, cfg,
		pr(1, "alice", mergedBy("bob"), files(
			store.File{Path: "server/a.go", Additions: 30},
			store.File{Path: "ui/a.tsx", Additions: 20},
			store.File{Path: "ui/yarn.lock", Additions: 5000})),
		pr(2, "carol", mergedBy("bob"), files(store.File{Path: "go.sum", Additions: 40}, store.File{Path: "server/b.go"})),
		pr(3, "dave", files()),
	)
	api, ui, root := component(res, "API"), component(res, "UI"), component(res, RootFiles)
	if api.Landed != 1 || api.Touched != 1 {
		t.Errorf("API = %+v, want PR 1, by counted lines despite the larger lock file in UI", api)
	}
	if ui.Landed != 0 || ui.Touched != 1 {
		t.Errorf("UI = %+v, want touched by PR 1 only", ui)
	}
	if root.Landed != 1 || root.Touched != 1 {
		t.Errorf("root files = %+v, want PR 2: no counted lines, so go.sum's changed lines decide", root)
	}
	if c := component(res, NoFiles); c.Opened != 1 {
		t.Errorf("PR with no files = %+v", c)
	}
}

func TestComponentReviewsAndResponses(t *testing.T) {
	cfg := withComponents(config.Component{Name: "UI", Paths: []string{"/ui/"}})
	ui := files(store.File{Path: "ui/a.tsx", Additions: 5})
	res, _, _ := computeWith(t, cfg,
		pr(1, "alice", ui, review("bob", "APPROVED", day(16), 0, ""), review("alice", "COMMENTED", day(15), 0, "")),
		pr(2, "alice", ui, review("bob", "COMMENTED", day(18), 1, ""), comment("carol", "looks good", day(17))),
		pr(3, "dave", ui, comment("dave", "ping", day(16)), comment("ci-bot", "/retest", day(16))),
		pr(4, "erin", ui, func(p *store.PullRequest) { p.IsDraft = true }),
		pr(5, "frank", ui, func(p *store.PullRequest) { p.CreatedAt = day(1) }, review("bob", "APPROVED", day(12), 0, "")),
	)
	c := component(res, "UI")
	if c.Reviews != 3 || len(c.Reviewers) != 1 || c.Reviewers[0].Login != "bob" || c.TopReviewerShare != 1 || c.ReviewersForHalf != 1 {
		t.Errorf("reviews = %d %+v share %v half %d", c.Reviews, c.Reviewers, c.TopReviewerShare, c.ReviewersForHalf)
	}
	if c.Opened != 3 || c.NoResponse != 1 {
		t.Errorf("opened = %d, no response = %d; want 3 non-drafts opened in the window, 1 never answered (author ping and bot only)", c.Opened, c.NoResponse)
	}
	if c.MedianFirstResponseHours == nil || *c.MedianFirstResponseHours != 36 {
		t.Errorf("median first response = %v, want 36h (24h and 48h)", c.MedianFirstResponseHours)
	}
	if len(c.Waiting) != 1 || c.Waiting[0].Number != 3 {
		t.Errorf("waiting = %+v, want only PR 3 (drafts are not waiting)", c.Waiting)
	}
}

func TestComponentReviewerConcentration(t *testing.T) {
	cfg := withComponents()
	var prs []store.PullRequest
	for i, reviewer := range []string{"r1", "r1", "r1", "r2", "r2", "r3"} {
		prs = append(prs, pr(i+1, "author", files(store.File{Path: "lib/a.go", Additions: 1}), review(reviewer, "APPROVED", inside, 0, "")))
	}
	cfg.Publish.OptOut = []string{"R2"}
	res, _, _ := computeWith(t, cfg, prs...)
	c := component(res, "lib")
	if c.Reviews != 6 || c.TopReviewerShare != 0.5 || c.ReviewersForHalf != 1 {
		t.Errorf("reviews %d share %v half %d", c.Reviews, c.TopReviewerShare, c.ReviewersForHalf)
	}
	if len(c.Reviewers) != 2 || c.Reviewers[0].Login != "r1" || c.Reviewers[1].Login != "r3" {
		t.Errorf("reviewers = %+v, want r1 and r3 (r2 opted out)", c.Reviewers)
	}
}

func TestMedian(t *testing.T) {
	if median(nil) != nil {
		t.Error("median of nothing should be nil")
	}
	if m := median([]float64{5, 1, 3}); *m != 3 {
		t.Errorf("odd median = %v", *m)
	}
	if m := median([]float64{4, 1, 2, 3}); *m != 2.5 {
		t.Errorf("even median = %v", *m)
	}
}

func TestFirstResponseFromMergeOrCloseButNotSelfClose(t *testing.T) {
	cfg := withComponents()
	lib := files(store.File{Path: "lib/a.go", Additions: 1})
	closed := func(login string, when time.Time) func(*store.PullRequest) {
		return func(p *store.PullRequest) {
			p.State = "CLOSED"
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.ClosedEvent, CreatedAt: at(when), Actor: actor(login)})
		}
	}
	res, _, _ := computeWith(t, cfg,
		pr(1, "alice", lib, func(p *store.PullRequest) {
			p.State, p.MergedAt = "MERGED", at(day(16))
			p.TimelineItems.Nodes = append(p.TimelineItems.Nodes, store.TimelineItem{Typename: store.MergedEvent, CreatedAt: at(day(16)), Actor: actor("bob")})
		}),
		pr(2, "alice", lib, closed("carol", day(17))),
		pr(3, "alice", lib, closed("alice", day(17))),
	)
	c := component(res, "lib")
	if c.Opened != 3 || c.NoResponse != 1 || c.MedianFirstResponseHours == nil || *c.MedianFirstResponseHours != 36 {
		t.Errorf("opened %d, no response %d, median %v; want merge (24h) and triage close (48h) as responses, self-close as none", c.Opened, c.NoResponse, c.MedianFirstResponseHours)
	}
	if len(c.LandedBy) != 1 || c.LandedBy[0] != (ComponentAuthor{Login: "alice", Landed: 1}) {
		t.Errorf("landed by = %+v", c.LandedBy)
	}
	if c.Landed != 1 || c.MedianTimeToMergeHours == nil || *c.MedianTimeToMergeHours != 24 || len(c.Waiting) != 0 {
		t.Errorf("landed %d, time to merge %v, waiting %d", c.Landed, c.MedianTimeToMergeHours, len(c.Waiting))
	}
}

func TestOptedOutTopReviewerAndWaitingAuthor(t *testing.T) {
	cfg := withComponents()
	cfg.Publish.OptOut = []string{"hidden"}
	lib := files(store.File{Path: "lib/a.go", Additions: 1})
	res, _, _ := computeWith(t, cfg,
		pr(1, "a", lib, review("hidden", "APPROVED", inside, 0, "")),
		pr(2, "a", lib, review("hidden", "APPROVED", inside, 0, "")),
		pr(3, "a", lib, review("r2", "APPROVED", inside, 0, "")),
		pr(4, "hidden", lib),
	)
	c := component(res, "lib")
	if c.TopReviewer != "" || c.TopReviewerShare != 0.667 || c.ReviewerCount != 2 || len(c.Reviewers) != 1 {
		t.Errorf("top %q share %v count %d reviewers %+v", c.TopReviewer, c.TopReviewerShare, c.ReviewerCount, c.Reviewers)
	}
	if len(c.Waiting) != 1 || c.Waiting[0].Number != 4 || c.Waiting[0].Author != "" {
		t.Errorf("waiting = %+v, want PR 4 with its author hidden", c.Waiting)
	}
	res, _, _ = computeWith(t, cfg,
		pr(5, "zed", lib, mergedBy("r2")), pr(6, "amy", lib, mergedBy("r2")), pr(7, "hidden", lib, mergedBy("r2")), pr(8, "zed", lib, mergedBy("r2")))
	if got := component(res, "lib"); fmt.Sprint(got.LandedBy) != "[{zed 2} {amy 1}]" || got.Authors != 3 {
		t.Errorf("landed by = %v, authors %d; want most first, then by login, hidden left out but counted", got.LandedBy, got.Authors)
	}
}

func TestComponentEdgeCases(t *testing.T) {
	cfg := withComponents()
	res, _, _ := computeWith(t, cfg,
		pr(1, "alice", mergedBy("bob")),
		pr(2, "carol", files(store.File{Path: "lib/a.go", Additions: 1}), mergedBy("bob"), func(p *store.PullRequest) { p.BaseRefName = "feature-base" }),
	)
	if c := component(res, NoFileData); c.Landed != 1 {
		t.Errorf("PR without file data = %+v", c)
	}
	if c := component(res, "lib"); c.Landed != 0 || c.Opened != 1 {
		t.Errorf("stacked PR = %+v, want opened but not landed", c)
	}
}
