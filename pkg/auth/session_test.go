package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/securecookie"
)

const testSecret = "test-session-secret"

func newTestStores(t *testing.T) *sessionStores {
	t.Helper()
	stores, err := newSessionStores(testSecret, time.Hour)
	if err != nil {
		t.Fatalf("newSessionStores: %v", err)
	}
	return stores
}

// replay builds the follow-up request a browser would send after receiving the
// recorded response's Set-Cookie.
func replay(t *testing.T, rec *httptest.ResponseRecorder) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func TestSaveAndReadSessionID(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveSessionID(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), rec, "sid-123"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}

	got, ok := readSessionID(stores.session, replay(t, rec))
	if !ok || got != "sid-123" {
		t.Fatalf("readSessionID = %q, %v; want %q, true", got, ok, "sid-123")
	}
}

func TestSaveAndReadState(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveState(stores.state, httptest.NewRequest(http.MethodGet, "/", nil), rec, "st", "vf", "/requests"); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	state, verifier, redirect, ok := readState(stores.state, replay(t, rec))
	if !ok || state != "st" || verifier != "vf" || redirect != "/requests" {
		t.Fatalf("readState = %q, %q, %q, %v", state, verifier, redirect, ok)
	}
}

// An empty verifier is the PKCE-disabled case and must not invalidate the state.
func TestReadStateWithoutVerifier(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveState(stores.state, httptest.NewRequest(http.MethodGet, "/", nil), rec, "st", "", "/"); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	state, verifier, redirect, ok := readState(stores.state, replay(t, rec))
	if !ok || state != "st" || verifier != "" || redirect != "/" {
		t.Fatalf("readState = %q, %q, %q, %v", state, verifier, redirect, ok)
	}
}

// Both stores use the same cookie name, so the kind field is the only thing
// keeping a login-in-flight cookie from being read as a session and vice versa.
func TestKindMismatchRejected(t *testing.T) {
	stores := newTestStores(t)

	stateRec := httptest.NewRecorder()
	if err := saveState(stores.state, httptest.NewRequest(http.MethodGet, "/", nil), stateRec, "st", "vf", "/"); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	if _, ok := readSessionID(stores.session, replay(t, stateRec)); ok {
		t.Fatal("state cookie was accepted as a session")
	}

	sessionRec := httptest.NewRecorder()
	if err := saveSessionID(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), sessionRec, "sid"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}
	if _, _, _, ok := readState(stores.state, replay(t, sessionRec)); ok {
		t.Fatal("session cookie was accepted as login state")
	}
}

func TestTamperedCookieRejected(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveSessionID(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), rec, "sid"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}

	cookie := rec.Result().Cookies()[0]
	// Flip one character of the encoded value; the HMAC must catch it.
	body := []byte(cookie.Value)
	if body[len(body)/2] == 'A' {
		body[len(body)/2] = 'B'
	} else {
		body[len(body)/2] = 'A'
	}
	cookie.Value = string(body)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	if _, ok := readSessionID(stores.session, req); ok {
		t.Fatal("tampered cookie was accepted")
	}
}

func TestOtherSecretCannotDecode(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveSessionID(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), rec, "sid"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}

	other, err := newSessionStores("a different secret", time.Hour)
	if err != nil {
		t.Fatalf("newSessionStores: %v", err)
	}
	if _, ok := readSessionID(other.session, replay(t, rec)); ok {
		t.Fatal("cookie decoded under a different secret")
	}
}

// The codec's timestamp check is what actually bounds a cookie's lifetime — the
// browser's Max-Age is only a hint. A past timestamp cannot be forged (the
// clock hook is unexported), so the symmetric MinAge bound is used to exercise
// the same branch: the point is that a rejected timestamp reads as "no
// session", never as an error.
func TestTimestampCheckRejectsOutOfWindow(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveSessionID(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), rec, "sid"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}

	for _, codec := range stores.session.Codecs {
		codec.(*securecookie.SecureCookie).MinAge(3600)
	}
	if _, ok := readSessionID(stores.session, replay(t, rec)); ok {
		t.Fatal("cookie outside the timestamp window was accepted")
	}
}

func TestClearSessionCookie(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := clearSessionCookie(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), rec); err != nil {
		t.Fatalf("clearSessionCookie: %v", err)
	}

	header := rec.Header().Get("Set-Cookie")
	if !strings.Contains(header, "Max-Age=0") {
		t.Fatalf("Set-Cookie = %q; want Max-Age=0", header)
	}
	// Clearing must not leave the store itself expiring every later cookie.
	if stores.session.Options.MaxAge != int(time.Hour.Seconds()) {
		t.Fatalf("store MaxAge = %d; want %d", stores.session.Options.MaxAge, int(time.Hour.Seconds()))
	}
}

// __Host- is only honored by browsers when Path=/, Secure is set, and no Domain
// is present; SameSite=Lax lets the IdP's top-level callback carry the cookie
// while keeping it off cross-site API calls.
func TestCookieAttributes(t *testing.T) {
	stores := newTestStores(t)
	rec := httptest.NewRecorder()
	if err := saveSessionID(stores.session, httptest.NewRequest(http.MethodGet, "/", nil), rec, "sid"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies; want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != SessionCookieName {
		t.Errorf("name = %q; want %q", c.Name, SessionCookieName)
	}
	if c.Path != "/" {
		t.Errorf("path = %q; want /", c.Path)
	}
	if c.Domain != "" {
		t.Errorf("domain = %q; want empty", c.Domain)
	}
	if !c.Secure || !c.HttpOnly {
		t.Errorf("secure = %v, httpOnly = %v; want both true", c.Secure, c.HttpOnly)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("sameSite = %v; want Lax", c.SameSite)
	}
	if c.MaxAge != int(time.Hour.Seconds()) {
		t.Errorf("maxAge = %d; want %d", c.MaxAge, int(time.Hour.Seconds()))
	}
}

func TestStateStoreUsesShorterLifetime(t *testing.T) {
	stores := newTestStores(t)
	if stores.state.Options.MaxAge != int(stateTTL.Seconds()) {
		t.Fatalf("state store MaxAge = %d; want %d", stores.state.Options.MaxAge, int(stateTTL.Seconds()))
	}
}

func TestNewSessionIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id, err := newSessionID()
		if err != nil {
			t.Fatalf("newSessionID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate session id %q", id)
		}
		seen[id] = true
	}
}
