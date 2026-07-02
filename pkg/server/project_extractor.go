// Package server — project_extractor.go
//
// Pulls candidate workspace paths out of a gateway request body using a fixed
// set of regexes, JSON-unescapes each capture, expands every path into its
// separator-aligned ancestors, and matches them to a project id with a single
// SQL query. Matching is scoped to the requesting user. When no project matches
// and the requesting user's `project.autoCreate` setting is enabled, a project
// is created on the fly so subsequent requests match it. Hooked from the gateway
// flow after authentication, once the user is known.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5"
)

// autoCreateSettingKey is the per-user setting key gating project auto-creation.
const autoCreateSettingKey = "project.autoCreate"

// autoCreateMinPathLen is the minimum decoded path length required before a
// path is eligible to seed an auto-created project.
const autoCreateMinPathLen = 5

// autoCreateMaxRetries bounds name-collision retries when inserting an
// auto-created project.
const autoCreateMaxRetries = 5

// projectExtractRegexps are the fixed patterns scanned over each request body.
// New patterns must be appended here — there is no runtime configuration.
//
// `(?:\\n|\n)` matches either the JSON-escape sequence `\n` (two bytes, the
// usual case since LLM gateway bodies are JSON) or an actual newline byte
// (defensive — for the rare non-JSON body).
var projectExtractRegexps = []*regexp.Regexp{
	regexp.MustCompile(`Workspace root folder: (.*?)(?:\\n|\n|$|")`),
	regexp.MustCompile(`Primary working directory: (.*?)(?:\\n|\n|$|")`),
	regexp.MustCompile(`Current working directory: (.*?)(?:\\n|\n|$|")`),
	regexp.MustCompile(`<cwd>(.*?)</cwd>`),
	regexp.MustCompile(`<env>(?:\\n|\n)Working directory: (.*?)(?:\\n|\n|$|")`),
	regexp.MustCompile(`Today is [0-9-]+, and the current working directory is '(.*?)'.(?:\\n|\n|$|")`), // omp
}

type projectExtractor struct {
	queries *db.Queries
}

func newProjectExtractor(queries *db.Queries) *projectExtractor {
	return &projectExtractor{queries: queries}
}

// Extract scans body, decodes capture groups, expands them into ancestor
// candidates, and asks the database for the longest matching project path owned
// by userID. On a miss it may auto-create a project for that user (subject to
// their setting). Returns (0, false, nil) when no candidates parse or nothing
// matches.
func (e *projectExtractor) Extract(ctx context.Context, body []byte, userID int64) (int32, bool, error) {
	if len(body) == 0 {
		return 0, false, nil
	}
	paths := extractProjectCandidates(ctx, body)
	if len(paths) == 0 {
		return 0, false, nil
	}
	logx.WithContext(ctx).WithField("paths", paths).Debug("project extractor: paths")

	candidates := make([]string, 0, len(paths)*4)
	seen := map[string]struct{}{}
	for _, p := range paths {
		for _, a := range ancestorPaths(p) {
			if _, dup := seen[a]; dup {
				continue
			}
			seen[a] = struct{}{}
			candidates = append(candidates, a)
		}
	}

	id, err := e.queries.MatchProjectByPaths(ctx, db.MatchProjectByPathsParams{
		UserID:         userID,
		CandidatePaths: candidates,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return e.maybeAutoCreate(ctx, paths, userID)
		}
		return 0, false, err
	}
	return id, true, nil
}

