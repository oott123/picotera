package server

import (
	"net/http"
	"testing"
	"time"

	"picotera/pkg/configx"
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
	tr, h2 := newGatewayTransport(&configx.Config{}, 5*time.Second)
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
	tr, h2 := newGatewayTransport(&configx.Config{GatewayDisableHTTP2: true}, 5*time.Second)
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
	// Proxy variants are Clone()s of the base; the empty map must survive so
	// they stay HTTP/1.1 too.
	cloned := tr.Clone()
	if cloned.TLSNextProto == nil || len(cloned.TLSNextProto) != 0 {
		t.Errorf("cloned TLSNextProto = %v, want a non-nil empty map", cloned.TLSNextProto)
	}
}
