package ledger

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
)

func TestUnitsMatchTheDocs(t *testing.T) {
	doc, err := os.ReadFile("../../docs/how-it-counts.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range Units {
		if row := "| " + u.Label + " | " + u.Text + " |"; !strings.Contains(string(doc), row) {
			t.Errorf("docs/how-it-counts.md has no row %q", row)
		}
	}
}

func TestUnitKeysAreLedgerFields(t *testing.T) {
	var fields map[string]any
	if err := json.Unmarshal(must(json.Marshal(Person{})), &fields); err != nil {
		t.Fatal(err)
	}
	for _, u := range Units {
		if _, ok := fields[u.Key]; !ok {
			t.Errorf("unit %s: no %q field in a person's ledger entry", u.Label, u.Key)
		}
	}
}

func rulesByLabel(rules []Rule) map[string]Rule {
	m := map[string]Rule{}
	for _, r := range rules {
		m[r.Label] = r
	}
	return m
}

func TestDefaultRulesAreNotMarkedCustom(t *testing.T) {
	rules := rulesOf(config.Default())
	for _, r := range rules {
		if r.Custom || r.Values == nil {
			t.Errorf("rule %q: custom %v, values %v", r.Label, r.Custom, r.Values)
		}
	}
	m := rulesByLabel(rules)
	if v := m["Left out of counted lines"].Values; len(v) != 2 || v[0] != builtinExclusions {
		t.Errorf("exclusions = %v", v)
	}
	if v := m["Components, first match wins"].Values; strings.Join(v, ",") != "CODEOWNERS,the top-level directory" {
		t.Errorf("components = %v", v)
	}
	if v := m["Counted lines"].Values; strings.Join(v, ",") != "additions + 1 × deletions" {
		t.Errorf("counted lines = %v", v)
	}
}

func TestRulesShowWhatTheProjectChanged(t *testing.T) {
	cfg := config.Default()
	cfg.Comments.MergeCommands = []string{"/merge"}
	cfg.Bots.Logins = nil
	cfg.Size.ExcludeExtra = []string{"gen/**"}
	cfg.Size.TestWeight = 0.5
	cfg.Size.ReviewFeedback = 3
	cfg.Components.Map = []config.Component{{Name: "UI", Paths: []string{"/ui/"}}}
	cfg.Publish.OptOut = []string{"secret-login"}
	rules := rulesOf(cfg)
	m := rulesByLabel(rules)
	custom := map[string]bool{
		"Merge commands, crediting a bot's merge to the person who gave them": true,
		"Bots: login is":            true,
		"Left out of counted lines": true,
		"Counted lines":             true,
		"Reviewing weight multiplier for a review with feedback": true,
		"Components, first match wins":                           true,
	}
	for _, r := range rules {
		if r.Custom != custom[r.Label] {
			t.Errorf("rule %q: custom = %v", r.Label, r.Custom)
		}
	}
	if v := m["Bots: login is"].Values; v == nil || len(v) != 0 {
		t.Errorf("emptied bot logins = %#v, want an empty list", v)
	}
	if v := m["Left out of counted lines"].Values; strings.Join(v, "|") != builtinExclusions+"|gen/**|files .gitattributes marks generated, vendored or binary" {
		t.Errorf("exclusions = %v", v)
	}
	if v := m["Counted lines"].Values; len(v) != 2 || !strings.HasSuffix(v[1], "× 0.5") {
		t.Errorf("counted lines = %v", v)
	}
	if v := m["Components, first match wins"].Values; v[0] != "the project's component map" {
		t.Errorf("components = %v", v)
	}
	if strings.Contains(string(must(json.Marshal(rules))), "secret-login") {
		t.Error("rules name an opted-out login")
	}
}

func TestComputeRecordsTheRules(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultBranch = "main"
	cfg.Comments.MergeCommands = []string{"/ship"}
	res := Compute(nil, cfg, from, to)
	if r := rulesByLabel(res.Rules)["Merge commands, crediting a bot's merge to the person who gave them"]; !r.Custom || r.Values[0] != "/ship" {
		t.Errorf("merge command rule = %+v", r)
	}
}

