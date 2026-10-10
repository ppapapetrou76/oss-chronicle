package ledger

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/config"
	"github.com/ppapapetrou76/oss-chronicle/internal/glob"
	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

// Component names used when no rule names one.
const (
	RootFiles  = "(root files)"
	NoFiles    = "(no changed files)"
	NoFileData = "(no file data)"
)

// ComponentStats describes the human pull requests whose primary component is Name.
//
// Landed, Authors and MedianTimeToMergeHours cover pull requests that landed on the default
// branch in the window; Touched also counts those that changed the component without it being
// primary. Opened, MedianFirstResponseHours and NoResponse cover non-draft pull requests opened
// in the window, where a response is a review, comment, merge or close by anyone but the author
// and bots; NoResponse includes pull requests their author closed before anyone answered.
// Reviews are the counted (reviewer, pull request) pairs; Reviewers lists them per person, most
// first, leaving out people who opted out, ReviewerCount counts everyone, TopReviewer is empty
// when the top reviewer opted out, and ReviewersForHalf is how few people did half of them.
// LandedBy lists the authors of the landed pull requests, most first, leaving out people
// who opted out. Waiting lists open, non-draft pull requests that have had no response at all.
type ComponentStats struct {
	Name                     string              `json:"name"`
	Landed                   int                 `json:"landed"`
	Touched                  int                 `json:"touched"`
	Authors                  int                 `json:"authors"`
	LandedBy                 []ComponentAuthor   `json:"landed_by"`
	MedianTimeToMergeHours   *float64            `json:"median_time_to_merge_hours"`
	Opened                   int                 `json:"opened"`
	MedianFirstResponseHours *float64            `json:"median_first_response_hours"`
	NoResponse               int                 `json:"no_response"`
	Reviews                  int                 `json:"reviews"`
	ReviewerCount            int                 `json:"reviewer_count"`
	TopReviewer              string              `json:"top_reviewer"`
	TopReviewerShare         float64             `json:"top_reviewer_share"`
	ReviewersForHalf         int                 `json:"reviewers_for_half"`
	Reviewers                []ComponentReviewer `json:"reviewers"`
	Waiting                  []WaitingPR         `json:"waiting"`
}

// ComponentReviewer is one person's reviews in a component: a cell of the person ×
// component grid.
type ComponentReviewer struct {
	Login   string `json:"login"`
	Reviews int    `json:"reviews"`
}

// ComponentAuthor is how many pull requests one person landed in a component.
type ComponentAuthor struct {
	Login  string `json:"login"`
	Landed int    `json:"landed"`
}

// WaitingPR is an open pull request no one but its author has responded to.
// Author is empty when the author opted out of publication.
type WaitingPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type mapper struct {
	rules  []componentRule
	owners glob.Owners
	cache  map[string]string
}

type componentRule struct {
	name  string
	paths glob.Set
}

func newMapper(cfg config.Components, optOut map[string]bool) *mapper {
	m := &mapper{cache: map[string]string{}}
	for _, c := range cfg.Map {
		paths, _ := glob.CompileAll(c.Paths)
		m.rules = append(m.rules, componentRule{name: strings.TrimSpace(c.Name), paths: paths})
	}
	if cfg.UseCodeowners {
		m.owners = glob.ParseCodeowners(cfg.Codeowners, optOut)
	}
	return m
}

// component names the component a file belongs to: the first matching map entry, then its
// CODEOWNERS owners, then its top-level directory. Owners who opted out of publication are
// left out of the name, and a file whose owners all opted out falls back to its directory.
func (m *mapper) component(path string) string {
	if c, ok := m.cache[path]; ok {
		return c
	}
	c := m.lookup(path)
	m.cache[path] = c
	return c
}

func (m *mapper) lookup(path string) string {
	for _, r := range m.rules {
		if r.paths.Match(path) {
			return r.name
		}
	}
	if o := m.owners.Owner(path); o != "" {
		return o
	}
	if dir, _, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/"); ok {
		return dir
	}
	return RootFiles
}

// assign returns a pull request's primary component, where most of its counted lines
// changed, and every component it touches. When no counted lines changed, changed lines
// decide, then the number of files. Ties go to the name that sorts first.
func (t *tally) assign(pr store.PullRequest) (string, []string) {
	if pr.Files == nil {
		return NoFileData, []string{NoFileData}
	}
	if len(pr.Files.Nodes) == 0 {
		return NoFiles, []string{NoFiles}
	}
	counted, changed, files := map[string]float64{}, map[string]float64{}, map[string]float64{}
	for _, f := range pr.Files.Nodes {
		c := t.mapper.component(f.Path)
		lines, excluded := t.sizer.fileLines(f)
		if !excluded {
			counted[c] += lines
		}
		changed[c] += float64(f.Additions + f.Deletions)
		files[c]++
	}
	scores := files
	switch {
	case positive(counted):
		scores = counted
	case positive(changed):
		scores = changed
	}
	var touches []string
	for c, v := range scores {
		if v > 0 {
			touches = append(touches, c)
		}
	}
	sort.Strings(touches)
	primary := touches[0]
	for _, c := range touches[1:] {
		if scores[c] > scores[primary] {
			primary = c
		}
	}
	return primary, touches
}

func positive(m map[string]float64) bool {
	for _, v := range m {
		if v > 0 {
			return true
		}
	}
	return false
}

type componentTally struct {
	landed, touched, opened, noResponse int
	authors                             map[string]int
	toMerge, toResponse                 []float64
	waiting                             []WaitingPR
}

