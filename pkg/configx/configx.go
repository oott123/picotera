package configx

import (
	"errors"
	"fmt"
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
	Auth                             AuthConfig    `mapstructure:"auth"`
}

type AuthConfig struct {
	HeaderEnabled  bool   `mapstructure:"header_enabled"`
	HeaderName     string `mapstructure:"header_name"`
	AutoCreateUser bool   `mapstructure:"auto_create_user"`
	SingleUserMode bool   `mapstructure:"single_user_mode"`
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
	viper.SetDefault("gateway_response_header_timeout", 91*time.Second)
	viper.SetDefault("gateway_http2_read_idle_timeout", 13*time.Second)
	viper.SetDefault("gateway_http2_ping_timeout", 6*time.Second)
	viper.SetDefault("gateway_disable_keep_alives", false)
	viper.SetDefault("gateway_disable_http2", false)
	viper.SetDefault("gateway_ephemeral_transport", false)
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
	if !config.Auth.SingleUserMode && !config.Auth.HeaderEnabled {
		return nil, errors.New("no auth provider enabled")
	}
	if config.S3.Endpoint != "" && (config.S3.AccessKey == "" || config.S3.SecretKey == "") {
		return nil, errors.New("s3 access_key and secret_key are required when s3.endpoint is configured")
	}

	return &config, nil
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
