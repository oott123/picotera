package server

import (
	"net/http"
	"net/url"
	"sync"

	"golang.org/x/net/http2"
)

// transportProfile is the connection-level identity of an upstream transport.
// Both fields are properties of the connection itself — neither can be
// overridden per request — so they jointly decide which connection pool a
// request may reuse. Two providers pointing at the same host but disagreeing on
// certificate verification therefore land on different transports and never
// share a connection.
type transportProfile struct {
	// ProxyURL is "" for the environment proxy, "direct" for no proxy at all,
	// or a proxy URL string.
	ProxyURL    string
	InsecureTLS bool
}

// transportKey identifies a cached transport by its connection profile and
// whether it carries the streaming header timeout.
type transportKey struct {
	profile   transportProfile
	streaming bool
}

// transportEntry keeps the *http.Transport together with the *http2.Transport
// bound to it by http2.ConfigureTransports. The h2 handle is needed because
// std's CloseIdleConnections only reaches the h2 pool through the unexported
// altProto map.
type transportEntry struct {
	t1 *http.Transport
	h2 *http2.Transport
}

// proxyTransportCache lazily creates and caches transports keyed by
// (connection profile, streaming) — streaming and non-streaming requests use
// transports with a different ResponseHeaderTimeout.
//
// Every key gets a freshly built transport with its own
// http2.ConfigureTransports call; entries are never cloned off a shared base,
// so no two keys can end up sharing an h2 connection pool.
type proxyTransportCache struct {
	build func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport)
	mu    sync.RWMutex
	cache map[transportKey]transportEntry
}

func newProxyTransportCache(build func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport)) *proxyTransportCache {
	return &proxyTransportCache{
		build: build,
		cache: make(map[transportKey]transportEntry),
	}
}

// entry returns the cached transport for the key, building it on first use.
func (c *proxyTransportCache) entry(profile transportProfile, streaming bool) transportEntry {
	key := transportKey{profile: profile, streaming: streaming}
	c.mu.RLock()
	e, ok := c.cache[key]
	c.mu.RUnlock()
	if ok {
		return e
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Double-check after acquiring write lock.
	if e, ok = c.cache[key]; ok {
		return e
	}

	t1, h2 := c.build(profile, streaming)
	e = transportEntry{t1: t1, h2: h2}
	c.cache[key] = e
	return e
}

// get returns an http.Transport configured for the given connection profile and
// streaming flag.
func (c *proxyTransportCache) get(profile transportProfile, streaming bool) *http.Transport {
	return c.entry(profile, streaming).t1
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
// (profile, streaming), including its h2 pool. Used to evict a connection that
// just produced a header timeout; connections still carrying an active stream
// survive and are retired by the quarantine's req.Close instead.
func (c *proxyTransportCache) closeIdle(profile transportProfile, streaming bool) {
	e := c.entry(profile, streaming)
	e.t1.CloseIdleConnections()
	if e.h2 != nil {
		e.h2.CloseIdleConnections()
	}
}
