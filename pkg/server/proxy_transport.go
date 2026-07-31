package server

import (
	"net/http"
	"net/url"
	"sync"

	"golang.org/x/net/http2"
)

// transportKey identifies a cached *http.Transport by its proxy configuration
// and whether it carries the streaming header timeout.
type transportKey struct {
	proxy     string
	streaming bool
}

// proxyTransportCache lazily creates and caches *http.Transport instances
// keyed by (proxy configuration, streaming) — streaming and non-streaming
// requests use bases with different ResponseHeaderTimeout.
//
// The two *http2.Transport handles are the ones bound to the bases by
// http2.ConfigureTransports. They are kept because that binding is where every
// h2 connection actually lives: Clone shares TLSNextProto, so a proxy variant's
// h2 connections land in its base's h2 pool, and a variant's
// CloseIdleConnections cannot reach that pool (Clone drops the unexported
// altProto map). closeIdle therefore has to call them explicitly.
type proxyTransportCache struct {
	streamBase    *http.Transport
	nonStreamBase *http.Transport
	streamH2      *http2.Transport
	nonStreamH2   *http2.Transport
	mu            sync.RWMutex
	cache         map[transportKey]*http.Transport
}

func newProxyTransportCache(streamBase, nonStreamBase *http.Transport, streamH2, nonStreamH2 *http2.Transport) *proxyTransportCache {
	return &proxyTransportCache{
		streamBase:    streamBase,
		nonStreamBase: nonStreamBase,
		streamH2:      streamH2,
		nonStreamH2:   nonStreamH2,
		cache:         make(map[transportKey]*http.Transport),
	}
}

func (c *proxyTransportCache) base(streaming bool) *http.Transport {
	if streaming {
		return c.streamBase
	}
	return c.nonStreamBase
}

// get returns an http.Transport configured for the given proxy URL and
// streaming flag.
//   - "" (empty) → ProxyFromEnvironment (default behavior, uses base transport)
//   - "direct"   → no proxy; connect directly
//   - URL string → use that URL as the proxy (e.g. "http://proxy:8080")
//
// The streaming flag selects between the streaming and non-streaming bases,
// which differ only in ResponseHeaderTimeout.
func (c *proxyTransportCache) get(proxyURL string, streaming bool) *http.Transport {
	base := c.base(streaming)
	if proxyURL == "" {
		return base
	}

	key := transportKey{proxy: proxyURL, streaming: streaming}
	c.mu.RLock()
	t, ok := c.cache[key]
	c.mu.RUnlock()
	if ok {
		return t
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Double-check after acquiring write lock.
	if t, ok = c.cache[key]; ok {
		return t
	}

	cloned := base.Clone()
	applyProxyConfig(cloned, proxyURL)
	c.cache[key] = cloned
	return cloned
}

// applyProxyConfig sets t.Proxy per the proxyURL semantics shared by the
// transport cache and ephemeral transports: "" keeps ProxyFromEnvironment,
// "direct" disables proxying, a URL string routes through that proxy, and an
// unparsable URL falls back to ProxyFromEnvironment (API validation catches it
// earlier; this mirrors the cache's historical fallback-to-base behavior).
func applyProxyConfig(t *http.Transport, proxyURL string) {
	switch proxyURL {
	case "":
		// Leave the transport's ProxyFromEnvironment default in place.
	case "direct":
		t.Proxy = nil // no proxy at all
	default:
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return
		}
		t.Proxy = http.ProxyURL(parsed)
	}
}

// closeIdle drops the idle connections of the transport variant selected by
// (proxyURL, streaming), including the h2 pool the variant shares with its base.
// Used to evict a connection that just produced a header timeout; connections
// still carrying an active stream survive and are retired by the quarantine's
// req.Close instead.
func (c *proxyTransportCache) closeIdle(proxyURL string, streaming bool) {
	c.get(proxyURL, streaming).CloseIdleConnections()
	h2 := c.nonStreamH2
	if streaming {
		h2 = c.streamH2
	}
	if h2 != nil {
		h2.CloseIdleConnections()
	}
}
