package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
)

// registerOp wraps huma.Register so every operation declares its auth
// requirement at the call site. The wrapper appends a per-operation
// middleware that reads *auth.Session from the request context (placed
// there by auth.LoadSession on the chi router) and calls auth.Check
// before invoking the handler. On failure, the canonical AuthError is
// written via huma.WriteErr — clients see {code, message} in the body
// and the correct HTTP status.
func registerOp[I, O any](
	api huma.API,
	op huma.Operation,
	handler func(context.Context, *I) (*O, error),
	req contract.AuthRequirement,
) {
	if req.Kind != contract.AuthPublic {
		// Public ops omit the security requirement in OpenAPI; everyone else
		// references the picoteraSession scheme (registered at server boot).
		op.Security = append(op.Security, map[string][]string{
			"picoteraSession": {},
		})
	}
	op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
		sess := auth.SessionFromContext(ctx.Context())
		if err := auth.Check(sess, req); err != nil {
			ae := auth.AsAuthError(err)
			_ = huma.WriteErr(api, ctx, ae.Status, ae.Message)
			return
		}
		next(ctx)
	})
	huma.Register(api, op, handler)
}

// registerOpHTTP wraps a raw chi handler with the same auth.Check gate.
// Used for endpoints that need to write Set-Cookie headers and read
// streaming/JSON request bodies — Huma's typed I/O doesn't accommodate
// cookie writes ergonomically. The trade-off is no OpenAPI doc for these
// routes; document them in api.md manually.
//
// router is anything that exposes the chi method we need. We accept the
// minimal interface so this helper composes with chi.Mux or a sub-router.
type chiRouter interface {
	Method(method, pattern string, h http.Handler)
}

func registerOpHTTP(
	router chiRouter,
	method, path string,
	req contract.AuthRequirement,
	h http.HandlerFunc,
) {
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := auth.SessionFromContext(r.Context())
		if err := auth.Check(sess, req); err != nil {
			ae := auth.AsAuthError(err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(ae.Status)
			// Minimal envelope mirroring huma.WriteErr's shape.
			_, _ = w.Write([]byte(`{"$schema":"","title":"` + http.StatusText(ae.Status) + `","status":` + itoa(ae.Status) + `,"detail":"` + jsonStr(ae.Code) + `"}`))
			return
		}
		h(w, r)
	})
	router.Method(method, path, wrapped)
}

// itoa avoids strconv import for a single use.
func itoa(i int) string {
	// Only used for small HTTP status codes (100-599). Sprintf would also work.
	if i == 0 {
		return "0"
	}
	negative := false
	if i < 0 {
		negative = true
		i = -i
	}
	var b [4]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if negative {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// jsonStr escapes a string for embedding in a small fixed-shape JSON
// response above. Escapes the minimal set: quote, backslash, control chars.
func jsonStr(s string) string {
	// Codes are constants from pkg/auth/errors.go — ASCII letters + underscore.
	// But escape defensively in case someone adds an unusual code later.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			out = append(out, '\\', c)
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if c < 0x20 {
				continue // drop weird control chars
			}
			out = append(out, c)
		}
	}
	return string(out)
}
