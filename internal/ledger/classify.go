package ledger

import (
	"path"
	"strings"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

// Kind is what a pull request is for.
type Kind string

// Pull request kinds. Only Human pull requests earn the counted units;
// reviews, merges and closes on the other kinds go to maintenance.
const (
	Human      Kind = "human"
	Dependency Kind = "dependency"
	Backport   Kind = "backport"
	Bot        Kind = "bot"
)

type classifier struct {
	cfg           config.Config
	defaultBranch string
	patterns      []string
	logins        map[string]bool
	approvals     []string
	merges        []string
}

func newClassifier(cfg config.Config, prs []store.PullRequest) classifier {
	c := classifier{cfg: cfg, defaultBranch: cfg.DefaultBranch, logins: map[string]bool{}}
	if c.defaultBranch == "" {
		c.defaultBranch = mostTargeted(prs)
	}
	for _, p := range cfg.Bots.Patterns {
		c.patterns = append(c.patterns, strings.ToLower(p))
	}
	for _, l := range cfg.Bots.Logins {
		c.logins[strings.ToLower(l)] = true
	}
	c.approvals = lowerAll(cfg.Comments.ApprovalCommands)
	c.merges = lowerAll(cfg.Comments.MergeCommands)
	return c
}

func mostTargeted(prs []store.PullRequest) string {
	counts := map[string]int{}
	best := ""
	for _, pr := range prs {
		counts[pr.BaseRefName]++
		n := counts[pr.BaseRefName]
		if n > counts[best] || (n == counts[best] && pr.BaseRefName < best) {
			best = pr.BaseRefName
		}
	}
	return best
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

func (c classifier) isBot(a *store.Actor) bool {
	if a == nil {
		return true
	}
	return a.Typename == "Bot" || c.isBotLogin(a.Login)
}

func (c classifier) isBotLogin(login string) bool {
	l := strings.ToLower(login)
	if l == "" || c.logins[l] {
		return true
	}
	for _, p := range c.patterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

func (c classifier) isBotItem(e store.TimelineItem) bool {
	return c.isBotLogin(e.Login()) || (e.Author != nil && e.Author.Typename == "Bot") || (e.Actor != nil && e.Actor.Typename == "Bot")
}

func (c classifier) kind(pr store.PullRequest) Kind {
	bp := c.cfg.PullRequests.Backport
	for _, p := range bp.HeadPrefixes {
		if strings.HasPrefix(pr.HeadRefName, p) {
			return Backport
		}
	}
	if pr.BaseRefName != c.defaultBranch {
		for _, g := range bp.BaseBranches {
			if ok, _ := path.Match(g, pr.BaseRefName); ok {
				return Backport
			}
		}
	}
	author := strings.ToLower(pr.AuthorLogin())
	for _, a := range c.cfg.PullRequests.Dependencies.Authors {
		if strings.Contains(author, strings.ToLower(a)) {
			return Dependency
		}
	}
	title := strings.ToLower(pr.Title)
	for _, p := range c.cfg.PullRequests.Dependencies.TitlePrefixes {
		if strings.HasPrefix(title, strings.ToLower(p)) {
			return Dependency
		}
	}
	if c.isBot(pr.Author) {
		return Bot
	}
	return Human
}

func (c classifier) isBotCommand(body string) bool {
	b := strings.ToLower(strings.TrimSpace(body))
	for _, p := range c.cfg.Comments.BotCommandPrefixes {
		if strings.HasPrefix(b, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func (c classifier) isApproval(body string) bool { return hasCommand(body, c.approvals) }

func (c classifier) isMergeCommand(body string) bool { return hasCommand(body, c.merges) }

func hasCommand(body string, cmds []string) bool {
	for _, line := range strings.Split(strings.ToLower(body), "\n") {
		line = strings.TrimSpace(line)
		for _, cmd := range cmds {
			if cmd == "" || !strings.HasPrefix(line, cmd) {
				continue
			}
			rest := line[len(cmd):]
			if rest != "" && isWordByte(cmd[len(cmd)-1]) && rest[0] != ' ' && rest[0] != '\t' {
				continue
			}
			if strings.Contains(rest, "cancel") {
				continue
			}
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || b == '-' || ('a' <= b && b <= 'z') || ('0' <= b && b <= '9')
}
