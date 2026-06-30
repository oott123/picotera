package jsx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"picotera/pkg/jsonast"
	"picotera/pkg/logx"

	"modernc.org/quickjs"
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

func scriptFilename(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("jsx: script id must not be empty")
	}
	return "script:" + id, nil
}

func internalFilename(name string) string {
	return "internal:" + name
}

// qjsSession is the in-process QuickJS implementation of Session. The
// underlying *quickjs.VM is not concurrency-safe; a session is used
// sequentially within a single request flow.
type qjsSession struct {
	engine    *qjsEngine
	vm        *quickjs.VM
	ctx       context.Context
	requestID string
	closed    bool
	tainted   bool

	logsMu    sync.Mutex
	logs      []LogEntry
	logsBytes int
	logsTrunc bool

	// registry backs the JS-visible body Proxies (ctx.request.body and
	// rewriteRequest's pending.body). See objects.go.
	registry *objectRegistry
}

func (s *qjsSession) appendLog(level, message string) {
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

// Logs returns a snapshot copy of the captured log entries. Must be called
// before Close.
func (s *qjsSession) Logs() []LogEntry {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	if len(s.logs) == 0 {
		return nil
	}
	out := make([]LogEntry, len(s.logs))
	copy(out, s.logs)
	return out
}

// ctxInit is the JS that installs the persistent globalThis.ctx with all
// fields at their zero/null state. Fields are filled in later via PatchContext.
const ctxInit = `globalThis.ctx = {
	endpointType: null,
	endpoint: null,
	requestModel: null,
	routedModel: null,
	request: null,
	apiKey: null,
	user: null,
	provider: null,
	providerModel: null,
	attempt: null,
	annotations: {},
	stream: false,
	sourceFormat: "",
	format: ""
};`

func newSession(ctx context.Context, eng *qjsEngine, requestID string) (*qjsSession, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, fmt.Errorf("jsx: quickjs.NewVM: %w", err)
	}
	if eng.cfg.MemoryLimit > 0 {
		vm.SetMemoryLimit(uintptr(eng.cfg.MemoryLimit))
	}
	s := &qjsSession{engine: eng, vm: vm, ctx: ctx, requestID: requestID, registry: newObjectRegistry()}
	registerHelpers(s)

	if _, err := vm.EvalFile(sdkSource, internalFilename("sdk.js"), quickjs.EvalGlobal); err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: eval sdk: %w", err)
	}
	if _, err := vm.EvalFile(ctxInit, internalFilename("ctx-init.js"), quickjs.EvalGlobal); err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: init ctx: %w", err)
	}

	scripts, err := eng.store.ListEnabledScripts(ctx)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: list scripts: %w", err)
	}
	for _, sc := range scripts {
		filename, err := scriptFilename(sc.ID)
		if err != nil {
			s.Close()
			return nil, err
		}
		if _, err := vm.EvalFile(sc.Source, filename, quickjs.EvalGlobal); err != nil {
			s.Close()
			return nil, fmt.Errorf("jsx: eval script %s: %w", sc.ID, err)
		}
	}
	return s, nil
}

// Close releases the underlying QuickJS VM. Safe to call multiple times.
func (s *qjsSession) Close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.vm != nil {
		func() {
			defer func() { _ = recover() }()
			_ = s.vm.Close()
		}()
		s.vm = nil
	}
}

func (s *qjsSession) timeout() time.Duration {
	if s.engine.cfg.HookTimeout > 0 {
		return s.engine.cfg.HookTimeout
	}
	return 5 * time.Second
}

// evalVoid evaluates expr purely for its side effects on globalThis and discards
// the result WITHOUT marshaling it. PatchContext and SetClientBody must use this
// rather than the marshaling EvalFile: EvalFile JSON-stringifies the eval's
// completion value, and `Object.assign(globalThis.ctx, ...)` evaluates to
// globalThis.ctx — so EvalFile would serialize the entire ctx, walking the
// enumerable lazy ctx.request.body getter and materializing a multi-MiB body into
// QuickJS (OOM) even when no hook ever reads it. EvalValueFile returns the raw
// value, which we free untouched.
func (s *qjsSession) evalVoid(expr, name string) error {
	v, err := s.vm.EvalValueFile(expr, internalFilename(name), quickjs.EvalGlobal)
	if err != nil {
		return err
	}
	v.Free()
	return nil
}

