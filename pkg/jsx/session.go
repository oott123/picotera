package jsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"picotera/pkg/logx"
	"sync"
	"time"

	"github.com/fastschema/qjs"
)

// ErrHookTimeout signals a hook ran past Engine.Config.HookTimeout. The
// session is then tainted and unusable for subsequent hooks.
var ErrHookTimeout = errors.New("jsx: hook timeout")

// LogEntry is a single console.{log,info,warn,error,debug} call captured
// during a session. Times are UTC RFC3339Nano. debug is normalized to info.
type LogEntry struct {
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Ts      time.Time `json:"ts"`
}

const (
	maxLogEntries    = 1000
	maxLogBytes      = 256 * 1024
	maxLogMessageLen = 8 * 1024
	logTruncSuffix   = "... [truncated]"
	logSentinelMsg   = "[picotera] log buffer truncated"
)

type Session struct {
	engine    *Engine
	rt        *qjs.Runtime
	cancel    context.CancelFunc
	requestID string
	closed    bool
	tainted   bool

	logsMu    sync.Mutex
	logs      []LogEntry
	logsBytes int
	logsTrunc bool
}

func (s *Session) appendLog(level, message string) {
	switch level {
	case "info", "warn", "error":
	default:
		level = "info"
	}
	if len(message) > maxLogMessageLen {
		message = message[:maxLogMessageLen-len(logTruncSuffix)] + logTruncSuffix
	}
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	if s.logsTrunc {
		return
	}
	if len(s.logs) >= maxLogEntries || s.logsBytes+len(message) > maxLogBytes {
		s.logs = append(s.logs, LogEntry{
			Level:   "warn",
			Message: logSentinelMsg,
			Ts:      time.Now().UTC(),
		})
		s.logsTrunc = true
		return
	}
	s.logs = append(s.logs, LogEntry{
		Level:   level,
		Message: message,
		Ts:      time.Now().UTC(),
	})
	s.logsBytes += len(message)
}

// Logs returns a snapshot copy of the captured log entries. Safe to call
// concurrently with appendLog. Must be called before Close.
func (s *Session) Logs() []LogEntry {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	if len(s.logs) == 0 {
		return nil
	}
	out := make([]LogEntry, len(s.logs))
	copy(out, s.logs)
	return out
}

func newSession(ctx context.Context, eng *Engine, requestID string) (*Session, error) {
	sessCtx, cancel := context.WithCancel(ctx)
	opt := qjs.Option{
		Context:            sessCtx,
		CloseOnContextDone: true,
		MemoryLimit:        int(eng.cfg.MemoryLimit),
	}
	rt, err := qjs.New(opt)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("jsx: qjs.New: %w", err)
	}
	s := &Session{engine: eng, rt: rt, cancel: cancel, requestID: requestID}
	registerHelpers(s)

	c := rt.Context()
	if _, err := c.Eval("sdk.js", qjs.Code(sdkSource)); err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: eval sdk: %w", err)
	}

	scripts, err := eng.store.ListEnabledScripts(sessCtx)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: list scripts: %w", err)
	}
	for _, sc := range scripts {
		wrapped := wrapScriptForSandbox(sc.ID, sc.Source)
		if _, err := c.Eval("script:"+sc.ID, qjs.Code(wrapped)); err != nil {
			s.Close()
			return nil, fmt.Errorf("jsx: eval script %s: %w", sc.ID, err)
		}
	}
	return s, nil
}

// wrapScriptForSandbox wraps a script's source in an IIFE that shadows
// `picotera.kv` with a per-script proxy. The proxy prefixes every key with
// `script:<scriptID>:` before forwarding to the real KV functions, so two
// scripts cannot read or overwrite each other's KV entries, and no script
// can write to namespaces the host uses for sessions / ceremonies.
//
// Hooks tapped by the script close over the local `picotera` (so callbacks
// invoked later by runWaterfall still see the sandboxed kv), but the
// `picotera.hooks.*` references inherit the same Waterfall instances via
// Object.create, so taps register on the shared dispatch.
func wrapScriptForSandbox(scriptID, source string) string {
	idLit, _ := json.Marshal(scriptID)
	return fmt.Sprintf(`(function(){
const __picoteraScriptID = %s;
const __picoteraGlobal = globalThis.picotera;
const __picoteraKVRaw = __picoteraGlobal.kv;
const __picoteraKVPrefix = "script:" + __picoteraScriptID + ":";
const __picoteraSandboxedKV = {
  get: function(k){ return __picoteraKVRaw.get(__picoteraKVPrefix + String(k)); },
  set: function(k, v){ return __picoteraKVRaw.set(__picoteraKVPrefix + String(k), v); },
  setex: function(k, s, v){ return __picoteraKVRaw.setex(__picoteraKVPrefix + String(k), s, v); },
  ttl: function(k){ return __picoteraKVRaw.ttl(__picoteraKVPrefix + String(k)); },
  del: function(k){ return __picoteraKVRaw.del(__picoteraKVPrefix + String(k)); },
};
const picotera = Object.create(__picoteraGlobal);
picotera.kv = __picoteraSandboxedKV;
%s
})();`, string(idLit), source)
}

