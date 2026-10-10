// Package github collects pull request activity from the GitHub GraphQL API
// in the shape the store package reads.
package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

// DefaultEndpoint is the public GitHub GraphQL endpoint. Actions runners set
// GITHUB_GRAPHQL_URL, which also covers GitHub Enterprise Server.
const DefaultEndpoint = "https://api.github.com/graphql"

const (
	maxPageSize  = 25
	minPageSize  = 2
	maxAttempts  = 6
	pageAttempts = 2

	growAfter         = 5
	maxRateLimitWaits = 10
	secondaryWait     = time.Minute
)

// Client fetches pull request activity for one repository.
type Client struct {
	Endpoint string
	Token    string
	HTTP     *http.Client
	Log      io.Writer
	Sleep    func(context.Context, time.Duration) error
	PageSize int
	Now      func() time.Time

	okStreak int
	ceiling  int
}

// NewClient returns a client for the endpoint in GITHUB_GRAPHQL_URL, or the public API.
func NewClient(token string) *Client {
	endpoint := os.Getenv("GITHUB_GRAPHQL_URL")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{Endpoint: endpoint, Token: token, HTTP: &http.Client{Timeout: 90 * time.Second}, Log: io.Discard}
}

// Collection is the result of one collection run. GitAttributes and Codeowners are the
// text of the default branch's .gitattributes and CODEOWNERS, empty when there is none.
type Collection struct {
	DefaultBranch string
	GitAttributes string
	Codeowners    string
	PullRequests  []store.PullRequest
	Requests      int
	Cost          int
}

const fileFields = `pageInfo{hasNextPage endCursor} nodes{path additions deletions changeType}`
const commitFields = `pageInfo{hasNextPage endCursor} nodes{commit{authoredDate parents{totalCount} author{user{login __typename}}}}`
const reviewFields = `pageInfo{hasNextPage endCursor} nodes{author{login __typename} state submittedAt body comments{totalCount}}`
const timelineFields = `pageInfo{hasNextPage endCursor} nodes{__typename
 ... on IssueComment{createdAt body author{login __typename}}
 ... on ClosedEvent{createdAt actor{login __typename} closer{__typename}}
 ... on MergedEvent{createdAt actor{login __typename}}
 ... on ReviewRequestedEvent{createdAt actor{login __typename}}
 ... on HeadRefForcePushedEvent{createdAt actor{login __typename}}}`
const timelineArgs = `itemTypes:[ISSUE_COMMENT,CLOSED_EVENT,MERGED_EVENT,REVIEW_REQUESTED_EVENT,HEAD_REF_FORCE_PUSHED_EVENT]`

const repoQuery = `query($owner:String!,$name:String!){
 rateLimit{cost remaining resetAt}
 repository(owner:$owner,name:$name){
  defaultBranchRef{name}
  gitattributes: object(expression:"HEAD:.gitattributes"){...blob}
  codeownersGithub: object(expression:"HEAD:.github/CODEOWNERS"){...blob}
  codeownersRoot: object(expression:"HEAD:CODEOWNERS"){...blob}
  codeownersDocs: object(expression:"HEAD:docs/CODEOWNERS"){...blob}}}
fragment blob on GitObject{... on Blob{text isTruncated isBinary}}`

const pageQuery = `query($owner:String!,$name:String!,$first:Int!,$after:String){
 rateLimit{cost remaining resetAt}
 repository(owner:$owner,name:$name){
  pullRequests(first:$first,after:$after,orderBy:{field:UPDATED_AT,direction:DESC}){
   pageInfo{hasNextPage endCursor}
   nodes{number title state isDraft createdAt updatedAt mergedAt baseRefName headRefName author{login __typename}
    files(first:100){` + fileFields + `}
    commits(first:100){` + commitFields + `}
    reviews(first:100){` + reviewFields + `}
    timelineItems(first:100,` + timelineArgs + `){` + timelineFields + `}}}}}`

// collectorRevision changes Fingerprint when what Collect stores changes in a way the
// query text does not show.
const collectorRevision = "1"

