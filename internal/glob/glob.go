// Package glob matches repository paths against patterns with .gitignore and
// .gitattributes semantics, and reads the generated and vendored rules of a
// .gitattributes file.
package glob

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Pattern is a compiled path pattern, with .gitignore semantics.
//
// A pattern with a slash at the start or in the middle is anchored at the repository
// root; otherwise it matches at any depth. A trailing slash matches a directory and
// everything in it. "**/" matches any number of directories, a trailing "/**"
// matches everything inside a directory, "*" and "?" do not cross "/", and bracket
// expressions support ranges, negation and POSIX classes such as [[:digit:]].
type Pattern struct {
	source string
	re     *regexp.Regexp
}

// Compile parses a pattern.
func Compile(p string) (Pattern, error) {
	src := p
	p = strings.TrimSpace(p)
	if p == "" {
		return Pattern{}, fmt.Errorf("empty pattern")
	}
	anchored := strings.Contains(strings.TrimSuffix(p, "/"), "/")
	if strings.HasSuffix(p, "/") {
		p += "**"
	}
	p = strings.TrimPrefix(p, "/")
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "/**") && i+3 == len(p):
			b.WriteString("/.*")
			i += 2
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := classEnd(p, i)
			if end < 0 {
				return Pattern{}, fmt.Errorf("pattern %q: unclosed [", src)
			}
			class := p[i+1 : end]
			neg := strings.HasPrefix(class, "!") || strings.HasPrefix(class, "^")
			if neg {
				class = class[1:]
			}
			class = strings.ReplaceAll(class, `\`, `\\`)
			if neg {
				b.WriteString("[^/" + class + "]")
			} else {
				b.WriteString("[" + class + "]")
			}
			i = end
		case c == '\\' && i+1 < len(p):
			i++
			b.WriteString(regexp.QuoteMeta(string(p[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return Pattern{}, fmt.Errorf("pattern %q: %w", src, err)
	}
	return Pattern{source: src, re: re}, nil
}

// classEnd returns the index of the "]" closing the bracket expression at p[open],
// allowing a leading "]" and POSIX classes, or -1.
func classEnd(p string, open int) int {
	i := open + 1
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		i++
	}
	if i < len(p) && p[i] == ']' {
		i++
	}
	for i < len(p) {
		switch {
		case strings.HasPrefix(p[i:], "[:"):
			j := strings.Index(p[i+2:], ":]")
			if j < 0 {
				return -1
			}
			i += j + 4
		case p[i] == ']':
			return i
		default:
			i++
		}
	}
	return -1
}

// Match reports whether the slash-separated repository path matches.
func (p Pattern) Match(path string) bool { return p.re.MatchString(strings.TrimPrefix(path, "/")) }

// String returns the pattern as written.
func (p Pattern) String() string { return p.source }

// Set is an ordered list of patterns.
type Set []Pattern

// CompileAll compiles every pattern, reporting each one that is invalid.
func CompileAll(patterns []string) (Set, error) {
	var s Set
	var bad []string
	for _, p := range patterns {
		c, err := Compile(p)
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		s = append(s, c)
	}
	if len(bad) > 0 {
		return s, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return s, nil
}

// Match reports whether any pattern matches the path.
func (s Set) Match(path string) bool {
	for _, p := range s {
		if p.Match(path) {
			return true
		}
	}
	return false
}

// Attributes are the .gitattributes rules that decide whether a file is generated,
// vendored or binary. Each attribute is resolved separately, the last line that sets
// or unsets it winning, as git does.
type Attributes struct {
	lines []attrLine
}

type attrLine struct {
	pattern Pattern
	attrs   map[string]bool
}

var tracked = map[string]bool{"linguist-generated": true, "linguist-vendored": true, "diff": true}

// ParseGitAttributes reads the rules of a .gitattributes file that mark files as
// generated (linguist-generated), vendored (linguist-vendored) or not diffable
// (-diff, binary), expanding [attr] macros. Lines git would ignore are skipped.
func ParseGitAttributes(text string) Attributes {
	macros := map[string][]string{"binary": {"-diff", "-merge", "-text"}}
	var a Attributes
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pattern, rest := splitPattern(line)
		fields := strings.Fields(rest)
		if name, ok := strings.CutPrefix(pattern, "[attr]"); ok {
			macros[name] = fields
			continue
		}
		if pattern == "" || strings.HasPrefix(pattern, "!") || strings.HasSuffix(pattern, "/") {
			continue
		}
		attrs := map[string]bool{}
		resolve(fields, macros, attrs, 0)
		if len(attrs) == 0 {
			continue
		}
		p, err := Compile(pattern)
		if err != nil {
			continue
		}
		a.lines = append(a.lines, attrLine{pattern: p, attrs: attrs})
	}
	return a
}

func splitPattern(line string) (pattern, rest string) {
	if strings.HasPrefix(line, `"`) {
		if j := strings.Index(line[1:], `"`); j >= 0 {
			return line[1 : j+1], line[j+2:]
		}
	}
	pattern, rest, _ = strings.Cut(line, " ")
	if i := strings.IndexByte(pattern, '\t'); i >= 0 {
		pattern, rest = pattern[:i], pattern[i+1:]+" "+rest
	}
	return pattern, rest
}

