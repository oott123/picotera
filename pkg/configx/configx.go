package configx

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL                      string        `mapstructure:"database_url"`
	Host                             string        `mapstructure:"host"`
	Port                             int           `mapstructure:"port"`
	GatewayReadTimeout               time.Duration `mapstructure:"gateway_read_timeout"`
	GatewayIdleConnTimeout           time.Duration `mapstructure:"gateway_idle_conn_timeout"`
	GatewayTLSHandshakeTimeout       time.Duration `mapstructure:"gateway_tls_handshake_timeout"`
	GatewayExpectContinueTimeout     time.Duration `mapstructure:"gateway_expect_continue_timeout"`
	GatewayResponseHeaderTimeout     time.Duration `mapstructure:"gateway_response_header_timeout"`
	GatewayDialTimeout               time.Duration `mapstructure:"gateway_dial_timeout"`
	GatewayDialKeepAlive             time.Duration `mapstructure:"gateway_dial_keep_alive"`
	GatewayHTTP2ReadIdleTimeout      time.Duration `mapstructure:"gateway_http2_read_idle_timeout"`
	GatewayHTTP2PingTimeout          time.Duration `mapstructure:"gateway_http2_ping_timeout"`
	GatewayDisableKeepAlives         bool          `mapstructure:"gateway_disable_keep_alives"`
	GatewayDisableHTTP2              bool          `mapstructure:"gateway_disable_http2"`
	GatewayEphemeralTransport        bool          `mapstructure:"gateway_ephemeral_transport"`
	GatewayExternalRequestIDHeaders  string        `mapstructure:"gateway_external_request_id_headers"`
	GatewayExternalResponseIDHeaders string        `mapstructure:"gateway_external_response_id_headers"`
	S3                               S3Config      `mapstructure:"s3"`
	KV                               KVConfig      `mapstructure:"kv"`
	JSHookTimeout                    time.Duration `mapstructure:"js_hook_timeout"`
	JSMemoryLimit                    int64         `mapstructure:"js_memory_limit"`
	JSMaxTotalAttempts               int           `mapstructure:"js_max_total_attempts"`
	JSMaxDelay                       time.Duration `mapstructure:"js_max_delay"`
	LLMBridgePluginPath              string        `mapstructure:"llmbridge_plugin_path"`
	LLMBridgePluginStartTimeout      time.Duration `mapstructure:"llmbridge_plugin_start_timeout"`
	HeapDumpDir                      string        `mapstructure:"heap_dump_dir"`
	AppTitle                         string        `mapstructure:"app_title"`
	// BaseURL is PicoTera's externally reachable base url. Required in oidc
	// mode, where it is the base of the OAuth callback url; ignored otherwise.
	BaseURL string     `mapstructure:"base_url"`
	Auth    AuthConfig `mapstructure:"auth"`
}

type AuthConfig struct {
	HeaderEnabled  bool       `mapstructure:"header_enabled"`
	HeaderName     string     `mapstructure:"header_name"`
	AutoCreateUser bool       `mapstructure:"auto_create_user"`
	SingleUserMode bool       `mapstructure:"single_user_mode"`
	OIDC           OIDCConfig `mapstructure:"oidc"`
}

// Client authentication methods accepted by auth.oidc.client_auth_method.
const (
	OIDCClientAuthBasic = "client_secret_basic"
	OIDCClientAuthPost  = "client_secret_post"
)

// OIDCConfig configures the "oidc" identity provider. Discovery is used unless
// all three endpoints (auth / token / userinfo) are configured explicitly, in
// which case the issuer is never contacted.
type OIDCConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Issuer  string `mapstructure:"issuer"`

	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	// SessionSecret is the raw key material the session cookie's authentication
	// and encryption keys are derived from. Its lifecycle is deliberately
	// decoupled from the client secret.
	SessionSecret string        `mapstructure:"session_secret"`
	SessionTTL    time.Duration `mapstructure:"session_ttl"`
	Scopes        string        `mapstructure:"scopes"`

	AuthEndpoint     string `mapstructure:"auth_endpoint"`
	TokenEndpoint    string `mapstructure:"token_endpoint"`
	UserinfoEndpoint string `mapstructure:"userinfo_endpoint"`

	// PKCE overrides what discovery infers. It is mandatory when all three
	// endpoints are configured, because then there is nothing to infer from.
	PKCE             *bool  `mapstructure:"pkce"`
	ClientAuthMethod string `mapstructure:"client_auth_method"`
}

