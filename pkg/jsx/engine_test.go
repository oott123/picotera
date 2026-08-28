package jsx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/kv"
)

type fakeStore struct{ scripts []db.Script }

func (f *fakeStore) ListEnabledScripts(_ context.Context) ([]db.Script, error) {
	return f.scripts, nil
}

// hostAnnoCall records one setAnnotation call that reached the fake HostAPI.
// Kind is "request" / "provider" / "apiKey"; only the matching id field is set.
// Value is nil for a delete.
type hostAnnoCall struct {
	Kind      string
	RequestID string
	ID        int32
	Key       string
	Value     *string
}

// fakeHostAPI is a recording jsx.HostAPI: it captures every set call and serves
// configurable get results (nil summary = not found).
type fakeHostAPI struct {
	annoCalls []hostAnnoCall
	setErr    error

	provider    *ProviderSummary
	apiKey      *ApiKeySummary
	getErr      error
	providerIDs []int32
	apiKeyIDs   []int32
}

func (h *fakeHostAPI) SetRequestAnnotation(_ context.Context, requestID, key string, value *string) error {
	h.annoCalls = append(h.annoCalls, hostAnnoCall{Kind: "request", RequestID: requestID, Key: key, Value: value})
	return h.setErr
}

func (h *fakeHostAPI) SetProviderAnnotation(_ context.Context, providerID int32, key string, value *string) error {
	h.annoCalls = append(h.annoCalls, hostAnnoCall{Kind: "provider", ID: providerID, Key: key, Value: value})
	return h.setErr
}

func (h *fakeHostAPI) SetApiKeyAnnotation(_ context.Context, apiKeyID int32, key string, value *string) error {
	h.annoCalls = append(h.annoCalls, hostAnnoCall{Kind: "apiKey", ID: apiKeyID, Key: key, Value: value})
	return h.setErr
}

func (h *fakeHostAPI) GetProvider(_ context.Context, providerID int32) (*ProviderSummary, error) {
	h.providerIDs = append(h.providerIDs, providerID)
	return h.provider, h.getErr
}

func (h *fakeHostAPI) GetApiKey(_ context.Context, apiKeyID int32) (*ApiKeySummary, error) {
	h.apiKeyIDs = append(h.apiKeyIDs, apiKeyID)
	return h.apiKey, h.getErr
}

func newTestEngine(t *testing.T, scripts ...db.Script) Engine {
	t.Helper()
	return newTestEngineWithHost(t, &fakeHostAPI{}, scripts...)
}

func newTestEngineWithHost(t *testing.T, host HostAPI, scripts ...db.Script) Engine {
	t.Helper()
	return NewEngine(
		Config{HookTimeout: 500 * time.Millisecond, MemoryLimit: 64 * 1024 * 1024},
		&fakeStore{scripts: scripts},
		kv.NewMemoryStore(),
		host,
	)
}

func newTestSession(t *testing.T, scripts ...db.Script) Session {
	t.Helper()
	return newTestSessionWithHost(t, &fakeHostAPI{}, scripts...)
}

