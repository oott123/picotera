package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"picotera/pkg/configx"
	"picotera/pkg/errorx"

	"golang.org/x/net/http2"
)

func TestBuildUpstreamRequestSkipsAuthHeader(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "http://client.example/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	original.Header.Set("X-Local-Auth", "secret-identity")
	original.Header.Set("X-Keep", "keep-me")

	t.Run("skips configured auth header", func(t *testing.T) {
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", "", 0, nil, "X-Local-Auth")
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
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", "", 0, nil, "x-local-auth")
		if err != nil {
			t.Fatalf("buildUpstreamRequest: %v", err)
		}
		if got := req.Header.Get("X-Local-Auth"); got != "" {
			t.Errorf("auth header forwarded with differing case: %q", got)
		}
	})

	t.Run("empty name skips nothing extra", func(t *testing.T) {
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", "", 0, nil, "")
		if err != nil {
			t.Fatalf("buildUpstreamRequest: %v", err)
		}
		if got := req.Header.Get("X-Local-Auth"); got != "secret-identity" {
			t.Errorf("auth header unexpectedly dropped: %q", got)
		}
	})
}

// TestBuildUpstreamRequestAppendPath pins the prefix endpoint's suffix landing
// on the upstream URL — including the case where the upstream URL carries its
// own query string, which must stay after the appended path.
func TestBuildUpstreamRequestAppendPath(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "http://client.example/api/codex/responses", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	cases := []struct {
		name        string
		upstreamURL string
		appendPath  string
		wantURL     string
	}{
		{"plain", "https://example.com/codex", "/responses", "https://example.com/codex/responses"},
		{"nested", "https://example.com/codex", "/responses/compact", "https://example.com/codex/responses/compact"},
		{"query string survives", "https://example.com/codex?v=1", "/responses", "https://example.com/codex/responses?v=1"},
		{"fragment survives", "https://example.com/codex#f", "/responses", "https://example.com/codex/responses#f"},
		{"empty suffix leaves url alone", "https://example.com/v1/messages", "", "https://example.com/v1/messages"},
	}
	for _, tc := range cases {
		req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), tc.upstreamURL, tc.appendPath, "", "", 0, nil, "")
		if err != nil {
			t.Fatalf("%s: buildUpstreamRequest: %v", tc.name, err)
		}
		if got := req.URL.String(); got != tc.wantURL {
			t.Errorf("%s: url = %s, want %s", tc.name, got, tc.wantURL)
		}
	}
}

func TestBuildUpstreamRequestStripsPicoteraHeaders(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "http://client.example/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	original.Header.Set("X-PicoTera-OTR", "body")
	original.Header.Set("X-PicoTera-Foo", "bar")
	original.Header.Set("X-Keep", "keep-me")

	req, _, err := buildUpstreamRequest(context.Background(), original, []byte(`{}`), "http://upstream.example/v1/messages", "", "", "", 0, nil, "")
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
		got, _ := redactRequestCredentials(h, "http://upstream.example/v1")
		if v := got.Get("Authorization"); v != "Bearer [REDACTED]" {
			t.Errorf("Authorization = %q, want %q", v, "Bearer [REDACTED]")
		}
	})

	t.Run("authorization without scheme replaced wholesale", func(t *testing.T) {
		h := http.Header{}
		h.Set("Authorization", "sk-supersecret")
		got, _ := redactRequestCredentials(h, "http://upstream.example/v1")
		if v := got.Get("Authorization"); v != "[REDACTED]" {
			t.Errorf("Authorization = %q, want %q", v, "[REDACTED]")
		}
	})

	t.Run("api key headers replaced wholesale", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Api-Key", "sk-anthropic")
		h.Set("X-Goog-Api-Key", "goog-key")
		got, _ := redactRequestCredentials(h, "http://upstream.example/v1")
		if v := got.Get("X-Api-Key"); v != "[REDACTED]" {
			t.Errorf("X-Api-Key = %q, want %q", v, "[REDACTED]")
		}
		if v := got.Get("X-Goog-Api-Key"); v != "[REDACTED]" {
			t.Errorf("X-Goog-Api-Key = %q, want %q", v, "[REDACTED]")
		}
	})

	t.Run("url key query param redacted, others intact", func(t *testing.T) {
		_, gotURL := redactRequestCredentials(http.Header{}, "http://upstream.example/v1beta/models/gemini:generateContent?key=goog-secret&alt=sse")
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
		got, gotURL := redactRequestCredentials(h, rawURL)
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
		got, _ := redactRequestCredentials(h, "http://upstream.example/v1")
		if v := got.Get("Cf-Access-Client-Id"); v != "[REDACTED]" {
			t.Errorf("Cf-Access-Client-Id = %q, want %q", v, "[REDACTED]")
		}
		if v := got.Get("Cf-Access-Client-Secret"); v != "[REDACTED]" {
			t.Errorf("Cf-Access-Client-Secret = %q, want %q", v, "[REDACTED]")
		}
	})
}

