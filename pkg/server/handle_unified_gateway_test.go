package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/llmbridge"
	"picotera/pkg/llmbridgeimpl"

	"github.com/go-chi/chi/v5"
	"github.com/tidwall/gjson"
)

// Smoke-coverage of the small helpers that translate between bridge
// formats, endpoint type ids, and the per-route stream behavior. The
// handler itself is not covered by tests yet — picotera has no postgres
// test harness and Server can't be built without one. See plan §8.

// unifiedRouteByPath looks a route up in the runtime-constant table. Tests use
// the real entries rather than hand-built ones so the table itself is covered.
func unifiedRouteByPath(t *testing.T, path string) unifiedRoute {
	t.Helper()
	for _, r := range unifiedRoutes {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("no unified route registered at %s", path)
	return unifiedRoute{}
}

// TestUnifiedRoutesTable pins the route table's invariants: paths are unique
// (chi would otherwise panic on the duplicate registration), every route
// declares the SourceType its synthetic endpoint reports, and passthrough is
// exactly the set of routes llmbridge has no format for.
func TestUnifiedRoutesTable(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range unifiedRoutes {
		if seen[r.Path] {
			t.Errorf("duplicate unified route path %s", r.Path)
		}
		seen[r.Path] = true
		if r.Name == "" {
			t.Errorf("route %s has no display name", r.Path)
		}
	}

	cases := []struct {
		path            string
		wantFormat      llmbridge.Format
		wantSourceType  int32
		wantPassthrough bool
	}{
		{"/api/unified/v1/messages", llmbridge.FormatAnthropicMessages, contract.EndpointType_AnthropicMessages, false},
		{"/api/unified/v1/responses", llmbridge.FormatOpenAIResponses, contract.EndpointType_OpenAIResponses, false},
		{"/api/unified/v1/chat/completions", llmbridge.FormatOpenAIChatCompletions, contract.EndpointType_OpenAIChatCompletions, false},
		{"/api/unified/v1beta/models/{model}:generateContent", llmbridge.FormatGeminiGenerateContent, contract.EndpointType_GeminiGenerateContent, false},
		{"/api/unified/v1beta/models/{model}:streamGenerateContent", llmbridge.FormatGeminiStreamGenerateContent, contract.EndpointType_GeminiStreamGenerateContent, false},
		// The Codex responses route is a second mount of the OpenAI Responses
		// source — same format and source type as /v1/responses.
		{"/api/unified/codex/responses", llmbridge.FormatOpenAIResponses, contract.EndpointType_OpenAIResponses, false},
		{"/api/unified/codex/responses/compact", llmbridge.FormatUnknown, contract.EndpointType_CodexCompact, true},
		{"/api/unified/v1/alpha/search", llmbridge.FormatUnknown, contract.EndpointType_CodexSearchV1Alpha, true},
		{"/api/unified/v1/embeddings", llmbridge.FormatUnknown, contract.EndpointType_OpenAIEmbedding, true},
	}
	if len(cases) != len(unifiedRoutes) {
		t.Fatalf("route table has %d entries, test covers %d", len(unifiedRoutes), len(cases))
	}
	for _, tc := range cases {
		r := unifiedRouteByPath(t, tc.path)
		if r.Format != tc.wantFormat {
			t.Errorf("%s: Format = %s, want %s", tc.path, r.Format, tc.wantFormat)
		}
		if r.SourceType != tc.wantSourceType {
			t.Errorf("%s: SourceType = %d, want %d", tc.path, r.SourceType, tc.wantSourceType)
		}
		if r.passthrough() != tc.wantPassthrough {
			t.Errorf("%s: passthrough() = %v, want %v", tc.path, r.passthrough(), tc.wantPassthrough)
		}
	}
}

func TestUpstreamFormatFor(t *testing.T) {
	cases := map[int32]llmbridge.Format{
		contract.EndpointType_AnthropicMessages:           llmbridge.FormatAnthropicMessages,
		contract.EndpointType_OpenAIChatCompletions:       llmbridge.FormatOpenAIChatCompletions,
		contract.EndpointType_OpenAIResponses:             llmbridge.FormatOpenAIResponses,
		contract.EndpointType_GeminiGenerateContent:       llmbridge.FormatGeminiGenerateContent,
		contract.EndpointType_GeminiStreamGenerateContent: llmbridge.FormatGeminiStreamGenerateContent,
		contract.EndpointType_AnthropicCountTokens:        llmbridge.FormatUnknown,
		contract.EndpointType_Unknown:                     llmbridge.FormatUnknown,
	}
	for t1, want := range cases {
		if got := upstreamFormatFor(t1); got != want {
			t.Errorf("upstreamFormatFor(%d) = %s, want %s", t1, got, want)
		}
	}
}

func TestResponseAggregationFormat(t *testing.T) {
	cases := []struct {
		endpointType int32
		wantFormat   llmbridge.Format
		wantOK       bool
	}{
		{contract.EndpointType_AnthropicMessages, llmbridge.FormatAnthropicMessages, true},
		{contract.EndpointType_OpenAIChatCompletions, llmbridge.FormatOpenAIChatCompletions, true},
		{contract.EndpointType_OpenAIResponses, llmbridge.FormatOpenAIResponses, true},
		{contract.EndpointType_GeminiStreamGenerateContent, llmbridge.FormatGeminiStreamGenerateContent, true},
		{contract.EndpointType_GeminiGenerateContent, llmbridge.FormatUnknown, false},
		{contract.EndpointType_General, llmbridge.FormatUnknown, false},
		{contract.EndpointType_Unknown, llmbridge.FormatUnknown, false},
	}
	for _, tt := range cases {
		gotFormat, gotOK := responseAggregationFormat(tt.endpointType)
		if gotFormat != tt.wantFormat || gotOK != tt.wantOK {
			t.Errorf("responseAggregationFormat(%d) = (%s, %v), want (%s, %v)", tt.endpointType, gotFormat, gotOK, tt.wantFormat, tt.wantOK)
		}
	}
}

func TestBuildAggregatedArtifactGeminiStreamAndNonStream(t *testing.T) {
	streamLine := `{"responseId":"resp-1","modelVersion":"gemini-test","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	profile, err := llmbridge.DefaultOutboundProfileForFormat(llmbridge.FormatGeminiStreamGenerateContent)
	if err != nil {
		t.Fatal(err)
	}
	aggregated := buildAggregatedArtifact(context.Background(), fakeLLMBridge{}, llmbridge.FormatGeminiStreamGenerateContent, "application/json", []byte(streamLine+"\n"), profile)
	if aggregated == nil {
		t.Fatal("expected aggregated artifact")
	}
	if aggregated.Error != "" {
		t.Fatalf("unexpected aggregation error: %s", aggregated.Error)
	}
	if aggregated.Format != "geminiStreamGenerateContent" || !strings.Contains(string(aggregated.Body), `"responseId":"resp-1"`) {
		t.Fatalf("unexpected aggregated body: format=%s body=%s", aggregated.Format, aggregated.Body)
	}

	nonStreamProfile, err := llmbridge.DefaultOutboundProfileForFormat(llmbridge.FormatGeminiGenerateContent)
	if err != nil {
		t.Fatal(err)
	}
	aggregated = buildAggregatedArtifact(context.Background(), fakeLLMBridge{}, llmbridge.FormatGeminiGenerateContent, "application/json", []byte(`{"candidates":[]}`), nonStreamProfile)
	if aggregated != nil {
		t.Fatalf("Gemini non-stream should not aggregate, got %+v", aggregated)
	}
}

type fakeLLMBridge struct{}

func (fakeLLMBridge) Enabled() bool {
	return true
}

func (fakeLLMBridge) Close(ctx context.Context) error {
	return nil
}

func (fakeLLMBridge) BridgeRequest(ctx context.Context, src, dst llmbridge.Format, body []byte, headers http.Header, pendingURL string, profile llmbridge.OutboundProfile) ([]byte, string, error) {
	return body, "application/json", nil
}

func (fakeLLMBridge) BridgeNonStream(ctx context.Context, src, upstream llmbridge.Format, upstreamBody []byte, upstreamHeaders http.Header, profile llmbridge.OutboundProfile) ([]byte, string, error) {
	return upstreamBody, "application/json", nil
}

func (fakeLLMBridge) BridgeStream(ctx context.Context, src, upstream llmbridge.Format, upstreamBody io.ReadCloser, upstreamCT string, profile llmbridge.OutboundProfile) (io.ReadCloser, error) {
	return upstreamBody, nil
}

func (fakeLLMBridge) AggregateStream(ctx context.Context, format llmbridge.Format, contentType string, body []byte, profile llmbridge.OutboundProfile) ([]byte, error) {
	return llmbridgeimpl.AggregateStream(ctx, format, contentType, body, profile)
}

func (fakeLLMBridge) SignalPlugin(sig syscall.Signal) error {
	return nil
}

func TestCandidateEndpointTypes(t *testing.T) {
	// Anthropic / OpenAI sources: stream flag picks the Gemini variant.
	got := candidateEndpointTypes(unifiedRouteByPath(t, "/api/unified/v1/messages"), false)
	want := []int32{
		contract.EndpointType_AnthropicMessages,
		contract.EndpointType_OpenAIChatCompletions,
		contract.EndpointType_OpenAIResponses,
		contract.EndpointType_GeminiGenerateContent,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Anthropic non-stream set = %v, want %v", got, want)
	}
	got = candidateEndpointTypes(unifiedRouteByPath(t, "/api/unified/v1/chat/completions"), true)
	want = []int32{
		contract.EndpointType_AnthropicMessages,
		contract.EndpointType_OpenAIChatCompletions,
		contract.EndpointType_OpenAIResponses,
		contract.EndpointType_GeminiStreamGenerateContent,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OpenAI stream set = %v, want %v", got, want)
	}

	// Gemini routes ignore the stream-flag arg and always use their own
	// fixed pair.
	got = candidateEndpointTypes(unifiedRouteByPath(t, "/api/unified/v1beta/models/{model}:streamGenerateContent"), false)
	if got[len(got)-1] != contract.EndpointType_GeminiStreamGenerateContent {
		t.Errorf("Gemini stream route returned wrong gemini variant: %v", got)
	}

	// The Codex responses route shares the OpenAI Responses candidate set.
	got = candidateEndpointTypes(unifiedRouteByPath(t, "/api/unified/codex/responses"), false)
	want = candidateEndpointTypes(unifiedRouteByPath(t, "/api/unified/v1/responses"), false)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codex responses set = %v, want same as /v1/responses %v", got, want)
	}
}

// TestCandidateEndpointTypesPassthrough pins that the passthrough routes only
// ever consider an upstream of their own endpoint type — there is no converter,
// so nothing else can serve them — and that the stream flag has no say in it
// (there is no Gemini variant to choose).
func TestCandidateEndpointTypesPassthrough(t *testing.T) {
	cases := map[string]int32{
		"/api/unified/codex/responses/compact": contract.EndpointType_CodexCompact,
		"/api/unified/v1/alpha/search":         contract.EndpointType_CodexSearchV1Alpha,
		"/api/unified/v1/embeddings":           contract.EndpointType_OpenAIEmbedding,
	}
	for path, wantType := range cases {
		route := unifiedRouteByPath(t, path)
		for _, streaming := range []bool{false, true} {
			got := candidateEndpointTypes(route, streaming)
			if !reflect.DeepEqual(got, []int32{wantType}) {
				t.Errorf("%s (streaming=%v) = %v, want [%d]", path, streaming, got, wantType)
			}
		}
	}
}

func TestExtractUnifiedModel_BodyFormats(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet","stream":true}`)
	r := httptest.NewRequest("POST", "/api/unified/v1/messages", nil)
	model, err := extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1/messages"), r, body)
	if err != nil {
		t.Fatal(err)
	}
	if model != "claude-3-5-sonnet" {
		t.Errorf("got model=%q", model)
	}

	// Missing model field: 400 MODEL_NOT_FOUND.
	_, err = extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1/chat/completions"), r, []byte(`{}`))
	if err == nil {
		t.Errorf("expected error for missing model, got nil")
	}
}