func newTestSessionWithHost(t *testing.T, host HostAPI, scripts ...db.Script) Session {
	t.Helper()
	s, err := newTestEngineWithHost(t, host, scripts...).NewSession(context.Background(), "test-req")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestEngine_LoadsScripts(t *testing.T) {
	// Two taps registered across two scripts both fire in priority order.
	s := newTestSession(t,
		db.Script{ID: "a", Source: `picotera.hooks.rewriteModel.tap("a", function (ctx, m) { return m + "-a"; }, 1);`},
		db.Script{ID: "b", Source: `picotera.hooks.rewriteModel.tap("b", function (ctx, m) { return m + "-b"; }, 10);`},
	)
	out, err := s.RunRewriteModel("base")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "base-b-a" {
		t.Errorf("want priority waterfall base-b-a, got %q", out)
	}
}

func TestSession_CloseIdempotent(t *testing.T) {
	s := newTestSession(t)
	s.Close()
	s.Close()
	s.Close()
}

func TestSession_BadScript_FailsSession(t *testing.T) {
	_, err := newTestEngine(t, db.Script{ID: "bad", Source: `this is not valid javascript @@@`}).
		NewSession(context.Background(), "")
	if err == nil {
		t.Fatalf("want error loading invalid script, got nil")
	}
	got := err.Error()
	if !strings.Contains(got, "jsx: eval script bad") || !strings.Contains(got, "script:bad") {
		t.Fatalf("error did not include script id filename: %v", got)
	}
}

func TestSession_LoadErrorIncludesScriptFilenameAndLine(t *testing.T) {
	_, err := newTestEngine(t, db.Script{ID: "script-load-fail", Source: "var ok = 1;\nvar bad = ;"}).
		NewSession(context.Background(), "")
	if err == nil {
		t.Fatalf("want error loading invalid script, got nil")
	}
	got := err.Error()
	for _, want := range []string{"eval script script-load-fail", "script:script-load-fail", ":2:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q: %v", want, got)
		}
	}
}

func TestSession_RuntimeErrorIncludesScriptStackAndTapName(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "script-runtime-fail", Source: `
function failFromUserScript() {
  throw new Error("runtime boom");
}
picotera.hooks.rewriteModel.tap("runtime-tap", function (ctx, m) {
  failFromUserScript();
  return m;
});
`})
	_, err := s.RunRewriteModel("base")
	if err == nil {
		t.Fatalf("want runtime error, got nil")
	}
	got := err.Error()
	for _, want := range []string{"script:script-runtime-fail", ":3:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q: %v", want, got)
		}
	}
}

func TestSession_PatchContext_VisibleToHook(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx) {
			return ctx.requestModel + "/" + ctx.routedModel.name + "/" + ctx.provider.name;
		});
	`})
	rm := "req-model"
	if err := s.PatchContext(ContextPatch{
		RequestModel: &rm,
		RoutedModel:  &ModelSummary{Name: "routed", Annotations: map[string]string{}},
		Provider:     &ProviderSummary{ID: 7, Name: "prov"},
	}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	out, err := s.RunRewriteModel("ignored")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "req-model/routed/prov" {
		t.Errorf("hook did not see patched ctx, got %q", out)
	}
}

func TestSession_PatchContext_PreservesCustomFields(t *testing.T) {
	// A hook stashes a value on ctx; a later PatchContext (Object.assign) must
	// not wipe it, and a later hook can read it back.
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx, m) { ctx.mine = 99; return m; });
		picotera.hooks.beforeRequest.tap("a", function (ctx, d) { return { delay: ctx.mine }; });
	`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	// Patch an unrelated field; must preserve ctx.mine.
	if err := s.PatchContext(ContextPatch{Provider: &ProviderSummary{ID: 1}}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if dec.Delay != 99 {
		t.Errorf("custom ctx field not preserved across patches, got delay=%d", dec.Delay)
	}
}

func TestSession_RewriteModel_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.rewriteModel.tap("a", function () {});`})
	out, err := s.RunRewriteModel("claude-3-haiku")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "claude-3-haiku" {
		t.Errorf("want passthrough, got %q", out)
	}
}

func TestSession_RewriteModel_NonStringKeepsInitial(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.rewriteModel.tap("a", function () { return 42; });`})
	out, err := s.RunRewriteModel("orig")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "orig" {
		t.Errorf("non-string result should keep initial, got %q", out)
	}
}

func TestSession_SortProviders_Reverse(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx, list) { return list.slice().reverse(); });
	`})
	out, err := s.RunSortProviders([]CandidateView{
		{Provider: ProviderSummary{ID: 1}},
		{Provider: ProviderSummary{ID: 2}},
	})
	if err != nil {
		t.Fatalf("RunSortProviders: %v", err)
	}
	if len(out) != 2 || out[0].Provider.ID != 2 {
		t.Errorf("want reversed, got %+v", out)
	}
}

func TestSession_SortProviders_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.sortProviders.tap("a", function () {});`})
	in := []CandidateView{{Provider: ProviderSummary{ID: 1}}}
	out, err := s.RunSortProviders(in)
	if err != nil {
		t.Fatalf("RunSortProviders: %v", err)
	}
	if len(out) != 1 || out[0].Provider.ID != 1 {
		t.Errorf("want passthrough preserves input, got %+v", out)
	}
}

