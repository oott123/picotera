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
		suffix       string
		wantFormat   llmbridge.Format
		wantOK       bool
	}{
		{contract.EndpointType_AnthropicMessages, "", llmbridge.FormatAnthropicMessages, true},
		{contract.EndpointType_OpenAIChatCompletions, "", llmbridge.FormatOpenAIChatCompletions, true},
		{contract.EndpointType_OpenAIResponses, "", llmbridge.FormatOpenAIResponses, true},
		{contract.EndpointType_GeminiStreamGenerateContent, "", llmbridge.FormatGeminiStreamGenerateContent, true},
		{contract.EndpointType_GeminiGenerateContent, "", llmbridge.FormatUnknown, false},
		{contract.EndpointType_General, "", llmbridge.FormatUnknown, false},
		{contract.EndpointType_Unknown, "", llmbridge.FormatUnknown, false},
		// Codex is a prefix endpoint: only the /responses sub-path carries an
		// aggregatable payload.
		{contract.EndpointType_Codex, "/responses", llmbridge.FormatOpenAIResponses, true},
		{contract.EndpointType_Codex, "/responses/compact", llmbridge.FormatUnknown, false},
		{contract.EndpointType_Codex, "/alpha/search", llmbridge.FormatUnknown, false},
		{contract.EndpointType_Codex, "", llmbridge.FormatUnknown, false},
	}
	for _, tt := range cases {
		gotFormat, gotOK := responseAggregationFormat(tt.endpointType, tt.suffix)
		if gotFormat != tt.wantFormat || gotOK != tt.wantOK {
			t.Errorf("responseAggregationFormat(%d, %q) = (%s, %v), want (%s, %v)", tt.endpointType, tt.suffix, gotFormat, gotOK, tt.wantFormat, tt.wantOK)
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

	// The codex /responses sub-path is the OpenAI Responses candidate set plus
	// codex itself (which serves it byte-for-byte).
	got = candidateEndpointTypes(codexUnifiedRoute("/responses"), false)
	want = append(candidateEndpointTypes(unifiedRouteByPath(t, "/api/unified/v1/responses"), false), contract.EndpointType_Codex)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codex responses set = %v, want %v", got, want)
	}
}

// TestNormalizeCodexSuffix pins the /v1 stripping that makes a base_url of
// …/api/unified/codex and one of …/api/unified/codex/v1 equivalent, and the
// empty-suffix cases that must 404 rather than dispatch on nothing.
func TestNormalizeCodexSuffix(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{"/responses", "/responses", true},
		{"/v1/responses", "/responses", true},
		{"/responses/compact", "/responses/compact", true},
		{"/v1/responses/compact", "/responses/compact", true},
		{"/alpha/search", "/alpha/search", true},
		{"/v1/alpha/search", "/alpha/search", true},
		// "/v1" is only a segment when followed by "/" — "/v1x/y" is a real path.
		{"/v1x/y", "/v1x/y", true},
		{"", "", false},
		{"/", "", false},
		{"/v1", "", false},
		{"/v1/", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeCodexSuffix(tc.raw)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("normalizeCodexSuffix(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestCodexMountPatterns wires codexMountPatterns onto a bare chi router (no
// Server, no DB) to pin that the /backend-api alias leaves chi the same
// wildcard remainder as the canonical prefix, so both normalize to the same
// suffix and record the same endpoint_path.
func TestCodexMountPatterns(t *testing.T) {
	router := chi.NewRouter()
	h := func(w http.ResponseWriter, r *http.Request) {
		suffix, ok := normalizeCodexSuffix("/" + chi.URLParam(r, "*"))
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, codexUnifiedRoute(suffix).Path)
	}
	for _, pattern := range codexMountPatterns {
		router.Post(pattern, h)
	}

	cases := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{"/api/unified/codex/responses", http.StatusOK, "/api/unified/codex/responses"},
		{"/api/unified/backend-api/codex/responses", http.StatusOK, "/api/unified/codex/responses"},
		{"/api/unified/backend-api/codex/v1/responses", http.StatusOK, "/api/unified/codex/responses"},
		{"/api/unified/backend-api/codex/responses/compact", http.StatusOK, "/api/unified/codex/responses/compact"},
		{"/api/unified/backend-api/codex/alpha/search", http.StatusOK, "/api/unified/codex/alpha/search"},
		// Nothing left to dispatch on — the handler answers 404 itself.
		{"/api/unified/backend-api/codex/v1", http.StatusNotFound, ""},
		{"/api/unified/backend-api/codex/", http.StatusNotFound, ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, nil))
		if rec.Code != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d", tc.path, rec.Code, tc.wantStatus)
		}
		if got := rec.Body.String(); got != tc.wantBody {
			t.Errorf("%s: body = %q, want %q", tc.path, got, tc.wantBody)
		}
	}
}

