package kv

import (
	"regexp"
	"testing"
)

func TestGlobToRegexp(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
		match   []string
		noMatch []string
	}{
		{"star", "a*", `^a.*$`, []string{"a", "abc", "a.b"}, []string{"ba", ""}},
		{"question", "a?c", `^a.c$`, []string{"abc", "a.c", "a中c"}, []string{"ac", "abbc"}},
		{"escaped star", `a\*`, `^a\*$`, []string{"a*"}, []string{"ab", "a"}},
		{"escaped alnum", `\a\1`, `^a1$`, []string{"a1"}, []string{`\a\1`}},
		{"escaped backslash", `a\\b`, `^a\\b$`, []string{`a\b`}, []string{"ab"}},
		{"punctuation literals", "a.b+c(d|e)$f{2}", `^a\.b\+c\(d\|e\)\$f\{2\}$`, []string{"a.b+c(d|e)$f{2}"}, []string{"axb+c(d|e)$f{2}", "a.bbc(d|e)$f{2}", "a.b+cd$f{2}", "a.b+ce$fff"}},
		{"non-ascii literal", "键-値*", `^键\-値.*$`, []string{"键-値", "键-値x"}, []string{"键値"}},
		{"class", "[abc]", `^[abc]$`, []string{"a", "c"}, []string{"d", "ab", ""}},
		{"negated class", "[^a]*", `^[^a].*$`, []string{"b", "bad"}, []string{"a", "abc", ""}},
		{"range", "[a-z]x", `^[a-z]x$`, []string{"ax", "zx"}, []string{"Ax", "0x"}},
		{"leading dash", "[-a]", `^[\-a]$`, []string{"-", "a"}, []string{"b"}},
		{"trailing dash", "[a-]", `^[a\-]$`, []string{"-", "a"}, []string{"b"}},
		{"escaped bracket in class", `[\]a]`, `^[\]a]$`, []string{"]", "a"}, []string{`\`, "b"}},
		{"caret not first", "[a^]", `^[a\^]$`, []string{"a", "^"}, []string{"b"}},
		{"non-ascii range", "[一-十]", `^[一-十]$`, []string{"一", "七", "十"}, []string{"a"}},
		{"literal brackets around class", `\[[0-9]\]`, `^\[[0-9]\]$`, []string{"[5]"}, []string{"5", "[a]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GlobToRegexp(tt.pattern)
			if err != nil {
				t.Fatalf("GlobToRegexp(%q) error: %v", tt.pattern, err)
			}
			if got != tt.want {
				t.Fatalf("GlobToRegexp(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
			re := regexp.MustCompile(got)
			for _, k := range tt.match {
				if !re.MatchString(k) {
					t.Errorf("%q should match %q", got, k)
				}
			}
			for _, k := range tt.noMatch {
				if re.MatchString(k) {
					t.Errorf("%q should not match %q", got, k)
				}
			}
		})
	}
}

func TestGlobToRegexpErrors(t *testing.T) {
	for _, pattern := range []string{
		`a\`,
		`[a\`,
		"[abc",
		"[",
		"[^",
		"[]",
		"[^]",
		"[z-a]",
	} {
		if got, err := GlobToRegexp(pattern); err == nil {
			t.Errorf("GlobToRegexp(%q) = %q, want error", pattern, got)
		}
	}
}