func TestSession_SortProviders_ProvidersWrapper(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx, list) { return { providers: list.slice(0, 1) }; });
	`})
	out, err := s.RunSortProviders([]CandidateView{
		{Provider: ProviderSummary{ID: 1}},
		{Provider: ProviderSummary{ID: 2}},
	})
	if err != nil {
		t.Fatalf("RunSortProviders: %v", err)
	}
	if len(out) != 1 || out[0].Provider.ID != 1 {
		t.Errorf("want {providers:[...]} shape accepted, got %+v", out)
	}
}

func TestSession_SortProviders_EmptyArrayMeansNoProviders(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.sortProviders.tap("a", function () { return []; });`})
	out, err := s.RunSortProviders([]CandidateView{{Provider: ProviderSummary{ID: 1}}})
	if err != nil {
		t.Fatalf("RunSortProviders: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty array should mean no providers, got %+v", out)
	}
}

func TestSession_BeforeRequest_Defaults(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.beforeRequest.tap("a", function () {});`})
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if dec.Next || dec.Delay != 0 || dec.UpstreamModel != "" {
		t.Errorf("want defaults, got %+v", dec)
	}
}

func TestSession_BeforeRequest_InitialNextPreserved(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.beforeRequest.tap("a", function () {});`})
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{Next: true})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if !dec.Next {
		t.Errorf("passthrough should preserve initial next=true, got %+v", dec)
	}
}

func TestSession_BeforeRequest_NextAndDelay(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.beforeRequest.tap("a", function () { return { next: true, delay: 100 }; });`})
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if !dec.Next || dec.Delay != 100 {
		t.Errorf("want {next:true, delay:100}, got %+v", dec)
	}
}

func TestSession_BeforeRequest_UpstreamModelNonStringDropped(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.beforeRequest.tap("a", function () { return { upstreamModel: 42 }; });`})
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if dec.UpstreamModel != "" {
		t.Errorf("non-string upstreamModel should be dropped, got %q", dec.UpstreamModel)
	}
}

