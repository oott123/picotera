package server

import (
	"context"
	"slices"
	"testing"
)

func TestExtractProjectCandidatesEnvWorkingDirectoryEscapedNewlines(t *testing.T) {
	body := []byte(`report/summary/findings/analysis .md files. Return findings directly as your final assistant message — the parent agent reads your text output, not files you create.\n\nHere is useful information about the environment you are running in:\n<env>\nWorking directory: /home/oott123/Work/Projects/picotera\nIs directory a git repo: Yes\nPlatform: linux\nShell: bash\nOS Version: Linux 7.0.2-zen1-1-zen\n</env>\nYou are powered by the model named Haiku 4.5. The exact model ID is claude-haiku-4-5-20251001.\n\nAssistant knowledge cutoff is February 2025.","cache_control":{"typ`)

	candidates := extractProjectCandidates(context.Background(), body)
	if !slices.Contains(candidates, "/home/oott123/Work/Projects/picotera") {
		t.Fatalf("expected env working directory candidate, got %#v", candidates)
	}
}

func TestAncestorPaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "unix multi-level",
			in:   "/path/to/foo/bar",
			want: []string{"/path/to/foo/bar", "/path/to/foo", "/path/to", "/path", "/"},
		},
		{
			name: "windows multi-level",
			in:   `C:\Users\foo`,
			want: []string{`C:\Users\foo`, `C:\Users`, "C:"},
		},
		{
			name: "unix root",
			in:   "/",
			want: []string{"/"},
		},
		{
			name: "trailing separator",
			in:   "/path/to/foo/",
			want: []string{"/path/to/foo/", "/path/to/foo", "/path/to", "/path", "/"},
		},
		{
			name: "mixed separators",
			in:   `/path\to/foo`,
			want: []string{`/path\to/foo`, `/path\to`, "/path", "/"},
		},
		{
			name: "empty",
			in:   "",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ancestorPaths(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ancestorPaths(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLastPathComponent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/a/b/foo", "foo"},
		{`C:\a\foo`, "foo"},
		{"/a/b/foo/", "foo"},
		{`C:\a\foo\`, "foo"},
		{"foo", "foo"},
		{"/", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := lastPathComponent(tc.in)
			if got != tc.want {
				t.Fatalf("lastPathComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAutoCreateSeedPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/srv/proj", "/srv/proj"},
		{"/srv/proj/worktrees/feat/sub", "/srv/proj"},
		{`D:\src\proj\worktrees\feat`, `D:\src\proj`},
		{`/srv/proj\worktrees/feat`, "/srv/proj"},
		{"/a/worktrees/b/worktrees/c", "/a"},
		{"/home/alice/proj/.claude/worktrees/feat", "/home/alice/proj"},
		{"/home/alice/.codex/worktrees/ab12/proj", "/home/alice"},
		{`D:\src\proj\.claude\worktrees\feat`, `D:\src\proj`},
		{`C:\Users\alice\.codex\worktrees\ab12\proj`, `C:\Users\alice`},
		{"/srv/proj/.git/worktrees/x", "/srv/proj/.git"},
		{"/srv/proj/worktrees", "/srv/proj/worktrees"},
		{"/srv/myworktrees/x", "/srv/myworktrees/x"},
		{"/srv/worktreesx/x", "/srv/worktreesx/x"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := autoCreateSeedPath(tc.in)
			if got != tc.want {
				t.Fatalf("autoCreateSeedPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsExcludedAutoCreatePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/home/alice", true},
		{"/home/alice/", true},
		{"/root", true},
		{"/root/", true},
		{"/tmp", true},
		{`C:\Users\alice`, true},
		{`C:\Users\alice\`, true},
		{`d:\Users\alice`, true},
		{"C:/Users/alice", true},
		{"/home", false},
		{"/home/alice/proj", false},
		{"/home//alice", false},
		{"/tmp/foo", false},
		{"/root/foo", false},
		{"/srv/proj", false},
		{`C:\Users\alice\proj`, false},
		{`C:\users\alice`, false},
		{`CD:\Users\alice`, false},
		{`C:\Users`, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := isExcludedAutoCreatePath(tc.in)
			if got != tc.want {
				t.Fatalf("isExcludedAutoCreatePath(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