// TestCodexUnifiedRoute pins the per-request route values the codex mount
// produces: /responses keeps a real source format (so it can bridge), every
// other sub-path is codex-only passthrough, and both record the normalized
// concrete path.
func TestCodexUnifiedRoute(t *testing.T) {
	cases := []struct {
		suffix          string
		wantPath        string
		wantFormat      llmbridge.Format
		wantPassthrough bool
	}{
		{"/responses", "/api/unified/codex/responses", llmbridge.FormatOpenAIResponses, false},
		{"/responses/compact", "/api/unified/codex/responses/compact", llmbridge.FormatUnknown, true},
		{"/alpha/search", "/api/unified/codex/alpha/search", llmbridge.FormatUnknown, true},
	}
	for _, tc := range cases {
		r := codexUnifiedRoute(tc.suffix)
		if r.Path != tc.wantPath {
			t.Errorf("%s: Path = %s, want %s", tc.suffix, r.Path, tc.wantPath)
		}
		if r.Format != tc.wantFormat {
			t.Errorf("%s: Format = %s, want %s", tc.suffix, r.Format, tc.wantFormat)
		}
		if r.passthrough() != tc.wantPassthrough {
			t.Errorf("%s: passthrough() = %v, want %v", tc.suffix, r.passthrough(), tc.wantPassthrough)
		}
		if r.SourceType != contract.EndpointType_Codex {
			t.Errorf("%s: SourceType = %d, want %d", tc.suffix, r.SourceType, contract.EndpointType_Codex)
		}
		if r.UpstreamSuffix != tc.suffix {
			t.Errorf("%s: UpstreamSuffix = %s", tc.suffix, r.UpstreamSuffix)
		}
		if !r.Codex {
			t.Errorf("%s: Codex = false", tc.suffix)
		}
	}

	// Passthrough codex sub-paths only ever consider a codex upstream.
	for _, suffix := range []string{"/responses/compact", "/alpha/search"} {
		for _, streaming := range []bool{false, true} {
			got := candidateEndpointTypes(codexUnifiedRoute(suffix), streaming)
			if !reflect.DeepEqual(got, []int32{contract.EndpointType_Codex}) {
				t.Errorf("%s (streaming=%v) = %v, want [%d]", suffix, streaming, got, contract.EndpointType_Codex)
			}
		}
	}
}

