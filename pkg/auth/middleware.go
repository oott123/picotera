package auth

import (
	"errors"
	"net/http"
)

// Middleware returns a chi middleware that authenticates internal management
// API requests. It is mounted only on the /api/picotera sub-router (see
// server.go), so the gateway catch-all, /api/unified, and static assets never
// reach it — they authenticate via API key. On success the resolved user is
// stored on the request context; on failure a 401 JSON response is written and
// the handler chain is short-circuited.
func Middleware(resolver *Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := resolver.ResolveWithImpersonation(r.Context(), r)
			if err != nil || user == nil {
				status, body := impersonationErrorResponse(err)
				// Point the dashboard at the login flow instead of making it
				// guess. Only oidc mode has one, so no other mode can be pushed
				// into a redirect loop by this header.
				if status == http.StatusUnauthorized {
					if loginURL := resolver.LoginURL(); loginURL != "" {
						w.Header().Set(LoginURLHeader, loginURL)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
				return
			}

			// Resolve just slid the session's database expiry; re-issue the
			// cookie so both sides expire at the same moment.
			resolver.RefreshSessionCookie(w, r)
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

// impersonationErrorResponse maps an auth error to an HTTP status and JSON body.
// Impersonation errors get distinct codes; everything else (including a nil
// error with a nil user) falls back to 401 unauthorized.
func impersonationErrorResponse(err error) (int, string) {
	switch {
	case errors.Is(err, ErrImpersonationForbidden):
		return http.StatusForbidden, `{"message":"forbidden"}`
	case errors.Is(err, ErrImpersonationBadID):
		return http.StatusBadRequest, `{"message":"invalid impersonation user id"}`
	case errors.Is(err, ErrImpersonationTargetNotFound):
		return http.StatusNotFound, `{"message":"impersonation target not found"}`
	default:
		return http.StatusUnauthorized, `{"message":"unauthorized"}`
	}
}
