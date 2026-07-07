package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShouldStripUpstreamHeader(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"access-control-allow-origin", true},
		{"Access-Control-Expose-Headers", true},
		{"ACCESS-CONTROL-MAX-AGE", true},
		{"access-control-allow-credentials", true},
		{"Alt-Svc", true},
		{"ALT-SVC", true},
		{"Nel", true},
		{"NEL", true},
		{"Report-To", true},
		{"REPORT-TO", true},
		{"Vary", true},
		{"VARY", true},
		// Below the prefix boundary: "access-control" lacks the trailing "-",
		// so it is not an Access-Control-* header.
		{"access-control", false},
		// "alt-svc-x" shares a prefix but is not the exact "alt-svc" name.
		{"alt-svc-x", false},
		// "vary-x" is not the exact "vary" name.
		{"vary-x", false},
		{"content-type", false},
		{"x-request-id", false},
		{"authorization", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.header, func(t *testing.T) {
			if got := shouldStripUpstreamHeader(lowerHeader(c.header)); got != c.want {
				t.Fatalf("shouldStripUpstreamHeader(%q) = %v, want %v", c.header, got, c.want)
			}
		})
	}
}

// lowerHeader lowercases an HTTP header name for the shouldStripUpstreamHeader
// predicate, which is defined to take the already-lowercased form. Mirrors how
// the copy loops build `lower` before the skip check.
func lowerHeader(h string) string {
	b := make([]byte, len(h))
	for i := 0; i < len(h); i++ {
		c := h[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// TestCopyPathSuccessHeaders_StripsUpstreamHeaders reproduces the bug where an
// upstream Access-Control-Allow-Origin: * was appended to the gateway's own
// "*" (set by writeCORSHeaders), serializing as "*, *" — and likewise for
// Access-Control-Expose-Headers. The gateway owns the downstream CORS policy,
// so upstream Access-Control-* headers must not be forwarded; upstream
// Alt-Svc points at the upstream's alternative endpoints and must not be
// forwarded either.
func TestCopyPathSuccessHeaders_StripsUpstreamHeaders(t *testing.T) {
	// Simulate the gateway having already written its CORS headers.
	w := httptest.NewRecorder()
	writeCORSHeaders(w, &http.Request{Header: http.Header{}})

	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Access-Control-Allow-Origin", "*")
	resp.Header.Set("Access-Control-Expose-Headers", "X-Foo")
	resp.Header.Set("Access-Control-Allow-Credentials", "true")
	resp.Header.Set("Alt-Svc", `h2="api.upstream.com:443"; ma=3600`)
	resp.Header.Set("Nel", `{"report_to":"https://upstream.example/report","max_age":3600}`)
	resp.Header.Set("Report-To", `{"group":"default","endpoints":[{"url":"https://upstream.example/report"}],"max_age":3600}`)
	resp.Header.Set("Vary", "Accept-Encoding, Authorization")
	resp.Header.Set("X-Trace", "abc")
	resp.Header.Set("Content-Length", "123")

	copyPathSuccessHeaders(w, resp)

	h := w.Result().Header

	if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q (single value, not \"*, *\")", got, "*")
	}
	if vals := h.Values("Access-Control-Allow-Origin"); len(vals) != 1 {
		t.Fatalf("Access-Control-Allow-Origin has %d values %v, want 1", len(vals), vals)
	}

	if got := h.Get("Access-Control-Expose-Headers"); got != "*" {
		t.Fatalf("Access-Control-Expose-Headers = %q, want %q (single value, not \"*, X-Foo\")", got, "*")
	}
	if vals := h.Values("Access-Control-Expose-Headers"); len(vals) != 1 {
		t.Fatalf("Access-Control-Expose-Headers has %d values %v, want 1", len(vals), vals)
	}

	// Upstream Allow-Credentials must not leak into the credential-less policy.
	if vals := h.Values("Access-Control-Allow-Credentials"); len(vals) != 0 {
		t.Fatalf("Access-Control-Allow-Credentials leaked %v, want absent", vals)
	}

	// Alt-Svc / Nel / Report-To point at the upstream's own endpoints and must
	// not be forwarded to the client.
	for _, hdr := range []string{"Alt-Svc", "Nel", "Report-To"} {
		if vals := h.Values(hdr); len(vals) != 0 {
			t.Fatalf("%s leaked %v, want absent", hdr, vals)
		}
	}

	// Upstream Vary is an unreliable caching hint after the gateway rewrites
	// the response, and the gateway emits no Vary of its own.
	if vals := h.Values("Vary"); len(vals) != 0 {
		t.Fatalf("Vary leaked %v, want absent", vals)
	}

	// Ordinary headers are still forwarded.
	if got := h.Get("X-Trace"); got != "abc" {
		t.Fatalf("X-Trace = %q, want %q", got, "abc")
	}

	// Content-Length is still skipped (pre-existing behavior).
	if vals := h.Values("Content-Length"); len(vals) != 0 {
		t.Fatalf("Content-Length leaked %v, want absent", vals)
	}
}
