package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsUpstreamCORSHeader(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		// Below the prefix boundary: "access-control" lacks the trailing "-",
		// so it is not an Access-Control-* header.
		{"access-control", false},
		{"access-control-allow-credentials", true},
		{"content-type", false},
		{"x-request-id", false},
		{"authorization", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.header, func(t *testing.T) {
			if got := isUpstreamCORSHeader(lowerHeader(c.header)); got != c.want {
				t.Fatalf("isUpstreamCORSHeader(%q) = %v, want %v", c.header, got, c.want)
			}
		})
	}
}

// lowerHeader lowercases an HTTP header name for the isUpstreamCORSHeader
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

// TestCopyPathSuccessHeaders_SkipsUpstreamCORS reproduces the bug where an
// upstream Access-Control-Allow-Origin: * was appended to the gateway's own
// "*" (set by writeCORSHeaders), serializing as "*, *" — and likewise for
// Access-Control-Expose-Headers. The gateway owns the downstream CORS policy,
// so upstream Access-Control-* headers must not be forwarded.
func TestCopyPathSuccessHeaders_SkipsUpstreamCORS(t *testing.T) {
	// Simulate the gateway having already written its CORS headers.
	w := httptest.NewRecorder()
	writeCORSHeaders(w, &http.Request{Header: http.Header{}})

	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Access-Control-Allow-Origin", "*")
	resp.Header.Set("Access-Control-Expose-Headers", "X-Foo")
	resp.Header.Set("Access-Control-Allow-Credentials", "true")
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

	// Ordinary headers are still forwarded.
	if got := h.Get("X-Trace"); got != "abc" {
		t.Fatalf("X-Trace = %q, want %q", got, "abc")
	}

	// Content-Length is still skipped (pre-existing behavior).
	if vals := h.Values("Content-Length"); len(vals) != 0 {
		t.Fatalf("Content-Length leaked %v, want absent", vals)
	}
}
