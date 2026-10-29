package llmbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

const (
	pluginName = "llmbridge"
	pluginABI  = 1
)

// MaxGRPCMessageSize 是插件 gRPC 单条消息的尺寸上限。聚合超长流式响应时整条
// 拼接后的 body 通过单个 unary 调用跨进程传输，远超 gRPC 默认的 4 MiB。
const MaxGRPCMessageSize = 256 << 20 // 256 MiB

var pluginHandshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PICOTERA_LLMBRIDGE_PLUGIN",
	MagicCookieValue: "1",
}

type llmBridgeGRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	server LLMBridgeServer
}

func (p *llmBridgeGRPCPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	RegisterLLMBridgeServer(s, p.server)
	return nil
}

func (p *llmBridgeGRPCPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return NewLLMBridgeClient(c), nil
}

func pluginMap(server LLMBridgeServer) plugin.PluginSet {
	return plugin.PluginSet{
		pluginName: &llmBridgeGRPCPlugin{server: server},
	}
}

func PluginMap(server LLMBridgeServer) plugin.PluginSet {
	return pluginMap(server)
}

func PluginHandshake() plugin.HandshakeConfig {
	return pluginHandshake
}

func PluginABI() uint32 {
	return pluginABI
}

func headerToProto(h http.Header) map[string]*HeaderValues {
	if h == nil {
		return nil
	}
	out := make(map[string]*HeaderValues, len(h))
	for key, values := range h {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = &HeaderValues{Values: copied}
	}
	return out
}

func HeaderToProto(h http.Header) map[string]*HeaderValues {
	return headerToProto(h)
}

func headerFromProto(h map[string]*HeaderValues) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for key, values := range h {
		if values == nil {
			out[key] = nil
			continue
		}
		copied := make([]string, len(values.Values))
		copy(copied, values.Values)
		out[key] = copied
	}
	return out
}

func HeaderFromProto(h map[string]*HeaderValues) http.Header {
	return headerFromProto(h)
}

func profileToProto(profile OutboundProfile) (*OutboundProfileMessage, error) {
	if profile.Config == nil {
		profile.Config = map[string]any{}
	}
	raw, err := json.Marshal(profile.Config)
	if err != nil {
		return nil, fmt.Errorf("llmbridge: encode outbound profile config: %w", err)
	}
	if err := validateJSONObject(raw); err != nil {
		return nil, fmt.Errorf("llmbridge: encode outbound profile config: %w", err)
	}
	return &OutboundProfileMessage{Type: profile.Type, ConfigJson: raw}, nil
}

func ProfileToProto(profile OutboundProfile) (*OutboundProfileMessage, error) {
	return profileToProto(profile)
}

func profileFromProto(profile *OutboundProfileMessage) (OutboundProfile, error) {
	if profile == nil {
		return OutboundProfile{Config: map[string]any{}}, nil
	}
	raw := profile.ConfigJson
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if err := validateJSONObject(raw); err != nil {
		return OutboundProfile{}, fmt.Errorf("llmbridge: decode outbound profile config: %w", err)
	}
	var cfg map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&cfg); err != nil {
		return OutboundProfile{}, fmt.Errorf("llmbridge: decode outbound profile config: %w", err)
	}
	if cfg == nil {
		return OutboundProfile{}, fmt.Errorf("llmbridge: decode outbound profile config: expected JSON object")
	}
	return OutboundProfile{Type: profile.Type, Config: cfg}, nil
}

func ProfileFromProto(profile *OutboundProfileMessage) (OutboundProfile, error) {
	return profileFromProto(profile)
}

func formatToProto(f Format) int32 {
	return int32(f)
}

func FormatToProto(f Format) int32 {
	return formatToProto(f)
}

func formatFromProto(v int32) (Format, error) {
	f := Format(v)
	switch f {
	case FormatAnthropicMessages, FormatOpenAIChatCompletions, FormatOpenAIResponses, FormatGeminiGenerateContent, FormatGeminiStreamGenerateContent:
		return f, nil
	default:
		return FormatUnknown, fmt.Errorf("llmbridge: unknown format value %d", v)
	}
}

func FormatFromProto(v int32) (Format, error) {
	return formatFromProto(v)
}

func validatePluginABI(got uint32) error {
	if got != pluginABI {
		return fmt.Errorf("llmbridge: plugin ABI version mismatch: got %d want %d", got, pluginABI)
	}
	return nil
}

func validateJSONObject(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var obj any
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	if _, ok := obj.(map[string]any); !ok {
		return fmt.Errorf("expected JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return fmt.Errorf("multiple JSON values")
}
