package glob

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"go.sum", "go.sum", true},
		{"go.sum", "hack/tools/go.sum", true},
		{"*.pb.go", "pkg/apiclient/session/session.pb.go", true},
		{"*.pb.go", "pkg/session.go", false},
		{"**/*.pb.go", "a.pb.go", true},
		{"**/mocks/*.go", "util/git/mocks/Client.go", true},
		{"**/mocks/*.go", "util/git/mocks/sub/Client.go", false},
		{"manifests/install.yaml", "manifests/install.yaml", true},
		{"manifests/install.yaml", "x/manifests/install.yaml", false},
		{"/assets/swagger.json", "assets/swagger.json", true},
		{"docs/commands/tool_*.md", "docs/commands/tool_app.md", true},
		{"docs/commands/tool_*.md", "docs/commands/sub/tool_app.md", false},
		{"manifests/crds/*-crd.yaml", "manifests/crds/application-crd.yaml", true},
		{"vendor/**", "vendor/github.com/x/y.go", true},
		{"vendor/**", "staging/vendor/a.go", false},
		{"docs/**", "ui/docs/a.md", false},
		{"vendor/", "vendor/a.go", true},
		{"vendor/", "staging/vendor/a.go", true},
		{"/vendor/", "staging/vendor/a.go", false},
		{"[[:digit:]]*.txt", "7z.txt", true},
		{"[[:digit:]]*.txt", "z7.txt", false},
		{"[]a].md", "].md", true},
		{"[!a].md", "dir/b.md", true},
		{"[!a].md", "a.md", false},
		{"**/vendor/**", "staging/vendor/a.go", true},
		{"**/vendor/**", "vendors/a.go", false},
		{"a/**/b.txt", "a/b.txt", true},
		{"a/**/b.txt", "a/x/y/b.txt", true},
		{"zz_generated*", "pkg/apis/v1/zz_generated.deepcopy.go", true},
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file10.txt", false},
		{"[abc].md", "b.md", true},
		{"[!abc].md", "b.md", false},
		{"*.min.js", "ui/dist/app.min.js", true},
		{"a+b(c).txt", "a+b(c).txt", true},
	}
	for _, tt := range tests {
		p, err := Compile(tt.pattern)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tt.pattern, err)
		}
		if got := p.Match(tt.path); got != tt.want {
			t.Errorf("%q matches %q = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	if _, err := CompileAll([]string{"ok", "bad[", ""}); err == nil {
		t.Error("want error for unclosed class and empty pattern")
	}
}

func TestGitAttributes(t *testing.T) {
	attrs := ParseGitAttributes(`# comment
[attr]gen linguist-generated -diff
**/*.pb.go linguist-generated=true
docs/generated/** linguist-generated
docs/generated/keep.md linguist-generated=false
third_party/** linguist-vendored
*.png binary
*.sh text eol=lf
*.lock -diff
foo.lock linguist-generated=false
api/*.go gen
legacy/** linguist-generated=1
old/** linguist-generated
old/keep.go !linguist-generated
"with space/file.txt" linguist-generated
dist/ linguist-generated
!ignored linguist-generated
`)
	for path, want := range map[string]bool{
		"api/x.pb.go":            true,
		"docs/generated/cli.md":  true,
		"docs/generated/keep.md": false,
		"third_party/lib/a.c":    true,
		"img/logo.png":           true,
		"hack/build.sh":          false,
		"main.go":                false,
		"foo.lock":               true,
		"api/client.go":          true,
		"agen":                   false,
		"legacy/a.go":            true,
		"old/keep.go":            false,
		"old/other.go":           true,
		"with space/file.txt":    true,
		"dist/app.js":            false,
	} {
		if got := attrs.Excluded(path); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestCodeowners(t *testing.T) {
	o := ParseCodeowners(`# All
** @org/approvers

/docs/**     @org/approvers @org/docs
/docs/operator-manual/ @org/approvers @org/docs @org/cli  # trailing comment
Makefile     @org/approvers @org/ci
/ui/**       @org/approvers @org/ui someone@example.com
/ui/vendored/
/hack/       @org/approvers
/web/        @org/UI @org/approvers
/site/       @org/approvers @org/ui
/mail/       ops@example.com
my\ dir/    @org/spaces
`, nil)
	tests := map[string]string{
		"docs/index.md":                  "@org/docs",
		"docs/operator-manual/rbac.md":   "@org/cli @org/docs",
		"build/Makefile":                 "@org/ci",
		"ui/src/app.tsx":                 "@org/ui",
		"ui/vendored/lib.js":             "",
		"hack/gen.sh":                    "@org/approvers",
		"controller/appcontroller.go":    "",
		"docs/operator-manual/x/deep.md": "@org/cli @org/docs",
		"web/index.html":                 "@org/ui",
		"site/index.html":                "@org/ui",
		"mail/list.txt":                  "",
		"my dir/a.txt":                   "@org/spaces",
	}
	for path, want := range tests {
		if got := o.Owner(path); got != want {
			t.Errorf("Owner(%q) = %q, want %q", path, got, want)
		}
	}
	if o.Len() != 10 {
		t.Errorf("Len = %d, want 10 (catch-all dropped)", o.Len())
	}
}
