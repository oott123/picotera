package jsx

import "testing"

// TestSession_PatchContext_LargeBody_NoScripts reproduces the production OOM
// seen on the k3s deploy with ZERO scripts:
//
//	jsx: patch context: TypeError: callback __picotera_obj_get: InternalError: out of memory
//	    at __picotera_obj_get (native)
//	    at getOwnPropertyDescriptor (internal:sdk.js:269)
//
// ctx.request.body is a lazy Proxy that must never cross into QuickJS unless a
// hook actually reads it. There are no hooks here, yet the body got materialized:
// PatchContext evaluated `Object.assign(globalThis.ctx, …)` with the marshaling
// EvalFile, whose completion value is globalThis.ctx, so EvalFile JSON-stringified
// the whole ctx — walking the enumerable ctx.request.body getter and pulling the
// multi-MiB base64 image scalar into QuickJS, blowing the 64 MiB JS memory limit.
//
// The sequence below mirrors the real gateway flow (pkg/server/gateway_flow.go
// resolveAndRewriteModel → resolveAndSortCandidates, then the attempt loop in
// gateway_flow_attempts.go). Each PatchContext that carries no Request ends in a
// bare Object.assign and is the failure point; mustPatch reports which one blows
// up first so the pre-fix run pinpoints it.
func TestSession_PatchContext_LargeBody_NoScripts(t *testing.T) {
	// 30 MiB multimodal body (base64 image + CJK text) under the production
	// default 64 MiB JS memory limit — solidly over the round-trip threshold.
	body := mockMessagesBody(30 * 1024 * 1024)
	s := newTestSession(t) // no scripts

	model := "claude-opus-4-6"
	anno := map[string]string{}
	endpointType := "gateway"
	ep := EndpointSummary{Name: "anthropicMessages", Path: "/v1/messages"}
	clientReq := RequestShape{
		Path:    "/v1/messages",
		Method:  "POST",
		Headers: map[string][]string{"content-type": {"application/json"}},
		Model:   model,
	}
	stream := false
	src := "anthropicMessages"
	routed := ModelSummary{Name: model, Annotations: anno}

	mustPatch := func(label string, p ContextPatch) {
		t.Helper()
		if err := s.PatchContext(p); err != nil {
			t.Fatalf("PatchContext %s failed — the body leaked into QuickJS with no script reading it: %v", label, err)
		}
	}

	// #1 gateway_flow.go:358 — Request present, but SetClientBody has not run yet,
	// so no body getter exists; the implicit stringify of ctx is harmless here.
	mustPatch("#1 {Request,…} pre-SetClientBody", ContextPatch{
		EndpointType: &endpointType, Endpoint: &ep, RequestModel: &model,
		Request: &clientReq, Annotations: &anno, Stream: &stream, SourceFormat: &src,
	})
	if err := s.SetClientBody(body); err != nil {
		t.Fatalf("SetClientBody #1: %v", err)
	}
	if _, err := s.RunRewriteModel(model); err != nil {
		t.Fatalf("RunRewriteModel: %v", err)
	}

	// #2 gateway_flow.go:405 — Request present and body getter installed, so the
	// expr ends in installRequestBody() (returns undefined) and EvalFile skips the
	// ctx stringify. This one was already accidentally safe.
	mustPatch("#2 {RoutedModel,Request,Annotations}", ContextPatch{
		RoutedModel: &routed, Request: &clientReq, Annotations: &anno,
	})
	if err := s.SetClientBody(body); err != nil {
		t.Fatalf("SetClientBody #2: %v", err)
	}

	// #3 gateway_flow.go:440 — NO Request → expr ends in Object.assign, whose value
	// is globalThis.ctx. Static analysis says THIS is the first trigger (it runs
	// before any upstream attempt row); the pre-fix run confirms it empirically.
	mustPatch("#3 {RoutedModel,Annotations} (resolveAndSortCandidates, no Request)", ContextPatch{
		RoutedModel: &routed, Annotations: &anno,
	})

	// attempt-level {Format} patch — gateway_flow_attempts.go:287, after the
	// upstream attempt row; the one the live repro pinned.
	format := "anthropicMessages"
	mustPatch("{Format} (attempt-level)", ContextPatch{Format: &format})

	// attempt-level {Attempt} patch — gateway_flow_attempts.go:380/446.
	mustPatch("{Attempt} (attempt-level)", ContextPatch{
		Attempt: &AttemptState{CurrentRetryCount: 0, TotalAttemptCount: 1},
	})
}
