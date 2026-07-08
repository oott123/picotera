package jsx

import (
	"reflect"
	"testing"

	"picotera/pkg/db"
)

// annoSession is a qjsSession-typed handle so tests can reach the accumulator
// accessors (MetaAnnotations / UpstreamAnnotations / ResetUpstreamAnnotations)
// that are part of the Session interface but read here as concrete methods.
func annoSession(t *testing.T, scripts ...db.Script) *qjsSession {
	t.Helper()
	return newTestSession(t, scripts...).(*qjsSession)
}

func TestAnnotations_MetaWriteOverwriteDelete(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("write", function (ctx, m) {
			ctx.metaRequest.annotations.agent = 'claude-code';
			ctx.metaRequest.annotations.team = 'infra';
			return m;
		});
		picotera.hooks.beforeRequest.tap("mutate", function (ctx, d) {
			ctx.metaRequest.annotations.agent = 'codex';   // overwrite
			delete ctx.metaRequest.annotations.team;        // delete
			return d;
		});
	`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if _, err := s.RunBeforeRequest(BeforeRequestDecision{}); err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	got := s.MetaAnnotations()
	want := map[string]string{"agent": "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MetaAnnotations = %v, want %v", got, want)
	}
}

func TestAnnotations_EmptyReturnsNil(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `picotera.hooks.rewriteModel.tap("a", function (ctx, m) { return m; });`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	if got := s.MetaAnnotations(); got != nil {
		t.Fatalf("MetaAnnotations = %v, want nil", got)
	}
	if got := s.UpstreamAnnotations(); got != nil {
		t.Fatalf("UpstreamAnnotations = %v, want nil", got)
	}
}

func TestAnnotations_ReadBack(t *testing.T) {
	// A value written by one hook is readable (=== undefined semantics) by a later
	// hook, and an empty-string value is distinguishable from a missing key.
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("w", function (ctx, m) {
			ctx.metaRequest.annotations.a = 'x';
			ctx.metaRequest.annotations.empty = '';
			return m;
		});
		picotera.hooks.beforeRequest.tap("r", function (ctx, d) {
			var parts = [
				String(ctx.metaRequest.annotations.a),
				String(ctx.metaRequest.annotations.empty),
				String(ctx.metaRequest.annotations.missing),
				('empty' in ctx.metaRequest.annotations),
				('missing' in ctx.metaRequest.annotations),
			];
			return { upstreamModel: parts.join('|') };
		});
	`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	want := "x||undefined|true|false"
	if dec.UpstreamModel != want {
		t.Fatalf("read-back = %q, want %q", dec.UpstreamModel, want)
	}
}

func TestAnnotations_TypeValidationThrows(t *testing.T) {
	cases := map[string]string{
		"nonStringValue": `ctx.metaRequest.annotations.a = 123;`,
		"emptyKey":       `ctx.metaRequest.annotations[''] = 'x';`,
		"symbolKey":      `ctx.metaRequest.annotations[Symbol('s')] = 'x';`,
		"nullValue":      `ctx.metaRequest.annotations.a = null;`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := annoSession(t, db.Script{ID: "a", Source: `
				picotera.hooks.rewriteModel.tap("a", function (ctx, m) { ` + body + ` return m; });
			`})
			_, err := s.RunRewriteModel("m")
			if err == nil {
				t.Fatalf("want error for %s, got nil", name)
			}
		})
	}
}

func TestAnnotations_ObjectKeysStringifySpread(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("w", function (ctx, m) {
			ctx.metaRequest.annotations.a = '1';
			ctx.metaRequest.annotations.b = '2';
			return m;
		});
		picotera.hooks.beforeRequest.tap("r", function (ctx, d) {
			var keys = Object.keys(ctx.metaRequest.annotations).sort().join(',');
			var json = JSON.stringify(ctx.metaRequest.annotations);
			var spread = Object.assign({}, ctx.metaRequest.annotations);
			return { upstreamModel: keys + '#' + json + '#' + spread.a + spread.b };
		});
	`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	want := `a,b#{"a":"1","b":"2"}#12`
	if dec.UpstreamModel != want {
		t.Fatalf("enumeration = %q, want %q", dec.UpstreamModel, want)
	}
}

