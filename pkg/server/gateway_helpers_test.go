package server

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestBuildUpstreamRequestSkipsAuthHeader(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "http://client.example/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	original.Header.Set("X-Local-Auth", "secret-identity")
	original.Header.Set("X-Keep", "keep-me")

	t.Run("skips configured auth header", func(t *testing.T) {
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", 0, nil, "X-Local-Auth")
		if err != nil {
			t.Fatalf("buildUpstreamRequest: %v", err)
		}
		if got := req.Header.Get("X-Local-Auth"); got != "" {
			t.Errorf("auth header forwarded: %q", got)
		}
		if got := req.Header.Get("X-Keep"); got != "keep-me" {
			t.Errorf("non-auth header dropped: %q", got)
		}
	})

	t.Run("case-insensitive match", func(t *testing.T) {
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", 0, nil, "x-local-auth")
		if err != nil {
			t.Fatalf("buildUpstreamRequest: %v", err)
		}
		if got := req.Header.Get("X-Local-Auth"); got != "" {
			t.Errorf("auth header forwarded with differing case: %q", got)
		}
	})

	t.Run("empty name skips nothing extra", func(t *testing.T) {
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", 0, nil, "")
		if err != nil {
			t.Fatalf("buildUpstreamRequest: %v", err)
		}
		if got := req.Header.Get("X-Local-Auth"); got != "secret-identity" {
			t.Errorf("auth header unexpectedly dropped: %q", got)
		}
	})
}

func TestBuildUpstreamRequestStripsPicoteraHeaders(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "http://client.example/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	original.Header.Set("X-PicoTera-OTR", "body")
	original.Header.Set("X-PicoTera-Foo", "bar")
	original.Header.Set("X-Keep", "keep-me")

	req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", 0, nil, "")
	if err != nil {
		t.Fatalf("buildUpstreamRequest: %v", err)
	}
	if got := req.Header.Get("X-PicoTera-OTR"); got != "" {
		t.Errorf("X-PicoTera-OTR forwarded upstream: %q", got)
	}
	if got := req.Header.Get("X-PicoTera-Foo"); got != "" {
		t.Errorf("X-PicoTera-Foo forwarded upstream: %q", got)
	}
	if got := req.Header.Get("X-Keep"); got != "keep-me" {
		t.Errorf("non-picotera header dropped: %q", got)
	}
}

func TestRedactUpstreamCredentials(t *testing.T) {
	t.Run("authorization keeps scheme prefix", func(t *testing.T) {
		h := http.Header{}
		h.Set("Authorization", "Bearer sk-supersecret")
		got, _ := redactUpstreamCredentials(h, "http://upstream.example/v1")
		if v := got.Get("Authorization"); v != "Bearer [REDACTED]" {
			t.Errorf("Authorization = %q, want %q", v, "Bearer [REDACTED]")
		}
	})

	t.Run("authorization without scheme replaced wholesale", func(t *testing.T) {
		h := http.Header{}
		h.Set("Authorization", "sk-supersecret")
		got, _ := redactUpstreamCredentials(h, "http://upstream.example/v1")
		if v := got.Get("Authorization"); v != "[REDACTED]" {
			t.Errorf("Authorization = %q, want %q", v, "[REDACTED]")
		}
	})

	t.Run("api key headers replaced wholesale", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Api-Key", "sk-anthropic")
		h.Set("X-Goog-Api-Key", "goog-key")
		got, _ := redactUpstreamCredentials(h, "http://upstream.example/v1")
		if v := got.Get("X-Api-Key"); v != "[REDACTED]" {
			t.Errorf("X-Api-Key = %q, want %q", v, "[REDACTED]")
		}
		if v := got.Get("X-Goog-Api-Key"); v != "[REDACTED]" {
			t.Errorf("X-Goog-Api-Key = %q, want %q", v, "[REDACTED]")
		}
	})

	t.Run("url key query param redacted, others intact", func(t *testing.T) {
		_, gotURL := redactUpstreamCredentials(http.Header{}, "http://upstream.example/v1beta/models/gemini:generateContent?key=goog-secret&alt=sse")
		u, err := url.Parse(gotURL)
		if err != nil {
			t.Fatalf("parse redacted url: %v", err)
		}
		if v := u.Query().Get("key"); v != "[REDACTED]" {
			t.Errorf("key param = %q, want %q", v, "[REDACTED]")
		}
		if v := u.Query().Get("alt"); v != "sse" {
			t.Errorf("alt param = %q, want %q", v, "sse")
		}
	})

	t.Run("no credentials returns input unchanged", func(t *testing.T) {
		h := http.Header{}
		h.Set("Content-Type", "application/json")
		rawURL := "http://upstream.example/v1/messages"
		got, gotURL := redactUpstreamCredentials(h, rawURL)
		if v := got.Get("Authorization"); v != "" {
			t.Errorf("unexpected Authorization: %q", v)
		}
		if got.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type altered: %q", got.Get("Content-Type"))
		}
		if gotURL != rawURL {
			t.Errorf("URL altered: %q", gotURL)
		}
	})

	t.Run("cf access headers replaced wholesale", func(t *testing.T) {
		h := http.Header{}
		h.Set("Cf-Access-Client-Id", "client-id-secret")
		h.Set("Cf-Access-Client-Secret", "client-secret-value")
		got, _ := redactUpstreamCredentials(h, "http://upstream.example/v1")
		if v := got.Get("Cf-Access-Client-Id"); v != "[REDACTED]" {
			t.Errorf("Cf-Access-Client-Id = %q, want %q", v, "[REDACTED]")
		}
		if v := got.Get("Cf-Access-Client-Secret"); v != "[REDACTED]" {
			t.Errorf("Cf-Access-Client-Secret = %q, want %q", v, "[REDACTED]")
		}
	})
}
