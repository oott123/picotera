package server

import "strings"

// shouldStripUpstreamHeader reports whether lower (the lowercased header
// name) is an upstream response header that must never be forwarded to the
// client. The gateway is the client-facing authority, so headers that serve
// the upstream's own infrastructure or policy are dropped:
//   - Access-Control-*: the gateway owns the downstream CORS policy
//     (writeCORSHeaders); an upstream "Access-Control-Allow-Origin: *" would
//     be appended to the gateway's own "*" and serialize as "*, *" (and
//     similarly for Access-Control-Expose-Headers).
//   - Alt-Svc: points at the upstream's own alternative endpoints, which the
//     client cannot reach (no upstream credentials) and must not bypass the
//     gateway to reach.
//   - Nel / Report-To: the upstream's error/reporting collection endpoints,
//     which the client cannot and should not report to.
//   - Vary: the upstream's caching hint, which is unreliable after the
//     gateway rewrites the body/headers (bridging, web-search emulation,
//     CORS injection, conditional Content-Encoding stripping). The gateway
//     is not a caching proxy and does not emit its own Vary.
func shouldStripUpstreamHeader(lower string) bool {
	switch lower {
	case "alt-svc", "nel", "report-to", "vary":
		return true
	}
	return strings.HasPrefix(lower, "access-control-")
}