// PatchContext shallow-merges patch's non-nil fields onto globalThis.ctx.
// Assigning patch.Request replaces ctx.request with a fresh plain object, so
// when a client body is registered we (re)install the lazy ctx.request.body
// Proxy getter afterwards.
func (s *qjsSession) PatchContext(patch ContextPatch) error {
	if s.tainted {
		return ErrHookTimeout
	}
	b, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("jsx: marshal context patch: %w", err)
	}
	if string(b) == "{}" {
		return nil
	}
	expr := "Object.assign(globalThis.ctx, " + string(b) + ")"
	if patch.Request != nil && s.registry.request.hasBody {
		expr += ";globalThis.__picotera_installRequestBody();"
	}
	if err := s.evalVoid(expr, "patch-context.js"); err != nil {
		return fmt.Errorf("jsx: patch context: %w", err)
	}
	return nil
}

// SetClientBody installs the JS-visible client request body. The bytes are
// parsed lazily on first access; replacing them invalidates any Proxy handed out
// for the previous body. If ctx.request already exists, the lazy body getter is
// installed immediately (so SetClientBody and a request PatchContext may run in
// either order).
func (s *qjsSession) SetClientBody(body []byte) error {
	if s.tainted {
		return ErrHookTimeout
	}
	s.registry.setRequestBody(body)
	if !s.registry.request.hasBody {
		return nil
	}
	if err := s.evalVoid(
		"if (globalThis.ctx && globalThis.ctx.request && typeof globalThis.ctx.request === 'object') { globalThis.__picotera_installRequestBody(); }",
		"set-client-body.js"); err != nil {
		return fmt.Errorf("jsx: set client body: %w", err)
	}
	return nil
}

// evalJSON evaluates a hook IIFE and returns the result as JSON bytes.
// isUndefined is true when the IIFE returned `undefined` (passthrough). On a
// timeout/interrupt the session is tainted and ErrHookTimeout is returned.
func (s *qjsSession) evalJSON(name, filename, expr string) (data json.RawMessage, isUndefined bool, err error) {
	if s.tainted {
		return nil, false, ErrHookTimeout
	}
	if terr := s.vm.SetEvalTimeout(s.timeout()); terr != nil {
		return nil, false, fmt.Errorf("jsx: %s set timeout: %w", name, terr)
	}
	v, err := s.vm.EvalValueFile(expr, filename, quickjs.EvalGlobal)
	if err != nil {
		if isInterrupt(err) {
			s.tainted = true
			return nil, false, ErrHookTimeout
		}
		return nil, false, fmt.Errorf("jsx: %s: %w", name, err)
	}
	defer v.Free()
	if v.IsUndefined() {
		return nil, true, nil
	}
	b, merr := v.MarshalJSON()
	if merr != nil {
		return nil, false, fmt.Errorf("jsx: %s marshal: %w", name, merr)
	}
	// marshalJSON relies on the underlying JS value's GC lifetime, so we need to clone the bytes to avoid use-after-free.
	b = bytes.Clone(b)
	return json.RawMessage(b), false, nil
}

func isInterrupt(err error) bool {
	return err != nil && strings.Contains(err.Error(), "interrupted")
}

func mustJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("jsx: marshal initial: %w", err)
	}
	return string(b), nil
}

// RunRewriteModel runs the rewriteModel waterfall. A non-string result keeps
// the initial value.
func (s *qjsSession) RunRewriteModel(initial string) (string, error) {
	init, err := mustJSON(initial)
	if err != nil {
		return initial, err
	}
	expr := `(function () {
		var r = picotera.hooks.rewriteModel.runWaterfall(globalThis.ctx, ` + init + `);
		if (typeof r !== 'string') return undefined;
		return r;
	})()`
	data, undef, err := s.evalJSON("rewriteModel", internalFilename("hook-rewriteModel.js"), expr)
	if err != nil || undef {
		return initial, err
	}
	var out string
	if err := json.Unmarshal(data, &out); err != nil {
		return initial, fmt.Errorf("jsx: rewriteModel decode: %w", err)
	}
	return out, nil
}

