package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"
)

// Login starts the authorization code flow: it stores the CSRF state (and the
// PKCE verifier) in the cookie, then redirects to the IdP.
func (o *OIDC) Login(w http.ResponseWriter, r *http.Request) {
	redirectTo, ok := validateRedirectTo(r.URL.Query().Get("redirect_to"))
	if !ok {
		writeJSONMessage(w, http.StatusBadRequest, "invalid redirect_to")
		return
	}

	bundle, err := o.bundle(r.Context())
	if err != nil {
		logx.WithContext(r.Context()).WithError(err).Error("oidc provider metadata unavailable")
		writeJSONMessage(w, http.StatusBadGateway, "oidc discovery failed")
		return
	}

	state, err := newStateToken()
	if err != nil {
		logx.WithContext(r.Context()).WithError(err).Error("failed to generate oidc state")
		writeJSONMessage(w, http.StatusInternalServerError, "internal error")
		return
	}

	var verifier string
	var opts []oauth2.AuthCodeOption
	if bundle.pkce {
		verifier = oauth2.GenerateVerifier()
		opts = append(opts, oauth2.S256ChallengeOption(verifier))
	}

	if err := saveState(o.stores.state, r, w, state, verifier, redirectTo); err != nil {
		logx.WithContext(r.Context()).WithError(err).Error("failed to write oidc state cookie")
		writeJSONMessage(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.Redirect(w, r, bundle.oauth.AuthCodeURL(state, opts...), http.StatusFound)
}

// Callback completes the flow: it checks the state, exchanges the code, reads
// the identity from the userinfo endpoint, and opens a session.
//
// Errors here are rendered as HTML because the browser lands on this url by
// top-level navigation — a JSON body would just be shown as raw text.
func (o *OIDC) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	if idpError := query.Get("error"); idpError != "" {
		detail := idpError
		if description := query.Get("error_description"); description != "" {
			detail = idpError + "：" + description
		}
		writeAuthError(w, http.StatusBadRequest, "登录失败", "身份提供方拒绝了这次登录："+detail)
		return
	}

	state, verifier, redirectTo, ok := readState(o.stores.state, r)
	if !ok {
		// The state cookie is missing, expired, or belongs to a login that a
		// later one in another tab has already replaced.
		writeAuthError(w, http.StatusBadRequest, "登录失败", "登录状态已失效，请重新登录。")
		return
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(query.Get("state"))) != 1 {
		writeAuthError(w, http.StatusBadRequest, "登录失败", "登录状态校验未通过，请重新登录。")
		return
	}
	code := query.Get("code")
	if code == "" {
		writeAuthError(w, http.StatusBadRequest, "登录失败", "身份提供方没有返回授权码，请重新登录。")
		return
	}

	bundle, err := o.bundle(ctx)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Error("oidc provider metadata unavailable")
		writeAuthError(w, http.StatusBadGateway, "登录失败", "无法读取身份提供方的配置，请稍后重试。")
		return
	}

	var opts []oauth2.AuthCodeOption
	if verifier != "" {
		opts = append(opts, oauth2.VerifierOption(verifier))
	}
	token, err := bundle.oauth.Exchange(o.backChannelContext(ctx), code, opts...)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Warn("oidc token exchange failed")
		writeAuthError(w, http.StatusBadGateway, "登录失败", "向身份提供方换取令牌失败，请重新登录。")
		return
	}

	subject, displayName, err := o.identity(ctx, bundle, token)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Warn("oidc userinfo failed")
		writeAuthError(w, http.StatusBadGateway, "登录失败", "无法从身份提供方读取用户信息，请重新登录。")
		return
	}

	// The display name is only used when the user is created; later logins do
	// not overwrite whatever an administrator has set.
	user, err := o.resolver.resolveOrCreate(ctx, ProviderOIDC, subject, displayName, false, o.autoCreate)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			logx.WithContext(ctx).WithError(err).WithField("sub", subject).Error("failed to resolve oidc user")
			writeAuthError(w, http.StatusUnauthorized, "无法登录", "这个账号尚未获得访问许可，请联系管理员。")
			return
		}
		logx.WithContext(ctx).WithError(err).Error("failed to resolve oidc user")
		writeAuthError(w, http.StatusInternalServerError, "登录失败", "服务器内部错误，请稍后重试。")
		return
	}

	if err := o.startSession(ctx, w, r, user.ID); err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to start oidc session")
		writeAuthError(w, http.StatusInternalServerError, "登录失败", "服务器内部错误，请稍后重试。")
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusFound)
}

