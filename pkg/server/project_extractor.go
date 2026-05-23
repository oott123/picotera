// Package server — project_extractor.go
//
// Pulls candidate workspace paths out of a gateway request body using a fixed
// set of regexes and JSON-unescapes each capture. Candidate extraction is
// pure (no DB / no account); resolution to a project_id is account-scoped
// and happens after authentication so the router can look up within the
// caller's per-user project namespace.
//
// Hooked from handle_gateway.go and handle_unified_gateway.go: candidates
// are extracted before authentication (so meta-row insert can still happen
// early), then resolution + auto-create runs once the api_key (and hence
// the account_id) is known.
package server

import (
	"context"
	"encoding/json"
	"regexp"

	"picotera/pkg/logx"
)

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
}

type projectExtractor struct {
	router *projectRouter
}

func newProjectExtractor(router *projectRouter) *projectExtractor {
	return &projectExtractor{router: router}
}

// ExtractCandidates scans body, decodes capture groups, dedupes them, and
// returns the deduped path strings in encounter order. Pure — no DB calls,
// no account context. Safe to call before authentication.
func (e *projectExtractor) ExtractCandidates(ctx context.Context, body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	candidates := extractProjectCandidates(ctx, body)
	if len(candidates) == 0 {
		return nil
	}
	logx.WithContext(ctx).WithField("candidates", candidates).Debug("project extractor: candidates")
	return candidates
}

// ResolveForAccount asks the per-account router for a project id matching
// any of the candidate paths within accountID's project namespace. Returns
// (0, false, nil) when no candidate matches or candidates is empty.
func (e *projectExtractor) ResolveForAccount(ctx context.Context, accountID int32, candidates []string) (int32, bool, error) {
	if len(candidates) == 0 || accountID == 0 {
		return 0, false, nil
	}
	return e.router.Match(ctx, accountID, candidates)
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
