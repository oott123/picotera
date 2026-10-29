package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"picotera/pkg/configx"

	"golang.org/x/oauth2"
)

// fakeIdP serves just enough of an OpenID Connect provider to exercise
// discovery and userinfo.
type fakeIdP struct {
	server *httptest.Server
	// issuer overrides the issuer advertised in the discovery document; empty
	// means "the server's own url" (the correct, matching case).
	issuer                        string
	codeChallengeMethodsSupported []string
	userinfo                      map[string]any
}

func newFakeIdP(t *testing.T, idp *fakeIdP) *fakeIdP {
	t.Helper()
	mux := http.NewServeMux()
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		issuer := idp.issuer
		if issuer == "" {
			issuer = idp.server.URL
		}
		doc := map[string]any{
			"issuer":                 issuer,
			"authorization_endpoint": idp.server.URL + "/authorize",
			"token_endpoint":         idp.server.URL + "/token",
			"userinfo_endpoint":      idp.server.URL + "/userinfo",
			"jwks_uri":               idp.server.URL + "/jwks",
		}
		if idp.codeChallengeMethodsSupported != nil {
			doc["code_challenge_methods_supported"] = idp.codeChallengeMethodsSupported
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(idp.userinfo)
	})
	return idp
}

func testOIDCConfig(issuer string) configx.OIDCConfig {
	return configx.OIDCConfig{
		Enabled:          true,
		Issuer:           issuer,
		ClientID:         "client",
		ClientSecret:     "secret",
		SessionSecret:    testSecret,
		SessionTTL:       time.Hour,
		Scopes:           "openid profile email",
		ClientAuthMethod: configx.OIDCClientAuthBasic,
	}
}

func newTestOIDC(t *testing.T, cfg configx.OIDCConfig) *OIDC {
	t.Helper()
	o, err := NewOIDC(cfg, "https://picotera.example", false)
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	return o
}

func boolPtr(b bool) *bool { return &b }

// go-oidc compares the document's issuer against the configured one; that check
// is why we do not have to compare it ourselves.
func TestBundleRejectsIssuerMismatch(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{issuer: "https://elsewhere.example"})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	if _, err := o.bundle(context.Background()); err == nil {
		t.Fatal("expected issuer mismatch to fail")
	}
	if o.cached != nil {
		t.Fatal("a failed build must not be cached")
	}
}

func TestBundleInfersPKCEFromDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		methods []string
		want    bool
	}{
		{"s256 advertised", []string{"S256", "plain"}, true},
		{"only plain", []string{"plain"}, false},
		{"not advertised", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t, &fakeIdP{codeChallengeMethodsSupported: tc.methods})
			o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

			b, err := o.bundle(context.Background())
			if err != nil {
				t.Fatalf("bundle: %v", err)
			}
			if b.pkce != tc.want {
				t.Fatalf("pkce = %v; want %v", b.pkce, tc.want)
			}
		})
	}
}

func TestBundleConfigOverridesInferredPKCE(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{codeChallengeMethodsSupported: []string{"S256"}})
	cfg := testOIDCConfig(idp.server.URL)
	cfg.PKCE = boolPtr(false)
	o := newTestOIDC(t, cfg)

	b, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if b.pkce {
		t.Fatal("explicit pkce=false was overridden by discovery")
	}
}

// With all three endpoints configured the issuer is never contacted, so PKCE can
// only come from configuration.
func TestBundleWithoutDiscovery(t *testing.T) {
	cfg := testOIDCConfig("")
	cfg.AuthEndpoint = "https://idp.example/authorize"
	cfg.TokenEndpoint = "https://idp.example/token"
	cfg.UserinfoEndpoint = "https://idp.example/userinfo"
	cfg.PKCE = boolPtr(true)
	o := newTestOIDC(t, cfg)

	b, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if !b.pkce {
		t.Error("pkce = false; want true from config")
	}
	if b.oauth.Endpoint.AuthURL != cfg.AuthEndpoint || b.oauth.Endpoint.TokenURL != cfg.TokenEndpoint {
		t.Errorf("endpoint = %+v; want the configured urls", b.oauth.Endpoint)
	}
}

