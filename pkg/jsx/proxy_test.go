package jsx

import (
	"encoding/json"
	"testing"

	"picotera/pkg/db"
)

// rrPending is the canonical JSON-content-type pending shape used by the body
// Proxy tests.
func rrPending() PendingRequestShape {
	return PendingRequestShape{
		URL:     "https://upstream/v1/messages",
		Method:  "POST",
		Headers: map[string][]string{"content-type": {"application/json"}},
	}
}

// runRR runs a single rewriteRequest hook over body and returns the result. A
// hook that throws (e.g. on a failed in-script assertion) surfaces as the error.
func runRR(t *testing.T, body, source string) (PendingRequestShape, error) {
	t.Helper()
	s := newTestSession(t, db.Script{ID: "a", Source: source})
	return s.RunRewriteRequest(rrPending(), []byte(body))
}

func TestProxy_Read(t *testing.T) {
	// Reads, enumeration, spread, Array.isArray, array methods, and Proxy
	// identity caching all work; the hook throws on any mismatch.
	_, err := runRR(t, `{"model":"m","nums":[1,2,3],"obj":{"k":"v"}}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			var b = p.body;
			if (b.model !== "m") throw new Error("scalar read");
			if (b.missing !== undefined) throw new Error("missing key should be undefined");
			if (!Array.isArray(b.nums)) throw new Error("Array.isArray");
			if (b.nums.length !== 3) throw new Error("length");
			if (b.nums[1] !== 2) throw new Error("index read");
			var doubled = b.nums.map(function (n) { return n * 2; });
			if (doubled.join(",") !== "2,4,6") throw new Error("map: " + doubled.join(","));
			var keys = Object.keys(b).sort().join(",");
			if (keys !== "model,nums,obj") throw new Error("keys: " + keys);
			if (!("model" in b)) throw new Error("in operator");
			var spread = Object.assign({}, b);
			if (spread.model !== "m") throw new Error("spread");
			if (b.obj !== b.obj) throw new Error("proxy identity not cached");
			if (b.obj.k !== "v") throw new Error("nested read");
			return undefined;
		});
	`)
	if err != nil {
		t.Fatalf("read hook failed: %v", err)
	}
}

func TestProxy_WriteObjectAndArray(t *testing.T) {
	out, err := runRR(t, `{"model":"old","messages":[{"role":"a"}],"drop":1}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			p.body.model = "new";
			p.body.messages.push({ role: "b" });
			delete p.body.drop;
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("write hook failed: %v", err)
	}
	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Drop *int `json:"drop"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode body: %v (raw=%s)", err, out.Body)
	}
	if got.Model != "new" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Messages) != 2 || got.Messages[1].Role != "b" {
		t.Errorf("messages = %+v", got.Messages)
	}
	if got.Drop != nil {
		t.Errorf("drop should be deleted, got %v", *got.Drop)
	}
}

