package server

import (
	"testing"

	"picotera/pkg/db"
)

// newLoadedRouter builds a router whose cache is pre-populated from rows,
// bypassing the database. load()'s compile + sort logic is exercised by calling
// it through the same code path the lazy load uses.
func newLoadedRouter(t *testing.T, rows ...db.Endpoint) *endpointRouter {
	t.Helper()
	r := &endpointRouter{}
	entries := make([]compiledEndpoint, 0, len(rows))
	for _, ep := range rows {
		if ep.PrefixMatch {
			entries = append(entries, compiledEndpoint{endpoint: ep, literalLen: len(ep.Path), prefix: true})
			continue
		}
		re, varNames, litLen, err := compilePattern(ep.Path)
		if err != nil {
			t.Fatalf("compilePattern(%q): %v", ep.Path, err)
		}
		entries = append(entries, compiledEndpoint{endpoint: ep, re: re, varNames: varNames, literalLen: litLen})
	}
	r.entries = entries
	r.loaded = true
	sortCompiledEndpoints(r.entries)
	return r
}

// TestMatchPrefixEndpoint pins the prefix-matching contract: only sub-paths hit,
// the bare prefix does not, and the returned suffix is the remainder including
// its leading slash.
func TestMatchPrefixEndpoint(t *testing.T) {
	codex := db.Endpoint{Path: "/api/codex", PrefixMatch: true}
	r := newLoadedRouter(t, codex)

	cases := []struct {
		path       string
		wantOK     bool
		wantSuffix string
	}{
		{"/api/codex/responses", true, "/responses"},
		{"/api/codex/responses/compact", true, "/responses/compact"},
		{"/api/codex/alpha/search", true, "/alpha/search"},
		// The prefix itself serves nothing — there is no sub-path to forward.
		{"/api/codex", false, ""},
		{"/api/codex/", true, "/"},
		{"/api/codexx/responses", false, ""},
		{"/api/other", false, ""},
	}
	for _, tc := range cases {
		ep, vars, suffix, ok, err := r.Match(t.Context(), tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.path, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if ep.Path != codex.Path {
			t.Errorf("%s: matched %s", tc.path, ep.Path)
		}
		if suffix != tc.wantSuffix {
			t.Errorf("%s: suffix = %q, want %q", tc.path, suffix, tc.wantSuffix)
		}
		if vars != nil {
			t.Errorf("%s: prefix endpoints carry no path vars, got %v", tc.path, vars)
		}
	}
}

// TestMatchPrefixEndpointLosesToExact pins the specificity ordering: an exact
// endpoint mounted under a prefix has the longer literal, so it wins — no new
// sort rule is needed for prefix entries.
func TestMatchPrefixEndpointLosesToExact(t *testing.T) {
	r := newLoadedRouter(t,
		db.Endpoint{Path: "/api/codex", PrefixMatch: true},
		db.Endpoint{Path: "/api/codex/responses"},
	)

	ep, _, suffix, ok, err := r.Match(t.Context(), "/api/codex/responses")
	if err != nil || !ok {
		t.Fatalf("Match: ok=%v err=%v", ok, err)
	}
	if ep.Path != "/api/codex/responses" || ep.PrefixMatch {
		t.Errorf("exact endpoint should win, got %+v", ep)
	}
	if suffix != "" {
		t.Errorf("exact endpoint suffix = %q, want empty", suffix)
	}

	// A sibling sub-path with no exact endpoint still falls to the prefix.
	ep, _, suffix, ok, err = r.Match(t.Context(), "/api/codex/alpha/search")
	if err != nil || !ok {
		t.Fatalf("Match: ok=%v err=%v", ok, err)
	}
	if ep.Path != "/api/codex" || suffix != "/alpha/search" {
		t.Errorf("got endpoint %s suffix %q", ep.Path, suffix)
	}
}

// TestMatchOrdinaryEndpointSuffixEmpty pins that adding the suffix return value
// left ordinary endpoints — including ones with {name} tokens — untouched.
func TestMatchOrdinaryEndpointSuffixEmpty(t *testing.T) {
	r := newLoadedRouter(t, db.Endpoint{Path: "/v1beta/models/{model}:generateContent"})
	ep, vars, suffix, ok, err := r.Match(t.Context(), "/v1beta/models/gemini-2.5-pro:generateContent")
	if err != nil || !ok {
		t.Fatalf("Match: ok=%v err=%v", ok, err)
	}
	if ep.Path != "/v1beta/models/{model}:generateContent" {
		t.Errorf("matched %s", ep.Path)
	}
	if vars["model"] != "gemini-2.5-pro" {
		t.Errorf("vars = %v", vars)
	}
	if suffix != "" {
		t.Errorf("suffix = %q, want empty", suffix)
	}
}
