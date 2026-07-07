package server

import "net/http"

// writeCORSHeaders emits a permissive, credential-less CORS policy for the
// gateway-facing routes (catch-all gateway + /api/unified). Origin is fixed to
// "*" (no Allow-Credentials), so browsers must call these routes without
// cookies. Allow-Headers reflects the preflight's requested headers when
// present so that Authorization (which a bare "*" does not cover per the CORS
// spec) is permitted.
func writeCORSHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		h.Set("Access-Control-Allow-Headers", reqHeaders)
	} else {
		h.Set("Access-Control-Allow-Headers", "*")
	}
	h.Set("Access-Control-Expose-Headers", "*")
	h.Set("Access-Control-Max-Age", "86400")
}

// corsMiddleware writes the CORS headers and short-circuits OPTIONS preflight
// requests with 204. Used for the /api/unified routes; the catch-all gateway
// handles CORS inline in gatewayHandler.ServeHTTP so static-asset fallbacks
// stay header-free.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