func TestSession_AfterUpstreamError_Passthrough(t *testing.T) {
	// A tap that returns nothing keeps the initial break and carries no
	// statusCode/message override (follow-upstream semantics).
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function () {});`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{StatusCode: 429, Message: "rate limited"})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if dec.Break || dec.StatusCode != 0 || dec.Message != "" {
		t.Errorf("passthrough should keep break and drop overrides, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_DefaultBreakSeed(t *testing.T) {
	// A break=true seed (e.g. the gateway's default 400 passthrough) with a
	// passthrough hook keeps break=true and carries no override, so the caller
	// faithfully passes through the upstream status/body/Content-Type.
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function () {});`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{Break: true, StatusCode: 400, Message: "bad"})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if !dec.Break || dec.StatusCode != 0 || dec.Message != "" {
		t.Errorf("want {break:true, statusCode:0, message:\"\"}, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_HookOverridesDefaultBreak(t *testing.T) {
	// A hook can switch off the seeded default break by returning { break: false }.
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function () { return { break: false }; });`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{Break: true, StatusCode: 400, Message: "bad"})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if dec.Break {
		t.Errorf("hook should override default break to false, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_Break(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function () { return { break: true, statusCode: 400, message: "nope" }; });`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{StatusCode: 500, Message: "boom"})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if !dec.Break || dec.StatusCode != 400 || dec.Message != "nope" {
		t.Errorf("want {break:true, statusCode:400, message:nope}, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_BreakFollowsUpstream(t *testing.T) {
	// break=true with no statusCode/message means "follow upstream": the
	// decision carries the empty overrides so the caller falls back.
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function () { return { break: true }; });`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{StatusCode: 503, Message: "unavailable"})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if !dec.Break || dec.StatusCode != 0 || dec.Message != "" {
		t.Errorf("want {break:true, statusCode:0, message:\"\"}, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_NonStringMessageDropped(t *testing.T) {
	// statusCode is integer-cast (|0): a numeric string coerces to its int; a
	// non-string message is dropped to "".
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function () { return { break: true, statusCode: "418", message: 42 }; });`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if !dec.Break || dec.StatusCode != 418 || dec.Message != "" {
		t.Errorf("statusCode should coerce to 418 and non-string message drop, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_Streamed(t *testing.T) {
	// The hook observes streamed=true; it can still return break but the caller
	// is responsible for ignoring it. Here we only verify the input is visible.
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function (ctx, v) { return { break: v.streamed, statusCode: 0, message: "" }; });`})
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{StatusCode: 200, Message: "stream error", Streamed: true})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if !dec.Break {
		t.Errorf("hook should see streamed=true, got %+v", dec)
	}
}

func TestSession_AfterUpstreamError_LastErrorVisible(t *testing.T) {
	// ctx.attempt.lastError is consumed by the hook (set via PatchContext before
	// the run). Mirror the server's patch and assert the hook reads it.
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.afterUpstreamError.tap("a", function (ctx) { return { break: true, statusCode: 0, message: ctx.attempt.lastError.message }; });`})
	if err := s.PatchContext(ContextPatch{Attempt: &AttemptState{LastError: &LastError{ProviderID: 7, StatusCode: 500, Message: "prev"}}}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	dec, err := s.RunAfterUpstreamError(UpstreamErrorView{StatusCode: 500, Message: "prev"})
	if err != nil {
		t.Fatalf("RunAfterUpstreamError: %v", err)
	}
	if dec.Message != "prev" {
		t.Errorf("hook should read ctx.attempt.lastError.message, got %q", dec.Message)
	}
}

func TestSession_RewriteRequest_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.rewriteRequest.tap("a", function () {});`})
	in := PendingRequestShape{
		URL:     "https://x/v1/chat",
		Method:  "POST",
		Headers: map[string][]string{"content-type": {"application/json"}},
	}
	out, err := s.RunRewriteRequest(in, []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("RunRewriteRequest: %v", err)
	}
	if out.URL != in.URL || out.Method != in.Method {
		t.Errorf("passthrough should preserve URL/Method, got %+v", out)
	}
	// Untouched body → nil Body so the caller falls back to its own bytes.
	if out.Body != nil {
		t.Errorf("passthrough should leave Body nil, got %s", string(out.Body))
	}
}

func TestSession_RewriteRequest_BodyJSONRoundtrip(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, pending) { pending.body.b = 2; return pending; });
	`})
	out, err := s.RunRewriteRequest(PendingRequestShape{
		URL:     "https://x",
		Method:  "POST",
		Headers: map[string][]string{"content-type": {"application/json"}},
	}, []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("RunRewriteRequest: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("body should be raw JSON object bytes: %v (raw=%q)", err, string(out.Body))
	}
	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("want {a:1,b:2}, got %+v", got)
	}
}

func TestSession_RewriteRequest_NoBody(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, pending) {
			pending.url = "https://y/models";
			pending.headers["x-added"] = ["1"];
			return pending;
		});
	`})
	out, err := s.RunRewriteRequest(PendingRequestShape{
		URL:     "https://x/models",
		Method:  "GET",
		Headers: map[string][]string{"authorization": {"Bearer t"}},
	}, nil)
	if err != nil {
		t.Fatalf("RunRewriteRequest: %v", err)
	}
	if out.URL != "https://y/models" {
		t.Errorf("URL not rewritten, got %+v", out)
	}
	if got := out.Headers["x-added"]; len(got) != 1 || got[0] != "1" {
		t.Errorf("header not added, got %+v", out.Headers)
	}
	if out.Body != nil {
		t.Errorf("no-body request should leave Body nil, got %s", string(out.Body))
	}
}

func TestSession_RewriteRequest_NoBody_ScriptAddsBody(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, pending) {
			return { ...pending, method: "POST", body: { scope: "all" } };
		});
	`})
	out, err := s.RunRewriteRequest(PendingRequestShape{
		URL:     "https://x/models",
		Method:  "GET",
		Headers: map[string][]string{},
	}, nil)
	if err != nil {
		t.Fatalf("RunRewriteRequest: %v", err)
	}
	if out.Method != "POST" {
		t.Errorf("method not rewritten, got %q", out.Method)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("body should be raw JSON object bytes: %v (raw=%q)", err, string(out.Body))
	}
	if got["scope"] != "all" {
		t.Errorf("want {scope:all}, got %+v", got)
	}
}

