package jsx

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/kv"

	"github.com/fastschema/qjs"
)

type fakeStore struct{ scripts []db.Script }

func (f *fakeStore) ListEnabledScripts(_ context.Context) ([]db.Script, error) {
	return f.scripts, nil
}

func TestEngine_LoadsScripts(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{
		{ID: "a", Source: `picotera.hooks.sortProviders.tap("a", function (ctx) { return ctx; });`},
		{ID: "b", Source: `picotera.hooks.sortProviders.tap("b", function (ctx) { return ctx; });`},
	}}
	eng := NewEngine(Config{HookTimeout: time.Second, MemoryLimit: 64 * 1024 * 1024}, store, kv.NewMemoryStore())
	s, err := eng.NewSession(context.Background(), "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	v, err := s.Context().Eval("probe.js", qjs.Code("picotera.hooks.sortProviders._taps.length"))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	defer v.Free()
	if got := v.Int32(); got != 2 {
		t.Errorf("want 2 taps, got %d", got)
	}
}

func TestSession_CloseIdempotent(t *testing.T) {
	store := &fakeStore{}
	eng := NewEngine(Config{HookTimeout: time.Second, MemoryLimit: 64 * 1024 * 1024}, store, kv.NewMemoryStore())
	s, err := eng.NewSession(context.Background(), "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s.Close()
	s.Close()
	s.Close()
}

func newTestSession(t *testing.T, scripts ...db.Script) *Session {
	t.Helper()
	eng := NewEngine(Config{HookTimeout: 500 * time.Millisecond, MemoryLimit: 64 * 1024 * 1024}, &fakeStore{scripts: scripts}, kv.NewMemoryStore())
	s, err := eng.NewSession(context.Background(), "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestSession_Promise_ResolvesValue(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `globalThis.giveMe42 = function () { return Promise.resolve(42); }`})
	v, err := s.evalWithTimeout("call.js", "giveMe42()")
	if err != nil {
		t.Fatalf("evalWithTimeout: %v", err)
	}
	defer v.Free()
	if got := v.Int32(); got != 42 {
		t.Errorf("want 42, got %d", got)
	}
}

func TestSession_Promise_PropagatesRejection(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `globalThis.boom = function () { return Promise.reject(new Error("boom message")); }`})
	_, err := s.evalWithTimeout("call.js", "boom()")
	if err == nil {
		t.Fatalf("want rejection, got nil")
	}
	if !contains(err.Error(), "boom message") {
		t.Errorf("error should mention `boom message`: %v", err)
	}
}

func TestSession_Promise_Timeout(t *testing.T) {
	// Tight loop ignoring the eval timeout — runaway script.
	s := newTestSession(t, db.Script{ID: "a", Source: `globalThis.loop = function () { for(;;){} }`})
	start := time.Now()
	_, err := s.evalWithTimeout("call.js", "loop()")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("want ErrHookTimeout, got nil")
	}
	if !errIs(err, ErrHookTimeout) {
		t.Errorf("want ErrHookTimeout, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestSession_CtxRoundTrip(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "rev", Source: `
		picotera.hooks.sortProviders.tap("rev", function (ctx, input) {
			return { providers: input.providers.slice().reverse() };
		});
	`})

	in := map[string]any{"providers": []map[string]any{
		{"id": 1}, {"id": 2}, {"id": 3},
	}}
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	expr := "(async () => JSON.stringify(await picotera.hooks.sortProviders.runWaterfall(" + lit + "," + lit + ")))()"
	jsonStr, err := s.runHookExpr("rev.js", expr)
	if err != nil {
		t.Fatalf("runHookExpr: %v", err)
	}
	var out struct {
		Providers []struct{ ID int } `json:"providers"`
	}
	if err := unmarshalJSON(jsonStr, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Providers) != 3 || out.Providers[0].ID != 3 || out.Providers[2].ID != 1 {
		t.Errorf("want reversed order, got %+v", out.Providers)
	}
}

func TestSession_Hooks_Sort(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx) {
			return ctx.providers.slice().reverse();
		});
	`})
	out, err := s.RunSortHook(SortInput{
		Providers: []Candidate{
			{Provider: ProviderSummary{ID: 1}, MPE: CandidateMPE{ProviderID: 1}},
			{Provider: ProviderSummary{ID: 2}, MPE: CandidateMPE{ProviderID: 2}},
		},
	})
	if err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0].Provider.ID != 2 {
		t.Errorf("want first id=2 after reverse, got %v", out[0].Provider.ID)
	}
}