// RunSortProviders runs the sortProviders waterfall. Passthrough keeps the
// initial list. A returned array (or {providers: [...]}) replaces it; an empty
// array means "no providers".
func (s *qjsSession) RunSortProviders(initial []CandidateView) ([]CandidateView, error) {
	init, err := mustJSON(initial)
	if err != nil {
		return initial, err
	}
	expr := `(function () {
		var initial = ` + init + `;
		var r = picotera.hooks.sortProviders.runWaterfall(globalThis.ctx, initial);
		if (r === globalThis.ctx || typeof r === 'undefined' || r === null) return undefined;
		if (Array.isArray(r)) return r;
		if (r && Array.isArray(r.providers)) return r.providers;
		return undefined;
	})()`
	data, undef, err := s.evalJSON("sortProviders", internalFilename("hook-sortProviders.js"), expr)
	if err != nil || undef {
		return initial, err
	}
	var out []CandidateView
	if err := json.Unmarshal(data, &out); err != nil {
		logx.WithContext(s.ctx).WithError(err).Debug("sortProviders hook returned undecodable value; keeping input")
		return initial, nil
	}
	return out, nil
}

// RunBeforeRequest runs the beforeRequest waterfall. Passthrough keeps the
// initial decision. A non-string upstreamModel is dropped at the JS boundary.
func (s *qjsSession) RunBeforeRequest(initial BeforeRequestDecision) (BeforeRequestDecision, error) {
	init, err := mustJSON(initial)
	if err != nil {
		return initial, err
	}
	expr := `(function () {
		var r = picotera.hooks.beforeRequest.runWaterfall(globalThis.ctx, ` + init + `);
		if (r === globalThis.ctx || typeof r === 'undefined' || r === null) return undefined;
		var um = (typeof r.upstreamModel === 'string') ? r.upstreamModel : '';
		return { next: !!r.next, delay: r.delay || 0, upstreamModel: um };
	})()`
	data, undef, err := s.evalJSON("beforeRequest", internalFilename("hook-beforeRequest.js"), expr)
	if err != nil || undef {
		return initial, err
	}
	var out BeforeRequestDecision
	if err := json.Unmarshal(data, &out); err != nil {
		return initial, fmt.Errorf("jsx: beforeRequest decode: %w", err)
	}
	return out, nil
}