func TestSession_BeforeTransform_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.beforeTransform.tap("a", function () {});`})
	out, err := s.RunBeforeTransform(OutboundProfile{Type: "openai", Config: map[string]any{}})
	if err != nil {
		t.Fatalf("RunBeforeTransform: %v", err)
	}
	if out.Type != "openai" || len(out.Config) != 0 {
		t.Errorf("want passthrough, got %+v", out)
	}
}

func TestSession_BeforeTransform_ReturnsNewProfile(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeTransform.tap("a", function () { return { type: "openrouter", config: { provider: "x" } }; });
	`})
	out, err := s.RunBeforeTransform(OutboundProfile{Type: "openai", Config: map[string]any{"keep": true}})
	if err != nil {
		t.Fatalf("RunBeforeTransform: %v", err)
	}
	if out.Type != "openrouter" || out.Config["provider"] != "x" {
		t.Errorf("want openrouter profile, got %+v", out)
	}
	if _, ok := out.Config["keep"]; ok {
		t.Errorf("new profile should replace config, got %+v", out.Config)
	}
}

func TestSession_BeforeTransform_TypeMustBeString(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.beforeTransform.tap("a", function () { return { type: 42 }; });`})
	_, err := s.RunBeforeTransform(OutboundProfile{Type: "openai", Config: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "jsx: beforeTransform type must be string") {
		t.Fatalf("err = %v, want beforeTransform type error", err)
	}
}

func TestSession_BeforeTransform_ConfigMustBeObject(t *testing.T) {
	for _, tt := range []struct{ name, source string }{
		{"number", `picotera.hooks.beforeTransform.tap("a", function () { return { config: 42 }; });`},
		{"array", `picotera.hooks.beforeTransform.tap("a", function () { return { config: [] }; });`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession(t, db.Script{ID: "a", Source: tt.source})
			_, err := s.RunBeforeTransform(OutboundProfile{Type: "openai", Config: map[string]any{}})
			if err == nil || !strings.Contains(err.Error(), "jsx: beforeTransform config must be object") {
				t.Fatalf("err = %v, want beforeTransform config error", err)
			}
		})
	}
}

func TestSession_RewriteProviderModels_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.rewriteProviderModels.tap("a", function () {});`})
	in := []ProviderModelEntry{{Model: "gpt-4o"}, {Model: "my-mini", UpstreamModelName: "gpt-4o-mini"}}
	out, err := s.RunRewriteProviderModels(in)
	if err != nil {
		t.Fatalf("RunRewriteProviderModels: %v", err)
	}
	if len(out) != 2 || out[0].Model != "gpt-4o" || out[1].Model != "my-mini" {
		t.Errorf("want passthrough, got %+v", out)
	}
}

func TestSession_RewriteProviderModels_Replace(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteProviderModels.tap("a", function () {
			return [{model: 'a'}, {model: 'b', upstreamModelName: 'B', priority: 7, annotations: {x: 'y'}}];
		});
	`})
	out, err := s.RunRewriteProviderModels(nil)
	if err != nil {
		t.Fatalf("RunRewriteProviderModels: %v", err)
	}
	if len(out) != 2 || out[1].Model != "b" || out[1].UpstreamModelName != "B" || out[1].Priority != 7 || out[1].Annotations["x"] != "y" {
		t.Errorf("unexpected result: %+v", out)
	}
}

func TestSession_RewriteProviderModels_NonArrayKeepsInput(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.rewriteProviderModels.tap("a", function () { return 42; });`})
	in := []ProviderModelEntry{{Model: "keep-me"}}
	out, err := s.RunRewriteProviderModels(in)
	if err != nil {
		t.Fatalf("RunRewriteProviderModels: %v", err)
	}
	if len(out) != 1 || out[0].Model != "keep-me" {
		t.Errorf("want fallback to input, got %+v", out)
	}
}

