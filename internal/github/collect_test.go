package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ppapapetrou76/oss-chronicle/internal/store"
)

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func prJSON(number int, updated string, extraTimeline bool) string {
	timeline := fmt.Sprintf(`{"pageInfo":{"hasNextPage":%t,"endCursor":"t1"},"nodes":[{"__typename":"MergedEvent","createdAt":"%s","actor":{"login":"bob","__typename":"User"}}]}`, extraTimeline, updated)
	return fmt.Sprintf(`{"number":%d,"title":"fix: %d","state":"MERGED","createdAt":"%s","updatedAt":"%s","mergedAt":"%s","baseRefName":"main","headRefName":"f%d",
"author":{"login":"alice","__typename":"User"},
"commits":{"pageInfo":{"hasNextPage":false},"nodes":[{"commit":{"authoredDate":"%s","parents":{"totalCount":1},"author":{"user":{"login":"alice","__typename":"User"}}}}]},
"reviews":{"pageInfo":{"hasNextPage":false},"nodes":[]},
"timelineItems":%s}`, number, number, updated, updated, updated, number, updated, timeline)
}

func page(hasNext bool, cursor string, prs ...string) string {
	return fmt.Sprintf(`{"data":{"rateLimit":{"cost":1,"remaining":4999},"repository":{"defaultBranchRef":{"name":"trunk"},"pullRequests":{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}}}}`,
		hasNext, cursor, strings.Join(prs, ","))
}

const repoInfoJSON = `{"data":{"rateLimit":{"cost":1,"remaining":4999},"repository":{"defaultBranchRef":{"name":"trunk"},"gitattributes":{"text":"gen/** linguist-generated\n","isTruncated":false,"isBinary":false},"codeownersGithub":null,"codeownersRoot":{"text":"/ui/ @o/ui\n","isTruncated":false,"isBinary":false},"codeownersDocs":{"text":"docs/ @o/docs\n","isTruncated":false,"isBinary":false}}}}`

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	return newTestClientWithRepo(t, repoInfoJSON, h)
}

// newTestClientWithRepo answers the one-time repository query with repoBody and
// passes every other request to h.
func newTestClientWithRepo(t *testing.T, repoBody string, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "gitattributes:") && repoBody != "" {
			io.WriteString(w, repoBody)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient("tok")
	c.Endpoint = srv.URL
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func decode(t *testing.T, r *http.Request) gqlRequest {
	t.Helper()
	var req gqlRequest
	b, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestCollectPagesUntilOlderThanSince(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "bearer tok" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		req := decode(t, r)
		switch calls.Add(1) {
		case 1:
			if req.Variables["after"] != nil {
				t.Errorf("first page after = %v", req.Variables["after"])
			}
			io.WriteString(w, page(true, "c1", prJSON(3, "2026-10-01T00:00:00Z", false), prJSON(2, "2026-09-01T00:00:00Z", false)))
		case 2:
			if req.Variables["after"] != "c1" {
				t.Errorf("second page after = %v", req.Variables["after"])
			}
			io.WriteString(w, page(true, "c2", prJSON(1, "2026-06-01T00:00:00Z", false)))
		case 3:
			if req.Variables["after"] != nil {
				t.Errorf("catch-up pass should start from the top, after = %v", req.Variables["after"])
			}
			io.WriteString(w, page(true, "c1", prJSON(3, "2026-10-01T00:00:00Z", false)))
		default:
			t.Error("fetched past the since date")
		}
	})
	col, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(col.PullRequests) != 2 || col.DefaultBranch != "trunk" {
		t.Fatalf("got %d PRs, default %q", len(col.PullRequests), col.DefaultBranch)
	}
	pr := col.PullRequests[0]
	if pr.Number != 3 || len(pr.Commits.Nodes) != 1 || pr.Author.Typename != "User" || len(pr.TimelineItems.Nodes) != 1 {
		t.Errorf("pr = %+v", pr)
	}
	if col.GitAttributes != "gen/** linguist-generated\n" {
		t.Errorf("gitattributes = %q", col.GitAttributes)
	}
	if col.Codeowners != "/ui/ @o/ui\n" {
		t.Errorf("codeowners = %q, want the root file (no .github/CODEOWNERS)", col.Codeowners)
	}
	if col.Cost != 4 || col.Requests != 4 {
		t.Errorf("cost = %d, requests = %d", col.Cost, col.Requests)
	}
}

