package jsx

import (
	"strings"
	"testing"

	"picotera/pkg/db"
)

// bmrScript wraps a beforeMetaRequest tap body into a loadable script.
func bmrScript(body string) db.Script {
	return db.Script{ID: "a", Source: `
		picotera.hooks.beforeMetaRequest.tap("a", function (ctx, input) { ` + body + ` });
	`}
}

func TestBeforeMetaRequest_PassthroughWithoutTap(t *testing.T) {
	s := newTestSession(t)
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if resp != nil {
		t.Errorf("want nil passthrough, got %+v", resp)
	}
}

func TestBeforeMetaRequest_PassthroughValues(t *testing.T) {
	for _, body := range []string{`return;`, `return undefined;`, `return null;`} {
		s := newTestSession(t, bmrScript(body))
		resp, err := s.RunBeforeMetaRequest()
		if err != nil {
			t.Fatalf("%s: RunBeforeMetaRequest: %v", body, err)
		}
		if resp != nil {
			t.Errorf("%s: want nil passthrough, got %+v", body, resp)
		}
	}
}

func TestBeforeMetaRequest_ObjectBodyIsStringified(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 200, body: { a: 1 } };`))
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if resp == nil {
		t.Fatal("want a response, got nil")
	}
	if resp.StatusCode != 200 {
		t.Errorf("statusCode = %d, want 200", resp.StatusCode)
	}
	if string(resp.Body) != `{"a":1}` {
		t.Errorf("body = %q, want {\"a\":1}", resp.Body)
	}
	if len(resp.Headers) != 0 {
		t.Errorf("want empty headers, got %v", resp.Headers)
	}
	if resp.Tokens != nil {
		t.Errorf("want nil tokens, got %+v", resp.Tokens)
	}
}

func TestBeforeMetaRequest_StringBodyIsVerbatim(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 200, body: "data: hi\n\n" };`))
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if string(resp.Body) != "data: hi\n\n" {
		t.Errorf("body = %q, want the raw string", resp.Body)
	}
}

func TestBeforeMetaRequest_HeadersNormalized(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 201, headers: { 'X-A': 'b', 'X-B': ['c', 'd'] } };`))
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if got := resp.Headers["X-A"]; len(got) != 1 || got[0] != "b" {
		t.Errorf("X-A = %v, want [b]", got)
	}
	if got := resp.Headers["X-B"]; len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Errorf("X-B = %v, want [c d]", got)
	}
}

func TestBeforeMetaRequest_NoBody(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 204 };`))
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if resp.Body != nil {
		t.Errorf("body = %q, want nil", resp.Body)
	}
}

func TestBeforeMetaRequest_Tokens(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 200, tokens: { outputTokens: 12 } };`))
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if resp.Tokens == nil {
		t.Fatal("want tokens, got nil")
	}
	if resp.Tokens.OutputTokens == nil || *resp.Tokens.OutputTokens != 12 {
		t.Errorf("outputTokens = %v, want 12", resp.Tokens.OutputTokens)
	}
	for name, v := range map[string]*int32{
		"inputTokens":        resp.Tokens.InputTokens,
		"cacheReadTokens":    resp.Tokens.CacheReadTokens,
		"cacheWriteTokens":   resp.Tokens.CacheWriteTokens,
		"cacheWrite1hTokens": resp.Tokens.CacheWrite1hTokens,
	} {
		if v != nil {
			t.Errorf("%s = %d, want nil (unreported)", name, *v)
		}
	}
}

func TestBeforeMetaRequest_ZeroTokenStaysReported(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 200, tokens: { outputTokens: 0 } };`))
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if resp.Tokens == nil || resp.Tokens.OutputTokens == nil || *resp.Tokens.OutputTokens != 0 {
		t.Fatalf("want an explicit 0 outputTokens, got %+v", resp.Tokens)
	}
}