// endpointsFullyConfigured reports whether all three endpoints are set, i.e.
// whether the discovery document is needed at all.
func (c OIDCConfig) endpointsFullyConfigured() bool {
	return c.AuthEndpoint != "" && c.TokenEndpoint != "" && c.UserinfoEndpoint != ""
}

// NeedsDiscovery reports whether the provider must be built from the issuer's
// discovery document rather than purely from configuration.
func (c OIDCConfig) NeedsDiscovery() bool {
	return !c.endpointsFullyConfigured()
}

type KVConfig struct {
	Driver   string `mapstructure:"driver"`
	RedisURL string `mapstructure:"redis_url"`
}

type S3Config struct {
	Endpoint  string `mapstructure:"endpoint"`
	Region    string `mapstructure:"region"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	UseSSL    bool   `mapstructure:"use_ssl"`
	PublicURL string `mapstructure:"public_url"`
	PathStyle *bool  `mapstructure:"path_style"`
}

func Parse() (*Config, error) {
	viper.SetEnvPrefix("PICOTERA")
	viper.AutomaticEnv()

	var config Config
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		var fileLookupError viper.ConfigFileNotFoundError
		if errors.As(err, &fileLookupError) {
			// do nothing
		} else {
			return nil, err
		}
	}

	viper.SetDefault("port", 9898)
	viper.SetDefault("gateway_read_timeout", 300*time.Second)
	viper.SetDefault("gateway_dial_timeout", 30*time.Second)
	viper.SetDefault("gateway_dial_keep_alive", 16*time.Second)
	viper.SetDefault("gateway_idle_conn_timeout", 24*time.Second)
	viper.SetDefault("gateway_tls_handshake_timeout", 16*time.Second)
	viper.SetDefault("gateway_expect_continue_timeout", 16*time.Second)
	viper.SetDefault("gateway_response_header_timeout", 185*time.Second)
	viper.SetDefault("gateway_http2_read_idle_timeout", 13*time.Second)
	viper.SetDefault("gateway_http2_ping_timeout", 6*time.Second)
	viper.SetDefault("gateway_disable_keep_alives", false)
	viper.SetDefault("gateway_disable_http2", true)
	viper.SetDefault("gateway_ephemeral_transport", true)
	viper.SetDefault("gateway_external_request_id_headers", "X-PicoTera-Request-Id,X-Request-Id,X-Ot-Span-Context,X-DataDog-Trace-Id,X-Amzn-Trace-Id,X-Client-Trace-Id,X-Log-Id,Cf-Ray")
	viper.SetDefault("gateway_external_response_id_headers", "X-Request-Id,X-Trace-Id,X-Kong-Request-Id,X-Oneapi-Request-Id,X-Zenmux-RequestId,X-Log-Id,Cf-Ray")
	viper.SetDefault("s3.region", "us-east-1")
	viper.SetDefault("s3.use_ssl", false)
	viper.SetDefault("js_hook_timeout", 5*time.Second)
	viper.SetDefault("js_memory_limit", int64(64*1024*1024))
	viper.SetDefault("js_max_total_attempts", 50)
	viper.SetDefault("js_max_delay", 60*time.Second)
	viper.SetDefault("kv.driver", "memory")
	viper.SetDefault("kv.redis_url", "localhost:6379")
	viper.SetDefault("llmbridge_plugin_start_timeout", 10*time.Second)
	viper.SetDefault("heap_dump_dir", os.TempDir())
	viper.SetDefault("app_title", "PicoTera")
	viper.SetDefault("auth.oidc.session_ttl", 12*time.Hour)
	viper.SetDefault("auth.oidc.scopes", "openid profile email")
	viper.SetDefault("auth.oidc.client_auth_method", OIDCClientAuthBasic)

	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	bindEnvs(Config{})
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("configx: unmarshal: %w", err)
	}

	if config.Auth.HeaderEnabled && config.Auth.HeaderName == "" {
		return nil, errors.New("auth.header_enabled is set but auth.header_name is empty")
	}
	if config.DatabaseURL == "" {
		return nil, errors.New("database_url is required")
	}
	if config.Port <= 0 || config.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if err := validateAuth(&config); err != nil {
		return nil, err
	}
	if config.S3.Endpoint != "" && (config.S3.AccessKey == "" || config.S3.SecretKey == "") {
		return nil, errors.New("s3 access_key and secret_key are required when s3.endpoint is configured")
	}

	return &config, nil
}

// validateAuth enforces that exactly one identity provider is enabled and, in
// oidc mode, that every setting the login flow will need is present and
// well-formed. Everything here is fail-fast at startup: a half-configured oidc
// provider must not become a runtime surprise on the first login attempt. On
// success config.BaseURL is normalized to its trailing-slash-free form.
func validateAuth(config *Config) error {
	enabled := 0
	for _, on := range []bool{config.Auth.SingleUserMode, config.Auth.HeaderEnabled, config.Auth.OIDC.Enabled} {
		if on {
			enabled++
		}
	}
	switch {
	case enabled == 0:
		return errors.New("no auth provider enabled")
	case enabled > 1:
		return errors.New("exactly one auth provider must be enabled")
	}

	oidc := config.Auth.OIDC
	if !oidc.Enabled {
		return nil
	}

	if oidc.ClientID == "" {
		return errors.New("auth.oidc.client_id is required when auth.oidc.enabled is set")
	}
	if oidc.ClientSecret == "" {
		return errors.New("auth.oidc.client_secret is required when auth.oidc.enabled is set")
	}
	if oidc.SessionSecret == "" {
		return errors.New("auth.oidc.session_secret is required when auth.oidc.enabled is set")
	}
	if oidc.SessionTTL <= 0 {
		return errors.New("auth.oidc.session_ttl must be positive")
	}
	if oidc.ClientAuthMethod != OIDCClientAuthBasic && oidc.ClientAuthMethod != OIDCClientAuthPost {
		return fmt.Errorf("auth.oidc.client_auth_method must be %q or %q", OIDCClientAuthBasic, OIDCClientAuthPost)
	}

	baseURL, err := normalizeBaseURL(config.BaseURL)
	if err != nil {
		return err
	}
	config.BaseURL = baseURL

	if oidc.NeedsDiscovery() {
		// The discovery document url is built by concatenation, so a trailing
		// slash is rejected outright rather than silently trimmed.
		if err := validateIssuer(oidc.Issuer); err != nil {
			return err
		}
	} else if oidc.PKCE == nil {
		return errors.New("auth.oidc.pkce must be set explicitly when auth / token / userinfo endpoints are all configured")
	}

	return nil
}

// normalizeBaseURL validates base_url as an absolute url naming only an origin
// and returns it without a trailing slash. The scheme is required to be present
// but is not checked against a whitelist — running behind a TLS-terminating
// proxy is the operator's call.
func normalizeBaseURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("base_url is required when auth.oidc.enabled is set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("base_url is not a valid url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("base_url must be an absolute url with a scheme and a host")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("base_url must not carry a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("base_url must not carry a query or fragment")
	}
	u.Path = ""
	return u.String(), nil
}

// validateIssuer checks the issuer url used for discovery. go-oidc compares the
// discovery document's own issuer against this value byte for byte, so a
// trailing slash here is a configuration error, not something to paper over.
func validateIssuer(raw string) error {
	if raw == "" {
		return errors.New("auth.oidc.issuer is required unless auth / token / userinfo endpoints are all configured")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("auth.oidc.issuer is not a valid url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("auth.oidc.issuer must be an absolute url with a scheme and a host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("auth.oidc.issuer must not carry a query or fragment")
	}
	if strings.HasSuffix(raw, "/") {
		return errors.New("auth.oidc.issuer must not end with a slash")
	}
	return nil
}

func bindEnvs(iface interface{}, parts ...string) {
	ifv := reflect.ValueOf(iface)
	ift := reflect.TypeOf(iface)
	for i := 0; i < ift.NumField(); i++ {
		v := ifv.Field(i)
		t := ift.Field(i)
		tv, ok := t.Tag.Lookup("mapstructure")
		if !ok {
			continue
		}
		switch v.Kind() {
		case reflect.Struct:
			bindEnvs(v.Interface(), append(parts, tv)...)
		default:
			viper.BindEnv(strings.Join(append(parts, tv), "."))
		}
	}
}