func TestSession_Hooks_Sort_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function () {});
	`})
	in := []Candidate{{Provider: ProviderSummary{ID: 1}}}
	out, err := s.RunSortHook(SortInput{Providers: in})
	if err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want passthrough preserves input, got %d entries", len(out))
	}
}

func TestSession_Hooks_BeforeRequest_Defaults(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function () {});
	`})
	dec, err := s.RunBeforeRequestHook(BeforeRequestInput{})
	if err != nil {
		t.Fatalf("RunBeforeRequestHook: %v", err)
	}
	if dec.Next || dec.Delay != 0 {
		t.Errorf("want defaults {Next:false, Delay:0}, got %+v", dec)
	}
}

func TestSession_Hooks_BeforeRequest_NextAndDelay(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function () { return { next: true, delay: 100 }; });
	`})
	dec, err := s.RunBeforeRequestHook(BeforeRequestInput{})
	if err != nil {
		t.Fatalf("RunBeforeRequestHook: %v", err)
	}
	if !dec.Next || dec.Delay != 100 {
		t.Errorf("want {Next:true, Delay:100}, got %+v", dec)
	}
}

func TestSession_Hooks_RewriteModel_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function () {});
	`})
	out, err := s.RunRewriteModelHook(RewriteModelInput{}, "claude-3-haiku")
	if err != nil {
		t.Fatalf("RunRewriteModelHook: %v", err)
	}
	if out != "claude-3-haiku" {
		t.Errorf("want passthrough modelName, got %q", out)
	}
}

func TestSession_Hooks_RewriteModel_Replace(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx, m) { return "new-model"; });
	`})
	out, err := s.RunRewriteModelHook(RewriteModelInput{}, "old-model")
	if err != nil {
		t.Fatalf("RunRewriteModelHook: %v", err)
	}
	if out != "new-model" {
		t.Errorf("want new-model, got %q", out)
	}
}

func TestSession_Hooks_BeforeRequest_UpstreamModel(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function () { return { upstreamModel: "foo" }; });
	`})
	dec, err := s.RunBeforeRequestHook(BeforeRequestInput{})
	if err != nil {
		t.Fatalf("RunBeforeRequestHook: %v", err)
	}
	if dec.UpstreamModel != "foo" {
		t.Errorf("want UpstreamModel=foo, got %q", dec.UpstreamModel)
	}
	if dec.Next || dec.Delay != 0 {
		t.Errorf("want {Next:false, Delay:0}, got %+v", dec)
	}
}

func TestSession_Hooks_BeforeRequest_UpstreamModel_NonString(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function () { return { upstreamModel: 42 }; });
	`})
	dec, err := s.RunBeforeRequestHook(BeforeRequestInput{})
	if err != nil {
		t.Fatalf("RunBeforeRequestHook: %v", err)
	}
	if dec.UpstreamModel != "" {
		t.Errorf("non-string upstreamModel should be dropped, got %q", dec.UpstreamModel)
	}
}

func TestSession_Hooks_Rewrite_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function () {});
	`})
	in := PendingRequestShape{
		URL:     "https://x/v1/chat",
		Method:  "POST",
		Headers: map[string][]string{"content-type": {"application/json"}},
		Body:    json.RawMessage(`{"a":1}`),
	}
	out, err := s.RunRewriteHook(RewriteInput{PendingRequest: in})
	if err != nil {
		t.Fatalf("RunRewriteHook: %v", err)
	}
	if out.URL != in.URL || out.Method != in.Method {
		t.Errorf("passthrough should preserve URL/Method, got %+v", out)
	}
	if got, want := len(out.Headers["content-type"]), 1; got != want || out.Headers["content-type"][0] != "application/json" {
		t.Errorf("passthrough should preserve headers, got %+v", out.Headers)
	}
	// Body is round-tripped through JS as an object then JSON.stringify'd:
	// expect the inner string == original JSON.
	var s1 string
	if err := json.Unmarshal(out.Body, &s1); err != nil {
		t.Fatalf("body should be JSON string token, got %s: %v", string(out.Body), err)
	}
	if s1 != `{"a":1}` {
		t.Errorf("body content mismatch: got %q", s1)
	}
}

