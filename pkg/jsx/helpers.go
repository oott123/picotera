package jsx

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/kv"
	"picotera/pkg/logx"

	"modernc.org/quickjs"
)

// fetchClient is the shared HTTP client used by picotera.fetch. The 5s
// timeout is the only backstop for a hook blocked in a host fetch call —
// SetEvalTimeout cannot interrupt a blocking host function.
var fetchClient = &http.Client{Timeout: 5 * time.Second}

// registerHelpers wires fetch / console / kv / body-proxy into the VM as
// synchronous host functions.
func registerHelpers(s *qjsSession) {
	registerFetch(s.vm)
	registerConsole(s)
	registerKV(s)
	registerObjects(s)
	registerAnnotations(s)
}

// registerAnnotations exposes the per-request annotation accumulators to JS.
// ctx.metaRequest.annotations and ctx.upstreamRequest.annotations are Proxies
// (see sdk.js __picotera_makeAnnotationsProxy) whose traps forward through these
// synchronous host functions, routed by slot ("meta" / "upstream"). Functions
// that can fail return (value, error) so the SDK throws on the error element;
// void ops return error (null on success). Type validation for values happens on
// the JS side; the Go set is defensive (rejects an empty key).
func registerAnnotations(s *qjsSession) {
	vm := s.vm
	// __picotera_anno_get returns the value's JSON encoding (so an empty-string
	// value stays distinguishable from a missing key, which returns "").
	_ = vm.RegisterFunc("__picotera_anno_get", func(slot, key string) (string, error) {
		v, ok, err := s.annoGet(slot, key)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
		b, merr := json.Marshal(v)
		if merr != nil {
			return "", merr
		}
		return string(b), nil
	}, false)
	_ = vm.RegisterFunc("__picotera_anno_set", func(slot, key, value string) error {
		return s.annoSet(slot, key, value)
	}, false)
	_ = vm.RegisterFunc("__picotera_anno_del", func(slot, key string) error {
		return s.annoDel(slot, key)
	}, false)
	_ = vm.RegisterFunc("__picotera_anno_keys", func(slot string) (string, error) {
		keys, err := s.annoKeys(slot)
		if err != nil {
			return "", err
		}
		b, merr := json.Marshal(keys)
		if merr != nil {
			return "", merr
		}
		return string(b), nil
	}, false)
	_ = vm.RegisterFunc("__picotera_anno_has", func(slot, key string) (int, error) {
		ok, err := s.annoHas(slot, key)
		if err != nil {
			return 0, err
		}
		if ok {
			return 1, nil
		}
		return 0, nil
	}, false)
}

// registerObjects exposes the body object registry to JS. ctx.request.body and
// rewriteRequest's pending.body are JS Proxies whose get/set/enumerate/delete
// traps forward through these synchronous host functions, so scalars cross into
// QuickJS only when a script reads them and writes land straight on the Go-side
// jsonast tree. Functions that can fail return (value, error) so the SDK throws
// on the error element; void ops return error (null on success). See objects.go.
func registerObjects(s *qjsSession) {
	vm := s.vm
	reg := s.registry
	_ = vm.RegisterFunc("__picotera_obj_root", func(slot string) (string, error) {
		return reg.rootDesc(slot)
	}, false)
	_ = vm.RegisterFunc("__picotera_obj_get", func(id int, key string) (string, error) {
		return reg.get(id, key)
	}, false)
	_ = vm.RegisterFunc("__picotera_obj_set", func(id int, key, valueJSON string) error {
		return reg.set(id, key, valueJSON)
	}, false)
	_ = vm.RegisterFunc("__picotera_obj_del", func(id int, key string) error {
		return reg.del(id, key)
	}, false)
	_ = vm.RegisterFunc("__picotera_obj_keys", func(id int) (string, error) {
		return reg.keysDesc(id)
	}, false)
	_ = vm.RegisterFunc("__picotera_obj_has", func(id int, key string) (int, error) {
		return reg.has(id, key)
	}, false)
	_ = vm.RegisterFunc("__picotera_obj_setlen", func(id, length int) error {
		return reg.setlen(id, length)
	}, false)
	_ = vm.RegisterFunc("__picotera_arr_splice", func(id, start, deleteCount int, itemsJSON string) (string, error) {
		return reg.arrSplice(id, start, deleteCount, itemsJSON)
	}, false)
	_ = vm.RegisterFunc("__picotera_arr_reverse", func(id int) error {
		return reg.arrReverse(id)
	}, false)
}

type fetchResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// registerFetch exposes a synchronous __picotera_fetch(url, initJSON) host
// function. It returns (jsonBody, error); the multi-return surfaces in JS as
// the array [jsonBody, errOrNull], which the SDK turns into a parsed object or
// a thrown error.
func registerFetch(vm *quickjs.VM) {
	_ = vm.RegisterFunc("__picotera_fetch", func(url, initJSON string) (string, error) {
		resp, err := doFetch(url, initJSON)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
	}, false)
}

func doFetch(url, initJSON string) (*fetchResponse, error) {
	var init struct {
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if initJSON != "" {
		_ = json.Unmarshal([]byte(initJSON), &init)
	}
	method := init.Method
	if method == "" {
		method = "GET"
	}
	req, err := http.NewRequest(method, url, strings.NewReader(init.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range init.Headers {
		req.Header.Set(k, v)
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return &fetchResponse{
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    string(body),
	}, nil
}

// registerKV exposes picotera.kv via __picotera_kv_* host functions. Functions
// that can fail return (value, error) so the SDK can throw on the error
// element; void operations return error (null on success). The session's
// request context bounds each operation.
func registerKV(s *qjsSession) {
	vm := s.vm
	store := s.engine.kvStore

	_ = vm.RegisterFunc("__picotera_kv_get", func(key string) (string, error) {
		val, err := store.Get(s.ctx, key)
		if err == kv.ErrKeyNotFound {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return val, nil
	}, false)

	_ = vm.RegisterFunc("__picotera_kv_set", func(key, value string) error {
		return store.Set(s.ctx, key, value)
	}, false)

	_ = vm.RegisterFunc("__picotera_kv_setex", func(key string, seconds int, value string) error {
		return store.SetEx(s.ctx, key, value, time.Duration(seconds)*time.Second)
	}, false)

	_ = vm.RegisterFunc("__picotera_kv_ttl", func(key string) (int, error) {
		ttl, err := store.TTL(s.ctx, key)
		if err != nil {
			return 0, err
		}
		return int(ttl), nil
	}, false)

	_ = vm.RegisterFunc("__picotera_kv_del", func(key string) error {
		return store.Del(s.ctx, key)
	}, false)
}

// registerConsole wires console.{log,info,warn,error} through __picotera_console
// to logx (tagged with the session's requestID) and appends a structured entry
// to the session's log buffer for inclusion in the meta response artifact.
func registerConsole(s *qjsSession) {
	_ = s.vm.RegisterFunc("__picotera_console", func(level, msg string) {
		entry := logx.New().WithField("source", "jsx").WithField("request_id", s.requestID)
		switch level {
		case "error":
			entry.Error(msg)
		case "warn":
			entry.Warn(msg)
		default:
			entry.Info(msg)
		}
		s.appendLog(level, msg)
	}, false)
}
