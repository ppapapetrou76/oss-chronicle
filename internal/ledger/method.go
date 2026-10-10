package ledger

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
)

// Unit is one number the ledger reports per person, with the sentence that says how it is
// computed. The web page, the run summary and docs/how-it-counts.md use the same sentences.
type Unit struct {
	Key   string
	Label string
	Text  string
}

// Units lists the per-person numbers in the order the web page shows them. Key is the
// field's name in ledger.json.
var Units = []Unit{
	{Key: "total", Label: "Total",
		Text: "Landed + reviewed + comments + merges + triage. Maintenance and the weights are shown beside it and are not part of it."},
	{Key: "landed", Label: "Landed",
		Text: "Pull requests the person opened that landed on the default branch in the period: merged, or closed by the commit that landed them. One credit per pull request, replacing the commits, rebases and replies behind it. Maintenance pull requests are not counted."},
	{Key: "reviewed", Label: "Reviewed",
		Text: "Someone else's pull requests the person reviewed in the period, once per pull request however many reviews they left. A review is a GitHub review or an approval command. Maintenance pull requests are not counted."},
	{Key: "reviewed_with_feedback", Label: "Reviews with feedback",
		Text: "Reviewed pull requests where one of the person's reviews requested changes, left inline comments or had a written message."},
	{Key: "commented", Label: "Comments",
		Text: "Conversation comments on someone else's pull request in the period; inline review comments belong to the review. Bot and merge commands are not counted, and approval commands count as reviews. Maintenance pull requests are not counted."},
	{Key: "merged", Label: "Merges",
		Text: "Pull requests the person merged into the default branch in the period. When a bot merged, the credit goes to the person whose merge command it acted on, or with no command to the last person other than the author who approved. Maintenance pull requests are not counted."},
	{Key: "triaged", Label: "Triage",
		Text: "Closes of someone else's pull request without merging it, in the period; each close counts. A pull request that landed later is not counted. Maintenance pull requests are not counted."},
	{Key: "maintenance", Label: "Maintenance",
		Text: "Each GitHub review and close on someone else's maintenance pull request, and each merge of one, in the period. Maintenance pull requests are backports, dependency updates and pull requests a bot or a deleted account opened. Approval commands and comments on them are not counted. Not part of the total."},
	{Key: "authoring_weight", Label: "Authoring weight",
		Text: "The size weights of the person's landed pull requests, added up. A pull request's size comes from its counted lines, as the size table shows; one whose files could not be collected adds nothing."},
	{Key: "reviewing_weight", Label: "Reviewing weight",
		Text: "The size weights of the pull requests the person reviewed, added up, with a weight multiplied when their review left feedback. A pull request whose files could not be collected adds nothing."},
}

// Rule is one counting rule as the run applied it, so readers can check how their activity
// was classified. Custom marks a rule the project set differently from the default.
type Rule struct {
	Label  string   `json:"label"`
	Values []string `json:"values"`
	Custom bool     `json:"custom,omitempty"`
}

// Generator names the oss-chronicle source that computed a ledger.
type Generator struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}

// DefaultGenerator is used when the action's own repository and ref are not known, as in
// local runs.
var DefaultGenerator = Generator{Repository: "ppapapetrou76/oss-chronicle", Ref: "main"}

var pathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// GeneratorFromActionPath reads the action's repository and ref from the directory the
// runner downloaded it into, .../_actions/<owner>/<repo>/<ref>, where a ref with slashes
// spans several directories. Any other path, such as a local checkout used with uses: ./,
// gives DefaultGenerator.
func GeneratorFromActionPath(dir string) Generator {
	var segs []string
	for _, s := range strings.Split(strings.ReplaceAll(dir, `\`, "/"), "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	i := len(segs) - 1
	for i >= 0 && segs[i] != "_actions" {
		i--
	}
	if i < 0 || len(segs) < i+4 {
		return DefaultGenerator
	}
	for _, s := range segs[i+1:] {
		if !pathSegment.MatchString(s) || strings.Contains(s, "..") {
			return DefaultGenerator
		}
	}
	return Generator{Repository: segs[i+1] + "/" + segs[i+2], Ref: strings.Join(segs[i+3:], "/")}
}

// DocsURL is the "How it counts" page for the generator's version.
func (g Generator) DocsURL() string {
	return "https://github.com/" + g.Repository + "/blob/" + g.Ref + "/docs/how-it-counts.md"
}

// DocsURL is the "How it counts" page for the version that computed the ledger, or for
// DefaultGenerator when that is not recorded.
func (r Result) DocsURL() string {
	if r.Generator == nil {
		return DefaultGenerator.DocsURL()
	}
	return r.Generator.DocsURL()
}

const builtinExclusions = "the built-in list: lock files, vendored code, generated protobuf and deepcopy files, minified files and test snapshots"

func rulesOf(cfg config.Config) []Rule {
	def := config.Default()
	optOut := map[string]bool{}
	for _, l := range cfg.Publish.OptOut {
		optOut[strings.ToLower(l)] = true
	}
	list := func(label string, got, want []string, fold bool) Rule {
		values := []string{}
		for _, v := range got {
			if !optOut[strings.ToLower(v)] {
				values = append(values, v)
			}
		}
		return Rule{Label: label, Values: values, Custom: !sameSet(got, want, fold)}
	}
	pr, dpr := cfg.PullRequests, def.PullRequests
	rules := []Rule{
		list("Backport pull requests: head branch starts with", pr.Backport.HeadPrefixes, dpr.Backport.HeadPrefixes, false),
		list("Backport pull requests: target branch, other than the default branch, matches", pr.Backport.BaseBranches, dpr.Backport.BaseBranches, false),
		list("Dependency pull requests: author login contains", pr.Dependencies.Authors, dpr.Dependencies.Authors, true),
		list("Dependency pull requests: title starts with", pr.Dependencies.TitlePrefixes, dpr.Dependencies.TitlePrefixes, true),
		list("Bots, besides GitHub App accounts: login contains", cfg.Bots.Patterns, def.Bots.Patterns, true),
		list("Bots: login is", cfg.Bots.Logins, def.Bots.Logins, true),
		list("Bot commands, not counted as comments: comments starting with", cfg.Comments.BotCommandPrefixes, def.Comments.BotCommandPrefixes, true),
		list("Approval commands, counted as a review", cfg.Comments.ApprovalCommands, def.Comments.ApprovalCommands, true),
		list("Merge commands, crediting a bot's merge to the person who gave them", cfg.Comments.MergeCommands, def.Comments.MergeCommands, true),
	}

	s, ds := cfg.Size, def.Size
	excluded := Rule{Label: "Left out of counted lines", Custom: len(s.ExcludeExtra) > 0 || !slices.Equal(s.Exclude, ds.Exclude) || s.UseGitAttributes != ds.UseGitAttributes}
	if slices.Equal(s.Exclude, ds.Exclude) {
		excluded.Values = append(excluded.Values, builtinExclusions)
	} else {
		excluded.Values = append(excluded.Values, s.Exclude...)
	}
	excluded.Values = append(excluded.Values, s.ExcludeExtra...)
	if s.UseGitAttributes {
		excluded.Values = append(excluded.Values, "files .gitattributes marks generated, vendored or binary")
	}
	lines := Rule{Label: "Counted lines", Values: []string{"additions + " + decimal(s.DeletionsWeight) + " × deletions"},
		Custom: s.DeletionsWeight != ds.DeletionsWeight || s.TestWeight != ds.TestWeight || (s.TestWeight != 1 && !slices.Equal(s.TestPatterns, ds.TestPatterns))}
	if s.TestWeight != 1 {
		lines.Values = append(lines.Values, "lines in test files ("+strings.Join(s.TestPatterns, ", ")+") × "+decimal(s.TestWeight))
	}
	buckets := Rule{Label: "Size buckets", Custom: !slices.EqualFunc(s.Buckets, ds.Buckets, sameBucket)}
	lower := 0.0
	for i, b := range s.Buckets {
		switch {
		case b.Max == nil && i == 0:
			buckets.Values = append(buckets.Values, b.Name+": any number of counted lines, weight "+decimal(b.Weight))
			continue
		case b.Max == nil:
			buckets.Values = append(buckets.Values, b.Name+": over "+decimal(lower)+" counted lines, weight "+decimal(b.Weight))
			continue
		}
		buckets.Values = append(buckets.Values, b.Name+": up to "+decimal(*b.Max)+" counted lines, weight "+decimal(b.Weight))
		lower = *b.Max
	}
	rules = append(rules, excluded, lines, buckets,
		Rule{Label: "Reviewing weight multiplier for a review with feedback", Values: []string{decimal(s.ReviewFeedback)}, Custom: s.ReviewFeedback != ds.ReviewFeedback})

	comps := Rule{Label: "Components, first match wins", Custom: len(cfg.Components.Map) > 0 || cfg.Components.UseCodeowners != def.Components.UseCodeowners}
	if len(cfg.Components.Map) > 0 {
		comps.Values = append(comps.Values, "the project's component map")
	}
	if cfg.Components.UseCodeowners {
		comps.Values = append(comps.Values, "CODEOWNERS")
	}
	comps.Values = append(comps.Values, "the top-level directory")
	return append(rules, comps)
}

func sameSet(a, b []string, fold bool) bool {
	norm := func(ss []string) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			if fold {
				s = strings.ToLower(s)
			}
			out[i] = s
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(norm(a), norm(b))
}

func sameBucket(a, b config.Bucket) bool {
	return a.Name == b.Name && a.Weight == b.Weight && (a.Max == nil) == (b.Max == nil) && (a.Max == nil || *a.Max == *b.Max)
}

func decimal(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
