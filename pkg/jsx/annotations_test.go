package jsx

import (
	"reflect"
	"strings"
	"testing"

	"picotera/pkg/db"
)

func strptr(s string) *string { return &s }

// runHookScript loads source as a single script, runs it through rewriteModel and
// returns the recorded host calls. The hook body is the only thing under test, so
// every case funnels through the same waterfall.
func runHookScript(t *testing.T, host *fakeHostAPI, body string) error {
	t.Helper()
	s := newTestSessionWithHost(t, host, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx, m) { ` + body + ` return m; });
	`})
	_, err := s.RunRewriteModel("m")
	return err
}

func TestSetRequestAnnotation_WriteDeleteAndPassthrough(t *testing.T) {
	cases := []struct {
		name string
		body string
		want hostAnnoCall
	}{
		{
			name: "string",
			body: `picotera.request.setAnnotation('r1', 'agent', 'claude-code');`,
			want: hostAnnoCall{Kind: "request", RequestID: "r1", Key: "agent", Value: strptr("claude-code")},
		},
		{
			name: "emptyString",
			body: `picotera.request.setAnnotation('r1', 'agent', '');`,
			want: hostAnnoCall{Kind: "request", RequestID: "r1", Key: "agent", Value: strptr("")},
		},
		{
			name: "nullDeletes",
			body: `picotera.request.setAnnotation('r1', 'agent', null);`,
			want: hostAnnoCall{Kind: "request", RequestID: "r1", Key: "agent", Value: nil},
		},
		{
			name: "undefinedDeletes",
			body: `picotera.request.setAnnotation('r1', 'agent', undefined);`,
			want: hostAnnoCall{Kind: "request", RequestID: "r1", Key: "agent", Value: nil},
		},
		{
			name: "missingArgDeletes",
			body: `picotera.request.setAnnotation('r1', 'agent');`,
			want: hostAnnoCall{Kind: "request", RequestID: "r1", Key: "agent", Value: nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &fakeHostAPI{}
			if err := runHookScript(t, host, tc.body); err != nil {
				t.Fatalf("RunRewriteModel: %v", err)
			}
			if !reflect.DeepEqual(host.annoCalls, []hostAnnoCall{tc.want}) {
				t.Fatalf("calls = %+v, want %+v", host.annoCalls, tc.want)
			}
		})
	}
}

func TestSetAnnotation_ProviderAndApiKeyPassthrough(t *testing.T) {
	host := &fakeHostAPI{}
	err := runHookScript(t, host, `
		picotera.provider.setAnnotation(7, 'tier', 'gold');
		picotera.provider.setAnnotation(7, 'stale', null);
		picotera.apiKey.setAnnotation(3, 'team', 'infra');
		picotera.apiKey.setAnnotation(3, 'old', undefined);
	`)
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	want := []hostAnnoCall{
		{Kind: "provider", ID: 7, Key: "tier", Value: strptr("gold")},
		{Kind: "provider", ID: 7, Key: "stale", Value: nil},
		{Kind: "apiKey", ID: 3, Key: "team", Value: strptr("infra")},
		{Kind: "apiKey", ID: 3, Key: "old", Value: nil},
	}
	if !reflect.DeepEqual(host.annoCalls, want) {
		t.Fatalf("calls = %+v, want %+v", host.annoCalls, want)
	}
}

func TestSetAnnotation_ValidationThrows(t *testing.T) {
	cases := map[string]string{
		"numberValue":        `picotera.request.setAnnotation('r1', 'k', 123);`,
		"objectValue":        `picotera.request.setAnnotation('r1', 'k', {a: 1});`,
		"booleanValue":       `picotera.request.setAnnotation('r1', 'k', true);`,
		"emptyKey":           `picotera.request.setAnnotation('r1', '', 'v');`,
		"nonStringKey":       `picotera.request.setAnnotation('r1', 5, 'v');`,
		"emptyRequestId":     `picotera.request.setAnnotation('', 'k', 'v');`,
		"numericRequestId":   `picotera.request.setAnnotation(1, 'k', 'v');`,
		"floatProviderId":    `picotera.provider.setAnnotation(1.5, 'k', 'v');`,
		"stringProviderId":   `picotera.provider.setAnnotation('7', 'k', 'v');`,
		"floatApiKeyId":      `picotera.apiKey.setAnnotation(1.5, 'k', 'v');`,
		"getFloatProviderId": `picotera.provider.get(1.5);`,
		"getStringApiKeyId":  `picotera.apiKey.get('3');`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			host := &fakeHostAPI{}
			err := runHookScript(t, host, body)
			if err == nil {
				t.Fatalf("want error for %s, got nil", name)
			}
			if !strings.Contains(err.Error(), "TypeError") {
				t.Fatalf("want TypeError for %s, got %v", name, err)
			}
			if len(host.annoCalls) != 0 || len(host.providerIDs) != 0 || len(host.apiKeyIDs) != 0 {
				t.Fatalf("host must not be reached on a validation failure, got %+v", host)
			}
		})
	}
}

func TestSetAnnotation_HostErrorBecomesJSError(t *testing.T) {
	host := &fakeHostAPI{setErr: errNotFound}
	err := runHookScript(t, host, `picotera.request.setAnnotation('missing', 'k', 'v');`)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "request \"missing\" not found") {
		t.Fatalf("host error should surface in the JS exception, got %v", err)
	}
}

// errNotFound stands in for the server-side "row not found" error the real
// HostAPI returns when a script annotates a nonexistent id.
var errNotFound = errStr("request \"missing\" not found")

type errStr string

func (e errStr) Error() string { return string(e) }

func TestGetProviderAndApiKey_HitAndMiss(t *testing.T) {
	host := &fakeHostAPI{
		provider: &ProviderSummary{ID: 1, Name: "openai", Priority: 10, Annotations: map[string]string{"tier": "gold"}},
		apiKey:   &ApiKeySummary{ID: 3, Name: "team-a", Annotations: map[string]string{}, Disabled: true},
	}
	s := newTestSessionWithHost(t, host, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx, m) {
			var p = picotera.provider.get(1);
			var k = picotera.apiKey.get(3);
			return [p.id, p.name, p.priority, p.annotations.tier, p.disabled, k.id, k.name, k.disabled].join('|');
		});
	`})
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if want := "1|openai|10|gold|false|3|team-a|true"; out != want {
		t.Fatalf("summaries = %q, want %q", out, want)
	}
	if !reflect.DeepEqual(host.providerIDs, []int32{1}) || !reflect.DeepEqual(host.apiKeyIDs, []int32{3}) {
		t.Fatalf("ids = %v / %v", host.providerIDs, host.apiKeyIDs)
	}

	// A missing id (nil summary, nil error) reads as null, matching picotera.kv.get.
	missHost := &fakeHostAPI{}
	missSession := newTestSessionWithHost(t, missHost, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx, m) {
			return String(picotera.provider.get(99)) + '|' + String(picotera.apiKey.get(98));
		});
	`})
	out, err = missSession.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel (miss): %v", err)
	}
	if out != "null|null" {
		t.Fatalf("miss = %q, want null|null", out)
	}
}

func TestGetProvider_HostErrorBecomesJSError(t *testing.T) {
	host := &fakeHostAPI{getErr: errStr("connection refused")}
	err := runHookScript(t, host, `picotera.provider.get(1);`)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("want host error in JS exception, got %v", err)
	}
}

// readRefs is a script that reports the JSON of both request refs, so the tests
// below can assert the exact shape (including nulls) the scripts observe.
const readRefsScript = `
	picotera.hooks.rewriteModel.tap("a", function (ctx, m) {
		return JSON.stringify(ctx.metaRequest) + '#' + JSON.stringify(ctx.upstreamRequest);
	});