// RunRewriteRequest runs the rewriteRequest waterfall. initial carries only
// url/method/headers (its Body MUST be nil); body is the raw upstream body bytes
// the hook may read/mutate via pending.body (nil = no JS-visible body).
//
// pending.body is a lazy Proxy over the Go-side jsonast tree: an untouched body
// (the common case — hooks usually rewrite only url/headers) is never parsed,
// serialized, or moved through QuickJS, keeping memory flat for multi-MiB
// bodies. When a hook does read/mutate it, writes land straight on the Go tree
// and the result is encoded from there; a fresh object returned by the hook is
// reconstructed via the marker protocol so any Proxy embedded in it (e.g.
// {...pending, body: pending.body}) is restored to its tracked content.
//
// The returned shape's Body carries the final upstream bytes, or nil to fall
// back to the caller's pre-hook bytes (untouched, removed, or a byte-identical
// clean passthrough).
func (s *qjsSession) RunRewriteRequest(initial PendingRequestShape, body []byte) (PendingRequestShape, error) {
	if initial.Body != nil {
		return initial, fmt.Errorf("jsx: RunRewriteRequest: initial.Body must be nil")
	}
	s.registry.setPendingBody(body)
	hasBody := body != nil

	init, err := mustJSON(initial)
	if err != nil {
		return initial, err
	}
	hasBodyJS := "false"
	if hasBody {
		hasBodyJS = "true"
	}

	// The hook sees pending.body as a lazy Proxy; the final body is handed back
	// out-of-band through globalThis.__picotera_rr_out so a multi-MiB string is
	// never re-escaped into the marshaled meta result. markerReplacer turns any
	// embedded Proxy into a {"__picotera_object":id} marker the Go side restores.
	expr := `(function () {
		var initial = ` + init + `;
		var hasBody = ` + hasBodyJS + `;
		var touched = false, bodyVal, bodySet = false;
		if (hasBody) {
			Object.defineProperty(initial, 'body', {
				enumerable: true,
				configurable: true,
				get: function () {
					touched = true;
					if (!bodySet) {
						var r = globalThis.__picotera_obj_root('pending');
						if (r[1]) throw new Error(r[1]);
						bodyVal = globalThis.__picotera_descToValue(JSON.parse(r[0]));
						bodySet = true;
					}
					return bodyVal;
				},
				set: function (val) { touched = true; bodySet = true; bodyVal = val; }
			});
		}
		var r = picotera.hooks.rewriteRequest.runWaterfall(globalThis.ctx, initial);
		var v = (typeof r === 'undefined' || r === null) ? initial : r;
		var meta = { url: v.url, method: v.method, headers: v.headers };
		globalThis.__picotera_rr_out = '';
		var nb;
		if (v === initial) {
			if (!hasBody) { meta.bodyState = 'none'; return meta; }
			if (!touched) { meta.bodyState = 'unchanged'; return meta; }
			nb = bodyVal;
		} else {
			nb = v.body;
		}
		if (typeof nb === 'undefined' || nb === null) {
			meta.bodyState = 'none';
		} else if (typeof nb === 'string') {
			meta.bodyState = 'raw';
			globalThis.__picotera_rr_out = nb;
		} else {
			meta.bodyState = 'json';
			globalThis.__picotera_rr_out = JSON.stringify(nb, globalThis.__picotera_markerReplacer);
		}
		return meta;
	})()`

	data, undef, err := s.evalJSON("rewriteRequest", internalFilename("hook-rewriteRequest.js"), expr)
	if err != nil || undef {
		return initial, err
	}

	var meta struct {
		URL       string              `json:"url"`
		Method    string              `json:"method"`
		Headers   map[string][]string `json:"headers"`
		BodyState string              `json:"bodyState"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return initial, fmt.Errorf("jsx: rewriteRequest decode: %w", err)
	}

	out := PendingRequestShape{URL: meta.URL, Method: meta.Method, Headers: meta.Headers}
	switch meta.BodyState {
	case "none", "unchanged":
		// Body absent / removed / untouched: leave Body nil so the caller falls
		// back to the original request bytes.
	case "raw":
		final, err := s.readGlobalString("__picotera_rr_out")
		if err != nil {
			return initial, fmt.Errorf("jsx: rewriteRequest read body: %w", err)
		}
		out.Body = []byte(final)
		if out.Body == nil {
			out.Body = []byte{}
		}
	case "json":
		final, err := s.readGlobalString("__picotera_rr_out")
		if err != nil {
			return initial, fmt.Errorf("jsx: rewriteRequest read body: %w", err)
		}
		node, perr := jsonast.Parse([]byte(final))
		if perr != nil {
			return initial, fmt.Errorf("jsx: rewriteRequest parse body: %w", perr)
		}
		node, perr = s.registry.resolveMarkers(node, false)
		if perr != nil {
			return initial, fmt.Errorf("jsx: rewriteRequest resolve markers: %w", perr)
		}
		pt := s.registry.pending.tree
		if pt != nil && node == pt.root && !pt.dirty {
			// Clean passthrough of the original root: fall back to pre-hook
			// bytes for a byte-identical send.
			break
		}
		enc, eerr := jsonast.Encode(node)
		if eerr != nil {
			return initial, fmt.Errorf("jsx: rewriteRequest encode body: %w", eerr)
		}
		out.Body = enc
	default:
		return initial, fmt.Errorf("jsx: rewriteRequest: unexpected bodyState %q", meta.BodyState)
	}
	return out, nil
}

// readGlobalString reads globalThis[name] and returns it as a Go string.
func (s *qjsSession) readGlobalString(name string) (string, error) {
	g := s.vm.GlobalObject()
	defer g.Free()
	atom, err := s.vm.NewAtom(name)
	if err != nil {
		return "", err
	}
	v, err := s.vm.GetProperty(g, atom)
	if err != nil {
		return "", err
	}
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("jsx: global %s is not a string (%T)", name, v)
	}
	return str, nil
}

// RunBeforeTransform runs the beforeTransform waterfall. The contract is strict
// because its result drives bridge construction: taps must return an object,
// with string type and object config when present.
func (s *qjsSession) RunBeforeTransform(initial OutboundProfile) (OutboundProfile, error) {
	if initial.Config == nil {
		initial.Config = map[string]any{}
	}
	init, err := mustJSON(initial)
	if err != nil {
		return initial, err
	}
	expr := `(function () {
		var initial = ` + init + `;
		var r = picotera.hooks.beforeTransform.runWaterfall(globalThis.ctx, initial);
		if (typeof r === 'undefined' || r === null) return undefined;
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
		return {
			type: Object.prototype.hasOwnProperty.call(r, "type") ? r.type : initial.type,
			config: Object.prototype.hasOwnProperty.call(r, "config") ? r.config : {}
		};
	})()`
	data, undef, err := s.evalJSON("beforeTransform", internalFilename("hook-beforeTransform.js"), expr)
	if err != nil || undef {
		return initial, err
	}
	var out OutboundProfile
	if err := json.Unmarshal(data, &out); err != nil {
		return initial, fmt.Errorf("jsx: beforeTransform decode: %w", err)
	}
	if out.Config == nil {
		out.Config = map[string]any{}
	}
	return out, nil
}

// RunRewriteProviderModels runs the rewriteProviderModels waterfall. A
// non-array / undefined result, or an undecodable array, keeps the input.
func (s *qjsSession) RunRewriteProviderModels(initial []ProviderModelEntry) ([]ProviderModelEntry, error) {
	init, err := mustJSON(initial)
	if err != nil {
		return initial, err
	}
	expr := `(function () {
		var initial = ` + init + `;
		var r = picotera.hooks.rewriteProviderModels.runWaterfall(globalThis.ctx, initial);
		if (typeof r === 'undefined' || r === null || !Array.isArray(r)) return undefined;
		return r;
	})()`
	data, undef, err := s.evalJSON("rewriteProviderModels", internalFilename("hook-rewriteProviderModels.js"), expr)
	if err != nil || undef {
		return initial, err
	}
	var out []ProviderModelEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return initial, nil
	}
	return out, nil
}

// RunAfterUpstreamError runs the afterUpstreamError waterfall. Passthrough
// (undefined / null / returning ctx / returning the unchanged input object)
// keeps the initial break and carries no statusCode/message override
// (equivalent to follow-upstream — the seed status/message are readable by the
// hook but never echoed back as overrides). A returned
// object is normalized: break is coerced to a boolean, statusCode to an
// integer, and a non-string message is dropped to "".
func (s *qjsSession) RunAfterUpstreamError(initial UpstreamErrorView) (AfterUpstreamErrorDecision, error) {
	zero := AfterUpstreamErrorDecision{Break: initial.Break}
	init, err := mustJSON(initial)
	if err != nil {
		return zero, err
	}
	expr := `(function () {
		var input = ` + init + `;
		var r = picotera.hooks.afterUpstreamError.runWaterfall(globalThis.ctx, input);
		if (r === globalThis.ctx || r === input || typeof r === 'undefined' || r === null) return undefined;
		return { break: !!r.break, statusCode: r.statusCode | 0, message: (typeof r.message === 'string') ? r.message : '' };
	})()`
	data, undef, err := s.evalJSON("afterUpstreamError", internalFilename("hook-afterUpstreamError.js"), expr)
	if err != nil || undef {
		return zero, err
	}
	var out AfterUpstreamErrorDecision
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, fmt.Errorf("jsx: afterUpstreamError decode: %w", err)
	}
	return out, nil
}