func TestSession_Hooks_Rewrite_FullReplace(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, pending) {
			return Object.assign({}, pending, { url: "https://y" });
		});
	`})
	out, err := s.RunRewriteHook(RewriteInput{
		PendingRequest: PendingRequestShape{
			URL:    "https://x",
			Method: "POST",
			Headers: map[string][]string{
				"x-keep": {"yes"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RunRewriteHook: %v", err)
	}
	if out.URL != "https://y" {
		t.Errorf("want URL=https://y, got %q", out.URL)
	}
	if out.Method != "POST" {
		t.Errorf("want Method preserved, got %q", out.Method)
	}
	if got := out.Headers["x-keep"]; len(got) != 1 || got[0] != "yes" {
		t.Errorf("want x-keep header preserved, got %+v", out.Headers)
	}
}

func TestSession_Hooks_Rewrite_BodyJSON_Roundtrip(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, pending) {
			pending.body.b = 2;
			return pending;
		});
	`})
	out, err := s.RunRewriteHook(RewriteInput{
		PendingRequest: PendingRequestShape{
			URL:     "https://x",
			Method:  "POST",
			Headers: map[string][]string{"content-type": {"application/json"}},
			Body:    json.RawMessage(`{"a":1}`),
		},
	})
	if err != nil {
		t.Fatalf("RunRewriteHook: %v", err)
	}
	// out.Body is a JSON-encoded string containing the new JSON text.
	var inner string
	if err := json.Unmarshal(out.Body, &inner); err != nil {
		t.Fatalf("body should be JSON string token: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal([]byte(inner), &got); err != nil {
		t.Fatalf("inner body should be JSON object: %v (raw=%q)", err, inner)
	}
	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("want {a:1,b:2}, got %+v", got)
	}
}

func TestSession_Hooks_Rewrite_BodyHidden_NonJSON(t *testing.T) {
	// Hook tries to read & set body, but Go-side serialization omitted body
	// because content-type was text/plain. The returned PendingRequest.Body
	// reflects whatever the script wrote (SDK stringifies it), but the
	// server layer is responsible for ignoring it via fallbackBody — that
	// behavior is exercised in the gateway, not here. Verify the hook
	// receives no body and that the return roundtrips.
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, pending) {
			globalThis.__sawBody = (typeof pending.body !== 'undefined');
			pending.body = "evil";
			return pending;
		});
	`})
	_, err := s.RunRewriteHook(RewriteInput{
		PendingRequest: PendingRequestShape{
			URL:     "https://x",
			Method:  "POST",
			Headers: map[string][]string{"content-type": {"text/plain"}},
		},
	})
	if err != nil {
		t.Fatalf("RunRewriteHook: %v", err)
	}
	v, err := s.evalWithTimeout("probe.js", "globalThis.__sawBody")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer v.Free()
	if v.Bool() {
		t.Errorf("hook should not see body when content-type is non-JSON")
	}
}

func TestSession_Hooks_BeforeTransform_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeTransform.tap("a", function () {});
	`})
	initial := OutboundProfile{Type: "openai", Config: map[string]any{}}
	out, err := s.RunBeforeTransformHook(BeforeTransformInput{}, initial)
	if err != nil {
		t.Fatalf("RunBeforeTransformHook: %v", err)
	}
	if out.Type != "openai" || len(out.Config) != 0 {
		t.Errorf("want passthrough initial profile, got %+v", out)
	}
}

func TestSession_Hooks_BeforeTransform_ReturnsNewProfile(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeTransform.tap("a", function () {
			return { type: "openrouter", config: { provider: "x" } };
		});
	`})
	out, err := s.RunBeforeTransformHook(BeforeTransformInput{}, OutboundProfile{Type: "openai", Config: map[string]any{"keep": true}})
	if err != nil {
		t.Fatalf("RunBeforeTransformHook: %v", err)
	}
	if out.Type != "openrouter" || out.Config["provider"] != "x" {
		t.Errorf("want openrouter profile, got %+v", out)
	}
	if _, ok := out.Config["keep"]; ok {
		t.Errorf("new profile should replace config, got %+v", out.Config)
	}
}

func TestSession_Hooks_BeforeTransform_MutatesInPlace(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeTransform.tap("a", function (ctx, outbound) {
			outbound.type = "deepseek";
			outbound.config = { include_reasoning: true };
			return outbound;
		});
	`})
	out, err := s.RunBeforeTransformHook(BeforeTransformInput{}, OutboundProfile{Type: "openai", Config: map[string]any{}})
	if err != nil {
		t.Fatalf("RunBeforeTransformHook: %v", err)
	}
	if out.Type != "deepseek" || out.Config["include_reasoning"] != true {
		t.Errorf("want mutated deepseek profile, got %+v", out)
	}
}