func (t *tally) comp(name string) *componentTally {
	c, ok := t.comps[name]
	if !ok {
		c = &componentTally{authors: map[string]int{}}
		t.comps[name] = c
	}
	return c
}

// addComponent records a human pull request in its components.
func (t *tally) addComponent(pr store.PullRequest, land landing, toDefault bool) {
	primary, touches := t.assign(pr)
	t.primary[pr.Number] = primary
	c := t.comp(primary)
	author := pr.AuthorLogin()
	responded := t.firstResponse(pr)
	if t.inWindow(&pr.CreatedAt) && !pr.IsDraft {
		c.opened++
		if responded != nil {
			c.toResponse = append(c.toResponse, responded.Sub(pr.CreatedAt).Hours())
		} else {
			c.noResponse++
		}
	}
	if land.at != nil && toDefault && t.inWindow(land.at) {
		c.landed++
		c.authors[author]++
		c.toMerge = append(c.toMerge, land.at.Sub(pr.CreatedAt).Hours())
		for _, name := range touches {
			t.comp(name).touched++
		}
	}
	if pr.State == "OPEN" && !pr.IsDraft && responded == nil {
		c.waiting = append(c.waiting, WaitingPR{Number: pr.Number, Title: pr.Title, Author: author, CreatedAt: pr.CreatedAt})
	}
}

// firstResponse is when someone other than the author and bots first reviewed, commented on,
// merged or closed the pull request, or nil if no one has.
func (t *tally) firstResponse(pr store.PullRequest) *time.Time {
	author := pr.AuthorLogin()
	var first *time.Time
	consider := func(at *time.Time, login string) {
		if at == nil || login == "" || login == author {
			return
		}
		if first == nil || at.Before(*first) {
			first = at
		}
	}
	for _, r := range pr.Reviews.Nodes {
		if !t.c.isBot(r.Author) {
			consider(r.SubmittedAt, r.Author.Login)
		}
	}
	for _, e := range pr.TimelineItems.Nodes {
		switch e.Typename {
		case store.IssueComment, store.ClosedEvent, store.MergedEvent:
			if !t.c.isBotItem(e) {
				consider(e.CreatedAt, e.Login())
			}
		}
	}
	return first
}

func (t *tally) componentStats(optOut map[string]bool) []ComponentStats {
	reviews := map[string]map[string]int{}
	for k := range t.reviews {
		name, ok := t.primary[k.number]
		if !ok {
			continue
		}
		if reviews[name] == nil {
			reviews[name] = map[string]int{}
			t.comp(name)
		}
		reviews[name][k.login]++
	}
	out := []ComponentStats{}
	for name, c := range t.comps {
		s := ComponentStats{
			Name:                     name,
			Landed:                   c.landed,
			Touched:                  c.touched,
			Authors:                  len(c.authors),
			MedianTimeToMergeHours:   median(c.toMerge),
			Opened:                   c.opened,
			MedianFirstResponseHours: median(c.toResponse),
			NoResponse:               c.noResponse,
			LandedBy:                 []ComponentAuthor{},
			Reviewers:                []ComponentReviewer{},
			Waiting:                  []WaitingPR{},
		}
		for login, n := range c.authors {
			if !optOut[strings.ToLower(login)] {
				s.LandedBy = append(s.LandedBy, ComponentAuthor{Login: login, Landed: n})
			}
		}
		sort.Slice(s.LandedBy, func(i, j int) bool {
			a, b := s.LandedBy[i], s.LandedBy[j]
			if a.Landed != b.Landed {
				return a.Landed > b.Landed
			}
			return a.Login < b.Login
		})
		for login, n := range reviews[name] {
			s.Reviews += n
			s.Reviewers = append(s.Reviewers, ComponentReviewer{Login: login, Reviews: n})
		}
		sort.Slice(s.Reviewers, func(i, j int) bool {
			a, b := s.Reviewers[i], s.Reviewers[j]
			if a.Reviews != b.Reviews {
				return a.Reviews > b.Reviews
			}
			return a.Login < b.Login
		})
		s.ReviewerCount = len(s.Reviewers)
		if s.Reviews > 0 {
			if top := s.Reviewers[0].Login; !optOut[strings.ToLower(top)] {
				s.TopReviewer = top
			}
			s.TopReviewerShare = math.Round(1000*float64(s.Reviewers[0].Reviews)/float64(s.Reviews)) / 1000
			sum := 0
			for _, r := range s.Reviewers {
				s.ReviewersForHalf++
				if sum += r.Reviews; 2*sum >= s.Reviews {
					break
				}
			}
		}
		published := s.Reviewers[:0]
		for _, r := range s.Reviewers {
			if !optOut[strings.ToLower(r.Login)] {
				published = append(published, r)
			}
		}
		s.Reviewers = published
		for _, w := range c.waiting {
			if optOut[strings.ToLower(w.Author)] {
				w.Author = ""
			}
			s.Waiting = append(s.Waiting, w)
		}
		sort.Slice(s.Waiting, func(i, j int) bool {
			a, b := s.Waiting[i], s.Waiting[j]
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.Number < b.Number
		})
		if s.Touched+s.Opened+s.Reviews+len(s.Waiting) > 0 {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Landed != b.Landed {
			return a.Landed > b.Landed
		}
		if a.Reviews != b.Reviews {
			return a.Reviews > b.Reviews
		}
		return a.Name < b.Name
	})
	return out
}

// median returns the median in hours, rounded to one decimal, or nil for no values.
func median(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := s[len(s)/2]
	if len(s)%2 == 0 {
		m = (s[len(s)/2-1] + m) / 2
	}
	m = math.Round(m*10) / 10
	return &m
}