func TestProxy_ArraySplice(t *testing.T) {
	// Native splice deletes the tail then shrinks length; the array Proxy must
	// support that even though arbitrary delete arr[i] is rejected.
	out, err := runRR(t, `{"messages":[{"i":0},{"i":1},{"i":2}]}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			p.body.messages.splice(1, 1);
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("splice hook failed: %v", err)
	}
	var got struct {
		Messages []struct {
			I int `json:"i"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	if len(got.Messages) != 2 || got.Messages[0].I != 0 || got.Messages[1].I != 2 {
		t.Errorf("splice result = %+v", got.Messages)
	}
}

func TestProxy_ArrayMutators(t *testing.T) {
	// shift/unshift/pop/splice-with-insert/reverse all run through the host
	// slice ops; the hook asserts return values and final order in-script.
	out, err := runRR(t, `{"a":[{"i":0},{"i":1},{"i":2},{"i":3}]}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			var a = p.body.a;
			var first = a.shift();
			if (first.i !== 0) throw new Error("shift returned " + first.i);
			if (a.length !== 3) throw new Error("len after shift " + a.length);
			var n = a.unshift({ i: -1 });
			if (n !== 4) throw new Error("unshift returned " + n);
			if (a[0].i !== -1) throw new Error("unshift order " + a[0].i);
			var last = a.pop();
			if (last.i !== 3) throw new Error("pop returned " + last.i);
			var removed = a.splice(1, 1, { i: 9 }, { i: 10 });
			if (removed.length !== 1 || removed[0].i !== 1) throw new Error("splice removed " + JSON.stringify(removed));
			a.reverse();
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("array mutator hook failed: %v", err)
	}
	var got struct {
		A []struct {
			I int `json:"i"`
		} `json:"a"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	// After shift/unshift/pop/splice the order is [-1, 9, 10, 2]; reverse → [2,10,9,-1].
	want := []int{2, 10, 9, -1}
	if len(got.A) != len(want) {
		t.Fatalf("a = %+v, want %v", got.A, want)
	}
	for i, w := range want {
		if got.A[i].I != w {
			t.Errorf("a[%d].i = %d, want %d (full %+v)", i, got.A[i].I, w, got.A)
		}
	}
}

func TestProxy_PopShiftEmpty(t *testing.T) {
	_, err := runRR(t, `{"a":[]}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			if (p.body.a.pop() !== undefined) throw new Error("pop empty");
			if (p.body.a.shift() !== undefined) throw new Error("shift empty");
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("empty pop/shift hook failed: %v", err)
	}
}

func TestProxy_UnshiftRelocatesNoAlias(t *testing.T) {
	// Relocating an existing object element via unshift must not alias it: a
	// freshly-inserted object can be mutated without touching the relocated one.
	out, err := runRR(t, `{"a":[{"v":"orig"}]}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			p.body.a.unshift({ v: "new" });
			p.body.a[0].v = "mutated";
			if (p.body.a[1].v !== "orig") throw new Error("relocated element aliased: " + p.body.a[1].v);
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	var got struct {
		A []struct {
			V string `json:"v"`
		} `json:"a"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	if len(got.A) != 2 || got.A[0].V != "mutated" || got.A[1].V != "orig" {
		t.Errorf("a = %+v", got.A)
	}
}

func TestProxy_NestedProxyDeepCopiedOnSet(t *testing.T) {
	// Assigning an object that embeds another Proxy deep-copies that subtree;
	// later mutating the source must not affect the copy.
	out, err := runRR(t, `{"src":{"deep":"orig"},"dst":null}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			p.body.dst = { wrapped: p.body.src };
			p.body.src.deep = "mutated";
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	var got struct {
		Src struct {
			Deep string `json:"deep"`
		} `json:"src"`
		Dst struct {
			Wrapped struct {
				Deep string `json:"deep"`
			} `json:"wrapped"`
		} `json:"dst"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	if got.Src.Deep != "mutated" {
		t.Errorf("src.deep = %q", got.Src.Deep)
	}
	if got.Dst.Wrapped.Deep != "orig" {
		t.Errorf("dst.wrapped.deep should be the pre-mutation copy, got %q", got.Dst.Wrapped.Deep)
	}
}

func TestProxy_AssignProxyDeepCopies(t *testing.T) {
	// Assigning one managed Proxy to another slot (body.a = body.b) deep-copies
	// it rather than aliasing; mutating the source afterwards must not change the
	// copy. This is also what lets native splice/shift relocate object elements.
	out, err := runRR(t, `{"a":null,"b":{"deep":"orig"}}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			p.body.a = p.body.b;
			p.body.b.deep = "mutated";
			return p;
		});
	`)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	var got struct {
		A struct {
			Deep string `json:"deep"`
		} `json:"a"`
		B struct {
			Deep string `json:"deep"`
		} `json:"b"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	if got.A.Deep != "orig" {
		t.Errorf("a should be a pre-mutation deep copy, got %q", got.A.Deep)
	}
	if got.B.Deep != "mutated" {
		t.Errorf("b.deep = %q", got.B.Deep)
	}
}

func TestProxy_WriteErrors(t *testing.T) {
	cases := []struct{ name, source string }{
		{"assign undefined", `picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { p.body.a = undefined; return p; });`},
		{"array OOB write", `picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { p.body.arr[9] = 1; return p; });`},
		{"delete non-last element", `picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { delete p.body.arr[0]; return p; });`},
		{"grow length", `picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { p.body.arr.length = 99; return p; });`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := runRR(t, `{"a":1,"b":{"x":1},"arr":[1,2,3]}`, c.source)
			if err == nil {
				t.Fatalf("expected error for %q", c.name)
			}
		})
	}
}

func TestProxy_RewriteRequest_Untouched(t *testing.T) {
	out, err := runRR(t, `{"model":"m"}`, `picotera.hooks.rewriteRequest.tap("a", function () {});`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Body != nil {
		t.Errorf("untouched body should yield nil Body, got %s", out.Body)
	}
}

func TestProxy_RewriteRequest_ReadOnlyCleanPassthrough(t *testing.T) {
	// Reading without mutating leaves the tree clean → byte-identical fallback.
	out, err := runRR(t, `{"model":"m"}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { var _ = p.body.model; return p; });
	`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Body != nil {
		t.Errorf("clean read should yield nil Body (fallback), got %s", out.Body)
	}
}

func TestProxy_RewriteRequest_UntouchedFieldsKeepBytes(t *testing.T) {
	// A mutation re-encodes, but untouched scalars keep their exact bytes (escape
	// form preserved by jsonast).
	out, err := runRR(t, `{"a":"\/keep","b":1}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { p.body.b = 2; return p; });
	`)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Body) != `{"a":"\/keep","b":2}` {
		t.Errorf("got %s", out.Body)
	}
}

func TestProxy_RewriteRequest_ReturnNewObjectWithProxy(t *testing.T) {
	out, err := runRR(t, `{"model":"m"}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			p.body.added = true;
			return { url: p.url, method: p.method, headers: p.headers, body: p.body };
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	if got["model"] != "m" || got["added"] != true {
		t.Errorf("got %+v", got)
	}
}

func TestProxy_RewriteRequest_RawString(t *testing.T) {
	out, err := runRR(t, `{"model":"m"}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { p.body = "literal-bytes"; return p; });
	`)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Body) != "literal-bytes" {
		t.Errorf("raw string body = %q", out.Body)
	}
}

func TestProxy_RewriteRequest_NullBodyFallsBack(t *testing.T) {
	out, err := runRR(t, `{"model":"m"}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) { p.body = null; return p; });
	`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Body != nil {
		t.Errorf("null body should fall back (nil Body), got %s", out.Body)
	}
}

func TestProxy_RewriteRequest_DeepCopyMaterializes(t *testing.T) {
	// JSON.parse(JSON.stringify(body)) is the escape hatch for a freely-mutable
	// plain object; assigning it back replaces the body.
	out, err := runRR(t, `{"model":"m","nested":{"x":1}}`, `
		picotera.hooks.rewriteRequest.tap("a", function (ctx, p) {
			var copy = JSON.parse(JSON.stringify(p.body));
			copy.nested.x = 99;
			copy.fresh = "yes";
			p.body = copy;
			return p;
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model  string `json:"model"`
		Nested struct {
			X int `json:"x"`
		} `json:"nested"`
		Fresh string `json:"fresh"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, out.Body)
	}
	if got.Model != "m" || got.Nested.X != 99 || got.Fresh != "yes" {
		t.Errorf("got %+v", got)
	}
}

func TestProxy_ClientBody_ReadWriteInRewriteModel(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx) {
			if (ctx.request.body.model !== "claude") throw new Error("client body read");
			ctx.request.body.touched = true;
			return ctx.request.body.touched ? "rewritten" : "no";
		});
	`})
	rm := "claude"
	if err := s.PatchContext(ContextPatch{
		RequestModel: &rm,
		Request:      &RequestShape{Path: "/x", Method: "POST", Model: "claude"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClientBody([]byte(`{"model":"claude"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := s.RunRewriteModel("claude")
	if err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if out != "rewritten" {
		t.Errorf("got %q", out)
	}
}

func TestProxy_ClientBody_AbsentWhenNoBody(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx) {
			return (typeof ctx.request.body === "undefined") ? "absent" : "present";
		});
	`})
	if err := s.PatchContext(ContextPatch{Request: &RequestShape{Path: "/x", Method: "POST"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClientBody(nil); err != nil {
		t.Fatal(err)
	}
	out, err := s.RunRewriteModel("m")
	if err != nil {
		t.Fatal(err)
	}
	if out != "absent" {
		t.Errorf("body should be absent for nil client body, got %q", out)
	}
}

func TestProxy_StaleAfterClientBodyReplaced(t *testing.T) {
	// A Proxy stashed across a SetClientBody (e.g. model rewrite changed the
	// body) must fail fast on later access.
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx) { globalThis.saved = ctx.request.body; return "m"; });
		picotera.hooks.sortProviders.tap("a", function (ctx, list) { var _ = globalThis.saved.model; return list; });
	`})
	if err := s.PatchContext(ContextPatch{Request: &RequestShape{Path: "/x", Method: "POST"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClientBody([]byte(`{"model":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunRewriteModel("a"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	// Replace the body tree → the saved Proxy's id is invalidated.
	if err := s.SetClientBody([]byte(`{"model":"b"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunSortProviders([]CandidateView{{Provider: ProviderSummary{ID: 1}}}); err == nil {
		t.Fatal("expected stale-proxy error after body replacement")
	}
}
