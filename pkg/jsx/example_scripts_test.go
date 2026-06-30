package jsx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"picotera/pkg/db"
)

// loadExampleScript returns the source of a script shipped under
// docs/example-scripts/ so the tests exercise the real, committed examples.
func loadExampleScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "example-scripts", name))
	if err != nil {
		t.Fatalf("read example script %s: %v", name, err)
	}
	return string(b)
}

func ptr[T any](v T) *T { return &v }

// TestExampleScripts_Behavior runs the committed example scripts through their
// hooks to guard that the PatchContext/SetClientBody change (EvalValueFile via
// evalVoid instead of the marshaling EvalFile) preserves normal script behavior:
// ctx fields set by PatchContext stay visible, and the lazy ctx.request /
// pending.body Proxy is still readable and mutable when a hook does touch it.
func TestExampleScripts_Behavior(t *testing.T) {
	// rewriteRequest, reads ctx.format + ctx.annotations, mutates pending.body.
	t.Run("convert-developer-to-system", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "convert-developer-to-system.js")})
		if err := s.PatchContext(ContextPatch{
			Format:      ptr("openaiChatCompletions"),
			Annotations: ptr(map[string]string{"rewrite-developer-role": "yes"}),
		}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		body := []byte(`{"model":"gpt-4","messages":[{"role":"developer","content":"a"},{"role":"user","content":"b"}]}`)
		out, err := s.RunRewriteRequest(PendingRequestShape{
			URL: "https://up/v1/chat/completions", Method: "POST",
			Headers: map[string][]string{"content-type": {"application/json"}},
		}, body)
		if err != nil {
			t.Fatalf("RunRewriteRequest: %v", err)
		}
		var got struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(out.Body, &got); err != nil {
			t.Fatalf("decode out body: %v (raw=%q)", err, out.Body)
		}
		if got.Messages[0].Role != "system" {
			t.Errorf("developer not converted: messages[0].role = %q, want system", got.Messages[0].Role)
		}
		if got.Messages[1].Role != "user" {
			t.Errorf("unrelated role mutated: messages[1].role = %q, want user", got.Messages[1].Role)
		}
	})

	// Negative case: same script, missing the gating annotation → body untouched
	// (passthrough → nil Body, caller keeps the original bytes).
	t.Run("convert-developer-to-system/no-annotation-passthrough", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "convert-developer-to-system.js")})
		if err := s.PatchContext(ContextPatch{Format: ptr("openaiChatCompletions")}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		body := []byte(`{"messages":[{"role":"developer","content":"a"}]}`)
		out, err := s.RunRewriteRequest(PendingRequestShape{
			URL: "https://up/v1/chat/completions", Method: "POST",
			Headers: map[string][]string{"content-type": {"application/json"}},
		}, body)
		if err != nil {
			t.Fatalf("RunRewriteRequest: %v", err)
		}
		if out.Body != nil {
			t.Errorf("expected passthrough (nil Body) when body untouched, got %q", out.Body)
		}
	})

	// rewriteRequest, reads ctx.format, mutates pending.body.stream_options.
	t.Run("include-usage", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "include-usage.js")})
		if err := s.PatchContext(ContextPatch{Format: ptr("openaiChatCompletions")}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		body := []byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		out, err := s.RunRewriteRequest(PendingRequestShape{
			URL: "https://up/v1/chat/completions", Method: "POST",
			Headers: map[string][]string{"content-type": {"application/json"}},
		}, body)
		if err != nil {
			t.Fatalf("RunRewriteRequest: %v", err)
		}
		var got struct {
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.Unmarshal(out.Body, &got); err != nil {
			t.Fatalf("decode out body: %v (raw=%q)", err, out.Body)
		}
		if !got.StreamOptions.IncludeUsage {
			t.Errorf("include_usage not added: out body = %q", out.Body)
		}
	})

	// rewriteModel, reads ctx.apiKey.annotations (set by PatchContext).
	t.Run("use-deepseek-in-claude-code", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "use-deepseek-in-claude-code.js")})
		if err := s.PatchContext(ContextPatch{
			ApiKey: &ApiKeySummary{Annotations: map[string]string{"cn-models": "dpsk"}},
		}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		if got, _ := s.RunRewriteModel("claude-haiku-3"); got != "deepseek-v4-flash" {
			t.Errorf("haiku rewrite = %q, want deepseek-v4-flash", got)
		}
		if got, _ := s.RunRewriteModel("claude-sonnet-4"); got != "deepseek-v4-pro" {
			t.Errorf("sonnet rewrite = %q, want deepseek-v4-pro", got)
		}
	})

	// beforeRequest, reads ctx.attempt (set by PatchContext).
	t.Run("retry-rules", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "retry-rules.js")})
		if err := s.PatchContext(ContextPatch{Attempt: &AttemptState{CurrentRetryCount: 0, TotalAttemptCount: 0}}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		d, err := s.RunBeforeRequest(BeforeRequestDecision{Next: true})
		if err != nil {
			t.Fatalf("RunBeforeRequest: %v", err)
		}
		if d.Next { // 0<2 && 0<5 → should keep retrying → next=false
			t.Errorf("retry #0: next=%v, want false (keep retrying)", d.Next)
		}
		if err := s.PatchContext(ContextPatch{Attempt: &AttemptState{CurrentRetryCount: 2, TotalAttemptCount: 3}}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		d, err = s.RunBeforeRequest(BeforeRequestDecision{Next: false})
		if err != nil {
			t.Fatalf("RunBeforeRequest: %v", err)
		}
		if !d.Next { // currentRetryCount==2 → stop → next=true
			t.Errorf("retry #2: next=%v, want true (stop)", d.Next)
		}
		if d.Delay != 1000 {
			t.Errorf("retry #2: delay=%d, want 1000", d.Delay)
		}
	})

	// beforeTransform, reads ctx.routedModel.name (set by PatchContext).
	t.Run("deepseek-fix", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "deepseek-fix.js")})
		if err := s.PatchContext(ContextPatch{RoutedModel: &ModelSummary{Name: "deepseek-v4-pro"}}); err != nil {
			t.Fatalf("PatchContext: %v", err)
		}
		out, err := s.RunBeforeTransform(OutboundProfile{Type: "openai", Config: map[string]any{}})
		if err != nil {
			t.Fatalf("RunBeforeTransform: %v", err)
		}
		if out.Type != "deepseek" {
			t.Errorf("beforeTransform type = %q, want deepseek", out.Type)
		}
	})

	// rewriteProviderModels, no ctx/body dependency — pure model-list transform.
	t.Run("normalize-model-names", func(t *testing.T) {
		s := newTestSession(t, db.Script{ID: "x", Source: loadExampleScript(t, "normalize-model-names.js")})
		out, err := s.RunRewriteProviderModels([]ProviderModelEntry{{Model: "Provider/Model-Name:FREE"}})
		if err != nil {
			t.Fatalf("RunRewriteProviderModels: %v", err)
		}
		if len(out) != 1 || out[0].Model != "model-name" {
			t.Fatalf("normalized model = %+v, want Model=model-name", out)
		}
		if out[0].UpstreamModelName != "Provider/Model-Name:FREE" {
			t.Errorf("upstreamModelName = %q, want original Provider/Model-Name:FREE", out[0].UpstreamModelName)
		}
	})
}
