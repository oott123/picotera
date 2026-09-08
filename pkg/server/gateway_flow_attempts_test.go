package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"picotera/pkg/configx"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"
	"picotera/pkg/kv"
	"picotera/pkg/llmbridge"
	"picotera/pkg/llmbridgeimpl"

	"github.com/tidwall/gjson"
)

type stubScriptStore struct{ scripts []db.Script }

func (s stubScriptStore) ListEnabledScripts(_ context.Context) ([]db.Script, error) {
	return s.scripts, nil
}

// realBridge delegates BridgeRequest to the in-process converter so the test
// exercises the actual cross-format transformation (the fakeLLMBridge identity
// passthrough would hide field-dropping behavior).
type realBridge struct{ fakeLLMBridge }

func (realBridge) BridgeRequest(ctx context.Context, src, dst llmbridge.Format, body []byte, headers http.Header, pendingURL string, profile llmbridge.OutboundProfile) ([]byte, string, error) {
	return llmbridgeimpl.BridgeRequest(ctx, src, dst, body, headers, pendingURL, profile)
}

// TestUnifiedRewriteRequestSeesUpstreamFormatBody guards the unified-gateway
// hook ordering: llmbridge conversion must run BEFORE rewriteRequest, so a hook
// keyed on the upstream endpoint mutates the upstream-format body. With the old
// after-hook bridging, a hook-added stream_options.include_usage=true on an
// OpenAI Responses source body was silently reduced to "stream_options": {} by
// the Responses→ChatCompletions conversion (the Responses request model has no
// include_usage field).
func TestUnifiedRewriteRequestSeesUpstreamFormatBody(t *testing.T) {
	script := db.Script{ID: "add-include-usage", Source: `
picotera.hooks.rewriteRequest.tap('add-include-usage', function (ctx, pending) {
  const endpoint = ctx.providerModel?.endpoint
  if (endpoint !== '/v1/chat/completions') return pending
  if (!pending.body) return
  if (!pending.body.stream_options) {
    pending.body.stream_options = { include_usage: true }
  }
  if (!pending.body.stream_options.include_usage) {
    pending.body.stream_options.include_usage = true
  }
  return pending
})`}
	eng := jsx.NewEngine(jsx.Config{HookTimeout: 2 * time.Second, MemoryLimit: 64 << 20}, stubScriptStore{[]db.Script{script}}, kv.NewMemoryStore(), stubJSXHostAPI{})
	session, err := eng.NewSession(context.Background(), "test-req")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	pm := jsx.ProviderModel{
		Name:           "gpt-4o",
		Endpoint:       "/v1/chat/completions",
		UpstreamFormat: llmbridge.FormatOpenAIChatCompletions.String(),
	}
	if err := session.PatchContext(jsx.ContextPatch{ProviderModel: &pm}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}

	f := &gatewayFlow{
		h: &gatewayHandler{Server: &Server{llmBridge: realBridge{}, config: &configx.Config{}}},
		r: httptest.NewRequest("POST", "/api/unified/v1/responses", nil),
		config: gatewayFlowConfig{
			Kind:         gatewayRouteUnified,
			SourceFormat: llmbridge.FormatOpenAIResponses,
			// buildRewrittenUpstreamRequest writes the per-attempt upstream
			// model through this closure, so a unified config must carry it.
			SetBodyModel: func(body []byte, model string) ([]byte, error) {
				return setUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1/responses"), body, model)
			},
			PrepareAttempt: prepareUnifiedAttempt,
		},
		body:    []byte(`{"model":"gpt-4o","input":[{"role":"user","content":"ping"}],"stream":true}`),
		session: session,
		model:   gatewayModelState{Original: "gpt-4o", Routed: "gpt-4o"},
	}
	input := attemptInput{
		AttemptCtx:    context.Background(),
		UpstreamModel: "gpt-4o",
		Sidecar: gatewayCandidateSidecar{
			ProviderID:     1,
			UpstreamURL:    "https://up.example/v1/chat/completions",
			Credentials:    "sk-test",
			SendResolver:   contract.CredentialsResolver_BearerToken,
			EndpointPath:   "/v1/chat/completions",
			EndpointType:   contract.EndpointType_OpenAIChatCompletions,
			UpstreamFormat: llmbridge.FormatOpenAIChatCompletions,
		},
	}

	prepared, err := f.buildRewrittenUpstreamRequest(input)
	if err != nil {
		t.Fatalf("buildRewrittenUpstreamRequest: %v", err)
	}
	body := prepared.RequestBody
	if !gjson.GetBytes(body, "messages").Exists() {
		t.Fatalf("upstream body is not chat-completions format: %s", body)
	}
	if !gjson.GetBytes(body, "stream_options.include_usage").Bool() {
		t.Errorf("hook-added stream_options.include_usage lost, got stream_options=%s; body=%s",
			gjson.GetBytes(body, "stream_options").Raw, body)
	}
}

// TestUnifiedNoModelBodyPassesThroughVerbatim pins the no-model contract on a
// prefix mount: when the inbound body carried no `model` field, the extractor
// degraded to no-model routing and UpstreamModel is "", so the upstream body
// must be the client's bytes verbatim — sjson must not inject `"model": ""`
// into a body that never had one.
func TestUnifiedNoModelBodyPassesThroughVerbatim(t *testing.T) {
	eng := jsx.NewEngine(jsx.Config{HookTimeout: 2 * time.Second, MemoryLimit: 64 << 20}, stubScriptStore{}, kv.NewMemoryStore(), stubJSXHostAPI{})
	session, err := eng.NewSession(context.Background(), "test-req")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	// /alpha/search: a codex passthrough sub-path whose body carries no model.
	route := codexUnifiedRoute("/alpha/search")
	clientBody := []byte(`{"query":"ping","max_results":3}`)
	f := &gatewayFlow{
		h: &gatewayHandler{Server: &Server{llmBridge: realBridge{}, config: &configx.Config{}}},
		r: httptest.NewRequest("POST", "/api/unified/codex/alpha/search", nil),
		config: gatewayFlowConfig{
			Kind:         gatewayRouteUnified,
			SourceFormat: route.Format,
			SetBodyModel: func(body []byte, model string) ([]byte, error) {
				return setUnifiedModel(route, body, model)
			},
			PrepareAttempt: identityPrepareAttempt,
		},
		body:    clientBody,
		session: session,
		model:   gatewayModelState{Mode: gatewayModelMode{}},
	}
	input := attemptInput{
		AttemptCtx:    context.Background(),
		UpstreamModel: "",
		Sidecar: gatewayCandidateSidecar{
			ProviderID:     1,
			UpstreamURL:    "https://up.example/backend-api/codex",
			AppendPath:     route.UpstreamSuffix,
			Credentials:    "sk-test",
			SendResolver:   contract.CredentialsResolver_BearerToken,
			EndpointPath:   "/backend-api/codex/alpha/search",
			EndpointType:   contract.EndpointType_Codex,
			UpstreamFormat: llmbridge.FormatUnknown,
		},
	}

	prepared, err := f.buildRewrittenUpstreamRequest(input)
	if err != nil {
		t.Fatalf("buildRewrittenUpstreamRequest: %v", err)
	}
	if string(prepared.RequestBody) != string(clientBody) {
		t.Errorf("upstream body was rewritten: got %s, want %s", prepared.RequestBody, clientBody)
	}
	if gjson.GetBytes(prepared.RequestBody, "model").Exists() {
		t.Errorf("no-model request got a model field injected: %s", prepared.RequestBody)
	}
}
