// Package server — endpoint_router.go
//
// Endpoint matching is cached in memory. The router loads all endpoints from
// the database lazily on the first Match call, compiles their path patterns,
// and sorts them by specificity. The cache is invalidated explicitly via
// Invalidate() whenever a mutation is made to the endpoint table (see
// handleUpsertEndpoint and handleDeleteEndpoint in handle_endpoint.go).
//
// Do not bypass this router for gateway routing. GetEndpointByPath is only
// retained for exact-path validation in handle_provider_endpoint.go.
// Any future writer of the endpoint table must call
// Server.endpointRouter.Invalidate() at the same site.
//
// The five unified generation routes (/api/unified/v1/messages,
// /v1/responses, /v1/chat/completions, and the two /v1beta/models/{model}:…
// Gemini variants) are registered as literal chi handlers in server.go
// BEFORE the catch-all gateway mount, so they never hit Match. They are not
// rows in the endpoint table — they are runtime constants, see
// handle_unified_gateway.go.
package server

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"picotera/pkg/db"
)

// tokenRe matches a single {name} placeholder token in an endpoint path.
var tokenRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// compiledEndpoint holds a pre-compiled pattern for a single endpoint path.
type compiledEndpoint struct {
	endpoint   db.Endpoint
	re         *regexp.Regexp
	varNames   []string
	literalLen int
}

// compilePattern converts an endpoint path (possibly containing {name} tokens)
// into a compiled regexp, a slice of variable names in match order, and the
// total count of literal characters in the path.
//
// Rules:
//   - {name} tokens are compiled to (.+?) — non-greedy, matches any non-empty
//     string including '/'.
//   - Literal characters are regexp.QuoteMeta'd.
//   - The expression is anchored at both ends (^ … $).
//   - Duplicate variable names in one path are rejected.
//   - Variable names must match [A-Za-z_][A-Za-z0-9_]* (enforced by tokenRe).
func compilePattern(path string) (*regexp.Regexp, []string, int, error) {
	var (
		pattern    string
		varNames   []string
		literalLen int
		seen       = map[string]bool{}
		cursor     = 0
	)
	matches := tokenRe.FindAllStringSubmatchIndex(path, -1)
	for _, loc := range matches {
		if err := validateEndpointPathLiteral(path, cursor, loc[0]); err != nil {
			return nil, nil, 0, err
		}
		// loc[0]:loc[1] is the full {name} match; loc[2]:loc[3] is the capture.
		name := path[loc[2]:loc[3]]
		if seen[name] {
			return nil, nil, 0, fmt.Errorf("endpoint path %q: duplicate variable name %q", path, name)
		}
		seen[name] = true
		varNames = append(varNames, name)

		literal := path[cursor:loc[0]]
		literalLen += len(literal)
		pattern += regexp.QuoteMeta(literal) + `(.+?)`
		cursor = loc[1]
	}
	// Remaining literal suffix.
	if err := validateEndpointPathLiteral(path, cursor, len(path)); err != nil {
		return nil, nil, 0, err
	}
	suffix := path[cursor:]
	literalLen += len(suffix)
	pattern += regexp.QuoteMeta(suffix)

	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		return nil, nil, 0, fmt.Errorf("endpoint path %q: compile regex: %w", path, err)
	}
	return re, varNames, literalLen, nil
}

func validateEndpointPathLiteral(path string, start, end int) error {
	for i := start; i < end; i++ {
		switch path[i] {
		case '{', '}':
			return fmt.Errorf("endpoint path %q: invalid variable token near byte %d", path, i)
		}
	}
	return nil
}

// endpointRouter matches incoming request paths against the compiled endpoint
// patterns and caches the compiled set in memory.
type endpointRouter struct {
	queries *db.Queries

	mu      sync.RWMutex
	entries []compiledEndpoint // sorted by literalLen desc, then path asc
	loaded  bool
}

// newEndpointRouter constructs a router backed by the given queries handle.
func newEndpointRouter(q *db.Queries) *endpointRouter {
	return &endpointRouter{queries: q}
}

// Match finds the best-matching endpoint for the given request path.
//
// On a hit it returns (endpoint, pathVars, true, nil). pathVars is nil when
// the matched endpoint contains no {name} tokens.
//
// On a miss it returns (zero, nil, false, nil).
//
// On a load/compile error it returns (zero, nil, false, err); the caller
// should surface this as a 500 INTERNAL_ERROR.
func (r *endpointRouter) Match(ctx context.Context, path string) (db.Endpoint, map[string]string, bool, error) {
	// Fast path: cache already warm.
	r.mu.RLock()
	if r.loaded {
		ep, vars, ok := r.matchLocked(path)
		r.mu.RUnlock()
		return ep, vars, ok, nil
	}
	r.mu.RUnlock()

	// Slow path: acquire write lock, double-check, then load.
	r.mu.Lock()
	if !r.loaded {
		if err := r.load(ctx); err != nil {
			r.mu.Unlock()
			return db.Endpoint{}, nil, false, err
		}
	}
	ep, vars, ok := r.matchLocked(path)
	r.mu.Unlock()
	return ep, vars, ok, nil
}

// matchLocked iterates entries (must be called under at least a read lock).
func (r *endpointRouter) matchLocked(path string) (db.Endpoint, map[string]string, bool) {
	for _, ce := range r.entries {
		subs := ce.re.FindStringSubmatch(path)
		if subs == nil {
			continue
		}
		if len(ce.varNames) == 0 {
			return ce.endpoint, nil, true
		}
		vars := make(map[string]string, len(ce.varNames))
		for i, name := range ce.varNames {
			vars[name] = subs[i+1]
		}
		return ce.endpoint, vars, true
	}
	return db.Endpoint{}, nil, false
}

// Invalidate drops the cached entries. The next Match call will reload from
// the database.
func (r *endpointRouter) Invalidate() {
	r.mu.Lock()
	r.entries = nil
	r.loaded = false
	r.mu.Unlock()
}

// load fetches all endpoints, compiles their patterns, and sorts them.
// Must be called with the write lock held.
func (r *endpointRouter) load(ctx context.Context) error {
	rows, err := r.queries.GetEndpoints(ctx)
	if err != nil {
		return fmt.Errorf("endpoint router: load: %w", err)
	}

	entries := make([]compiledEndpoint, 0, len(rows))
	for _, ep := range rows {
		re, varNames, litLen, err := compilePattern(ep.Path)
		if err != nil {
			// Skip malformed patterns rather than failing all routing.
			continue
		}
		entries = append(entries, compiledEndpoint{
			endpoint:   ep,
			re:         re,
			varNames:   varNames,
			literalLen: litLen,
		})
	}

	// Most-specific first: largest literal length wins; ties broken by path asc.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].literalLen != entries[j].literalLen {
			return entries[i].literalLen > entries[j].literalLen
		}
		return entries[i].endpoint.Path < entries[j].endpoint.Path
	})

	r.entries = entries
	r.loaded = true
	return nil
}