func TestBundlePartialEndpointOverride(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{userinfo: map[string]any{"sub": "u-1"}})
	cfg := testOIDCConfig(idp.server.URL)
	cfg.TokenEndpoint = "https://proxy.example/token"
	o := newTestOIDC(t, cfg)

	b, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if b.oauth.Endpoint.TokenURL != cfg.TokenEndpoint {
		t.Errorf("tokenURL = %q; want the configured override", b.oauth.Endpoint.TokenURL)
	}
	if b.oauth.Endpoint.AuthURL != idp.server.URL+"/authorize" {
		t.Errorf("authURL = %q; want the discovered url", b.oauth.Endpoint.AuthURL)
	}
	// The userinfo url is not exposed by the provider, so check it by using it.
	if _, _, err := o.identity(context.Background(), b, testToken()); err != nil {
		t.Errorf("identity: %v", err)
	}
}

func TestBundleAuthStyle(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   oauth2.AuthStyle
	}{
		{configx.OIDCClientAuthBasic, oauth2.AuthStyleInHeader},
		{configx.OIDCClientAuthPost, oauth2.AuthStyleInParams},
	} {
		t.Run(tc.method, func(t *testing.T) {
			idp := newFakeIdP(t, &fakeIdP{})
			cfg := testOIDCConfig(idp.server.URL)
			cfg.ClientAuthMethod = tc.method
			o := newTestOIDC(t, cfg)

			b, err := o.bundle(context.Background())
			if err != nil {
				t.Fatalf("bundle: %v", err)
			}
			if b.oauth.Endpoint.AuthStyle != tc.want {
				t.Fatalf("authStyle = %v; want %v", b.oauth.Endpoint.AuthStyle, tc.want)
			}
		})
	}
}

func TestBundleIsCached(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	first, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	second, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if first != second {
		t.Fatal("bundle was rebuilt instead of served from cache")
	}
}

func TestBundleRedirectURL(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	b, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if want := "https://picotera.example" + CallbackPath; b.oauth.RedirectURL != want {
		t.Fatalf("redirectURL = %q; want %q", b.oauth.RedirectURL, want)
	}
	if strings.Join(b.oauth.Scopes, " ") != "openid profile email" {
		t.Fatalf("scopes = %v", b.oauth.Scopes)
	}
}

func testToken() *oauth2.Token {
	return &oauth2.Token{AccessToken: "access", TokenType: "Bearer"}
}

func TestIdentityRequiresSubject(t *testing.T) {
	idp := newFakeIdP(t, &fakeIdP{userinfo: map[string]any{"name": "No Subject"}})
	o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

	b, err := o.bundle(context.Background())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if _, _, err := o.identity(context.Background(), b, testToken()); err == nil {
		t.Fatal("expected a missing subject to fail")
	}
}

func TestIdentityDisplayNameFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		userinfo map[string]any
		want     string
	}{
		{"name wins", map[string]any{"sub": "u", "name": "N", "preferred_username": "P", "email": "E"}, "N"},
		{"preferred_username next", map[string]any{"sub": "u", "preferred_username": "P", "email": "E"}, "P"},
		{"email next", map[string]any{"sub": "u", "email": "E"}, "E"},
		{"subject last", map[string]any{"sub": "u"}, "u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t, &fakeIdP{userinfo: tc.userinfo})
			o := newTestOIDC(t, testOIDCConfig(idp.server.URL))

			b, err := o.bundle(context.Background())
			if err != nil {
				t.Fatalf("bundle: %v", err)
			}
			sub, name, err := o.identity(context.Background(), b, testToken())
			if err != nil {
				t.Fatalf("identity: %v", err)
			}
			if sub != "u" {
				t.Errorf("sub = %q; want u", sub)
			}
			if name != tc.want {
				t.Errorf("displayName = %q; want %q", name, tc.want)
			}
		})
	}
}
