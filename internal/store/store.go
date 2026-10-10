// Package store reads pull request records in the JSON shape returned by
// the GitHub GraphQL API, one pull request per line.
package store

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// PullRequest is one pull request with the activity oss-chronicle needs.
type PullRequest struct {
	Number        int        `json:"number"`
	Title         string     `json:"title"`
	State         string     `json:"state"`
	IsDraft       bool       `json:"isDraft,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	MergedAt      *time.Time `json:"mergedAt"`
	Author        *Actor     `json:"author"`
	BaseRefName   string     `json:"baseRefName"`
	HeadRefName   string     `json:"headRefName"`
	Commits       Commits    `json:"commits"`
	Reviews       Reviews    `json:"reviews"`
	TimelineItems Timeline   `json:"timelineItems"`
	Files         *Files     `json:"files,omitempty"`
}

// Files holds the files a pull request changes. A nil *Files means they were not collected.
type Files struct {
	Nodes []File `json:"nodes"`
}

// File is one changed file with its line counts.
type File struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"changeType,omitempty"`
}

// Meta describes the repository a data file was collected from, so the ledger can be
// recomputed from the data alone.
type Meta struct {
	Repository    string    `json:"repository"`
	DefaultBranch string    `json:"default_branch"`
	GitAttributes string    `json:"gitattributes,omitempty"`
	Codeowners    string    `json:"codeowners,omitempty"`
	CollectedAt   time.Time `json:"collected_at"`
	CollectedFrom string    `json:"collected_from,omitempty"`
}

// ReadMeta reads a meta file written by WriteMeta.
func ReadMeta(path string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("read %s: %w", path, err)
	}
	return m, nil
}

// WriteMeta writes the meta file as indented JSON.
func WriteMeta(path string, m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Actor is a GitHub account. Login is empty for deleted accounts.
type Actor struct {
	Login    string `json:"login"`
	Typename string `json:"__typename,omitempty"`
}

// Commits holds the commits on a pull request branch.
type Commits struct {
	Nodes []CommitNode `json:"nodes"`
}

// CommitNode wraps one commit.
type CommitNode struct {
	Commit Commit `json:"commit"`
}

// Commit is a commit on a pull request branch.
type Commit struct {
	AuthoredDate time.Time   `json:"authoredDate"`
	Parents      Count       `json:"parents"`
	Author       *CommitUser `json:"author"`
}

// CommitUser links a commit author to a GitHub account when GitHub can match it.
type CommitUser struct {
	User *Actor `json:"user"`
}

// Count is a GraphQL connection total.
type Count struct {
	TotalCount int `json:"totalCount"`
}

// Reviews holds the reviews submitted on a pull request.
type Reviews struct {
	Nodes []Review `json:"nodes"`
}

// Review is one submitted review. SubmittedAt is nil for pending reviews.
type Review struct {
	Author      *Actor     `json:"author"`
	State       string     `json:"state"`
	SubmittedAt *time.Time `json:"submittedAt"`
	Body        string     `json:"body"`
	Comments    Count      `json:"comments"`
}

// Timeline holds timeline events of the types oss-chronicle fetches.
type Timeline struct {
	Nodes []TimelineItem `json:"nodes"`
}

// Timeline item type names, as GitHub reports them in __typename.
const (
	IssueComment            = "IssueComment"
	ClosedEvent             = "ClosedEvent"
	MergedEvent             = "MergedEvent"
	ReviewRequestedEvent    = "ReviewRequestedEvent"
	HeadRefForcePushedEvent = "HeadRefForcePushedEvent"
)

// TimelineItem is one timeline event. Comments carry Author, events carry Actor.
// Closer is set on closed events and says what closed the pull request.
type TimelineItem struct {
	Typename  string     `json:"__typename"`
	CreatedAt *time.Time `json:"createdAt"`
	Author    *Actor     `json:"author"`
	Actor     *Actor     `json:"actor"`
	Body      string     `json:"body,omitempty"`
	Closer    *Closer    `json:"closer,omitempty"`
}

// Closer is what closed a pull request. A Commit closer means the pull request's change
// was pushed to the base branch outside the merge button, as ghstack and mirror workflows do.
type Closer struct {
	Typename string `json:"__typename"`
}

// ClosedByCommit reports whether the item is a closed event fired by a pushed commit.
func (t TimelineItem) ClosedByCommit() bool {
	return t.Typename == ClosedEvent && t.Closer != nil && t.Closer.Typename == "Commit"
}

// Login returns the account that performed the item, or "" if unknown.
func (t TimelineItem) Login() string {
	if t.Author != nil {
		return t.Author.Login
	}
	if t.Actor != nil {
		return t.Actor.Login
	}
	return ""
}

// AuthorLogin returns the pull request author's login, or "" for a deleted account.
func (p PullRequest) AuthorLogin() string {
	if p.Author == nil {
		return ""
	}
	return p.Author.Login
}

// ReadFile reads pull requests from a JSON Lines file, gzipped when the name ends in .gz.
func ReadFile(path string) ([]PullRequest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("open gzip %s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}
	prs, err := Read(r)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return prs, nil
}

// WriteFile writes pull requests as JSON Lines, gzipped when the name ends in .gz.
func WriteFile(path string, prs []PullRequest) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	var w io.Writer = f
	if strings.HasSuffix(path, ".gz") {
		gz := gzip.NewWriter(f)
		defer func() {
			if cerr := gz.Close(); err == nil {
				err = cerr
			}
		}()
		w = gz
	}
	return Write(w, prs)
}

// Write encodes pull requests as JSON Lines.
func Write(w io.Writer, prs []PullRequest) error {
	enc := json.NewEncoder(w)
	for _, pr := range prs {
		if err := enc.Encode(pr); err != nil {
			return err
		}
	}
	return nil
}

// Read decodes pull requests from JSON Lines.
func Read(r io.Reader) ([]PullRequest, error) {
	var prs []PullRequest
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(strings.TrimSpace(string(b))) == 0 {
			continue
		}
		var pr PullRequest
		if err := json.Unmarshal(b, &pr); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		prs = append(prs, pr)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return prs, nil
}