func TestAnnotations_UpstreamUndefinedBeforeReset(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("a", function (ctx, m) {
			return { }; // ignore
		});
		picotera.hooks.sortProviders.tap("a", function (ctx, list) {
			ctx.__upstreamDefined = (typeof ctx.upstreamRequest !== 'undefined');
			return list;
		});
		picotera.hooks.beforeRequest.tap("a", function (ctx, d) {
			return { upstreamModel: String(ctx.__upstreamDefined) };
		});
	`})
	if _, err := s.RunSortProviders(nil); err != nil {
		t.Fatalf("RunSortProviders: %v", err)
	}
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if dec.UpstreamModel != "false" {
		t.Fatalf("ctx.upstreamRequest should be undefined before reset, got %q", dec.UpstreamModel)
	}
}

func TestAnnotations_UpstreamResetInstallsAndClears(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function (ctx, d) {
			ctx.upstreamRequest.annotations.route = 'fallback';
			return d;
		});
	`})
	// First attempt: install + write.
	if err := s.ResetUpstreamAnnotations(); err != nil {
		t.Fatalf("ResetUpstreamAnnotations: %v", err)
	}
	if _, err := s.RunBeforeRequest(BeforeRequestDecision{}); err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if got := s.UpstreamAnnotations(); !reflect.DeepEqual(got, map[string]string{"route": "fallback"}) {
		t.Fatalf("after attempt 1 = %v", got)
	}
	// Second attempt: reset clears the accumulator; meta is untouched.
	if err := s.ResetUpstreamAnnotations(); err != nil {
		t.Fatalf("ResetUpstreamAnnotations #2: %v", err)
	}
	if got := s.UpstreamAnnotations(); got != nil {
		t.Fatalf("reset did not clear upstream annotations, got %v", got)
	}
}

func TestAnnotations_MetaAndUpstreamIndependent(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function (ctx, d) {
			ctx.metaRequest.annotations.m = '1';
			ctx.upstreamRequest.annotations.u = '2';
			return d;
		});
	`})
	if err := s.ResetUpstreamAnnotations(); err != nil {
		t.Fatalf("ResetUpstreamAnnotations: %v", err)
	}
	if _, err := s.RunBeforeRequest(BeforeRequestDecision{}); err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if got := s.MetaAnnotations(); !reflect.DeepEqual(got, map[string]string{"m": "1"}) {
		t.Fatalf("meta = %v", got)
	}
	if got := s.UpstreamAnnotations(); !reflect.DeepEqual(got, map[string]string{"u": "2"}) {
		t.Fatalf("upstream = %v", got)
	}
}

func TestAnnotations_PatchContextPreservesProxies(t *testing.T) {
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.rewriteModel.tap("w", function (ctx, m) { ctx.metaRequest.annotations.a = 'x'; return m; });
		picotera.hooks.beforeRequest.tap("r", function (ctx, d) { return { upstreamModel: String(ctx.metaRequest.annotations.a) }; });
	`})
	if _, err := s.RunRewriteModel("m"); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}
	// An Attempt patch (Object.assign) must not clobber ctx.metaRequest.
	if err := s.PatchContext(ContextPatch{Attempt: &AttemptState{CurrentRetryCount: 1}}); err != nil {
		t.Fatalf("PatchContext: %v", err)
	}
	dec, err := s.RunBeforeRequest(BeforeRequestDecision{})
	if err != nil {
		t.Fatalf("RunBeforeRequest: %v", err)
	}
	if dec.UpstreamModel != "x" {
		t.Fatalf("metaRequest proxy lost across PatchContext, got %q", dec.UpstreamModel)
	}
}

func TestAnnotations_SurviveTimeoutTaint(t *testing.T) {
	// A hook writes an annotation, then spins until the timeout taints the session.
	// The already-written annotation must still be retrievable.
	s := annoSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx, list) {
			ctx.metaRequest.annotations.agent = 'claude-code';
			for (;;) {}
		});
	`})
	if _, err := s.RunSortProviders(nil); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout, got %v", err)
	}
	if got := s.MetaAnnotations(); !reflect.DeepEqual(got, map[string]string{"agent": "claude-code"}) {
		t.Fatalf("annotations lost after taint, got %v", got)
	}
}