func TestGeneratorFromActionPath(t *testing.T) {
	for dir, want := range map[string]string{
		"/home/runner/work/_actions/ppapapetrou76/oss-chronicle/main": "https://github.com/ppapapetrou76/oss-chronicle/blob/main/docs/how-it-counts.md",
		"/home/runner/work/_actions/me/fork/release/v1":               "https://github.com/me/fork/blob/release/v1/docs/how-it-counts.md",
		"/opt/_actions/runner/_work/_actions/me/fork/v1":              "https://github.com/me/fork/blob/v1/docs/how-it-counts.md",
		"/__w/_actions/me/fork/v1":                                    "https://github.com/me/fork/blob/v1/docs/how-it-counts.md",
		"/home/runner/work/_actions/me/fork/v1.2/":                    "https://github.com/me/fork/blob/v1.2/docs/how-it-counts.md",
		`D:\a\_actions\me\fork\0123abcd`:                              "https://github.com/me/fork/blob/0123abcd/docs/how-it-counts.md",
		"":                                                            DefaultGenerator.DocsURL(),
		"/home/runner/work/project/project":                           DefaultGenerator.DocsURL(),
		"/home/runner/work/_actions/me/fork":                          DefaultGenerator.DocsURL(),
		"/home/runner/work/_actions/../fork/main":                     DefaultGenerator.DocsURL(),
		"/home/runner/work/_actions/me/fork/-x":                       DefaultGenerator.DocsURL(),
		"/home/runner/work/_actions/me/fork/a..b":                     DefaultGenerator.DocsURL(),
		"/home/runner/work/_actions/me/fork/x?y#z":                    DefaultGenerator.DocsURL(),
	} {
		if got := GeneratorFromActionPath(dir).DocsURL(); got != want {
			t.Errorf("GeneratorFromActionPath(%q) links to %s, want %s", dir, got, want)
		}
	}
	if got := (Result{}).DocsURL(); got != DefaultGenerator.DocsURL() {
		t.Errorf("ledger without a generator links to %s", got)
	}
}

func TestRulesLeaveOutOptedOutLogins(t *testing.T) {
	cfg := config.Default()
	cfg.Bots.Logins = append(cfg.Bots.Logins, "Alice")
	cfg.Bots.Patterns = append(cfg.Bots.Patterns, "alice")
	cfg.PullRequests.Dependencies.Authors = append(cfg.PullRequests.Dependencies.Authors, "ALICE")
	cfg.Publish.OptOut = []string{"alice"}
	rules := rulesOf(cfg)
	if strings.Contains(strings.ToLower(string(must(json.Marshal(rules)))), "alice") {
		t.Error("rules name an opted-out login")
	}
	if !rulesByLabel(rules)["Bots: login is"].Custom {
		t.Error("a rule changed by the config is still marked, even when an opted-out value is hidden")
	}
}

func TestRulesIgnoreOrderAndCaseWhereMatchingDoes(t *testing.T) {
	cfg := config.Default()
	cfg.Bots.Logins = []string{"HOMU", "bors"}
	cfg.PullRequests.Backport.HeadPrefixes = []string{"Cherry-pick", "backport", "automated-cherry-pick-of-", "mergify/bp/"}
	m := rulesByLabel(rulesOf(cfg))
	if m["Bots: login is"].Custom {
		t.Error("bot logins differing only in order and case are marked as set by the project")
	}
	if !m["Backport pull requests: head branch starts with"].Custom {
		t.Error("head prefixes match case-sensitively, so a case change is a real change")
	}
}

func TestRulesMarkChangedSizeBuckets(t *testing.T) {
	cfg := config.Default()
	cfg.Size.Buckets[1].Weight = 1.5
	r := rulesByLabel(rulesOf(cfg))["Size buckets"]
	if !r.Custom || strings.Join(r.Values, "|") != "XS: up to 10 counted lines, weight 0.5|S: up to 50 counted lines, weight 1.5|M: up to 250 counted lines, weight 2|L: over 250 counted lines, weight 3" {
		t.Errorf("buckets = %+v", r)
	}
}

func TestBuiltinExclusionsDescribeTheDefaultList(t *testing.T) {
	want := []string{
		"go.sum", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb",
		"Cargo.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "Gemfile.lock", "composer.lock", "mix.lock",
		"vendor/**", "**/vendor/**", "node_modules/**", "**/node_modules/**",
		"*.pb.go", "*.pb.gw.go", "*_pb2.py", "*_pb2_grpc.py", "zz_generated*",
		"*.min.js", "*.min.css", "*.snap", "__snapshots__/**", "**/__snapshots__/**",
	}
	if !slices.Equal(config.Default().Size.Exclude, want) {
		t.Error("the default exclusions changed: update builtinExclusions to describe them, then this list")
	}
}
