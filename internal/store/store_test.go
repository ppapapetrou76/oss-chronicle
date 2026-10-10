package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadSkipsBlankLinesAndDecodesTimeline(t *testing.T) {
	in := `{"number":1,"state":"MERGED","createdAt":"2026-07-10T08:00:00Z","author":{"login":"alice"},"timelineItems":{"nodes":[{"__typename":"IssueComment","createdAt":"2026-07-11T09:00:00Z","author":{"login":"bob"},"body":"lgtm"},{"__typename":"MergedEvent","createdAt":"2026-07-12T10:00:00Z","actor":{"login":"carol"}}]}}

{"number":2,"state":"OPEN","createdAt":"2026-07-13T08:00:00Z","author":null}
`
	prs, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d pull requests, want 2", len(prs))
	}
	items := prs[0].TimelineItems.Nodes
	if items[0].Login() != "bob" || items[1].Login() != "carol" {
		t.Errorf("logins = %q, %q", items[0].Login(), items[1].Login())
	}
	if prs[1].AuthorLogin() != "" {
		t.Errorf("deleted author login = %q, want empty", prs[1].AuthorLogin())
	}
}

func TestReadReportsLineNumber(t *testing.T) {
	_, err := Read(strings.NewReader("{\"number\":1}\nnot json\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want line 2", err)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	in := []PullRequest{{
		Number: 1, UpdatedAt: at, Author: &Actor{Login: "a", Typename: "Bot"},
		TimelineItems: Timeline{Nodes: []TimelineItem{{Typename: ClosedEvent, CreatedAt: &at, Actor: &Actor{Login: "b"}, Closer: &Closer{Typename: "Commit"}}}},
	}}
	path := filepath.Join(t.TempDir(), "prs.jsonl.gz")
	if err := WriteFile(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].UpdatedAt.Equal(at) || out[0].Author.Typename != "Bot" || !out[0].TimelineItems.Nodes[0].ClosedByCommit() {
		t.Errorf("round trip = %+v", out)
	}
}