`

func TestRequestRefs_ZeroStateIsNull(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: readRefsScript})
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "null#null" {
		t.Fatalf("zero state = %q, want null#null", out)
	}
}

func TestRequestRefs_MetaPatchAndUpstreamLifecycle(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: readRefsScript})
	parent, trace := "sess-1", "tr-1"
	meta := &RequestRef{ID: "meta-1", SpanID: "meta-1", ParentSpanID: &parent, TraceID: &trace}
	if err := s.PatchContext(ContextPatch{MetaRequest: meta}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	// Before the first attempt, upstreamRequest is still the zero state.
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	wantMeta := `{"id":"meta-1","spanId":"meta-1","parentSpanId":"sess-1","traceId":"tr-1"}`
	if out != wantMeta+"#null" {
		t.Fatalf("after meta patch = %q, want %q", out, wantMeta+"#null")
	}
	// Attempt start: explicit reset to null.
	if err := s.SetUpstreamRequest(nil); err != nil {
		t.Fatalf("SetUpstreamRequest(nil): %v", err)
	}
	if out, err = s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != wantMeta+"#null" {
		t.Fatalf("after reset = %q, want upstream null", out)
	}
	// Upstream row inserted: the full ref becomes visible.
	if err := s.SetUpstreamRequest(&RequestRef{ID: "up-1", SpanID: "meta-1", ParentSpanID: &parent, TraceID: &trace}); err != nil {
		t.Fatalf("SetUpstreamRequest: %v", err)
	}
	if out, err = s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	wantUp := `{"id":"up-1","spanId":"meta-1","parentSpanId":"sess-1","traceId":"tr-1"}`
	if out != wantMeta+"#"+wantUp {
		t.Fatalf("after install = %q, want %q", out, wantMeta+"#"+wantUp)
	}
}

func TestRequestRefs_MissingParentAndTraceAreNull(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: readRefsScript})
	if err := s.PatchContext(ContextPatch{MetaRequest: &RequestRef{ID: "meta-1", SpanID: "meta-1"}}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	want := `{"id":"meta-1","spanId":"meta-1","parentSpanId":null,"traceId":null}#null`
	if out != want {
		t.Fatalf("refs = %q, want %q", out, want)
	}
}