// maybeAutoCreate creates a project for userID seeded with the longest eligible
// path when that user's `project.autoCreate` setting is enabled. Returns
// (0, false, nil) when disabled or no path qualifies.
func (e *projectExtractor) maybeAutoCreate(ctx context.Context, paths []string, userID int64) (int32, bool, error) {
	if !e.autoCreateEnabled(ctx, userID) {
		return 0, false, nil
	}

	source := ""
	for _, p := range paths {
		if len(p) > autoCreateMinPathLen && len(p) > len(source) {
			source = p
		}
	}
	if source == "" {
		return 0, false, nil
	}

	base := lastPathComponent(source)
	if base == "" {
		return 0, false, nil
	}

	pathsJSON, err := json.Marshal([]string{source})
	if err != nil {
		return 0, false, err
	}

	name := base
	for attempt := 0; attempt < autoCreateMaxRetries; attempt++ {
		row, err := e.queries.InsertAutoCreatedProject(ctx, db.InsertAutoCreatedProjectParams{
			Name:   name,
			Paths:  pathsJSON,
			UserID: userID,
		})
		if err == nil {
			logx.WithContext(ctx).WithField("project_id", row.ID).WithField("path", source).Info("project extractor: auto-created project")
			return row.ID, true, nil
		}
		if !isUniqueViolation(err) {
			return 0, false, err
		}
		name = base + "-" + randomSuffix()
	}
	return 0, false, nil
}

func (e *projectExtractor) autoCreateEnabled(ctx context.Context, userID int64) bool {
	setting, err := e.queries.GetUserSetting(ctx, db.GetUserSettingParams{
		UserID: userID,
		Key:    autoCreateSettingKey,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logx.WithContext(ctx).WithError(err).Warn("project extractor: failed to read autoCreate setting")
		}
		return false
	}
	var enabled bool
	if err := json.Unmarshal(setting.Value, &enabled); err != nil {
		logx.WithContext(ctx).WithError(err).Warn("project extractor: failed to parse autoCreate setting")
		return false
	}
	return enabled
}

func extractProjectCandidates(ctx context.Context, body []byte) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, re := range projectExtractRegexps {
		matches := re.FindAllSubmatch(body, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			decoded, ok := decodeJSONString(ctx, m[1])
			if !ok || decoded == "" {
				continue
			}
			if _, dup := seen[decoded]; dup {
				continue
			}
			seen[decoded] = struct{}{}
			out = append(out, decoded)
		}
	}
	return out
}

// ancestorPaths expands p into itself plus each separator-aligned ancestor
// directory. Separators are `/` or `\` so both Unix and Windows paths work.
//
//	/path/to/foo/bar -> [/path/to/foo/bar, /path/to/foo, /path/to, /path, /]
//	C:\Users\foo     -> [C:\Users\foo, C:\Users, C:]
func ancestorPaths(p string) []string {
	if p == "" {
		return nil
	}
	out := []string{p}
	cur := p
	for {
		i := strings.LastIndexAny(cur, `/\`)
		if i < 0 {
			break
		}
		if i == 0 {
			// Root separator. Skip when cur is already just the root (avoids
			// emitting "/" twice for input "/").
			if len(cur) > 1 {
				out = append(out, cur[:1])
			}
			break
		}
		cur = cur[:i]
		out = append(out, cur)
	}
	return out
}

// lastPathComponent returns the final path component of p, stripping any
// trailing separators first. Works for both `/` and `\` separators.
//
//	/a/b/foo  -> foo
//	C:\a\foo  -> foo
//	/a/b/foo/ -> foo
func lastPathComponent(p string) string {
	p = strings.TrimRight(p, `/\`)
	if p == "" {
		return ""
	}
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// randomSuffix returns 8 hex characters from 4 crypto-random bytes.
func randomSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func decodeJSONString(ctx context.Context, raw []byte) (string, bool) {
	wrapped := make([]byte, 0, len(raw)+2)
	wrapped = append(wrapped, '"')
	wrapped = append(wrapped, raw...)
	wrapped = append(wrapped, '"')
	var s string
	if err := json.Unmarshal(wrapped, &s); err != nil {
		logx.WithContext(ctx).WithError(err).WithField("raw", string(raw)).Debug("project extractor: json unmarshal failed")
		return "", false
	}
	return s, true
}
