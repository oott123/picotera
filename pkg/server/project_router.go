// Package server — project_router.go
//
// In-memory cache of every (account_id, project_id, path) tuple derived from
// the project table's `paths` JSONB array. Used by project_extractor.go to
// map a candidate path string to a project id within a single account's
// namespace via longest-prefix wins.
//
// Buckets are keyed by account_id; lookups never cross accounts (projects
// are user-bound, two users may legitimately have the same paths). The
// endpoint router stays globally keyed; only this router became per-account.
//
// Mirrors endpoint_router.go: lazy load on first Match, explicit Invalidate()
// on every project mutation. Any future writer of the project table MUST call
// Server.projectRouter.Invalidate() at the same site.
package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"picotera/pkg/db"
)

type projectEntry struct {
	path      string
	projectID int32
}

type projectRouter struct {
	queries *db.Queries

	mu       sync.RWMutex
	byAcct   map[int32][]projectEntry // per-account, each slice sorted: len(path) desc, then projectID asc
	loaded   bool
}

func newProjectRouter(q *db.Queries) *projectRouter {
	return &projectRouter{queries: q}
}

// Match walks the cached entries for accountID (longest-path first) and
// returns the project id of the first entry whose path is a prefix of any
// candidate. Returns (0, false) when no entry matches, candidates is empty,
// or accountID has no projects.
func (r *projectRouter) Match(ctx context.Context, accountID int32, candidates []string) (int32, bool, error) {
	if accountID == 0 || len(candidates) == 0 {
		return 0, false, nil
	}

	r.mu.RLock()
	if r.loaded {
		id, ok := r.matchLocked(accountID, candidates)
		r.mu.RUnlock()
		return id, ok, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	if !r.loaded {
		if err := r.load(ctx); err != nil {
			r.mu.Unlock()
			return 0, false, err
		}
	}
	id, ok := r.matchLocked(accountID, candidates)
	r.mu.Unlock()
	return id, ok, nil
}

func (r *projectRouter) matchLocked(accountID int32, candidates []string) (int32, bool) {
	entries, ok := r.byAcct[accountID]
	if !ok {
		return 0, false
	}
	for _, e := range entries {
		for _, c := range candidates {
			if strings.HasPrefix(c, e.path) {
				return e.projectID, true
			}
		}
	}
	return 0, false
}

// Invalidate drops the cached entries. The next Match call will reload from
// the database. Global drop (not per-account) — cheaper than tracking which
// account changed, and reload is a single query.
func (r *projectRouter) Invalidate() {
	r.mu.Lock()
	r.byAcct = nil
	r.loaded = false
	r.mu.Unlock()
}

func (r *projectRouter) load(ctx context.Context) error {
	rows, err := r.queries.ListProjectPaths(ctx)
	if err != nil {
		return fmt.Errorf("project router: load: %w", err)
	}

	byAcct := make(map[int32][]projectEntry)
	for _, row := range rows {
		if row.Path == "" {
			continue
		}
		byAcct[row.AccountID] = append(byAcct[row.AccountID], projectEntry{
			path:      row.Path,
			projectID: row.ProjectID,
		})
	}

	for acct, entries := range byAcct {
		sortProjectEntries(entries)
		byAcct[acct] = entries
	}
	r.byAcct = byAcct
	r.loaded = true
	return nil
}

func sortProjectEntries(entries []projectEntry) {
	// len(path) desc, ties broken by projectID asc.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			a, b := entries[j-1], entries[j]
			if len(a.path) > len(b.path) {
				break
			}
			if len(a.path) == len(b.path) && a.projectID <= b.projectID {
				break
			}
			entries[j-1], entries[j] = b, a
		}
	}
}