// startSession drops the user's expired sessions, inserts a fresh one, and puts
// its id in the cookie.
func (o *OIDC) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	now := time.Now()
	// Cleanup is not part of the login: a failure here only leaves dead rows
	// behind, which is no reason to refuse a valid login.
	if err := o.resolver.queries.DeleteExpiredUserSessions(ctx, db.DeleteExpiredUserSessionsParams{
		UserID:    userID,
		ExpiresAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		logx.WithContext(ctx).WithError(err).Warn("failed to clean up expired user sessions")
	}

	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
	if err := o.resolver.queries.InsertUserSession(ctx, db.InsertUserSessionParams{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(o.cfg.SessionTTL), Valid: true},
	}); err != nil {
		return err
	}
	return saveSessionID(o.stores.session, r, w, sessionID)
}

// Logout deletes the session row and expires the cookie. Deleting the row is
// what actually revokes access, so a browser that keeps the cookie gains
// nothing. The IdP's own SSO session is untouched.
func (o *OIDC) Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID, ok := readSessionID(o.stores.session, r); ok {
		if err := o.resolver.queries.DeleteUserSession(r.Context(), sessionID); err != nil {
			logx.WithContext(r.Context()).WithError(err).Error("failed to delete user session")
			writeJSONMessage(w, http.StatusInternalServerError, "failed to log out")
			return
		}
	}
	if err := clearSessionCookie(o.stores.session, r, w); err != nil {
		logx.WithContext(r.Context()).WithError(err).Error("failed to clear session cookie")
		writeJSONMessage(w, http.StatusInternalServerError, "failed to log out")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RedirectUnauthenticatedNav sends a browser navigating to the dashboard into
// the login flow, and reports whether it wrote a response. Accept: text/html is
// what tells a top-level navigation apart from a subresource fetch, which must
// keep falling through to the static handler.
//
// Only the cookie is inspected — no database round trip on the navigation path.
// A browser holding a session id that no longer resolves is caught by the next
// management API call, whose 401 carries the login url.
func (o *OIDC) RedirectUnauthenticatedNav(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
		return false
	}
	if _, ok := readSessionID(o.stores.session, r); ok {
		return false
	}
	target := LoginPath + "?redirect_to=" + url.QueryEscape(r.URL.RequestURI())
	http.Redirect(w, r, target, http.StatusFound)
	return true
}

// backChannelContext injects our bounded HTTP client into the oauth2 exchange.
func (o *OIDC) backChannelContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, o.client)
}

// validateRedirectTo accepts only a same-site absolute path. A leading "//" or
// "/\" would be read by browsers as a scheme-relative url pointing at another
// host, and CR/LF would let a crafted value split the response header.
func validateRedirectTo(raw string) (string, bool) {
	if raw == "" {
		return "/", true
	}
	if !strings.HasPrefix(raw, "/") {
		return "", false
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return "", false
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "", false
	}
	return raw, true
}

func writeJSONMessage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"message":%q}`, message)
}

// writeAuthError renders a minimal error page with a link back into the login
// flow. It is deliberately self-contained HTML: the dashboard bundle may not
// even have loaded at this point.
func writeAuthError(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1 style="font-size:1.25rem">%s</h1>
<p>%s</p>
<p><a href="%s">重新登录</a></p>
</body>
</html>
`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(detail), LoginPath)
}
