package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"picotera/pkg/configx"

	"golang.org/x/net/http2"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

func TestIsAwaitHeadersTimeout(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"h2", errors.New("http2: timeout awaiting response headers"), true},
		{"h1", errors.New("net/http: timeout awaiting response headers"), true},
		{"wrapped", fmt.Errorf("forward: %w", errors.New("http2: timeout awaiting response headers")), true},
		{"url error", &url.Error{Op: "Post", URL: "https://x/y", Err: errors.New("net/http: timeout awaiting response headers")}, true},
		{"deadline", context.DeadlineExceeded, false},
		{"canceled", context.Canceled, false},
		{"dns timeout", &net.DNSError{Err: "i/o timeout", IsTimeout: true}, false},
		{"dial timeout", errors.New("dial tcp 1.2.3.4:443: i/o timeout"), false},
		{"tls handshake timeout", errors.New("net/http: TLS handshake timeout"), false},
		{"plain", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAwaitHeadersTimeout(c.err); got != c.want {
				t.Fatalf("isAwaitHeadersTimeout(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// newTestQuarantine returns a quarantine whose clock is the returned pointer,
// so a test can advance time without sleeping.
func newTestQuarantine() (*connQuarantine, *time.Time) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	q := newConnQuarantine()
	q.now = func() time.Time { return now }
	return q, &now
}

func TestConnQuarantineMarkAndActive(t *testing.T) {
	q, _ := newTestQuarantine()

	if q.active(transportProfile{}, true, "api.example.com") {
		t.Fatal("fresh quarantine should not be active")
	}

	q.mark(transportProfile{}, true, "api.example.com")
	if !q.active(transportProfile{}, true, "api.example.com") {
		t.Fatal("marked key should be active")
	}

	// Different host / proxy / streaming flag are separate pools.
	if q.active(transportProfile{}, true, "other.example.com") {
		t.Error("different host should not be quarantined")
	}
	if q.active(transportProfile{ProxyURL: "http://proxy:8080"}, true, "api.example.com") {
		t.Error("different proxy should not be quarantined")
	}
	if q.active(transportProfile{InsecureTLS: true}, true, "api.example.com") {
		t.Error("different TLS policy should not be quarantined")
	}
	if q.active(transportProfile{}, false, "api.example.com") {
		t.Error("different streaming flag should not be quarantined")
	}
}

func TestConnQuarantineExpiry(t *testing.T) {
	q, now := newTestQuarantine()
	q.mark(transportProfile{}, false, "api.example.com")

	*now = now.Add(connQuarantineTTL - time.Second)
	if !q.active(transportProfile{}, false, "api.example.com") {
		t.Fatal("should still be active before TTL")
	}

	*now = now.Add(2 * time.Second) // past TTL
	if q.active(transportProfile{}, false, "api.example.com") {
		t.Fatal("should have expired")
	}
	q.mu.Lock()
	n := len(q.until)
	q.mu.Unlock()
	if n != 0 {
		t.Fatalf("expired entry should be dropped, got %d entries", n)
	}
}

func TestConnQuarantineMarkSweepsExpired(t *testing.T) {
	q, now := newTestQuarantine()
	q.mark(transportProfile{}, false, "stale.example.com")

	*now = now.Add(connQuarantineTTL + time.Second)
	q.mark(transportProfile{}, false, "fresh.example.com")

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.until) != 1 {
		t.Fatalf("mark should sweep expired entries, got %d entries", len(q.until))
	}
	if _, ok := q.until[quarantineKey{host: "fresh.example.com"}]; !ok {
		t.Fatal("fresh entry missing")
	}
}

// newForwardTestServer builds a Server wired with just the transport plumbing
// forwardRequest needs, following the package's hand-built-struct test style.
func newForwardTestServer(t *testing.T) *Server {
	t.Helper()
	config := &configx.Config{}
	return &Server{
		config: config,
		proxyCache: newProxyTransportCache(func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport) {
			t, h2 := newGatewayTransport(config, 5*time.Second, profile.InsecureTLS)
			applyProxyConfig(t, profile.ProxyURL)
			return t, h2
		}),
		connQuarantine: newConnQuarantine(),
	}
}

func TestForwardRequestQuarantineClosesConnection(t *testing.T) {
	var gotClose bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClose = r.Close
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	host := mustHost(t, upstream.URL)

	t.Run("not quarantined", func(t *testing.T) {
		s := newForwardTestServer(t)
		resp, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
		if err != nil {
			t.Fatalf("forwardRequest: %v", err)
		}
		resp.Body.Close()
		if gotClose {
			t.Error("upstream saw Connection: close without a quarantine")
		}
	})

	t.Run("quarantined", func(t *testing.T) {
		s := newForwardTestServer(t)
		s.connQuarantine.mark(transportProfile{ProxyURL: "direct"}, false, host)
		resp, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
		if err != nil {
			t.Fatalf("forwardRequest: %v", err)
		}
		resp.Body.Close()
		if !gotClose {
			t.Error("quarantined host should send Connection: close")
		}
	})
}

func TestForwardRequestQuarantinesOnHeaderTimeout(t *testing.T) {
	// ResponseHeaderTimeout is a transport field, so the upstream just stalls
	// past the (very short) timeout configured below.
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		upstream.Close()
	}()

	config := &configx.Config{}
	s := &Server{
		config: config,
		proxyCache: newProxyTransportCache(func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport) {
			t, h2 := newGatewayTransport(config, 50*time.Millisecond, profile.InsecureTLS)
			applyProxyConfig(t, profile.ProxyURL)
			return t, h2
		}),
		connQuarantine: newConnQuarantine(),
	}

	_, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, true)
	if err == nil {
		t.Fatal("expected a header timeout")
	}
	if !isAwaitHeadersTimeout(err) {
		t.Fatalf("expected an await-headers timeout, got %v", err)
	}
	if !s.connQuarantine.active(transportProfile{ProxyURL: "direct"}, true, mustHost(t, upstream.URL)) {
		t.Fatal("header timeout should quarantine the host")
	}
}

func TestForwardRequestLogsConnectionIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	hook := test.NewLocal(logrus.StandardLogger())
	defer hook.Reset()

	s := newForwardTestServer(t)
	resp, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
	if err != nil {
		t.Fatalf("forwardRequest: %v", err)
	}
	resp.Body.Close()

	var entry *logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Message == "got upstream connection" {
			entry = e
		}
	}
	if entry == nil {
		t.Fatal("no GotConn log entry")
	}
	for _, field := range []string{"conn_local", "conn_remote"} {
		if v, _ := entry.Data[field].(string); v == "" {
			t.Errorf("%s is empty, got %#v", field, entry.Data[field])
		}
	}
	if _, ok := entry.Data["conn_reused"].(bool); !ok {
		t.Errorf("conn_reused missing, got %#v", entry.Data["conn_reused"])
	}
}

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func mustHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return u.Host
}
