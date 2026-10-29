package server

import (
	"context"
	"encoding/json"

	"picotera/pkg/artifacts"
	"picotera/pkg/contract"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"
)

// responseAggregationFormat maps an endpoint to the llmbridge format its
// response should be aggregated as. suffix is the prefix endpoint's sub-path
// for this request (empty for ordinary endpoints); it only matters for codex,
// whose sub-paths are open-ended and mostly carry no aggregatable payload —
// only /responses is OpenAI Responses, mirroring codexUnifiedRoute.
func responseAggregationFormat(endpointType int32, suffix string) (llmbridge.Format, bool) {
	switch endpointType {
	case contract.EndpointType_AnthropicMessages:
		return llmbridge.FormatAnthropicMessages, true
	case contract.EndpointType_OpenAIChatCompletions:
		return llmbridge.FormatOpenAIChatCompletions, true
	case contract.EndpointType_OpenAIResponses:
		return llmbridge.FormatOpenAIResponses, true
	case contract.EndpointType_GeminiStreamGenerateContent:
		return llmbridge.FormatGeminiStreamGenerateContent, true
	case contract.EndpointType_Codex:
		if suffix == codexResponsesSuffix {
			return llmbridge.FormatOpenAIResponses, true
		}
		return llmbridge.FormatUnknown, false
	default:
		return llmbridge.FormatUnknown, false
	}
}

func buildAggregatedArtifact(ctx context.Context, bridge llmbridge.Bridge, format llmbridge.Format, contentType string, body []byte, profile llmbridge.OutboundProfile) *artifacts.AggregatedResponse {
	// Some upstreams stream SSE without a Content-Type header at all. Sniff the
	// body in that case so aggregation still runs; a header that says something
	// else is taken at face value. The sniffed value replaces contentType
	// outright, so StreamAggregationKind and AggregateStream stay in agreement.
	if contentType == "" && bodyIsSSE(body) {
		contentType = "text/event-stream"
	}
	kind := llmbridge.StreamAggregationKind(format, contentType)
	if kind == llmbridge.StreamAggregationNone {
		return nil
	}
	if bridge == nil || !bridge.Enabled() {
		return nil
	}
	aggregated := &artifacts.AggregatedResponse{
		Format:       format.String(),
		BodyEncoding: "json",
	}
	resp, err := bridge.AggregateStream(ctx, format, contentType, body, profile)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("format", format.String()).Warn("artifact: aggregate response stream failed")
		aggregated.Error = err.Error()
		return aggregated
	}
	aggregated.Body = json.RawMessage(resp)
	return aggregated
}

func defaultAggregationProfile(format llmbridge.Format) (llmbridge.OutboundProfile, bool) {
	profile, err := llmbridge.DefaultOutboundProfileForFormat(format)
	if err != nil {
		return llmbridge.OutboundProfile{}, false
	}
	return profile, true
}