// TestExtractUnifiedModel_Passthrough pins that the passthrough routes route by
// the body's `model` like every non-Gemini route — that is what makes
// rewriteModel and the beforeRequest upstreamModel override work on them.
func TestExtractUnifiedModel_Passthrough(t *testing.T) {
	cases := map[string]struct {
		body      string
		wantModel string
	}{
		"/api/unified/codex/responses/compact": {`{"model":"gpt-5-codex","query":"hi"}`, "gpt-5-codex"},
		"/api/unified/v1/alpha/search":         {`{"model":"gpt-5-codex","query":"hi"}`, "gpt-5-codex"},
		"/api/unified/v1/embeddings":           {`{"model":"text-embedding-3-small","input":"hi"}`, "text-embedding-3-small"},
	}
	for path, tc := range cases {
		route := unifiedRouteByPath(t, path)
		r := httptest.NewRequest("POST", path, nil)
		model, err := extractUnifiedModel(route, r, []byte(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if model != tc.wantModel {
			t.Errorf("%s: got model=%q, want %s", path, model, tc.wantModel)
		}

		// Missing / empty model is a 400, not a fallback.
		for _, bad := range [][]byte{[]byte(`{}`), []byte(`{"model":""}`)} {
			_, err = extractUnifiedModel(route, r, bad)
			var gerr *gatewayError
			if !errors.As(err, &gerr) {
				t.Fatalf("%s: body %s: expected gatewayError, got %v", path, bad, err)
			}
			if gerr.status != http.StatusBadRequest || gerr.code != errorx.ModelNotFound.Error() {
				t.Errorf("%s: body %s: got status=%d code=%s, want 400 %s", path, bad, gerr.status, gerr.code, errorx.ModelNotFound.Error())
			}
		}
	}
}

func TestExtractUnifiedModel_GeminiFromPath(t *testing.T) {
	// Build a chi route context that simulates the chi router placing
	// {model} into the URL params.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("model", "gemini-2.5-pro")
	r := httptest.NewRequest("POST", "/api/unified/v1beta/models/gemini-2.5-pro:streamGenerateContent", nil)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	model, err := extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1beta/models/{model}:streamGenerateContent"), r, []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if model != "gemini-2.5-pro" {
		t.Errorf("got model=%q", model)
	}

	model, err = extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1beta/models/{model}:generateContent"), r, []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if model != "gemini-2.5-pro" {
		t.Errorf("non-stream variant: got model=%q", model)
	}
}

