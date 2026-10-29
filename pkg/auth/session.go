package auth

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/sessions"
)

// SessionCookieName is the single cookie the oidc mode uses. The __Host- prefix
// requires Path=/, Secure, and no Domain — the browser enforces those, which is
// why the cookie options below are fixed rather than environment-dependent.
const SessionCookieName = "__Host-Http-Picotera-Auth"

// stateTTL bounds how long a login round-trip may take. It is a codec-level
// lifetime (see newSessionStores), not just a browser hint.
const stateTTL = 10 * time.Minute

// Payload keys. One cookie name carries two shapes, told apart by keyKind: a
// login is in flight or a session exists, never both.
const (
	keyKind      = "k"
	keyState     = "state"
	keyVerifier  = "verifier"
	keyRedirect  = "redirect"
	keySessionID = "sid"

	kindState   = "a"
	kindSession = "s"
)

// HKDF info strings. Two distinct labels off one operator-supplied secret so the
// authentication key and the encryption key are independent.
const (
	infoHashKey  = "picotera session cookie hash key v1"
	infoBlockKey = "picotera session cookie block key v1"
)

// sessionStores holds the two cookie stores. They share one key pair but have
// different codec lifetimes, because securecookie's expiry check lives on the
// codec: a 10-minute login window and a session-long cookie cannot come out of
// the same store.
type sessionStores struct {
	state   *sessions.CookieStore
	session *sessions.CookieStore
}

// newSessionStores derives the cookie keys from secret and builds both stores.
func newSessionStores(secret string, sessionTTL time.Duration) (*sessionStores, error) {
	hashKey, err := hkdf.Key(sha256.New, []byte(secret), nil, infoHashKey, 32)
	if err != nil {
		return nil, fmt.Errorf("derive cookie hash key: %w", err)
	}
	// 32 bytes selects AES-256 in securecookie.
	blockKey, err := hkdf.Key(sha256.New, []byte(secret), nil, infoBlockKey, 32)
	if err != nil {
		return nil, fmt.Errorf("derive cookie block key: %w", err)
	}
	return &sessionStores{
		state:   newCookieStore(hashKey, blockKey, stateTTL),
		session: newCookieStore(hashKey, blockKey, sessionTTL),
	}, nil
}

func newCookieStore(hashKey, blockKey []byte, maxAge time.Duration) *sessions.CookieStore {
	store := sessions.NewCookieStore(hashKey, blockKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	}
	// MaxAge on the store also re-keys the codecs' own expiry window, which is
	// what actually enforces the lifetime — the browser's Max-Age is a hint we
	// do not trust.
	store.MaxAge(store.Options.MaxAge)
	return store
}

// newSessionID returns an unguessable session id. The cookie's encryption is a
// layer on top of this, not the sole source of security.
func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// newStateToken returns the random CSRF state tying a callback to the browser
// that started the login.
func newStateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// decode reads the cookie through the given store. It always uses store.New, not
// store.Get: Get caches by cookie name in a per-request registry, and both
// stores use the same name, so Get would hand the second caller the first one's
// decode. A decode failure (rotated key, tampering, expiry) is not an error
// here — it is simply the absence of the payload.
func decode(store *sessions.CookieStore, r *http.Request, kind string) (map[any]any, bool) {
	sess, err := store.New(r, SessionCookieName)
	if err != nil || sess.IsNew {
		return nil, false
	}
	if got, _ := sess.Values[keyKind].(string); got != kind {
		return nil, false
	}
	return sess.Values, true
}

// stringValue reads a string field, treating a missing field, a wrong type, and
// an empty string alike.
func stringValue(values map[any]any, key string) (string, bool) {
	s, ok := values[key].(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// readState returns the in-flight login state. verifier is empty when PKCE is
// off, so only state and redirect are required.
func readState(store *sessions.CookieStore, r *http.Request) (state, verifier, redirect string, ok bool) {
	values, ok := decode(store, r, kindState)
	if !ok {
		return "", "", "", false
	}
	state, ok = stringValue(values, keyState)
	if !ok {
		return "", "", "", false
	}
	redirect, ok = stringValue(values, keyRedirect)
	if !ok {
		return "", "", "", false
	}
	verifier, _ = values[keyVerifier].(string)
	return state, verifier, redirect, true
}

func saveState(store *sessions.CookieStore, r *http.Request, w http.ResponseWriter, state, verifier, redirect string) error {
	sess, _ := store.New(r, SessionCookieName)
	sess.Values = map[any]any{
		keyKind:     kindState,
		keyState:    state,
		keyVerifier: verifier,
		keyRedirect: redirect,
	}
	return sess.Save(r, w)
}

func readSessionID(store *sessions.CookieStore, r *http.Request) (string, bool) {
	values, ok := decode(store, r, kindSession)
	if !ok {
		return "", false
	}
	return stringValue(values, keySessionID)
}

func saveSessionID(store *sessions.CookieStore, r *http.Request, w http.ResponseWriter, sessionID string) error {
	sess, _ := store.New(r, SessionCookieName)
	sess.Values = map[any]any{
		keyKind:      kindSession,
		keySessionID: sessionID,
	}
	return sess.Save(r, w)
}

// clearSessionCookie expires the cookie in the browser. The authoritative
// revocation is deleting the user_session row; this only stops the browser from
// re-sending a value that no longer resolves.
func clearSessionCookie(store *sessions.CookieStore, r *http.Request, w http.ResponseWriter) error {
	sess, _ := store.New(r, SessionCookieName)
	sess.Values = map[any]any{}
	sess.Options = cloneOptions(store.Options)
	sess.Options.MaxAge = -1
	return sess.Save(r, w)
}

func cloneOptions(o *sessions.Options) *sessions.Options {
	clone := *o
	return &clone
}
