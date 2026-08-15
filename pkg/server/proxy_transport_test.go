package server

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"picotera/pkg/configx"

	"golang.org/x/net/http2"
)

func TestApplyProxyConfig(t *testing.T) {
	t.Run("empty keeps environment proxy", func(t *testing.T) {
		tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
		applyProxyConfig(tr, "")
		if tr.Proxy == nil {
			t.Fatal("empty proxyURL should leave ProxyFromEnvironment in place")
		}
	})

	t.Run("direct disables proxying", func(t *testing.T) {
		tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
		applyProxyConfig(tr, "direct")
		if tr.Proxy != nil {
			t.Fatal(`"direct" should clear Proxy`)
		}
	})

	t.Run("url routes through that proxy", func(t *testing.T) {
		tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
		applyProxyConfig(tr, "http://proxy.example.com:8080")
		if tr.Proxy == nil {
			t.Fatal("Proxy should be set")
		}
		req := mustRequest(t, "https://api.example.com/v1/messages")
		got, err := tr.Proxy(req)
		if err != nil {
			t.Fatalf("Proxy: %v", err)
		}
		if got == nil || got.Host != "proxy.example.com:8080" {
			t.Fatalf("proxy target = %v, want proxy.example.com:8080", got)
		}
	})

	t.Run("unparsable url falls back to environment proxy", func(t *testing.T) {
		tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
		applyProxyConfig(tr, "http://[::1]:namedport")
		if tr.Proxy == nil {
			t.Fatal("invalid proxyURL should leave ProxyFromEnvironment in place")
		}
	})
}

func TestNewGatewayTransportDefault(t *testing.T) {
	tr, h2 := newGatewayTransport(&configx.Config{}, 5*time.Second, false)
	if h2 == nil {
		t.Fatal("h2 handle should be configured by default")
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be true by default")
	}
	if tr.MaxIdleConnsPerHost != 100 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 100", tr.MaxIdleConnsPerHost)
	}
}

func TestNewGatewayTransportDisableHTTP2(t *testing.T) {
	tr, h2 := newGatewayTransport(&configx.Config{GatewayDisableHTTP2: true}, 5*time.Second, false)
	if h2 != nil {
		t.Fatal("h2 handle should be nil when HTTP/2 is disabled")
	}
	if tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be false when HTTP/2 is disabled")
	}
	if tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto = %v, want a non-nil empty map", tr.TLSNextProto)
	}
	if tr.MaxIdleConnsPerHost != 100 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 100", tr.MaxIdleConnsPerHost)
	}
	// An insecure variant is built through the same path, so it must stay
	// HTTP/1.1 too.
	insecure, h2 := newGatewayTransport(&configx.Config{GatewayDisableHTTP2: true}, 5*time.Second, true)
	if h2 != nil {
		t.Fatal("h2 handle should be nil when HTTP/2 is disabled")
	}
	if insecure.TLSNextProto == nil || len(insecure.TLSNextProto) != 0 {
		t.Errorf("insecure TLSNextProto = %v, want a non-nil empty map", insecure.TLSNextProto)
	}
}

func TestNewGatewayTransportInsecureTLS(t *testing.T) {
	tr, h2 := newGatewayTransport(&configx.Config{}, 5*time.Second, true)
	if h2 == nil {
		t.Fatal("h2 handle should still be configured with insecure TLS")
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify should be set")
	}
	// ConfigureTransports appends the ALPN protocols to the TLS config it finds,
	// so the insecure config must have been installed before that call — an empty
	// NextProtos here would silently downgrade every HTTPS upstream to HTTP/1.1.
	if !slices.Contains(tr.TLSClientConfig.NextProtos, "h2") {
		t.Fatalf("NextProtos = %v, want it to contain h2", tr.TLSClientConfig.NextProtos)
	}
}

func TestProxyTransportCacheKeysOnProfile(t *testing.T) {
	config := &configx.Config{}
	var built int
	cache := newProxyTransportCache(func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport) {
		built++
		t, h2 := newGatewayTransport(config, 5*time.Second, profile.InsecureTLS)
		applyProxyConfig(t, profile.ProxyURL)
		return t, h2
	})

	secure := cache.get(transportProfile{ProxyURL: "direct"}, false)
	if again := cache.get(transportProfile{ProxyURL: "direct"}, false); again != secure {
		t.Fatal("the same key should return the cached transport")
	}
	insecure := cache.get(transportProfile{ProxyURL: "direct", InsecureTLS: true}, false)
	if insecure == secure {
		t.Fatal("a different TLS policy must get its own transport")
	}
	if secure.TLSClientConfig != nil && secure.TLSClientConfig.InsecureSkipVerify {
		t.Error("the secure variant must verify certificates")
	}
	if insecure.TLSClientConfig == nil || !insecure.TLSClientConfig.InsecureSkipVerify {
		t.Error("the insecure variant must skip verification")
	}
	if streaming := cache.get(transportProfile{ProxyURL: "direct"}, true); streaming == secure {
		t.Fatal("the streaming flag must get its own transport")
	}
	if built != 3 {
		t.Fatalf("build called %d times, want 3", built)
	}
}

// TestForwardRequestInsecureTLS drives forwardRequest against an httptest TLS
// server (self-signed cert): verification must fail by default and succeed once
// the provider's insecureTls flag is on.
func TestForwardRequestInsecureTLS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	for _, ephemeral := range []bool{false, true} {
		name := "cached"
		if ephemeral {
			name = "ephemeral"
		}
		t.Run(name, func(t *testing.T) {
			s := newEphemeralTestServer(ephemeral)

			_, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct"}, false)
			if err == nil {
				t.Fatal("expected a certificate verification error")
			}
			var certErr *tls.CertificateVerificationError
			if !errors.As(err, &certErr) {
				t.Fatalf("expected a certificate verification error, got %v", err)
			}

			resp, err := s.forwardRequest(mustRequest(t, upstream.URL), transportProfile{ProxyURL: "direct", InsecureTLS: true}, false)
			if err != nil {
				t.Fatalf("insecure request should succeed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}