func TestSession_Hooks_BeforeTransform_PriorityWaterfall(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeTransform.tap("low", function (ctx, outbound) {
			outbound.type = outbound.type + "-low";
			return outbound;
		}, 1);
		picotera.hooks.beforeTransform.tap("high", function (ctx, outbound) {
			outbound.type = "high";
			return outbound;
		}, 10);
	`})
	out, err := s.RunBeforeTransformHook(BeforeTransformInput{}, OutboundProfile{Type: "openai", Config: map[string]any{}})
	if err != nil {
		t.Fatalf("RunBeforeTransformHook: %v", err)
	}
	if out.Type != "high-low" {
		t.Errorf("want priority waterfall high-low, got %q", out.Type)
	}
}

func TestSession_Hooks_BeforeTransform_TypeMustBeString(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeTransform.tap("a", function () { return { type: 42 }; });
	`})
	_, err := s.RunBeforeTransformHook(BeforeTransformInput{}, OutboundProfile{Type: "openai", Config: map[string]any{}})
	if err == nil || !contains(err.Error(), "jsx: beforeTransform type must be string") {
		t.Fatalf("err = %v, want beforeTransform type error", err)
	}
}

func TestSession_Hooks_BeforeTransform_ConfigMustBeObject(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{name: "number", source: `picotera.hooks.beforeTransform.tap("a", function () { return { config: 42 }; });`},
		{name: "array", source: `picotera.hooks.beforeTransform.tap("a", function () { return { config: [] }; });`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession(t, db.Script{ID: "a", Source: tt.source})
			_, err := s.RunBeforeTransformHook(BeforeTransformInput{}, OutboundProfile{Type: "openai", Config: map[string]any{}})
			if err == nil || !contains(err.Error(), "jsx: beforeTransform config must be object") {
				t.Fatalf("err = %v, want beforeTransform config error", err)
			}
		})
	}
}

func TestSession_Helpers_Console(t *testing.T) {
	// No log capture — verify no panic and the script can call console.* freely.
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx) {
			console.log("hello", "world", { nested: true });
			console.warn("warn");
			console.error("err");
			return ctx;
		});
	`})
	if _, err := s.RunSortHook(SortInput{}); err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
}

func TestSession_Helpers_SetTimeout(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", async function (ctx) {
			await new Promise(function (r) { setTimeout(r, 5); });
			return ctx;
		});
	`})
	start := time.Now()
	if _, err := s.RunSortHook(SortInput{}); err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("setTimeout took too long: %v", time.Since(start))
	}
}

func TestSession_Hooks_RewriteProviderModels_Passthrough(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteProviderModels.tap("a", function () {});
	`})
	in := []ProviderModelEntry{
		{Model: "gpt-4o"},
		{Model: "my-mini", UpstreamModelName: "gpt-4o-mini"},
	}
	out, err := s.RunRewriteProviderModelsHook(RewriteProviderModelsInput{}, in)
	if err != nil {
		t.Fatalf("RunRewriteProviderModelsHook: %v", err)
	}
	if len(out) != 2 || out[0].Model != "gpt-4o" || out[1].Model != "my-mini" {
		t.Errorf("want passthrough preserves input, got %+v", out)
	}
}

func TestSession_Hooks_RewriteProviderModels_Replace(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteProviderModels.tap("a", function (ctx, models) {
			return [{model: 'a'}, {model: 'b', upstreamModelName: 'B', priority: 7, annotations: {x: 'y'}}];
		});
	`})
	out, err := s.RunRewriteProviderModelsHook(RewriteProviderModelsInput{}, nil)
	if err != nil {
		t.Fatalf("RunRewriteProviderModelsHook: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out))
	}
	if out[0].Model != "a" {
		t.Errorf("want first model=a, got %q", out[0].Model)
	}
	if out[1].Model != "b" || out[1].UpstreamModelName != "B" || out[1].Priority != 7 {
		t.Errorf("want second entry b/B/7, got %+v", out[1])
	}
	if out[1].Annotations["x"] != "y" {
		t.Errorf("want annotations x=y, got %v", out[1].Annotations)
	}
}