func TestRedactResponseHeaders(t *testing.T) {
	t.Run("single cookie value redacted, attributes preserved", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "session=abc123; Path=/; HttpOnly")
		got := redactResponseHeaders(h)
		want := "session=[REDACTED]; Path=/; HttpOnly"
		if vals := got.Values("Set-Cookie"); len(vals) != 1 || vals[0] != want {
			t.Errorf("Set-Cookie = %v, want [%q]", vals, want)
		}
	})

	t.Run("multiple set-cookie headers redacted independently", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "session=abc123; Path=/; HttpOnly")
		h.Add("Set-Cookie", "token=xyz789; Path=/; Secure")
		got := redactResponseHeaders(h)
		want := []string{
			"session=[REDACTED]; Path=/; HttpOnly",
			"token=[REDACTED]; Path=/; Secure",
		}
		if vals := got.Values("Set-Cookie"); len(vals) != 2 || vals[0] != want[0] || vals[1] != want[1] {
			t.Errorf("Set-Cookie = %v, want %v", vals, want)
		}
	})

	t.Run("cookie without attributes", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "token=xyz")
		got := redactResponseHeaders(h)
		want := "token=[REDACTED]"
		if vals := got.Values("Set-Cookie"); len(vals) != 1 || vals[0] != want {
			t.Errorf("Set-Cookie = %v, want [%q]", vals, want)
		}
	})

	t.Run("empty value redacted", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "session=; Path=/")
		got := redactResponseHeaders(h)
		want := "session=[REDACTED]; Path=/"
		if vals := got.Values("Set-Cookie"); len(vals) != 1 || vals[0] != want {
			t.Errorf("Set-Cookie = %v, want [%q]", vals, want)
		}
	})

	t.Run("value with equals signs (base64)", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "s=a=b=c; Path=/")
		got := redactResponseHeaders(h)
		want := "s=[REDACTED]; Path=/"
		if vals := got.Values("Set-Cookie"); len(vals) != 1 || vals[0] != want {
			t.Errorf("Set-Cookie = %v, want [%q]", vals, want)
		}
	})

	t.Run("quoted value with semicolon inside quotes", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", `foo="a;b"; Path=/`)
		got := redactResponseHeaders(h)
		want := "foo=[REDACTED]; Path=/"
		if vals := got.Values("Set-Cookie"); len(vals) != 1 || vals[0] != want {
			t.Errorf("Set-Cookie = %v, want [%q]", vals, want)
		}
	})

	t.Run("malformed header without equals replaced wholesale", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "justflags")
		got := redactResponseHeaders(h)
		want := "[REDACTED]"
		if vals := got.Values("Set-Cookie"); len(vals) != 1 || vals[0] != want {
			t.Errorf("Set-Cookie = %v, want [%q]", vals, want)
		}
	})

	t.Run("no set-cookie leaves header unchanged", func(t *testing.T) {
		h := http.Header{}
		h.Set("Content-Type", "application/json")
		got := redactResponseHeaders(h)
		if vals := got.Values("Set-Cookie"); len(vals) != 0 {
			t.Errorf("unexpected Set-Cookie: %v", vals)
		}
		if v := got.Get("Content-Type"); v != "application/json" {
			t.Errorf("Content-Type altered: %q", v)
		}
	})

	t.Run("non-cookie headers unaffected", func(t *testing.T) {
		h := http.Header{}
		h.Set("Content-Type", "application/json")
		h.Set("X-Trace-Id", "trace-123")
		h.Add("Set-Cookie", "session=abc123; Path=/")
		got := redactResponseHeaders(h)
		if v := got.Get("Content-Type"); v != "application/json" {
			t.Errorf("Content-Type altered: %q", v)
		}
		if v := got.Get("X-Trace-Id"); v != "trace-123" {
			t.Errorf("X-Trace-Id altered: %q", v)
		}
		if v := got.Values("Set-Cookie")[0]; v != "session=[REDACTED]; Path=/" {
			t.Errorf("Set-Cookie = %q, want %q", v, "session=[REDACTED]; Path=/")
		}
	})
}

// newEphemeralTestServer builds a Server whose forwardRequest path is driven by
// the GatewayEphemeralTransport switch, with keep-alives otherwise on so the
// cached-transport case really does reuse connections.
func newEphemeralTestServer(ephemeral bool) *Server {
	config := &configx.Config{
		GatewayResponseHeaderTimeout: 5 * time.Second,
		GatewayReadTimeout:           5 * time.Second,
		GatewayEphemeralTransport:    ephemeral,
	}
	return &Server{
		config: config,
		proxyCache: newProxyTransportCache(func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport) {
			responseHeaderTimeout := config.GatewayResponseHeaderTimeout
			if !streaming {
				responseHeaderTimeout = config.GatewayReadTimeout
			}
			t, h2 := newGatewayTransport(config, responseHeaderTimeout, profile.InsecureTLS)
			applyProxyConfig(t, profile.ProxyURL)
			return t, h2
		}),
		connQuarantine: newConnQuarantine(),
	}
}