func TestCollectFetchesRemainingNestedPages(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		req := decode(t, r)
		if !strings.Contains(req.Query, "pullRequest(number:") {
			io.WriteString(w, page(false, "", prJSON(7, "2026-10-01T00:00:00Z", true)))
			return
		}
		if req.Variables["number"] != float64(7) || req.Variables["after"] != "t1" || !strings.Contains(req.Query, "itemTypes:") {
			t.Errorf("follow-up query vars = %v", req.Variables)
		}
		io.WriteString(w, `{"data":{"repository":{"pullRequest":{"timelineItems":{"pageInfo":{"hasNextPage":false},"nodes":[
{"__typename":"ClosedEvent","createdAt":"2026-10-01T00:00:00Z","actor":{"login":"carol"},"closer":{"__typename":"Commit"}}]}}}}}`)
	})
	col, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	items := col.PullRequests[0].TimelineItems.Nodes
	if len(items) != 2 || !items[1].ClosedByCommit() {
		t.Errorf("timeline = %+v", items)
	}
}

func TestCollectShrinksPageOnTimeouts(t *testing.T) {
	var sizes []float64
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		req := decode(t, r)
		size := req.Variables["first"].(float64)
		sizes = append(sizes, size)
		if size > 6 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, page(false, ""))
	})
	if _, err := c.Collect(context.Background(), "o/r", time.Now()); err != nil {
		t.Fatal(err)
	}
	want := []float64{25, 25, 12, 12, 6, 6}
	if fmt.Sprint(sizes) != fmt.Sprint(want) {
		t.Errorf("page sizes = %v, want %v", sizes, want)
	}
}

func TestCollectWaitsOutRateLimit(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		io.WriteString(w, page(false, ""))
	})
	var slept []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	if _, err := c.Collect(context.Background(), "o/r", time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 42*time.Second {
		t.Errorf("slept = %v, want [42s]", slept)
	}
}