func TestSession_RewriteProviderModels_ReadsUpstreamResponse(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteProviderModels.tap("a", function (ctx) {
			return [{model: ctx.upstreamResponse.data[0].id}];
		});
	`})
	if err := s.PatchContext(ContextPatch{
		UpstreamResponse: json.RawMessage(`{"data":[{"id":"up-model"}]}`),
	}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	out, err := s.RunRewriteProviderModels(nil)
	if err != nil {
		t.Fatalf("RunRewriteProviderModels: %v", err)
	}
	if len(out) != 1 || out[0].Model != "up-model" {
		t.Errorf("want model from upstreamResponse, got %+v", out)
	}
}

func TestSession_Timeout_TaintsSession(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.sortProviders.tap("a", function () { for(;;){} });`})
	start := time.Now()
	_, err := s.RunSortProviders([]CandidateView{{Provider: ProviderSummary{ID: 1}}})
	if err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
	// Tainted: subsequent hooks fast-fail.
	if _, err := s.RunBeforeRequest(BeforeRequestDecision{}); err != ErrHookTimeout {
		t.Errorf("tainted session should fast-fail, got %v", err)
	}
	if err := s.PatchContext(ContextPatch{Provider: &ProviderSummary{ID: 1}}); err != ErrHookTimeout {
		t.Errorf("tainted session PatchContext should fast-fail, got %v", err)
	}
}

func TestSession_MemoryLimit(t *testing.T) {
	eng := NewEngine(
		Config{HookTimeout: 2 * time.Second, MemoryLimit: 2 * 1024 * 1024},
		&fakeStore{scripts: []db.Script{{ID: "a", Source: `
			picotera.hooks.sortProviders.tap("a", function () { var x = new Array(5000000).fill(0); return [{provider:{id:x.length}}]; });
		`}}},
		kv.NewMemoryStore(),
		&fakeHostAPI{},
	)
	s, err := eng.NewSession(context.Background(), "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	_, err = s.RunSortProviders([]CandidateView{{Provider: ProviderSummary{ID: 1}}})
	if err == nil {
		t.Fatalf("want memory-limit error, got nil")
	}
	if err == ErrHookTimeout {
		t.Errorf("memory exhaustion should not surface as a timeout")
	}
}

func TestSession_Fetch_Sync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function () {
			var resp = picotera.fetch("` + srv.URL + `");
			var parsed = JSON.parse(resp.body);
			return String(resp.status) + ":" + parsed.hello;
		});
	`})
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "200:world" {
		t.Errorf("fetch result mismatch, got %q", out)
	}
}

func TestSession_KV_RoundTrip(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function () {
			picotera.kv.set("k", { n: 5 });
			var v = picotera.kv.get("k");
			var missing = picotera.kv.get("nope");
			return v.n + ":" + (missing === null);
		});
	`})
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "5:true" {
		t.Errorf("kv round-trip mismatch, got %q", out)
	}
}

func TestSession_Console_Captured(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function () {
			console.log("hello", { nested: true });
			console.warn("careful");
			console.error("boom");
			return "m";
		});
	`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	logs := s.Logs()
	if len(logs) != 3 {
		t.Fatalf("want 3 log entries, got %d (%+v)", len(logs), logs)
	}
	if logs[0].Level != "info" || !strings.Contains(logs[0].Message, "hello") {
		t.Errorf("unexpected first log: %+v", logs[0])
	}
	if logs[1].Level != "warn" || logs[2].Level != "error" {
		t.Errorf("unexpected log levels: %+v", logs)
	}
}