func TestDetectStreaming(t *testing.T) {
	newReq := func(accept ...string) *http.Request {
		r := httptest.NewRequest("POST", "/api/unified/v1/messages", nil)
		for _, a := range accept {
			r.Header.Add("Accept", a)
		}
		return r
	}

	cases := []struct {
		name   string
		src    llmbridge.Format
		req    *http.Request
		body   []byte
		expect bool
	}{
		{"gemini stream route", llmbridge.FormatGeminiStreamGenerateContent, newReq(), []byte(`{}`), true},
		{"gemini non-stream route", llmbridge.FormatGeminiGenerateContent, newReq(), []byte(`{}`), false},
		{"body stream true", llmbridge.FormatAnthropicMessages, newReq(), []byte(`{"stream":true}`), true},
		{"body stream false", llmbridge.FormatAnthropicMessages, newReq(), []byte(`{"stream":false}`), false},
		{"accept sse", llmbridge.FormatOpenAIChatCompletions, newReq("text/event-stream"), []byte(`{}`), true},
		{"accept ndjson", llmbridge.FormatOpenAIChatCompletions, newReq("application/x-ndjson"), []byte(`{}`), true},
		{"accept case-insensitive", llmbridge.FormatOpenAIChatCompletions, newReq("Text/Event-Stream"), []byte(`{}`), true},
		{"accept json only", llmbridge.FormatOpenAIChatCompletions, newReq("application/json"), []byte(`{}`), false},
		{"no signals", llmbridge.FormatAnthropicMessages, newReq(), []byte(`{}`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectStreaming(tc.src, tc.req, tc.body); got != tc.expect {
				t.Errorf("detectStreaming = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestSetUnifiedModel(t *testing.T) {
	// Body-bearing source: model is rewritten via sjson.
	body := []byte(`{"model":"old","messages":[]}`)
	out, err := setUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1/messages"), body, "new")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == string(body) {
		t.Errorf("expected model rewrite, body unchanged: %s", out)
	}
	// Gemini: body unchanged because the model lives in the URL.
	body = []byte(`{"contents":[]}`)
	out, err = setUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1beta/models/{model}:generateContent"), body, "new")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(body) {
		t.Errorf("expected Gemini body unchanged, got %s", out)
	}
}

// TestSetUnifiedModelPassthrough pins that the upstream model override still
// reaches the wire on the passthrough routes: the body is forwarded verbatim
// apart from this one field.
func TestSetUnifiedModelPassthrough(t *testing.T) {
	for _, path := range []string{"/api/unified/codex/responses/compact", "/api/unified/v1/alpha/search", "/api/unified/v1/embeddings"} {
		out, err := setUnifiedModel(unifiedRouteByPath(t, path), []byte(`{"model":"old","query":"hi"}`), "upstream-model")
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := gjson.GetBytes(out, "model").Str; got != "upstream-model" {
			t.Errorf("%s: model = %q, want upstream-model", path, got)
		}
		if got := gjson.GetBytes(out, "query").Str; got != "hi" {
			t.Errorf("%s: query = %q, want hi (rest of body must survive)", path, got)
		}
	}
}

// TestUnifiedUpstreamPathVars pins the fix for the unified Gemini upstream URL
// bug: when a model is configured only on a Gemini endpoint and the inbound
// request is a unified non-Gemini source (e.g. Anthropic Messages), the inbound
// route carries no {model} path variable, so the {model} token in the Gemini
// upstream URL must be filled from the resolved upstream model name instead.
//
// Before the fix the unified branch passed chiURLParams(r) (empty for the
// Anthropic/OpenAI source routes) straight through, leaving {model} unresolved
// and sending ".../models/%7Bmodel%7D:generateContent" upstream.
func TestUnifiedUpstreamPathVars(t *testing.T) {
	if got := unifiedUpstreamPathVars("gemini-2.5-flash"); !reflect.DeepEqual(got, map[string]string{"model": "gemini-2.5-flash"}) {
		t.Errorf("unifiedUpstreamPathVars(model) = %v, want {model: gemini-2.5-flash}", got)
	}
	if got := unifiedUpstreamPathVars(""); got != nil {
		t.Errorf("unifiedUpstreamPathVars(\"\") = %v, want nil", got)
	}

	// End-to-end: the Gemini upstream URL token resolves to the upstream model.
	geminiURL := "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent"
	resolved, err := substitutePathVars(geminiURL, unifiedUpstreamPathVars("gemini-2.5-flash"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resolved, "{") {
		t.Fatalf("upstream URL still has unresolved token: %s", resolved)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"
	if resolved != want {
		t.Fatalf("substituted URL = %s, want %s", resolved, want)
	}
}

// TestBridgeUnifiedRequestGeminiStreamAltSSE pins the fix for the unified
// bridge-to-Gemini-streamGenerateContent case: a non-Gemini source (e.g.
// Anthropic Messages) never carries alt=sse, so Gemini would return a JSON
// array stream instead of SSE and BridgeStream would fail to parse it. The
// bridge path must force alt=sse onto the upstream URL.
func TestBridgeUnifiedRequestGeminiStreamAltSSE(t *testing.T) {
	f := &gatewayFlow{
		h:      &gatewayHandler{&Server{llmBridge: fakeLLMBridge{}}},
		config: gatewayFlowConfig{SourceFormat: llmbridge.FormatAnthropicMessages},
	}
	req := httptest.NewRequest("POST", "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent", nil)
	input := attemptInput{Sidecar: gatewayCandidateSidecar{UpstreamFormat: llmbridge.FormatGeminiStreamGenerateContent}}

	got, _, err := bridgeUnifiedRequest(context.Background(), f, input, req, []byte(`{}`), llmbridge.OutboundProfile{})
	if err != nil {
		t.Fatal(err)
	}
	if alt := got.URL.Query().Get("alt"); alt != "sse" {
		t.Fatalf("bridge to Gemini stream: alt=%q, want \"sse\"", alt)
	}
}

// TestBridgeUnifiedRequestIdentityNoAltSSE pins that identity passthrough
// (source == upstream == Gemini streamGenerateContent) returns early and does
// NOT inject alt=sse — the client's own query is preserved byte-for-byte.
func TestBridgeUnifiedRequestIdentityNoAltSSE(t *testing.T) {
	f := &gatewayFlow{
		h:      &gatewayHandler{&Server{llmBridge: fakeLLMBridge{}}},
		config: gatewayFlowConfig{SourceFormat: llmbridge.FormatGeminiStreamGenerateContent},
	}
	req := httptest.NewRequest("POST", "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent", nil)
	input := attemptInput{Sidecar: gatewayCandidateSidecar{UpstreamFormat: llmbridge.FormatGeminiStreamGenerateContent}}

	got, _, err := bridgeUnifiedRequest(context.Background(), f, input, req, []byte(`{}`), llmbridge.OutboundProfile{})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.RawQuery != "" {
		t.Fatalf("identity passthrough must not inject query, got %q", got.URL.RawQuery)
	}
}

func TestDedupeUnifiedRows(t *testing.T) {
	row := func(providerID int32, et int32, path string) db.GetProvidersByEndpointTypesAndModelRow {
		return db.GetProvidersByEndpointTypesAndModelRow{
			ModelName:    "m",
			ProviderID:   providerID,
			EndpointType: et,
			EndpointPath: path,
		}
	}
	type want struct {
		providerID int32
		path       string
	}
	cases := []struct {
		name    string
		rows    []db.GetProvidersByEndpointTypesAndModelRow
		srcType int32
		want    []want
	}{
		{
			name:    "single",
			rows:    []db.GetProvidersByEndpointTypesAndModelRow{row(1, contract.EndpointType_OpenAIChatCompletions, "/v1/chat")},
			srcType: contract.EndpointType_OpenAIChatCompletions,
			want:    []want{{1, "/v1/chat"}},
		},
		{
			name: "src match",
			rows: []db.GetProvidersByEndpointTypesAndModelRow{
				row(1, contract.EndpointType_AnthropicMessages, "/a"),
				row(1, contract.EndpointType_OpenAIChatCompletions, "/c"),
			},
			srcType: contract.EndpointType_OpenAIChatCompletions,
			want:    []want{{1, "/c"}},
		},
		{
			name: "anthropic preferred",
			rows: []db.GetProvidersByEndpointTypesAndModelRow{
				row(1, contract.EndpointType_OpenAIResponses, "/r"),
				row(1, contract.EndpointType_AnthropicMessages, "/a"),
			},
			srcType: contract.EndpointType_GeminiGenerateContent,
			want:    []want{{1, "/a"}},
		},
		{
			name: "chat preferred",
			rows: []db.GetProvidersByEndpointTypesAndModelRow{
				row(1, contract.EndpointType_OpenAIResponses, "/r"),
				row(1, contract.EndpointType_OpenAIChatCompletions, "/c"),
			},
			srcType: contract.EndpointType_GeminiGenerateContent,
			want:    []want{{1, "/c"}},
		},
		{
			name: "path tiebreak",
			rows: []db.GetProvidersByEndpointTypesAndModelRow{
				row(1, contract.EndpointType_OpenAIResponses, "/z"),
				row(1, contract.EndpointType_OpenAIResponses, "/a"),
			},
			srcType: contract.EndpointType_GeminiGenerateContent,
			want:    []want{{1, "/a"}},
		},
		{
			name: "multi provider",
			rows: []db.GetProvidersByEndpointTypesAndModelRow{
				row(1, contract.EndpointType_OpenAIChatCompletions, "/c"),
				row(2, contract.EndpointType_AnthropicMessages, "/a"),
			},
			srcType: contract.EndpointType_OpenAIChatCompletions,
			want: []want{
				{1, "/c"},
				{2, "/a"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupeUnifiedRows(tc.rows, tc.srcType)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got=%+v)", len(got), len(tc.want), got)
			}
			gotW := make([]want, len(got))
			for i, r := range got {
				gotW[i] = want{r.ProviderID, r.EndpointPath}
			}
			if !reflect.DeepEqual(gotW, tc.want) {
				t.Errorf("got %+v, want %+v", gotW, tc.want)
			}
		})
	}
}