func TestForwardRequestEphemeralTransport(t *testing.T) {
	var remoteAddrs []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteAddrs = append(remoteAddrs, r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	twoRequests := func(t *testing.T, s *Server) []string {
		t.Helper()
		remoteAddrs = nil
		for range 2 {
			resp, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
			if err != nil {
				t.Fatalf("forwardRequest: %v", err)
			}
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Fatalf("drain body: %v", err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatalf("close body: %v", err)
			}
		}
		if len(remoteAddrs) != 2 {
			t.Fatalf("upstream saw %d requests, want 2", len(remoteAddrs))
		}
		return remoteAddrs
	}

	t.Run("ephemeral does not reuse the connection", func(t *testing.T) {
		got := twoRequests(t, newEphemeralTestServer(true))
		if got[0] == got[1] {
			t.Fatalf("both attempts came from %s; an ephemeral transport must not reuse the connection", got[0])
		}
	})

	t.Run("cached transport reuses the connection", func(t *testing.T) {
		got := twoRequests(t, newEphemeralTestServer(false))
		if got[0] != got[1] {
			t.Fatalf("attempts came from %s and %s; the cached transport should reuse the connection", got[0], got[1])
		}
	})

	t.Run("ephemeral body recycles the transport on close", func(t *testing.T) {
		resp, err := newEphemeralTestServer(true).forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
		if err != nil {
			t.Fatalf("forwardRequest: %v", err)
		}
		body, ok := resp.Body.(*closeIdleOnCloseBody)
		if !ok {
			t.Fatalf("resp.Body = %T, want *closeIdleOnCloseBody", resp.Body)
		}
		if _, err := io.Copy(io.Discard, body); err != nil {
			t.Fatalf("drain body: %v", err)
		}
		if err := body.Close(); err != nil {
			t.Fatalf("close body: %v", err)
		}
		// The sync.Once makes a second Close (the gateway's deferred close after
		// an explicit one) harmless.
		if err := body.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
	})

	t.Run("cached transport body is not wrapped", func(t *testing.T) {
		resp, err := newEphemeralTestServer(false).forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
		if err != nil {
			t.Fatalf("forwardRequest: %v", err)
		}
		defer resp.Body.Close()
		if _, ok := resp.Body.(*closeIdleOnCloseBody); ok {
			t.Fatal("cached-transport response body should not carry the recycling wrapper")
		}
	})
}

// TestExtractModel pins the routing decision the extractor produces, in
// particular the prefix-endpoint degradation: an *absent* body field routes as
// no-model, while a present-but-invalid one is always a 400.
func TestExtractModel(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		modelPath string
		pathVars  map[string]string
		optional  bool
		wantModel string
		wantErr   bool
	}{
		{name: "body model", body: `{"model":"gpt-5"}`, modelPath: "model", wantModel: "gpt-5"},
		{name: "nested model path", body: `{"req":{"model":"gpt-5"}}`, modelPath: "req.model", wantModel: "gpt-5"},
		{name: "absent required", body: `{}`, modelPath: "model", wantErr: true},
		{name: "absent optional", body: `{}`, modelPath: "model", optional: true},
		{name: "absent optional nested", body: `{"req":{}}`, modelPath: "req.model", optional: true},
		{name: "empty string required", body: `{"model":""}`, modelPath: "model", wantErr: true},
		{name: "empty string optional", body: `{"model":""}`, modelPath: "model", optional: true, wantErr: true},
		{name: "number optional", body: `{"model":123}`, modelPath: "model", optional: true, wantErr: true},
		{name: "null optional", body: `{"model":null}`, modelPath: "model", optional: true, wantErr: true},
		{name: "object optional", body: `{"model":{"name":"gpt-5"}}`, modelPath: "model", optional: true, wantErr: true},
		{name: "path var", body: `{}`, modelPath: "{model}", pathVars: map[string]string{"model": "gemini-2.5-pro"}, wantModel: "gemini-2.5-pro"},
		// The degradation covers the body branch only: an unbound path variable
		// is a 400 even when optional. (The combination is rejected at endpoint
		// configuration time anyway — a prefix path carries no variables.)
		{name: "path var unset optional", body: `{"model":"gpt-5"}`, modelPath: "{model}", optional: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := extractModel([]byte(tc.body), tc.modelPath, tc.pathVars, tc.optional)
			if tc.wantErr {
				var gerr *gatewayError
				if !errors.As(err, &gerr) {
					t.Fatalf("expected gatewayError, got %v", err)
				}
				if gerr.status != http.StatusBadRequest || gerr.code != errorx.ModelNotFound.Error() {
					t.Errorf("got status=%d code=%s, want 400 %s", gerr.status, gerr.code, errorx.ModelNotFound.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("extractModel: %v", err)
			}
			if mode.OriginalModel != tc.wantModel || mode.HasModel != (tc.wantModel != "") {
				t.Errorf("got %+v, want model %q", mode, tc.wantModel)
			}
		})
	}
}