// resolve records, for each tracked attribute a line touches, whether it marks the file
// as excluded (true) or not (false).
func resolve(fields []string, macros map[string][]string, attrs map[string]bool, depth int) {
	for _, f := range fields {
		name, value, hasValue := strings.Cut(f, "=")
		state := "set"
		switch {
		case strings.HasPrefix(name, "-"):
			name, state = name[1:], "unset"
		case strings.HasPrefix(name, "!"):
			name, state = name[1:], "unspecified"
		case hasValue:
			state = "value"
		}
		if m, ok := macros[name]; ok && state == "set" && depth < 8 {
			resolve(m, macros, attrs, depth+1)
			continue
		}
		if !tracked[name] {
			continue
		}
		if name == "diff" {
			attrs[name] = state == "unset"
			continue
		}
		attrs[name] = state == "set" || (state == "value" && value != "false")
	}
}

// Excluded reports whether the path is generated, vendored or not diffable.
func (a Attributes) Excluded(path string) bool {
	resolved := map[string]bool{}
	for i := len(a.lines) - 1; i >= 0 && len(resolved) < len(tracked); i-- {
		l := a.lines[i]
		if !l.pattern.Match(path) {
			continue
		}
		for name, v := range l.attrs {
			if _, done := resolved[name]; !done {
				resolved[name] = v
			}
		}
	}
	for _, v := range resolved {
		if v {
			return true
		}
	}
	return false
}

// Len is the number of lines that touch a tracked attribute.
func (a Attributes) Len() int { return len(a.lines) }

// Owners are the rules of a CODEOWNERS file. Rules that match every file, such as "*"
// or "**", are dropped because they would put the whole repository in one component,
// and so are their owners from the remaining rules. Only @user and @org/team owners are
// kept, lowercased and sorted; email owners are never read.
type Owners struct {
	rules []ownerRule
}

type ownerRule struct {
	pattern Pattern
	owners  string
}

var catchAll = map[string]bool{"*": true, "/*": true, "**": true, "/**": true, "/**/*": true, "**/*": true}

// ParseCodeowners reads a CODEOWNERS file. Owners in exclude, lowercased logins without the
// @, are read as if the file did not list them.
func ParseCodeowners(text string, exclude map[string]bool) Owners {
	type line struct {
		pattern string
		owners  []string
	}
	var lines []line
	common := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		fields := strings.Fields(strings.ReplaceAll(l, `\ `, "\x00"))
		fields[0] = strings.ReplaceAll(fields[0], "\x00", `\ `)
		var owners []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "#") {
				break
			}
			if o := strings.ToLower(f); strings.HasPrefix(o, "@") && !exclude[o[1:]] {
				owners = append(owners, o)
			}
		}
		sort.Strings(owners)
		if catchAll[fields[0]] {
			for _, o := range owners {
				common[o] = true
			}
			continue
		}
		lines = append(lines, line{pattern: fields[0], owners: owners})
	}
	var o Owners
	for _, l := range lines {
		p, err := Compile(l.pattern)
		if err != nil {
			continue
		}
		var specific []string
		for _, owner := range l.owners {
			if !common[owner] {
				specific = append(specific, owner)
			}
		}
		if len(specific) == 0 {
			specific = l.owners
		}
		o.rules = append(o.rules, ownerRule{pattern: p, owners: strings.Join(specific, " ")})
	}
	return o
}

// Owner returns the owners of the last rule matching the path, as GitHub picks them,
// or "" when no rule matches or the matching rule has no owners.
func (o Owners) Owner(path string) string {
	for i := len(o.rules) - 1; i >= 0; i-- {
		if o.rules[i].pattern.Match(path) {
			return o.rules[i].owners
		}
	}
	return ""
}

// Len is the number of rules kept.
func (o Owners) Len() int { return len(o.rules) }