func TestBeforeMetaRequest_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"string", `return "nope";`, "must be an object"},
		{"number", `return 200;`, "must be an object"},
		{"array", `return [1];`, "must be an object"},
		{"missingStatus", `return { body: "x" };`, "statusCode must be an integer"},
		{"statusFloat", `return { statusCode: 200.5 };`, "statusCode must be an integer"},
		{"statusTooLow", `return { statusCode: 99 };`, "statusCode must be an integer"},
		{"statusTooHigh", `return { statusCode: 600 };`, "statusCode must be an integer"},
		{"headersArray", `return { statusCode: 200, headers: ['a'] };`, "headers must be an object"},
		{"headerNumber", `return { statusCode: 200, headers: { 'X-A': 1 } };`, "must be a string or string[]"},
		{"headerArrayOfNumbers", `return { statusCode: 200, headers: { 'X-A': [1] } };`, "must be a string or string[]"},
		{"contentLength", `return { statusCode: 200, headers: { 'content-length': '3' } };`, "not allowed"},
		{"transferEncoding", `return { statusCode: 200, headers: { 'Transfer-Encoding': 'chunked' } };`, "not allowed"},
		{"bodyNumber", `return { statusCode: 200, body: 1 };`, "body must be a string"},
		{"bodyBool", `return { statusCode: 200, body: true };`, "body must be a string"},
		{"bodyFunction", `return { statusCode: 200, body: function () {} };`, "body must be a string"},
		{"tokensArray", `return { statusCode: 200, tokens: [1] };`, "tokens must be an object"},
		{"tokensUnknownKey", `return { statusCode: 200, tokens: { input_tokens: 1 } };`, "unknown tokens key input_tokens"},
		{"tokenNegative", `return { statusCode: 200, tokens: { inputTokens: -1 } };`, "must be an integer in [0, 2147483647]"},
		{"tokenFloat", `return { statusCode: 200, tokens: { inputTokens: 1.5 } };`, "must be an integer in [0, 2147483647]"},
		{"tokenTooBig", `return { statusCode: 200, tokens: { inputTokens: 2147483648 } };`, "must be an integer in [0, 2147483647]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSession(t, bmrScript(tc.body))
			resp, err := s.RunBeforeMetaRequest()
			if err == nil {
				t.Fatalf("want an error, got response %+v", resp)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestBeforeMetaRequest_WaterfallChaining(t *testing.T) {
	s := newTestSession(t,
		db.Script{ID: "high", Source: `
			picotera.hooks.beforeMetaRequest.tap("high", function () {
				return { statusCode: 200, body: { hit: true } };
			}, 10);
		`},
		db.Script{ID: "low", Source: `
			picotera.hooks.beforeMetaRequest.tap("low", function (ctx, input) {
				if (!input) return;
				input.statusCode = 503;
				return input;
			}, 1);
		`},
	)
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("statusCode = %d, want 503 (rewritten by the lower-priority tap)", resp.StatusCode)
	}
	if string(resp.Body) != `{"hit":true}` {
		t.Errorf("body = %q, want the shape from the higher-priority tap", resp.Body)
	}
}

func TestBeforeMetaRequest_BodyProxyIsMaterialized(t *testing.T) {
	s := newTestSession(t, bmrScript(`return { statusCode: 200, body: ctx.request.body };`))
	if err := s.PatchContext(ContextPatch{Request: &RequestShape{Path: "/x", Method: "POST"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClientBody([]byte(`{"model":"claude","messages":[{"role":"user"}]}`)); err != nil {
		t.Fatal(err)
	}
	resp, err := s.RunBeforeMetaRequest()
	if err != nil {
		t.Fatalf("RunBeforeMetaRequest: %v", err)
	}
	want := `{"model":"claude","messages":[{"role":"user"}]}`
	if string(resp.Body) != want {
		t.Errorf("body = %q, want %q", resp.Body, want)
	}
}

func TestBeforeMetaRequest_TaintedSessionFastFails(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `picotera.hooks.sortProviders.tap("a", function () { for(;;){} });`})
	if _, err := s.RunSortProviders(nil); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout from the spinning hook, got %v", err)
	}
	if _, err := s.RunBeforeMetaRequest(); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout on a tainted session, got %v", err)
	}
}