func TestCollectReportsPermanentErrors(t *testing.T) {
	tests := []struct {
		name, want string
		h          http.HandlerFunc
	}{
		{"bad token", "401", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }},
		{"graphql error", "Could not resolve", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`)
		}},
		{"missing repository", "not found", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"data":{"repository":null}}`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestClientWithRepo(t, "", tt.h).Collect(context.Background(), "o/r", time.Now())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
	if _, err := NewClient("x").Collect(context.Background(), "no-slash", time.Now()); err == nil {
		t.Error("want error for a malformed repository")
	}
}

func TestRateLimitOnLastAttemptWaitsAndKeepsPageSize(t *testing.T) {
	var calls atomic.Int32
	var sizes []float64
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		sizes = append(sizes, decode(t, r).Variables["first"].(float64))
		if calls.Add(1) <= 3 {
			w.Header().Set("X-Ratelimit-Remaining", "4000")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"message":"You have exceeded a secondary rate limit."}`)
			return
		}
		io.WriteString(w, page(false, ""))
	})
	var slept []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	if _, err := c.Collect(context.Background(), "o/r", time.Now()); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(slept) != "[1m0s 1m0s 1m0s]" {
		t.Errorf("slept = %v, want three one-minute waits", slept)
	}
	for _, s := range sizes {
		if s != maxPageSize {
			t.Fatalf("page sizes = %v, rate limits must not shrink the page", sizes)
		}
	}
}

func TestGraphQLRateLimitWithStatus200(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			io.WriteString(w, `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`)
			return
		}
		io.WriteString(w, page(false, ""))
	})
	var slept []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	if _, err := c.Collect(context.Background(), "o/r", time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] < time.Minute {
		t.Errorf("slept = %v", slept)
	}
}

func TestPartialGraphQLErrorsKeepTheData(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := page(false, "", prJSON(1, "2026-10-01T00:00:00Z", false))
		body = strings.TrimSuffix(body, "}") + `,"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration","path":["repository","pullRequests","nodes",0,"timelineItems","nodes",0,"closer"]}]}`
		io.WriteString(w, body)
	})
	var log strings.Builder
	c.Log = &log
	col, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(col.PullRequests) != 1 || !strings.Contains(log.String(), "Resource not accessible") {
		t.Errorf("got %d PRs, log %q", len(col.PullRequests), log.String())
	}
}

func TestNullFollowUpPageKeepsWhatWasFetched(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(decode(t, r).Query, "pullRequest(number:") {
			io.WriteString(w, `{"data":{"repository":{"pullRequest":null}}}`)
			return
		}
		io.WriteString(w, page(false, "", prJSON(7, "2026-10-01T00:00:00Z", true)))
	})
	col, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(col.PullRequests[0].TimelineItems.Nodes); n != 1 {
		t.Errorf("timeline items = %d, want the first page kept", n)
	}
}

func TestNullFirstPageConnection(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		pr := strings.Replace(prJSON(1, "2026-10-01T00:00:00Z", false), `"reviews":{"pageInfo":{"hasNextPage":false},"nodes":[]}`, `"reviews":null`, 1)
		io.WriteString(w, page(false, "", pr))
	})
	if _, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestPRsUpdatedDuringTheRunAreCollectedOnce(t *testing.T) {
	var calls atomic.Int32
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			io.WriteString(w, page(true, "c1", prJSON(3, "2026-10-09T11:00:00Z", false)))
		case 2:
			io.WriteString(w, page(false, "", prJSON(2, "2026-09-01T00:00:00Z", false)))
		default:
			io.WriteString(w, page(true, "c1",
				prJSON(1, "2026-10-09T12:05:00Z", false),
				prJSON(2, "2026-10-09T12:01:00Z", false),
				prJSON(3, "2026-10-09T11:00:00Z", false)))
		}
	})
	c.Now = func() time.Time { return start }
	col, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, pr := range col.PullRequests {
		if _, dup := got[pr.Number]; dup {
			t.Errorf("pull request #%d collected twice", pr.Number)
		}
		got[pr.Number] = pr.UpdatedAt.Format(time.RFC3339)
	}
	want := map[int]string{1: "2026-10-09T12:05:00Z", 2: "2026-10-09T12:01:00Z", 3: "2026-10-09T11:00:00Z"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCancelledContextStopsWaiting(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c.Sleep = nil
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Collect(ctx, "o/r", time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}

func TestPageSizeGrowsBackAfterSuccesses(t *testing.T) {
	var calls atomic.Int32
	var sizes []float64
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		req := decode(t, r)
		sizes = append(sizes, req.Variables["first"].(float64))
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, page(n < 12, fmt.Sprintf("c%d", n), prJSON(int(100-n), "2026-10-01T00:00:00Z", false)))
	})
	c.Now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	want := "[25 25 12 12 12 12 12 18 18 18 18 18 18]"
	if got := fmt.Sprint(sizes[:13]); got != want {
		t.Errorf("page sizes = %v, want %s", got, want)
	}
}

func TestGitAttributesThatCannotBeRead(t *testing.T) {
	for name, ga := range map[string]string{
		"absent":    `null`,
		"truncated": `{"text":"a/** linguist-generated\n","isTruncated":true,"isBinary":false}`,
		"binary":    `{"text":null,"isTruncated":false,"isBinary":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := `{"data":{"repository":{"defaultBranchRef":{"name":"main"},"gitattributes":` + ga + `}}}`
			c := newTestClientWithRepo(t, repo, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, page(false, "")) })
			var log strings.Builder
			c.Log = &log
			col, err := c.Collect(context.Background(), "o/r", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if col.GitAttributes != "" || col.DefaultBranch != "main" {
				t.Errorf("collection = %+v", col)
			}
			if name != "absent" && !strings.Contains(log.String(), "warning") {
				t.Errorf("no warning logged: %q", log.String())
			}
		})
	}
}