// Fingerprint identifies what Collect fetches, so pull requests stored by one version are
// not mixed with those from a version that fetches different fields.
func Fingerprint() string {
	sum := sha256.Sum256([]byte(collectorRevision + pageQuery))
	return hex.EncodeToString(sum[:4])
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type connection struct {
	PageInfo pageInfo        `json:"pageInfo"`
	Nodes    json.RawMessage `json:"nodes"`
}

type prNode struct {
	store.PullRequest
	FilesPage    connection `json:"files"`
	CommitsPage  connection `json:"commits"`
	ReviewsPage  connection `json:"reviews"`
	TimelinePage connection `json:"timelineItems"`
}

type rateLimit struct {
	Cost      int    `json:"cost"`
	Remaining int    `json:"remaining"`
	ResetAt   string `json:"resetAt"`
}

// Collect fetches every pull request updated on or after since, newest first,
// with all its commits, reviews and the timeline events oss-chronicle counts.
// Pull requests updated while the run is in progress move behind the cursor, so a
// second pass collects everything updated since the run started; duplicates keep
// the newer copy.
func (c *Client) Collect(ctx context.Context, repo string, since time.Time) (*Collection, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("repository %q must look like owner/name", repo)
	}
	if c.PageSize <= 0 || c.PageSize > maxPageSize {
		c.PageSize = maxPageSize
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.okStreak, c.ceiling = 0, maxPageSize
	col := &Collection{}
	if err := c.repoInfo(ctx, col, owner, name, repo); err != nil {
		return nil, err
	}
	started := now()
	byNumber := map[int]int{}
	for _, cutoff := range []time.Time{since, started} {
		prs, err := c.pass(ctx, col, owner, name, repo, cutoff)
		if err != nil {
			return nil, err
		}
		for _, pr := range prs {
			if i, seen := byNumber[pr.Number]; seen {
				if !pr.UpdatedAt.Before(col.PullRequests[i].UpdatedAt) {
					col.PullRequests[i] = pr
				}
				continue
			}
			byNumber[pr.Number] = len(col.PullRequests)
			col.PullRequests = append(col.PullRequests, pr)
		}
	}
	return col, nil
}

type blob struct {
	Text        *string `json:"text"`
	IsTruncated bool    `json:"isTruncated"`
	IsBinary    *bool   `json:"isBinary"`
}

// read returns the blob's text, logging why it cannot be used. A nil blob is a missing file.
func (b *blob) read(log io.Writer, file, use string) string {
	switch {
	case b == nil:
		return ""
	case b.Text == nil || (b.IsBinary != nil && *b.IsBinary):
		fmt.Fprintf(log, "warning: %s is not readable as text; %s\n", file, use)
	case b.IsTruncated:
		fmt.Fprintf(log, "warning: %s is too large to read in full; %s\n", file, use)
	default:
		return *b.Text
	}
	return ""
}

func (c *Client) repoInfo(ctx context.Context, col *Collection, owner, name, repo string) error {
	var resp struct {
		Repository *struct {
			DefaultBranchRef *struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
			GitAttributes    *blob `json:"gitattributes"`
			CodeownersGithub *blob `json:"codeownersGithub"`
			CodeownersRoot   *blob `json:"codeownersRoot"`
			CodeownersDocs   *blob `json:"codeownersDocs"`
		} `json:"repository"`
	}
	if err := c.query(ctx, col, repoQuery, map[string]any{"owner": owner, "name": name}, &resp, maxAttempts); err != nil {
		return err
	}
	if resp.Repository == nil {
		return fmt.Errorf("repository %s not found or not readable with this token", repo)
	}
	if ref := resp.Repository.DefaultBranchRef; ref != nil {
		col.DefaultBranch = ref.Name
	}
	r := resp.Repository
	col.GitAttributes = r.GitAttributes.read(c.Log, ".gitattributes", "its generated-file rules are not applied")
	for _, f := range []struct {
		path string
		b    *blob
	}{{".github/CODEOWNERS", r.CodeownersGithub}, {"CODEOWNERS", r.CodeownersRoot}, {"docs/CODEOWNERS", r.CodeownersDocs}} {
		if f.b != nil {
			col.Codeowners = f.b.read(c.Log, f.path, "use_codeowners cannot name components after it")
			break
		}
	}
	return nil
}

func (c *Client) pass(ctx context.Context, col *Collection, owner, name, repo string, since time.Time) ([]store.PullRequest, error) {
	var prs []store.PullRequest
	var after *string
	for {
		var resp struct {
			RateLimit  rateLimit `json:"rateLimit"`
			Repository *struct {
				PullRequests struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []prNode `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		}
		err := c.queryPage(ctx, col, pageQuery, map[string]any{"owner": owner, "name": name, "after": after}, &resp)
		if err != nil {
			return nil, err
		}
		if resp.Repository == nil {
			return nil, fmt.Errorf("repository %s not found or not readable with this token", repo)
		}
		done := false
		for _, n := range resp.Repository.PullRequests.Nodes {
			if n.UpdatedAt.Before(since) {
				done = true
				break
			}
			pr, err := c.complete(ctx, col, owner, name, n)
			if err != nil {
				return nil, err
			}
			prs = append(prs, pr)
		}
		page := resp.Repository.PullRequests.PageInfo
		if len(prs) > 0 {
			fmt.Fprintf(c.Log, "fetched %d pull requests (updated back to %s), rate limit remaining %d\n",
				len(prs), prs[len(prs)-1].UpdatedAt.Format(time.DateOnly), resp.RateLimit.Remaining)
		}
		if done || !page.HasNextPage {
			return prs, nil
		}
		after = &page.EndCursor
	}
}

// complete decodes the first page of each nested connection and fetches the rest.
func (c *Client) complete(ctx context.Context, col *Collection, owner, name string, n prNode) (store.PullRequest, error) {
	pr := n.PullRequest
	steps := []struct {
		field, args, fields string
		first               connection
		decode              func(json.RawMessage) error
	}{
		{"files", "", fileFields, n.FilesPage, func(b json.RawMessage) error {
			if pr.Files == nil {
				pr.Files = &store.Files{}
			}
			var nodes []store.File
			err := json.Unmarshal(b, &nodes)
			pr.Files.Nodes = append(pr.Files.Nodes, nodes...)
			return err
		}},
		{"commits", "", commitFields, n.CommitsPage, func(b json.RawMessage) error {
			var nodes []store.CommitNode
			err := json.Unmarshal(b, &nodes)
			pr.Commits.Nodes = append(pr.Commits.Nodes, nodes...)
			return err
		}},
		{"reviews", "", reviewFields, n.ReviewsPage, func(b json.RawMessage) error {
			var nodes []store.Review
			err := json.Unmarshal(b, &nodes)
			pr.Reviews.Nodes = append(pr.Reviews.Nodes, nodes...)
			return err
		}},
		{"timelineItems", timelineArgs + ",", timelineFields, n.TimelinePage, func(b json.RawMessage) error {
			var nodes []store.TimelineItem
			err := json.Unmarshal(b, &nodes)
			pr.TimelineItems.Nodes = append(pr.TimelineItems.Nodes, nodes...)
			return err
		}},
	}
	pr.Files = nil
	pr.Commits.Nodes, pr.Reviews.Nodes, pr.TimelineItems.Nodes = nil, nil, nil
	for _, s := range steps {
		conn := s.first
		q := fmt.Sprintf(`query($owner:String!,$name:String!,$number:Int!,$after:String){
 rateLimit{cost remaining resetAt}
 repository(owner:$owner,name:$name){pullRequest(number:$number){%s(%sfirst:100,after:$after){%s}}}}`, s.field, s.args, s.fields)
		for {
			if len(conn.Nodes) > 0 && string(conn.Nodes) != "null" {
				if err := s.decode(conn.Nodes); err != nil {
					return pr, fmt.Errorf("pull request #%d %s: %w", pr.Number, s.field, err)
				}
			}
			if !conn.PageInfo.HasNextPage {
				break
			}
			var resp struct {
				Repository struct {
					PullRequest map[string]*connection `json:"pullRequest"`
				} `json:"repository"`
			}
			vars := map[string]any{"owner": owner, "name": name, "number": pr.Number, "after": conn.PageInfo.EndCursor}
			if err := c.query(ctx, col, q, vars, &resp, maxAttempts); err != nil {
				return pr, fmt.Errorf("pull request #%d %s: %w", pr.Number, s.field, err)
			}
			next := resp.Repository.PullRequest[s.field]
			if next == nil {
				if s.field == "files" {
					pr.Files = nil
					fmt.Fprintf(c.Log, "pull request #%d: GitHub returned only part of the changed files; it is left unsized\n", pr.Number)
					break
				}
				fmt.Fprintf(c.Log, "pull request #%d %s: GitHub returned no further page; keeping what was fetched\n", pr.Number, s.field)
				break
			}
			conn = *next
		}
	}
	return pr, nil
}

// queryPage runs a pull request page query. It halves the page size when GitHub keeps
// timing out on large pages, and doubles it again after growAfter pages in a row succeed,
// staying below three quarters of the smallest size that timed out.
func (c *Client) queryPage(ctx context.Context, col *Collection, q string, vars map[string]any, out any) error {
	for {
		vars["first"] = c.PageSize
		attempts := pageAttempts
		if c.PageSize <= minPageSize {
			attempts = maxAttempts
		}
		err := c.query(ctx, col, q, vars, out, attempts)
		if err == nil {
			if c.okStreak++; c.okStreak >= growAfter && c.PageSize < c.ceiling {
				c.PageSize, c.okStreak = min(c.ceiling, c.PageSize*2), 0
			}
			return nil
		}
		var te *transientError
		if !errors.As(err, &te) || te.rateLimited || c.PageSize <= minPageSize {
			return err
		}
		c.ceiling = max(minPageSize, min(c.ceiling, c.PageSize*3/4))
		c.PageSize, c.okStreak = max(minPageSize, c.PageSize/2), 0
		fmt.Fprintf(c.Log, "%v; retrying with %d pull requests per page\n", err, c.PageSize)
	}
}

// transientError is a failure worth retrying. Rate limits carry the wait GitHub asked for
// and do not count against the retry budget.
type transientError struct {
	msg         string
	rateLimited bool
	wait        time.Duration
}

func (e *transientError) Error() string { return e.msg }

// query sends one GraphQL request, retrying transient failures and waiting out rate limits.
func (c *Client) query(ctx context.Context, col *Collection, q string, vars map[string]any, out any, attempts int) error {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	failures, waits := 0, 0
	for {
		err := c.do(ctx, col, body, out)
		var te *transientError
		if err == nil || !errors.As(err, &te) {
			return err
		}
		wait := te.wait
		if te.rateLimited {
			waits++
			if waits > maxRateLimitWaits {
				return fmt.Errorf("still rate limited after %d waits: %w", maxRateLimitWaits, err)
			}
		} else {
			failures++
			if failures >= attempts {
				return err
			}
			wait = time.Duration(failures*failures) * time.Second
		}
		fmt.Fprintf(c.Log, "%v; retrying in %s\n", err, wait.Round(time.Second))
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) do(ctx context.Context, col *Collection, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "oss-chronicle")
	col.Requests++
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &transientError{msg: err.Error()}
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return &transientError{msg: err.Error()}
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return errors.New("GitHub rejected the token (401)")
	case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests:
		if wait, limited := rateLimitWait(res.Header, b); limited {
			return &transientError{msg: fmt.Sprintf("rate limited (%d)", res.StatusCode), rateLimited: true, wait: wait}
		}
		return fmt.Errorf("GitHub refused the request (%d): %s", res.StatusCode, truncate(b))
	case res.StatusCode >= 500:
		return &transientError{msg: fmt.Sprintf("GitHub returned %d", res.StatusCode)}
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("GitHub returned %d: %s", res.StatusCode, truncate(b))
	}

	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	hasData := len(env.Data) > 0 && string(env.Data) != "null"
	for _, e := range env.Errors {
		switch {
		case e.Type == "RATE_LIMITED":
			wait, _ := rateLimitWait(res.Header, nil)
			return &transientError{msg: "GraphQL rate limit reached", rateLimited: true, wait: max(wait, secondaryWait)}
		case strings.Contains(strings.ToLower(e.Message), "timeout") || strings.Contains(e.Message, "Something went wrong"):
			return &transientError{msg: "GitHub: " + e.Message}
		case !hasData || len(e.Path) == 0:
			return fmt.Errorf("GitHub: %s", e.Message)
		}
	}
	for _, e := range env.Errors {
		fmt.Fprintf(c.Log, "warning: GitHub left out %v: %s\n", e.Path, e.Message)
	}
	var rl struct {
		RateLimit rateLimit `json:"rateLimit"`
	}
	if json.Unmarshal(env.Data, &rl) == nil {
		col.Cost += rl.RateLimit.Cost
	}
	return json.Unmarshal(env.Data, out)
}

// rateLimitWait reports whether a 403 or 429 is a rate limit and how long to wait:
// Retry-After when given, the reset time when the primary limit is spent, and a minute
// for secondary limits that give neither.
func rateLimitWait(h http.Header, body []byte) (time.Duration, bool) {
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return time.Duration(n) * time.Second, true
		}
	}
	if h.Get("X-Ratelimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			return max(time.Until(time.Unix(n, 0))+time.Second, time.Second), true
		}
	}
	if strings.Contains(strings.ToLower(string(body)), "rate limit") {
		return secondaryWait, true
	}
	return 0, false
}

func truncate(b []byte) string {
	const n = 300
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