func TestSession_Hooks_RewriteProviderModels_NonArray(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteProviderModels.tap("a", function (ctx, models) {
			return 42;
		});
	`})
	in := []ProviderModelEntry{{Model: "keep-me"}}
	out, err := s.RunRewriteProviderModelsHook(RewriteProviderModelsInput{}, in)
	if err != nil {
		t.Fatalf("RunRewriteProviderModelsHook: %v", err)
	}
	if len(out) != 1 || out[0].Model != "keep-me" {
		t.Errorf("want fallback to input, got %+v", out)
	}
}

func TestSession_Hooks_RewriteProviderModels_FieldTypeMismatch(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteProviderModels.tap("a", function (ctx, models) {
			return [{model: 'a', priority: 'bad'}];
		});
	`})
	in := []ProviderModelEntry{{Model: "fallback"}}
	out, err := s.RunRewriteProviderModelsHook(RewriteProviderModelsInput{}, in)
	if err != nil {
		t.Fatalf("RunRewriteProviderModelsHook: %v", err)
	}
	if len(out) != 1 || out[0].Model != "fallback" {
		t.Errorf("want fallback to input on type mismatch, got %+v", out)
	}
}

func TestSession_KVSandbox_ScriptsIsolated(t *testing.T) {
	// Two scripts both call picotera.kv with the same key. Without sandboxing
	// they would clobber each other. With the per-script prefix wrap, each
	// script's view of "shared" is independent and admin/session/etc. keys
	// remain unreachable.
	store := &fakeStore{scripts: []db.Script{
		{ID: "alpha", Source: `globalThis.alphaWrite = function(){ picotera.kv.set("shared", "from-alpha"); }; globalThis.alphaRead = function(){ return picotera.kv.get("shared"); };`},
		{ID: "beta", Source: `globalThis.betaWrite = function(){ picotera.kv.set("shared", "from-beta"); }; globalThis.betaRead = function(){ return picotera.kv.get("shared"); };`},
	}}
	kvStore := kv.NewMemoryStore()
	eng := NewEngine(Config{HookTimeout: time.Second, MemoryLimit: 64 * 1024 * 1024}, store, kvStore)
	s, err := eng.NewSession(context.Background(), "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if _, err := s.Context().Eval("write.js", qjs.Code(`alphaWrite(); betaWrite();`)); err != nil {
		t.Fatalf("write eval: %v", err)
	}
	a, err := s.Context().Eval("readA.js", qjs.Code(`alphaRead()`))
	if err != nil {
		t.Fatalf("readA eval: %v", err)
	}
	defer a.Free()
	b, err := s.Context().Eval("readB.js", qjs.Code(`betaRead()`))
	if err != nil {
		t.Fatalf("readB eval: %v", err)
	}
	defer b.Free()
	if got := a.String(); got != "from-alpha" {
		t.Errorf("alpha read: want %q, got %q", "from-alpha", got)
	}
	if got := b.String(); got != "from-beta" {
		t.Errorf("beta read: want %q, got %q", "from-beta", got)
	}

	// Direct inspection: the underlying KV must have BOTH prefixed entries
	// and zero raw "shared" entry. A bug that fails to prefix would leave
	// "shared" populated with one of the values.
	if v, err := kvStore.Get(context.Background(), "shared"); err == nil {
		t.Errorf("unprefixed key leaked into kv store: %q", v)
	}
	v1, err := kvStore.Get(context.Background(), "script:alpha:shared")
	if err != nil {
		t.Fatalf("expected script:alpha:shared in store, got err: %v", err)
	}
	if v1 != `"from-alpha"` {
		t.Errorf("script:alpha:shared = %q, want %q", v1, `"from-alpha"`)
	}
	v2, err := kvStore.Get(context.Background(), "script:beta:shared")
	if err != nil {
		t.Fatalf("expected script:beta:shared in store, got err: %v", err)
	}
	if v2 != `"from-beta"` {
		t.Errorf("script:beta:shared = %q, want %q", v2, `"from-beta"`)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func errIs(err, target error) bool {
	for e := err; e != nil; {
		if e == target {
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
