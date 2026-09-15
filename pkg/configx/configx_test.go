package configx

import (
	"strings"
	"testing"
	"time"
)

// validOIDC returns a configuration that passes validateAuth, so each test can
// break exactly one thing.
func validOIDC() *Config {
	return &Config{
		BaseURL: "https://picotera.example",
		Auth: AuthConfig{
			OIDC: OIDCConfig{
				Enabled:          true,
				Issuer:           "https://idp.example",
				ClientID:         "client",
				ClientSecret:     "secret",
				SessionSecret:    "session",
				SessionTTL:       12 * time.Hour,
				Scopes:           "openid profile email",
				ClientAuthMethod: OIDCClientAuthBasic,
			},
		},
	}
}

func TestValidateAuthProviderCount(t *testing.T) {
	for _, tc := range []struct {
		name    string
		auth    AuthConfig
		wantErr string
	}{
		{"none", AuthConfig{}, "no auth provider enabled"},
		{"single user only", AuthConfig{SingleUserMode: true}, ""},
		{"header only", AuthConfig{HeaderEnabled: true}, ""},
		{
			"single user and header",
			AuthConfig{SingleUserMode: true, HeaderEnabled: true},
			"exactly one auth provider must be enabled",
		},
		{
			"single user and oidc",
			AuthConfig{SingleUserMode: true, OIDC: OIDCConfig{Enabled: true}},
			"exactly one auth provider must be enabled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuth(&Config{Auth: tc.auth})
			assertErrContains(t, err, tc.wantErr)
		})
	}
}

func TestValidateAuthOIDC(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"no client id", func(c *Config) { c.Auth.OIDC.ClientID = "" }, "client_id is required"},
		{"no client secret", func(c *Config) { c.Auth.OIDC.ClientSecret = "" }, "client_secret is required"},
		{"no session secret", func(c *Config) { c.Auth.OIDC.SessionSecret = "" }, "session_secret is required"},
		{"zero session ttl", func(c *Config) { c.Auth.OIDC.SessionTTL = 0 }, "session_ttl must be positive"},
		{"unknown client auth method", func(c *Config) { c.Auth.OIDC.ClientAuthMethod = "private_key_jwt" }, "client_auth_method must be"},

		{"no base url", func(c *Config) { c.BaseURL = "" }, "base_url is required"},
		{"relative base url", func(c *Config) { c.BaseURL = "/picotera" }, "base_url must be an absolute url"},
		{"base url with a path", func(c *Config) { c.BaseURL = "https://picotera.example/app" }, "base_url must not carry a path"},
		{"base url with a query", func(c *Config) { c.BaseURL = "https://picotera.example?a=1" }, "base_url must not carry a query"},

		{"no issuer", func(c *Config) { c.Auth.OIDC.Issuer = "" }, "issuer is required"},
		{"relative issuer", func(c *Config) { c.Auth.OIDC.Issuer = "idp.example" }, "issuer must be an absolute url"},
		{"issuer with a trailing slash", func(c *Config) { c.Auth.OIDC.Issuer = "https://idp.example/" }, "issuer must not end with a slash"},

		{
			// All three endpoints configured: the issuer is never used, so it may
			// be empty, but PKCE can no longer be inferred.
			"full endpoints without pkce",
			func(c *Config) {
				c.Auth.OIDC.Issuer = ""
				c.Auth.OIDC.AuthEndpoint = "https://idp.example/authorize"
				c.Auth.OIDC.TokenEndpoint = "https://idp.example/token"
				c.Auth.OIDC.UserinfoEndpoint = "https://idp.example/userinfo"
			},
			"pkce must be set explicitly",
		},
		{
			"full endpoints with pkce",
			func(c *Config) {
				c.Auth.OIDC.Issuer = ""
				c.Auth.OIDC.AuthEndpoint = "https://idp.example/authorize"
				c.Auth.OIDC.TokenEndpoint = "https://idp.example/token"
				c.Auth.OIDC.UserinfoEndpoint = "https://idp.example/userinfo"
				pkce := true
				c.Auth.OIDC.PKCE = &pkce
			},
			"",
		},
		{
			// A partial override still needs discovery, hence still needs an issuer.
			"partial endpoints still need an issuer",
			func(c *Config) {
				c.Auth.OIDC.Issuer = ""
				c.Auth.OIDC.TokenEndpoint = "https://idp.example/token"
			},
			"issuer is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := validOIDC()
			tc.mutate(config)
			assertErrContains(t, validateAuth(config), tc.wantErr)
		})
	}
}

// base_url is normalized in place so the callback url can be built by
// concatenation.
func TestValidateAuthNormalizesBaseURL(t *testing.T) {
	config := validOIDC()
	config.BaseURL = "https://picotera.example/"
	if err := validateAuth(config); err != nil {
		t.Fatalf("validateAuth: %v", err)
	}
	if config.BaseURL != "https://picotera.example" {
		t.Fatalf("baseURL = %q; want the trailing slash removed", config.BaseURL)
	}
}

// base_url is only meaningful in oidc mode.
func TestValidateAuthIgnoresBaseURLOutsideOIDC(t *testing.T) {
	if err := validateAuth(&Config{Auth: AuthConfig{SingleUserMode: true}}); err != nil {
		t.Fatalf("validateAuth: %v", err)
	}
}

func assertErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v; want it to contain %q", err, want)
	}
}