// TestCandidateEndpointTypesPassthrough pins that the passthrough routes only
// ever consider an upstream of their own endpoint type — there is no converter,
// so nothing else can serve them — and that the stream flag has no say in it
// (there is no Gemini variant to choose).
func TestCandidateEndpointTypesPassthrough(t *testing.T) {
	cases := map[string]int32{
		"/api/unified/v1/embeddings": contract.EndpointType_OpenAIEmbedding,
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
	mode, err := extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1/messages"), r, body)
	if err != nil {
		t.Fatal(err)
	}
	if !mode.HasModel || mode.OriginalModel != "claude-3-5-sonnet" {
		t.Errorf("got %+v", mode)
	}

	// Missing model field on a fixed route (not a prefix mount): 400
	// MODEL_NOT_FOUND, no degradation to no-model routing.
	_, err = extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1/chat/completions"), r, []byte(`{}`))
	if err == nil {
		t.Errorf("expected error for missing model, got nil")
	}
}

// TestExtractUnifiedModel_Passthrough pins that the passthrough routes route by
// the body's `model` like every non-Gemini route — that is what makes
// rewriteModel and the beforeRequest upstreamModel override work on them.
func TestExtractUnifiedModel_Passthrough(t *testing.T) {
	cases := []struct {
		name      string
		route     unifiedRoute
		body      string
		wantModel string
	}{
		{"embeddings", unifiedRouteByPath(t, "/api/unified/v1/embeddings"), `{"model":"text-embedding-3-small","input":"hi"}`, "text-embedding-3-small"},
		{"codex compact", codexUnifiedRoute("/responses/compact"), `{"model":"gpt-5-codex","input":[]}`, "gpt-5-codex"},
		{"codex search", codexUnifiedRoute("/alpha/search"), `{"model":"gpt-5-codex","query":"hi"}`, "gpt-5-codex"},
	}
	// Only the Gemini routes read the URL, so one request stands in for all.
	r := httptest.NewRequest("POST", "/api/unified/codex/responses/compact", nil)
	for _, tc := range cases {
		mode, err := extractUnifiedModel(tc.route, r, []byte(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !mode.HasModel || mode.OriginalModel != tc.wantModel {
			t.Errorf("%s: got %+v, want model %s", tc.name, mode, tc.wantModel)
		}

		// An absent model field degrades to no-model routing only on the prefix
		// mount (codex); the fixed embeddings route still 400s.
		mode, err = extractUnifiedModel(tc.route, r, []byte(`{}`))
		if tc.route.PrefixMount {
			if err != nil {
				t.Fatalf("%s: absent model on prefix mount: %v", tc.name, err)
			}
			if mode.HasModel || mode.OriginalModel != "" {
				t.Errorf("%s: absent model on prefix mount: got %+v, want no-model", tc.name, mode)
			}
		} else {
			assertModelNotFound(t, tc.name+" absent model", err)
		}

		// A present-but-invalid model is a 400 either way — the degradation
		// covers "this sub-path carries no model", not bad input.
		for _, bad := range [][]byte{[]byte(`{"model":""}`), []byte(`{"model":123}`), []byte(`{"model":null}`)} {
			_, err = extractUnifiedModel(tc.route, r, bad)
			assertModelNotFound(t, tc.name+" body "+string(bad), err)
		}
	}
}

// assertModelNotFound asserts err is a 400 MODEL_NOT_FOUND gatewayError.
func assertModelNotFound(t *testing.T, label string, err error) {
	t.Helper()
	var gerr *gatewayError
	if !errors.As(err, &gerr) {
		t.Fatalf("%s: expected gatewayError, got %v", label, err)
	}
	if gerr.status != http.StatusBadRequest || gerr.code != errorx.ModelNotFound.Error() {
		t.Errorf("%s: got status=%d code=%s, want 400 %s", label, gerr.status, gerr.code, errorx.ModelNotFound.Error())
	}
}

func TestExtractUnifiedModel_GeminiFromPath(t *testing.T) {
	// Build a chi route context that simulates the chi router placing
	// {model} into the URL params.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("model", "gemini-2.5-pro")
	r := httptest.NewRequest("POST", "/api/unified/v1beta/models/gemini-2.5-pro:streamGenerateContent", nil)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	mode, err := extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1beta/models/{model}:streamGenerateContent"), r, []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !mode.HasModel || mode.OriginalModel != "gemini-2.5-pro" {
		t.Errorf("got %+v", mode)
	}

	mode, err = extractUnifiedModel(unifiedRouteByPath(t, "/api/unified/v1beta/models/{model}:generateContent"), r, []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !mode.HasModel || mode.OriginalModel != "gemini-2.5-pro" {
		t.Errorf("non-stream variant: got %+v", mode)
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
	routes := []unifiedRoute{
		unifiedRouteByPath(t, "/api/unified/v1/embeddings"),
		codexUnifiedRoute("/responses/compact"),
		codexUnifiedRoute("/alpha/search"),
	}
	for _, route := range routes {
		out, err := setUnifiedModel(route, []byte(`{"model":"old","query":"hi"}`), "upstream-model")
		if err != nil {
			t.Fatalf("%s: %v", route.Path, err)
		}
		if got := gjson.GetBytes(out, "model").Str; got != "upstream-model" {
			t.Errorf("%s: model = %q, want upstream-model", route.Path, got)
		}
		if got := gjson.GetBytes(out, "query").Str; got != "hi" {
			t.Errorf("%s: query = %q, want hi (rest of body must survive)", route.Path, got)
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