// Close releases the underlying QuickJS runtime. Safe to call multiple times.
// If the session was tainted by a hook timeout, the runtime's wasm module is
// already gone — Close still tries rt.Close() but recovers any panic so the
// caller's `defer session.Close()` is always safe.
func (s *Session) Close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.rt != nil {
		func() {
			defer func() { _ = recover() }()
			s.rt.Close()
		}()
		s.rt = nil
	}
	if s.cancel != nil {
		s.cancel()
	}
}

// Context returns the underlying qjs.Context for direct use within the package.
func (s *Session) Context() *qjs.Context {
	if s.rt == nil {
		return nil
	}
	return s.rt.Context()
}

// RunSortHook calls the sortProviders waterfall with the given input. Return
// semantics: if no tap mutated the value (passthrough), in.Providers is
// returned unchanged; if a tap returned an array of candidates, that array
// is returned; if a tap returned a `{providers: [...]}` shape, that array is
// returned. An empty array means "no providers" (gateway then fails 502).
func (s *Session) RunSortHook(in SortInput) ([]Candidate, error) {
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return nil, err
	}
	expr := fmt.Sprintf(`(async () => {
		const context = %s;
		const r = await picotera.hooks.sortProviders.runWaterfall(context, { providers: JSON.parse(JSON.stringify(context.providers)) });
		if (r === context || typeof r === 'undefined') return null;
		return JSON.stringify(r);
	})()`, lit)
	jsonStr, err := s.runHookExpr("sortProviders.js", expr)
	if err != nil {
		return nil, err
	}
	if jsonStr == "" || jsonStr == "null" {
		return in.Providers, nil
	}
	// Try {providers: [...]} first.
	var obj struct {
		Providers []Candidate `json:"providers"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil && obj.Providers != nil {
		logx.WithContext(s.Context()).WithField("new_providers", obj.Providers).Debug("sortProviders hook returned new providers")
		return obj.Providers, nil
	}
	// Try direct array.
	var arr []Candidate
	if err := json.Unmarshal([]byte(jsonStr), &arr); err == nil {
		logx.WithContext(s.Context()).WithField("new_providers", arr).Debug("sortProviders hook returned new providers")
		return arr, nil
	}
	return in.Providers, nil
}

// RunBeforeRequestHook calls the beforeRequest waterfall and decodes the
// returned `{next, delay, upstreamModel}` shape. Passthrough or a return that
// doesn't carry these keys collapses to `Next=false, Delay=0, UpstreamModel=""`.
// A non-string upstreamModel is dropped at the JS boundary (treated as "" =
// keep default).
func (s *Session) RunBeforeRequestHook(in BeforeRequestInput) (BeforeRequestDecision, error) {
	var dec BeforeRequestDecision
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return dec, err
	}
	expr := fmt.Sprintf(`(async () => {
		const ctx = %s;
		const r = await picotera.hooks.beforeRequest.runWaterfall(ctx, { next: ctx.currentRetryCount > 0, delay: 0 });
		if (r === ctx || typeof r === 'undefined' || r === null) return null;
		const um = (typeof r.upstreamModel === 'string') ? r.upstreamModel : '';
		return JSON.stringify({ next: !!r.next, delay: r.delay || 0, upstreamModel: um });
	})()`, lit)
	jsonStr, err := s.runHookExpr("beforeRequest.js", expr)
	if err != nil {
		return dec, err
	}
	if jsonStr == "" || jsonStr == "null" {
		if in.CurrentRetryCount > 0 {
			dec.Next = true
		}
		return dec, nil
	}
	if err := json.Unmarshal([]byte(jsonStr), &dec); err != nil {
		return dec, fmt.Errorf("jsx: beforeRequest decode: %w", err)
	}
	return dec, nil
}

// RunRewriteModelHook calls the rewriteModel waterfall once before MPE
// resolution. Returns the (possibly rewritten) model name. If no tap mutated
// the value, or a tap returned a non-string, the original modelName is
// returned. Equality with the input means the caller should skip body rewriting
// and re-resolution.
func (s *Session) RunRewriteModelHook(in RewriteModelInput, modelName string) (string, error) {
	ctxLit, err := marshalToJSLiteral(in)
	if err != nil {
		return modelName, err
	}
	initLit, err := marshalToJSLiteral(modelName)
	if err != nil {
		return modelName, err
	}
	expr := fmt.Sprintf(`(async () => {
		const ctx = %s;
		const initial = %s;
		const r = await picotera.hooks.rewriteModel.runWaterfall(ctx, initial);
		if (typeof r !== 'string') return null;
		return JSON.stringify(r);
	})()`, ctxLit, initLit)
	jsonStr, err := s.runHookExpr("rewriteModel.js", expr)
	if err != nil {
		return modelName, err
	}
	if jsonStr == "" || jsonStr == "null" {
		return modelName, nil
	}
	var out string
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return modelName, fmt.Errorf("jsx: rewriteModel decode: %w", err)
	}
	return out, nil
}

// RunRewriteHook calls the rewriteRequest waterfall. The hook input value is
// ctx.pendingRequest; passthrough yields the input pendingRequest unchanged.
// The hook must return a complete PendingRequest object — partial overrides
// are not supported.
//
// Body roundtrip: if a tap leaves `body` as a non-string (object/array/...),
// it is JSON.stringify'd at the JS boundary so Go always receives a JSON
// string token in PendingRequestShape.Body when the field is present.
func (s *Session) RunRewriteHook(in RewriteInput) (PendingRequestShape, error) {
	out := in.PendingRequest
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return out, err
	}
	expr := fmt.Sprintf(`(async () => {
		const ctx = %s;
		const initial = ctx.pendingRequest;
		const r = await picotera.hooks.rewriteRequest.runWaterfall(ctx, initial);
		const v = (typeof r === 'undefined' || r === null) ? initial : r;
		if (v && typeof v.body !== 'undefined' && typeof v.body !== 'string') {
			v.body = JSON.stringify(v.body);
		}
		return JSON.stringify(v);
	})()`, lit)
	jsonStr, err := s.runHookExpr("rewriteRequest.js", expr)
	if err != nil {
		return out, err
	}
	if jsonStr == "" || jsonStr == "null" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return out, fmt.Errorf("jsx: rewriteRequest decode: %w", err)
	}
	return out, nil
}

// RunBeforeTransformHook calls the beforeTransform waterfall with the current
// outbound profile. The hook contract is strict because its result controls
// bridge construction: taps must return an object, with string type and object
// config when present.
func (s *Session) RunBeforeTransformHook(in BeforeTransformInput, initial OutboundProfile) (OutboundProfile, error) {
	if initial.Config == nil {
		initial.Config = map[string]any{}
	}
	ctxLit, err := marshalToJSLiteral(in)
	if err != nil {
		return initial, err
	}
	initLit, err := marshalToJSLiteral(initial)
	if err != nil {
		return initial, err
	}
	expr := fmt.Sprintf(`(async () => {
		const ctx = %s;
		const initial = %s;
		const r = await picotera.hooks.beforeTransform.runWaterfall(ctx, initial);
		if (typeof r === 'undefined' || r === null) return null;
		if (typeof r !== 'object' || Array.isArray(r)) {
			throw new Error("jsx: beforeTransform result must be object");
		}
		if (Object.prototype.hasOwnProperty.call(r, "type") && typeof r.type !== 'string') {
			throw new Error("jsx: beforeTransform type must be string");
		}
		if (Object.prototype.hasOwnProperty.call(r, "config")) {
			if (r.config === null || typeof r.config !== 'object' || Array.isArray(r.config)) {
				throw new Error("jsx: beforeTransform config must be object");
			}
		}
		return JSON.stringify({
			type: Object.prototype.hasOwnProperty.call(r, "type") ? r.type : initial.type,
			config: Object.prototype.hasOwnProperty.call(r, "config") ? r.config : {}
		});
	})()`, ctxLit, initLit)
	jsonStr, err := s.runHookExpr("beforeTransform.js", expr)
	if err != nil {
		return initial, err
	}
	if jsonStr == "" || jsonStr == "null" {
		return initial, nil
	}
	var out OutboundProfile
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return initial, fmt.Errorf("jsx: beforeTransform decode: %w", err)
	}
	if out.Config == nil {
		out.Config = map[string]any{}
	}
	return out, nil
}

// RunRewriteProviderModelsHook calls the rewriteProviderModels waterfall after
// default aggregation. Returns the hook-processed model list, falling back to
// the input if the hook returns a non-array, returns undefined, or if
// json.Unmarshal of the result fails.
func (s *Session) RunRewriteProviderModelsHook(in RewriteProviderModelsInput, models []ProviderModelEntry) ([]ProviderModelEntry, error) {
	ctxLit, err := marshalToJSLiteral(in)
	if err != nil {
		return models, err
	}
	initLit, err := marshalToJSLiteral(models)
	if err != nil {
		return models, err
	}
	expr := fmt.Sprintf(`(async () => {
		const ctx = %s;
		const initial = %s;
		const r = await picotera.hooks.rewriteProviderModels.runWaterfall(ctx, initial);
		if (typeof r === 'undefined' || r === null || !Array.isArray(r)) return null;
		return JSON.stringify(r);
	})()`, ctxLit, initLit)
	jsonStr, err := s.runHookExpr("rewriteProviderModels.js", expr)
	if err != nil {
		return models, err
	}
	if jsonStr == "" || jsonStr == "null" {
		return models, nil
	}
	var out []ProviderModelEntry
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return models, nil
	}
	return out, nil
}

// runHookExpr evaluates the JS expression with HookTimeout, then
// JSON-serializes the resolved value (inside the same goroutine as the
// eval — fastschema/qjs values are not safe to use across goroutines).
// Returns the JSON string ready for json.Unmarshal.
func (s *Session) runHookExpr(name, expr string) (string, error) {
	if s.tainted {
		return "", ErrHookTimeout
	}
	type result struct {
		jsonStr string
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("jsx: eval panic: %v", r)}
			}
		}()
		v, err := s.rt.Context().Eval(name, qjs.Code(expr))
		if err != nil {
			ch <- result{err: err}
			return
		}
		if v.IsPromise() {
			v, err = v.Await()
			if err != nil {
				ch <- result{err: err}
				return
			}
		}
		defer v.Free()
		switch v.Type() {
		case "Undefined", "Null":
			ch <- result{jsonStr: "null"}
			return
		}

		// Workaround: v.JSONStringify() is not working blocked by https://github.com/fastschema/qjs/issues/44, so we only accept string values from hooks and treat them as JSON strings.
		jsonStr := v.String()
		ch <- result{jsonStr: jsonStr}
	}()

	timeout := s.engine.cfg.HookTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	select {
	case r := <-ch:
		return r.jsonStr, r.err
	case <-time.After(timeout):
		s.tainted = true
		s.cancel() // wakes the goroutine via panic; recover handles it
		<-ch       // drain
		return "", ErrHookTimeout
	}
}

// evalWithTimeout runs an Eval, racing against Engine.Config.HookTimeout.
// On timeout: cancels the runtime context (causing the in-flight Eval to
// panic via wazero's CloseOnContextDone), recovers, marks the session as
// tainted, returns ErrHookTimeout. Subsequent calls fail fast.
//
// NOTE: the returned *qjs.Value is bound to the goroutine that produced it.
// Use runHookExpr instead of touching the Value from another goroutine.
func (s *Session) evalWithTimeout(name, src string) (*qjs.Value, error) {
	if s.tainted {
		return nil, ErrHookTimeout
	}
	type result struct {
		v   *qjs.Value
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("jsx: eval panic: %v", r)}
			}
		}()
		v, err := s.rt.Context().Eval(name, qjs.Code(src))
		ch <- result{v: v, err: err}
	}()

	timeout := s.engine.cfg.HookTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(timeout):
		s.tainted = true
		s.cancel() // wakes the goroutine via panic; recover handles it
		<-ch       // drain so we don't leak the channel send (goroutine will deliver shortly)
		return nil, ErrHookTimeout
	}
}