func TestRequestRefs_SurviveAttemptPatch(t *testing.T) {
	// An Attempt patch (Object.assign) must not clobber the installed refs.
	s := newTestSession(t, db.Script{ID: "a", Source: readRefsScript})
	if err := s.PatchContext(ContextPatch{MetaRequest: &RequestRef{ID: "meta-1", SpanID: "meta-1"}}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	if err := s.SetUpstreamRequest(&RequestRef{ID: "up-1", SpanID: "meta-1"}); err != nil {
		t.Fatalf("SetUpstreamRequest: %v", err)
	}
	if err := s.PatchContext(ContextPatch{Attempt: &AttemptState{CurrentRetryCount: 1}}); err != nil {
		t.Fatalf("PatchContext(attempt): %v", err)
	}
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if !strings.Contains(out, `"id":"meta-1"`) || !strings.Contains(out, `"id":"up-1"`) {
		t.Fatalf("refs lost across PatchContext, got %q", out)
	}
}

func TestRunRequestFinished_TapReadsEveryField(t *testing.T) {
	host := &fakeHostAPI{}
	s := newTestSessionWithHost(t, host, db.Script{ID: "a", Source: `
		picotera.hooks.requestFinished.tap("usage", function (ctx, info) {
			picotera.request.setAnnotation(info.requestId, 'snapshot', JSON.stringify(info));
		});
	`})
	input := RequestFinishedView{
		RequestID:          "meta-1",
		StatusCode:         200,
		FinishReason:       3,
		ErrorMessage:       "",
		TimeSpentMs:        1200,
		TtftMs:             300,
		InputTokens:        11,
		OutputTokens:       22,
		CacheReadTokens:    33,
		CacheWriteTokens:   44,
		CacheWrite1hTokens: 55,
		ModelCost:          0.125,
		ModelCostCurrency:  "USD",
		ProviderID:         7,
		Model:              "sonnet",
		UpstreamModel:      "claude-sonnet",
	}
	if err := s.RunRequestFinished(input); err != nil {
		t.Fatalf("RunRequestFinished: %v", err)
	}
	if len(host.annoCalls) != 1 {
		t.Fatalf("calls = %+v, want one", host.annoCalls)
	}
	call := host.annoCalls[0]
	if call.RequestID != "meta-1" || call.Key != "snapshot" || call.Value == nil {
		t.Fatalf("call = %+v", call)
	}
	want := `{"requestId":"meta-1","statusCode":200,"finishReason":3,"errorMessage":"",` +
		`"timeSpentMs":1200,"ttftMs":300,"inputTokens":11,"outputTokens":22,` +
		`"cacheReadTokens":33,"cacheWriteTokens":44,"cacheWrite1hTokens":55,` +
		`"modelCost":0.125,"modelCostCurrency":"USD","providerId":7,` +
		`"model":"sonnet","upstreamModel":"claude-sonnet"}`
	if *call.Value != want {
		t.Fatalf("info =\n%s\nwant\n%s", *call.Value, want)
	}
}

func TestRunRequestFinished_NoTapIsNoop(t *testing.T) {
	host := &fakeHostAPI{}
	s := newTestSessionWithHost(t, host)
	if err := s.RunRequestFinished(RequestFinishedView{RequestID: "meta-1"}); err != nil {
		t.Fatalf("RunRequestFinished: %v", err)
	}
	if len(host.annoCalls) != 0 {
		t.Fatalf("calls = %+v, want none", host.annoCalls)
	}
}

func TestRunRequestFinished_TaintedSessionFastFails(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("spin", function () { for (;;) {} });
	`})
	if _, err := s.RunSortProviders(nil); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout from the spinning hook, got %v", err)
	}
	if err := s.RunRequestFinished(RequestFinishedView{RequestID: "meta-1"}); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout on a tainted session, got %v", err)
	}
}

func TestSetUpstreamRequest_TaintedSessionFastFails(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("spin", function () { for (;;) {} });
	`})
	if _, err := s.RunSortProviders(nil); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout from the spinning hook, got %v", err)
	}
	if err := s.SetUpstreamRequest(nil); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout on a tainted session, got %v", err)
	}
}
