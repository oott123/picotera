package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// parseExternalIDHeaderNames splits a comma-separated header name list,
// trimming whitespace from each entry. An empty string produces an empty
// slice (feature off). A non-empty string with an empty entry (e.g. "a,,b")
// is rejected.
func parseExternalIDHeaderNames(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			return nil, fmt.Errorf("empty header name in %q", s)
		}
		names = append(names, name)
	}
	return names, nil
}

// matchExternalIDHeader returns the first non-empty value among the named
// headers, matched in order. http.Header.Get uses canonical MIME header
// lookup so matching is case-insensitive.
func matchExternalIDHeader(header http.Header, names []string) pgtype.Text {
	for _, name := range names {
		if v := header.Get(name); v != "" {
			return pgtype.Text{String: v, Valid: true}
		}
	}
	return pgtype.Text{Valid: false}
}
