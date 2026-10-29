package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"picotera/pkg/configx"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Paths of the three bare chi routes the oidc mode registers. They live under
// /api/picotera but outside Middleware, since an unauthenticated user is exactly
// who needs them.
const (
	LoginPath    = "/api/picotera/auth/login"
	CallbackPath = "/api/picotera/auth/callback"
	LogoutPath   = "/api/picotera/auth/logout"
)

// LoginURLHeader tells the dashboard where to send the browser after a 401. It
// is only ever set in oidc mode, so a client in another mode cannot be pushed
// into a redirect loop.
const LoginURLHeader = "X-PicoTera-Login-Url"

// oidcHTTPTimeout bounds every back-channel call to the IdP (discovery, token
// exchange, userinfo).
const oidcHTTPTimeout = 10 * time.Second

// OIDC drives the authorization code flow and owns the session cookie stores.
// The provider metadata behind it is fetched lazily — startup must not depend on
// the IdP being reachable.
type OIDC struct {
	cfg        configx.OIDCConfig
	baseURL    string
	autoCreate bool
	stores     *sessionStores
	resolver   *Resolver
	client     *http.Client

	mu     sync.Mutex
	cached *providerBundle
}

// providerBundle is everything derived from the IdP's metadata: it is built as a
// unit and cached as a unit.
type providerBundle struct {
	provider *oidc.Provider
	oauth    *oauth2.Config
	pkce     bool
}

// NewOIDC builds the OIDC driver. It derives the cookie keys and nothing else —
// no network access, so a misbehaving IdP cannot keep the process from starting.
func NewOIDC(cfg configx.OIDCConfig, baseURL string, autoCreate bool) (*OIDC, error) {
	stores, err := newSessionStores(cfg.SessionSecret, cfg.SessionTTL)
	if err != nil {
		return nil, err
	}
	return &OIDC{
		cfg:        cfg,
		baseURL:    baseURL,
		autoCreate: autoCreate,
		stores:     stores,
		client:     &http.Client{Timeout: oidcHTTPTimeout},
	}, nil
}

// SetResolver completes the two-way wiring between the driver and the resolver.
// It is called once during server construction, before any request is served.
func (o *OIDC) SetResolver(r *Resolver) { o.resolver = r }

// discoveryDocument is the subset of the OpenID Connect discovery document we
// read ourselves. *oidc.Provider only exposes the authorization and token
// endpoints, so the userinfo and JWKS urls have to come from the raw claims.
type discoveryDocument struct {
	UserInfoURL                   string   `json:"userinfo_endpoint"`
	JWKSURL                       string   `json:"jwks_uri"`
	AuthURL                       string   `json:"authorization_endpoint"`
	TokenURL                      string   `json:"token_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// bundle returns the provider metadata, building it on first use. Only a
// successful build is cached; a failure is retried by the next request rather
// than poisoning the process.
func (o *OIDC) bundle(ctx context.Context) (*providerBundle, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cached != nil {
		return o.cached, nil
	}
	b, err := o.buildBundle(ctx)
	if err != nil {
		return nil, err
	}
	o.cached = b
	return b, nil
}

func (o *OIDC) buildBundle(ctx context.Context) (*providerBundle, error) {
	ctx = oidc.ClientContext(ctx, o.client)

	provider, pkce, err := o.resolveProvider(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := provider.Endpoint()
	// Pin the client authentication style instead of letting oauth2 probe for
	// it: the probe costs a failed round trip against every IdP that wants the
	// other style.
	if o.cfg.ClientAuthMethod == configx.OIDCClientAuthPost {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	} else {
		endpoint.AuthStyle = oauth2.AuthStyleInHeader
	}

	return &providerBundle{
		provider: provider,
		pkce:     pkce,
		oauth: &oauth2.Config{
			ClientID:     o.cfg.ClientID,
			ClientSecret: o.cfg.ClientSecret,
			RedirectURL:  o.baseURL + CallbackPath,
			Scopes:       strings.Fields(o.cfg.Scopes),
			Endpoint:     endpoint,
		},
	}, nil
}

// resolveProvider builds the *oidc.Provider and decides whether to use PKCE.
func (o *OIDC) resolveProvider(ctx context.Context) (*oidc.Provider, bool, error) {
	if !o.cfg.NeedsDiscovery() {
		// Fully configured: never contact the issuer. JWKSURL stays empty, so a
		// userinfo endpoint answering application/jwt will fail loudly — in this
		// mode it must return JSON.
		pc := &oidc.ProviderConfig{
			IssuerURL:   o.cfg.Issuer,
			AuthURL:     o.cfg.AuthEndpoint,
			TokenURL:    o.cfg.TokenEndpoint,
			UserInfoURL: o.cfg.UserinfoEndpoint,
		}
		// Config validation guarantees PKCE is set explicitly here: with no
		// discovery document there is nothing to infer it from.
		return pc.NewProvider(ctx), *o.cfg.PKCE, nil
	}

	provider, err := oidc.NewProvider(ctx, o.cfg.Issuer)
	if err != nil {
		return nil, false, fmt.Errorf("oidc discovery: %w", err)
	}
	var doc discoveryDocument
	if err := provider.Claims(&doc); err != nil {
		return nil, false, fmt.Errorf("oidc discovery document: %w", err)
	}

	pkce := slices.Contains(doc.CodeChallengeMethodsSupported, "S256")
	if o.cfg.PKCE != nil {
		pkce = *o.cfg.PKCE
	}

	if o.cfg.AuthEndpoint == "" && o.cfg.TokenEndpoint == "" && o.cfg.UserinfoEndpoint == "" {
		return provider, pkce, nil
	}
	// A partial override has to go through ProviderConfig, and that rebuild must
	// happen after Claims: a ProviderConfig-built provider carries no raw
	// document, so Claims on it would fail.
	pc := &oidc.ProviderConfig{
		IssuerURL:   o.cfg.Issuer,
		AuthURL:     firstNonEmpty(o.cfg.AuthEndpoint, doc.AuthURL),
		TokenURL:    firstNonEmpty(o.cfg.TokenEndpoint, doc.TokenURL),
		UserInfoURL: firstNonEmpty(o.cfg.UserinfoEndpoint, doc.UserInfoURL),
		JWKSURL:     doc.JWKSURL,
	}
	return pc.NewProvider(ctx), pkce, nil
}

// identity fetches the authenticated user's subject and a display name from the
// userinfo endpoint. The ID token is deliberately not consumed: identity comes
// from a direct TLS back-channel response, and code injection is already ruled
// out by the state cookie and PKCE.
func (o *OIDC) identity(ctx context.Context, b *providerBundle, token *oauth2.Token) (string, string, error) {
	ctx = oidc.ClientContext(ctx, o.client)
	info, err := b.provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return "", "", fmt.Errorf("oidc userinfo: %w", err)
	}
	if info.Subject == "" {
		return "", "", errors.New("oidc userinfo: empty subject")
	}

	var claims struct {
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
	}
	// A userinfo response that isn't decodable into these optional fields is not
	// worth failing the login over — the subject already identifies the user.
	if err := info.Claims(&claims); err != nil {
		var unmarshalErr *json.UnmarshalTypeError
		if !errors.As(err, &unmarshalErr) {
			return "", "", fmt.Errorf("oidc userinfo claims: %w", err)
		}
	}
	name := firstNonEmpty(claims.Name, claims.PreferredUsername, claims.Email, info.Subject)
	return info.Subject, name, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