func TestFilesArePagedAndIncompleteFilesStayUnsized(t *testing.T) {
	withFiles := func(number int, filesJSON string) string {
		pr := prJSON(number, "2026-10-01T00:00:00Z", false)
		return strings.Replace(pr, `"commits":`, `"files":`+filesJSON+`,"commits":`, 1)
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		req := decode(t, r)
		if strings.Contains(req.Query, "pullRequest(number:") {
			if req.Variables["number"] == float64(1) {
				io.WriteString(w, `{"data":{"repository":{"pullRequest":{"files":{"pageInfo":{"hasNextPage":false},"nodes":[{"path":"b.go","additions":2,"deletions":0}]}}}}}`)
				return
			}
			io.WriteString(w, `{"data":{"repository":{"pullRequest":null}}}`)
			return
		}
		io.WriteString(w, page(false, "",
			withFiles(1, `{"pageInfo":{"hasNextPage":true,"endCursor":"f1"},"nodes":[{"path":"a.go","additions":1,"deletions":1}]}`),
			withFiles(2, `{"pageInfo":{"hasNextPage":true,"endCursor":"f1"},"nodes":[{"path":"a.go","additions":1,"deletions":1}]}`),
			withFiles(3, `null`),
			withFiles(4, `{"pageInfo":{"hasNextPage":false},"nodes":[]}`)))
	})
	col, err := c.Collect(context.Background(), "o/r", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	byNumber := map[int]*store.Files{}
	for _, pr := range col.PullRequests {
		byNumber[pr.Number] = pr.Files
	}
	if f := byNumber[1]; f == nil || len(f.Nodes) != 2 {
		t.Errorf("#1 files = %+v, want both pages", f)
	}
	if byNumber[2] != nil || byNumber[3] != nil {
		t.Errorf("incomplete or missing file lists must stay nil: #2 %+v, #3 %+v", byNumber[2], byNumber[3])
	}
	if f := byNumber[4]; f == nil || len(f.Nodes) != 0 {
		t.Errorf("#4 files = %+v, want an empty, collected list", f)
	}
}

func TestCodeownersLocation(t *testing.T) {
	blob := func(text string, truncated bool) string {
		return fmt.Sprintf(`{"text":%q,"isTruncated":%t,"isBinary":false}`, text, truncated)
	}
	tests := []struct {
		name, github, root, docs, want string
		warn                           bool
	}{
		{name: ".github wins", github: blob("/a/ @o/gh\n", false), root: blob("/a/ @o/root\n", false), docs: "null", want: "/a/ @o/gh\n"},
		{name: "docs last", github: "null", root: "null", docs: blob("/a/ @o/docs\n", false), want: "/a/ @o/docs\n"},
		{name: "none", github: "null", root: "null", docs: "null"},
		{name: "truncated is not replaced by a lower one", github: blob("/a/ @o/gh\n", true), root: blob("/a/ @o/root\n", false), docs: "null", warn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := fmt.Sprintf(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},"gitattributes":null,"codeownersGithub":%s,"codeownersRoot":%s,"codeownersDocs":%s}}}`, tt.github, tt.root, tt.docs)
			c := newTestClientWithRepo(t, repo, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, page(false, "")) })
			var log strings.Builder
			c.Log = &log
			col, err := c.Collect(context.Background(), "o/r", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if col.Codeowners != tt.want {
				t.Errorf("codeowners = %q, want %q", col.Codeowners, tt.want)
			}
			if got := strings.Contains(log.String(), "CODEOWNERS"); got != tt.warn {
				t.Errorf("warning logged = %v, want %v: %q", got, tt.warn, log.String())
			}
		})
	}
}
