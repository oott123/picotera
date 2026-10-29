package llmbridgeimpl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"picotera/pkg/llmbridge"

	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/transformer"
	anthropictrans "github.com/looplj/axonhub/llm/transformer/anthropic"
	deepseektrans "github.com/looplj/axonhub/llm/transformer/deepseek"
	fireworkstrans "github.com/looplj/axonhub/llm/transformer/fireworks"
	geminitrans "github.com/looplj/axonhub/llm/transformer/gemini"
	openaitrans "github.com/looplj/axonhub/llm/transformer/openai"
	openairesponses "github.com/looplj/axonhub/llm/transformer/openai/responses"
	openroutertrans "github.com/looplj/axonhub/llm/transformer/openrouter"
	zaitrans "github.com/looplj/axonhub/llm/transformer/zai"
)

const (
	placeholderURL = "https://upstream.invalid"
	placeholderKey = "placeholder"
)

// inboundFor returns the axonhub Inbound transformer for parsing a body
// written in this format. We treat the four canonical Inbound choices as
// stateless singletons; the constructors only allocate an empty struct.
func inboundFor(f llmbridge.Format) (transformer.Inbound, error) {
	switch f {
	case llmbridge.FormatAnthropicMessages:
		return anthropictrans.NewInboundTransformer(), nil
	case llmbridge.FormatOpenAIChatCompletions:
		return openaitrans.NewInboundTransformer(), nil
	case llmbridge.FormatOpenAIResponses:
		return openairesponses.NewInboundTransformer(), nil
	case llmbridge.FormatGeminiGenerateContent, llmbridge.FormatGeminiStreamGenerateContent:
		return geminitrans.NewInboundTransformer(), nil
	default:
		return nil, fmt.Errorf("llmbridge: unsupported source format %q", f)
	}
}

// outboundFor returns the axonhub Outbound transformer for writing a body
// in this format. The transformers expect a baseURL and APIKeyProvider for
// URL construction and auth — picotera handles both itself, so we hand them
// throwaway placeholders. Only the body bytes are read out of the result.
func outboundFor(f llmbridge.Format, profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	switch profile.Type {
	case "anthropic":
		if f != llmbridge.FormatAnthropicMessages {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatAnthropicMessages, f)
		}
		return anthropicOutbound(profile)
	case "openai":
		if f != llmbridge.FormatOpenAIChatCompletions {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatOpenAIChatCompletions, f)
		}
		return openAIOutbound(profile)
	case "openaiResponses":
		if f != llmbridge.FormatOpenAIResponses {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatOpenAIResponses, f)
		}
		return openAIResponsesOutbound(profile)
	case "gemini":
		if f != llmbridge.FormatGeminiGenerateContent && f != llmbridge.FormatGeminiStreamGenerateContent {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s or %s, got %s", profile.Type, llmbridge.FormatGeminiGenerateContent, llmbridge.FormatGeminiStreamGenerateContent, f)
		}
		return geminiOutbound(profile)
	case "openrouter":
		if f != llmbridge.FormatOpenAIChatCompletions {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatOpenAIChatCompletions, f)
		}
		return openRouterOutbound(profile)
	case "deepseek":
		if f != llmbridge.FormatOpenAIChatCompletions {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatOpenAIChatCompletions, f)
		}
		return deepSeekOutbound(profile)
	case "fireworks":
		if f != llmbridge.FormatOpenAIChatCompletions {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatOpenAIChatCompletions, f)
		}
		return fireworksOutbound(profile)
	case "zai":
		if f != llmbridge.FormatOpenAIChatCompletions {
			return nil, fmt.Errorf("llmbridge: outbound type %q is only compatible with %s, got %s", profile.Type, llmbridge.FormatOpenAIChatCompletions, f)
		}
		return zaiOutbound(profile)
	default:
		return nil, fmt.Errorf("llmbridge: unsupported outbound type %q", profile.Type)
	}
}

func anthropicOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &anthropictrans.Config{Type: anthropictrans.PlatformDirect}
	forceAnthropicConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	if cfg.Type == "" {
		cfg.Type = anthropictrans.PlatformDirect
	}
	forceAnthropicConfig(cfg)
	return anthropictrans.NewOutboundTransformerWithConfig(cfg)
}

func openAIOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &openaitrans.Config{PlatformType: openaitrans.PlatformOpenAI}
	forceOpenAIConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	if cfg.PlatformType == "" {
		cfg.PlatformType = openaitrans.PlatformOpenAI
	}
	forceOpenAIConfig(cfg)
	return openaitrans.NewOutboundTransformerWithConfig(cfg)
}

func openAIResponsesOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &openairesponses.Config{}
	forceOpenAIResponsesConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	forceOpenAIResponsesConfig(cfg)
	return openairesponses.NewOutboundTransformerWithConfig(cfg)
}

func geminiOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := geminitrans.Config{}
	forceGeminiConfig(&cfg)
	if err := decodeOutboundConfig(profile.Config, &cfg); err != nil {
		return nil, err
	}
	forceGeminiConfig(&cfg)
	return geminitrans.NewOutboundTransformerWithConfig(cfg)
}

func openRouterOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &openroutertrans.Config{}
	forceOpenRouterConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	forceOpenRouterConfig(cfg)
	return openroutertrans.NewOutboundTransformerWithConfig(cfg)
}

func deepSeekOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &deepseektrans.Config{}
	forceDeepSeekConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	forceDeepSeekConfig(cfg)
	return deepseektrans.NewOutboundTransformerWithConfig(cfg)
}

func fireworksOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &fireworkstrans.Config{}
	forceFireworksConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	forceFireworksConfig(cfg)
	return fireworkstrans.NewOutboundTransformerWithConfig(cfg)
}

func zaiOutbound(profile llmbridge.OutboundProfile) (transformer.Outbound, error) {
	cfg := &zaitrans.Config{}
	forceZaiConfig(cfg)
	if err := decodeOutboundConfig(profile.Config, cfg); err != nil {
		return nil, err
	}
	forceZaiConfig(cfg)
	return zaitrans.NewOutboundTransformerWithConfig(cfg)
}

func decodeOutboundConfig(config map[string]any, dst any) error {
	if len(config) == 0 {
		return nil
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("llmbridge: encode outbound config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("llmbridge: decode outbound config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("llmbridge: decode outbound config: multiple JSON values")
		}
		return fmt.Errorf("llmbridge: decode outbound config: %w", err)
	}
	return nil
}

func forceAnthropicConfig(cfg *anthropictrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceOpenAIConfig(cfg *openaitrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceOpenAIResponsesConfig(cfg *openairesponses.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceGeminiConfig(cfg *geminitrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceOpenRouterConfig(cfg *openroutertrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceDeepSeekConfig(cfg *deepseektrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceFireworksConfig(cfg *fireworkstrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}

func forceZaiConfig(cfg *zaitrans.Config) {
	cfg.BaseURL = placeholderURL
	cfg.APIKeyProvider = auth.NewStaticKeyProvider(placeholderKey)
}
