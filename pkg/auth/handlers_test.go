package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidateRedirectTo(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "/", true},
		{"/", "/", true},
		{"/requests?page=2", "/requests?page=2", true},
		{"//evil.example", "", false},
		{`/\evil.example`, "", false},
		{"https://evil.example", "", false},
		{"requests", "", false},
		{"/requests\r\nSet-Cookie: x=y", "", false},
	} {
		got, ok := validateRedirectTo(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("validateRedirectTo(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLoginRedirectsToAuthorizationEndpoint(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{codeChallengeMethodsSupported: []string{"S256"}})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/api/picotera/auth/login?redirect_to=/requests", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	target, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	query := target.Query()
	if query.Get("response_type") != "code" || query.Get("client_id") != "client" {
		t.Errorf("query = %v", query)
	}
	if query.Get("redirect_uri") != "https://picotera.example"+CallbackPath {
		t.Errorf("redirect_uri = %q", query.Get("redirect_uri"))
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Errorf("expected a PKCE challenge, got %v", query)
	}

	state, verifier, redirect, ok := readState(o.stores.state, replay(t, rec))
	if !ok {
		t.Fatal("no state cookie was written")
	}
	if state != query.Get("state") {
		t.Errorf("cookie state %q does not match the url state %q", state, query.Get("state"))
	}
	if verifier == "" {
		t.Error("expected the PKCE verifier to be stored")
	}
	if redirect != "/requests" {
		t.Errorf("redirect = %q; want /requests", redirect)
	}
}

func TestLoginWithoutPKCE(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, LoginPath, nil))

	target, _ := url.Parse(rec.Header().Get("Location"))
	if target.Query().Has("code_challenge") {
		t.Error("PKCE was used although the IdP does not advertise S256")
	}
	if _, verifier, redirect, ok := readState(o.stores.state, replay(t, rec)); !ok || verifier != "" || redirect != "/" {
		t.Errorf("state = verifier %q, redirect %q, ok %v", verifier, redirect, ok)
	}
}

func TestLoginRejectsBadRedirectTo(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, LoginPath+"?redirect_to=//evil.example", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a rejected login must not write a cookie")
	}
}

// Every one of these fails before the token exchange, so no IdP is involved.
func TestCallbackRejections(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	stateRec := httptest.NewRecorder()
	if err := saveState(o.stores.state, httptest.NewRequest(http.MethodGet, "/", nil), stateRec, "the-state", "vf", "/"); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	for _, tc := range []struct {
		name       string
		query      string
		withCookie bool
		want       int
	}{
		{"idp returned an error", "?error=access_denied&error_description=denied", true, http.StatusBadRequest},
		{"no state cookie", "?code=c&state=the-state", false, http.StatusBadRequest},
		{"state mismatch", "?code=c&state=other", true, http.StatusBadRequest},
		{"missing code", "?state=the-state", true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, CallbackPath+tc.query, nil)
			if tc.withCookie {
				for _, c := range stateRec.Result().Cookies() {
					req.AddCookie(c)
				}
			}
			rec := httptest.NewRecorder()
			o.Callback(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d; want %d", rec.Code, tc.want)
			}
			if !strings.Contains(rec.Body.String(), LoginPath) {
				t.Error("the error page should link back to the login flow")
			}
		})
	}
}

func TestRedirectUnauthenticatedNav(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	sessionRec := httptest.NewRecorder()
	if err := saveSessionID(o.stores.session, httptest.NewRequest(http.MethodGet, "/", nil), sessionRec, "sid"); err != nil {
		t.Fatalf("saveSessionID: %v", err)
	}

	for _, tc := range []struct {
		name        string
		method      string
		accept      string
		withSession bool
		want        bool
	}{
		{"navigation without a session", http.MethodGet, "text/html,*/*", false, true},
		{"head navigation", http.MethodHead, "text/html", false, true},
		{"subresource fetch", http.MethodGet, "application/json", false, false},
		{"no accept header", http.MethodGet, "", false, false},
		{"post", http.MethodPost, "text/html", false, false},
		{"navigation with a session", http.MethodGet, "text/html", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/requests?page=2", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			if tc.withSession {
				for _, c := range sessionRec.Result().Cookies() {
					req.AddCookie(c)
				}
			}
			rec := httptest.NewRecorder()

			if got := o.RedirectUnauthenticatedNav(rec, req); got != tc.want {
				t.Fatalf("RedirectUnauthenticatedNav = %v; want %v", got, tc.want)
			}
			if !tc.want {
				return
			}
			location := rec.Header().Get("Location")
			if !strings.HasPrefix(location, LoginPath+"?redirect_to=") {
				t.Fatalf("Location = %q", location)
			}
			target, _ := url.Parse(location)
			if got := target.Query().Get("redirect_to"); got != "/requests?page=2" {
				t.Fatalf("redirect_to = %q; want /requests?page=2", got)
			}
		})
	}
}
