# QuickJS Hook Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed a `modernc.org/quickjs` JavaScript engine into the gateway so user-supplied scripts can hook three points in the request lifecycle (`sortProviders`, `beforeRequest`, `rewriteRequest`) to influence routing, retry, delay, and upstream request shape.

**Architecture:** A new `pkg/jsx` package owns the JS engine — `Engine` is constructed once at server boot, and `Session` is created per gateway request. A new `script` table stores enabled JS sources; on each gateway request the session creates a fresh `*quickjs.VM`, evaluates an embedded SDK that defines `picotera.hooks.<name>.tap`, then evaluates each enabled user script in id-order. Three hook entrypoints (`RunSortHook`, `RunBeforeRequestHook`, `RunRewriteHook`) are exposed to Go; each marshals a Go ctx into JS, runs the registered taps as a tapable Waterfall, awaits the resulting Promise (driven by a microtask pump), and unmarshals the final return value back to Go. The gateway retry loop in `handle_gateway.go` is rewritten to call the three hooks at the documented points; failure of any hook fails the meta request. A management CRUD API + dashboard view manages scripts.

**Tech Stack:** Go 1.26, `modernc.org/quickjs` (new dep), Huma v2, sqlc/pgx, goose. Vue 3 + Tailwind v4 dashboard.

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `db/migrations/004_script.sql` | Create | `script` table + partial index |
| `db/queries/script.sql` | Create | sqlc queries: List/Get/Insert/Update/Delete + ListEnabled |
| `pkg/db/script.sql.go` | Regenerate | `sqlc generate` output |
| `pkg/db/models.go` | Regenerate | adds `Script` struct |
| `pkg/db/querier.go` | Regenerate | adds Script methods |
| `pkg/configx/configx.go` | Modify | add `JSHookTimeout`, `JSMemoryLimit`, `JSMaxTotalAttempts`, `JSMaxDelay` config |
| `pkg/jsx/engine.go` | Create | `Engine` constructed at server boot, holds config + `*db.Queries`, factory for `Session` |
| `pkg/jsx/session.go` | Create | per-request `*Session`: VM lifecycle, hook entrypoints, ctx marshaling |
| `pkg/jsx/store.go` | Create | thin wrapper for `ListEnabledScripts` |
| `pkg/jsx/sdk.go` | Create | `//go:embed sdk.js` |
| `pkg/jsx/sdk.js` | Create | tapable Waterfall + `picotera.hooks` global setup |
| `pkg/jsx/helpers.go` | Create | `RegisterFunc` registrations: `picotera.fetch`, `setTimeout`/`clearTimeout`, `console.*` |
| `pkg/jsx/promise.go` | Create | `awaitPromise` — microtask pump until settled / timeout |
| `pkg/jsx/types.go` | Create | Go ↔ JS struct marshaling for hook ctx (Endpoint, Model, providers list, request shapes) |
| `pkg/jsx/validate.go` | Create | `ValidateSyntax(source string) error` — boots throwaway VM, runs `Eval`, returns syntax error if any |
| `pkg/jsx/engine_test.go` | Create | engine + session tests (sort/before/rewrite, async, errors) |
| `pkg/jsx/validate_test.go` | Create | validation tests |
| `pkg/contract/script.go` | Create | `ScriptView`, request/response types, Operation* declarations |
| `pkg/server/handle_script.go` | Create | CRUD handlers for script |
| `pkg/server/handle_gateway.go` | Modify | rewrite retry loop to call jsx session hooks |
| `pkg/server/gateway_helpers.go` | Modify | helpers if needed (model lookup) |
| `pkg/server/server.go` | Modify | construct `*jsx.Engine`, register Script operations |
| `db/queries/model.sql` | Modify | add `GetModelByEndpointAndName` (or reuse existing GetModelByName) |
| `cmd/picotera/main.go` | Unmodified | (no changes needed; openapi subcommand already wired) |
| `openapi.yaml` | Regenerate | via `mise run openapi` |
| `dashboard/src/api.d.ts` (or `src/openapi-types.ts`) | Regenerate | via `pnpm` openapi-typescript step (whichever is wired) |
| `dashboard/src/router/index.ts` | Modify | add `/scripts` route |
| `dashboard/src/components/AppSidebar.vue` | Modify | add scripts nav entry |
| `dashboard/src/views/ScriptsView.vue` | Create | list + enable toggle + CRUD entrypoints |
| `dashboard/src/components/ScriptForm.vue` | Create | create/edit side panel with source textarea |
| `go.mod`, `go.sum` | Modify | add `modernc.org/quickjs` |

---

## Pre-Flight: Architectural Notes

Several details in the spec deserve clarification before tasks begin so the implementer doesn't have to guess.

**Endpoint annotations field.** The current `Endpoint` row (`db.Endpoint` in `pkg/db/models.go`) has only `path`, `name`, `model_path`, `credentials_resolver`. The spec says `ctx.endpoint` includes annotations, but the schema doesn't. **Decision:** mirror the spec literally — pass the existing `db.Endpoint` row as-is, with `annotations: null` placeholder field. Adding endpoint annotations is out of scope.

**Model lookup.** Spec says "model may need an additional fetch by name + endpoint." There is no per-endpoint variant; `GetModelByName(ctx, model)` is sufficient and already exists. Use it. If it returns `pgx.ErrNoRows` (a script targets a model that has no `model` row even though it has a `model_provider_endpoint`), pass `null` for `ctx.model` rather than failing — `model_provider_endpoint` is the source of truth for routing, the `model` table is metadata.

**Provider/MPE ctx shape.** `resolveProviders` returns `[]GetProvidersByEndpointAndModelRow` — a flat row that joins `mpe + provider + provider_endpoint`. The JS side wants `{ provider: Provider, mpe: ModelProviderEndpoint }`. We will fetch the full `db.Provider` row by ID once per unique provider in the candidate list (cache in a map for the request) and the `db.ModelProviderEndpoint` row from the join row's fields. `provider_endpoint.upstream_url` is stored on the candidate row but does not need to be JS-visible — it is only used to build the upstream request after `rewriteRequest`.

**Upstream URL after sortProviders/skip.** The current `buildUpstreamRequest` reads `provider.UpstreamUrl.String` from the candidate row. The candidate's `upstream_url` must travel with the JS-returned `{ provider, mpe }` shape — store it as a Go-side sidecar map keyed by `(providerID, endpointPath)` populated from the original candidate list. When JS returns a re-ordered/filtered array, look up the matching upstream URL by these keys.

**Retry loop wins.** The new retry loop replaces the existing `for _, provider := range providers` block in `handle_gateway.go`. The new loop is index-based and re-enters `beforeRequest` after every failed attempt. The current `lastErr := error` accumulator stays.

**Promise pump.** `modernc.org/quickjs` exposes `Eval` which evaluates synchronously. JS that returns a Promise resolves only when QuickJS's internal microtask queue is drained. The library's `EvalValue` returns a `Value`; we will check whether it is a promise (via JS interop — call a helper JS function `__picotera_await(p)` that wraps in `Promise.resolve(p).then(v=>__deliver(v), e=>__reject(e))` and signals back via two registered Go funcs that store the result on the session). The session loops, calling a no-op `vm.Eval("0", EvalGlobal)` (or library-provided pump if available) until either `__deliver` or `__reject` is called or the wall-clock timeout fires. Using a registered "pump" callback via `setTimeout(0)` is the fallback.

**setTimeout/fetch reentry.** Go-side timers and fetches must deliver results back into the JS event loop. Each scheduled callback will queue a JS-side resolution by calling a registered Go helper that stores `(handlerId, result)` on the session; the pump in `awaitPromise` checks this queue every iteration and calls a JS function `__picotera_dispatch(handlerId, result)` that resolves the matching Promise.

These design notes are normative for the implementer; they fill the gaps the spec leaves to "to be confirmed during implementation."

---

### Task 1: Add `script` table migration

**Goal:** Create the `script` table with the partial index defined in the spec.

**Files:**
- Create: `db/migrations/004_script.sql`

**Acceptance Criteria:**
- [ ] Migration creates `script` table with all columns from the spec
- [ ] Partial index on `(enabled) WHERE enabled = TRUE`
- [ ] Up + Down sections both present
- [ ] Migration runs cleanly on empty DB and on dev DB

**Verify:** `go run ./cmd/picotera/main.go --help` (boots, runs migrations) — should print usage without migration errors. Then `psql $DATABASE_URL -c '\d script'` confirms table.

**Steps:**

- [ ] **Step 1: Create the migration file**

```sql
-- db/migrations/004_script.sql
-- +goose Up
CREATE TABLE script (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  source TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX script_enabled_idx ON script (id) WHERE enabled = TRUE;

-- +goose Down
DROP INDEX IF EXISTS script_enabled_idx;
DROP TABLE script;
```

- [ ] **Step 2: Boot the server to run the migration**

Run: `docker compose up -d && go run ./cmd/picotera/main.go &`
Expected: log line `migrations completed`. Kill the server (`kill %1`).

- [ ] **Step 3: Confirm the table exists**

Run: `psql 'postgres://picotera:picotera@localhost:34052/picotera' -c '\d script'`
Expected: column listing matching the migration.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/004_script.sql
git commit -m "feat(db): add script table for js hook engine"
```

---

### Task 2: Add sqlc queries for `script`

**Goal:** Define CRUD + `ListEnabledScripts` queries and regenerate the Go code.

**Files:**
- Create: `db/queries/script.sql`
- Regenerate: `pkg/db/script.sql.go`, `pkg/db/models.go`, `pkg/db/querier.go`

**Acceptance Criteria:**
- [ ] `ListScripts` (all, ordered by `created_at DESC`)
- [ ] `ListEnabledScripts` (enabled only, ordered by `id ASC`) — used by the engine on every gateway request
- [ ] `GetScript(id)`
- [ ] `InsertScript(id, name, source, enabled)` returning `*`
- [ ] `UpdateScript(id, name, source, enabled, updated_at=now())` returning `*`
- [ ] `DeleteScript(id)`
- [ ] `sqlc generate` produces working code; `go build ./...` succeeds

**Verify:** `sqlc generate && go build ./...` → no errors.

**Steps:**

- [ ] **Step 1: Write the queries**

```sql
-- db/queries/script.sql

-- name: ListScripts :many
SELECT * FROM script ORDER BY created_at DESC, id DESC;

-- name: ListEnabledScripts :many
SELECT * FROM script WHERE enabled = TRUE ORDER BY id ASC;

-- name: GetScript :one
SELECT * FROM script WHERE id = $1 LIMIT 1;

-- name: InsertScript :one
INSERT INTO script (id, name, source, enabled)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateScript :one
UPDATE script
SET name = $2, source = $3, enabled = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteScript :exec
DELETE FROM script WHERE id = $1;
```

- [ ] **Step 2: Regenerate sqlc**

Run: `sqlc generate`
Expected: no errors. `pkg/db/script.sql.go` is created. `pkg/db/models.go` gains a `Script` struct. `pkg/db/querier.go` gains the new methods.

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add db/queries/script.sql pkg/db/
git commit -m "feat(db): add script queries and sqlc-generated code"
```

---

### Task 3: Add JS-engine config fields

**Goal:** Add `JSHookTimeout`, `JSMemoryLimit`, `JSMaxTotalAttempts`, `JSMaxDelay` to `configx.Config` with the spec's defaults.

**Files:**
- Modify: `pkg/configx/configx.go`

**Acceptance Criteria:**
- [ ] Four new fields with `mapstructure` tags
- [ ] Defaults: `js_hook_timeout=5s`, `js_memory_limit=64MiB` (i.e. `67108864`), `js_max_total_attempts=50`, `js_max_delay=60s`
- [ ] Env vars `PICOTERA_JS_HOOK_TIMEOUT`, `PICOTERA_JS_MEMORY_LIMIT`, `PICOTERA_JS_MAX_TOTAL_ATTEMPTS`, `PICOTERA_JS_MAX_DELAY` are auto-bound by existing `bindEnvs` reflection logic

**Verify:** `PICOTERA_JS_HOOK_TIMEOUT=10s go run ./cmd/picotera/main.go --help` (or a small inline check; just verify build).

**Steps:**

- [ ] **Step 1: Add the fields and defaults**

```go
// pkg/configx/configx.go (add fields to Config struct)
type Config struct {
	DatabaseURL        string        `mapstructure:"database_url"`
	Host               string        `mapstructure:"host"`
	Port               int           `mapstructure:"port"`
	GatewayReadTimeout time.Duration `mapstructure:"gateway_read_timeout"`
	S3                 S3Config      `mapstructure:"s3"`
	JSHookTimeout      time.Duration `mapstructure:"js_hook_timeout"`
	JSMemoryLimit      int64         `mapstructure:"js_memory_limit"`
	JSMaxTotalAttempts int           `mapstructure:"js_max_total_attempts"`
	JSMaxDelay         time.Duration `mapstructure:"js_max_delay"`
}
```

In `Parse()`, after the existing `viper.SetDefault` calls, add:

```go
viper.SetDefault("js_hook_timeout", 5*time.Second)
viper.SetDefault("js_memory_limit", int64(64*1024*1024))
viper.SetDefault("js_max_total_attempts", 50)
viper.SetDefault("js_max_delay", 60*time.Second)
```

- [ ] **Step 2: Build to confirm**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add pkg/configx/configx.go
git commit -m "feat(config): add quickjs hook engine config fields"
```

---

### Task 4: Add `modernc.org/quickjs` dependency

**Goal:** Add the QuickJS Go module so subsequent tasks can import it.

**Files:**
- Modify: `go.mod`, `go.sum`

**Acceptance Criteria:**
- [ ] `go get modernc.org/quickjs` succeeds
- [ ] A trivial program that does `quickjs.NewVM()` then `vm.Close()` builds and runs
- [ ] Module appears in `go.mod`'s `require` block (not `// indirect`)

**Verify:** the trivial smoke-test runs without panicking.

**Steps:**

- [ ] **Step 1: Add the dep**

Run:
```bash
cd /home/oott123/Work/Projects/picotera
go get modernc.org/quickjs@latest
go mod tidy
```
Expected: `go.mod` updated, `go.sum` populated.

- [ ] **Step 2: Smoke-test in a throwaway file**

```go
// pkg/jsx/smoke_test.go (will be deleted in step 4)
package jsx

import (
	"testing"

	"modernc.org/quickjs"
)

func TestQuickJSSmoke(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	v, err := vm.Eval("1 + 1", quickjs.EvalGlobal)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if n, ok := v.(int64); !ok || n != 2 {
		// the lib may return float64; accept both
		if f, ok := v.(float64); !ok || f != 2 {
			t.Fatalf("want 2, got %v (%T)", v, v)
		}
	}
}
```

Create `pkg/jsx/` directory if missing. Save the file.

- [ ] **Step 3: Run the smoke test**

Run: `go test ./pkg/jsx/ -run TestQuickJSSmoke -v`
Expected: PASS.

- [ ] **Step 4: Delete the smoke file (Task 5 will replace it)**

Run: `rm pkg/jsx/smoke_test.go`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add modernc.org/quickjs dependency"
```

---

### Task 5: jsx package skeleton — Engine, Session, embedded SDK

**Goal:** Create the foundational structure of `pkg/jsx`: an `Engine` with config, a per-request `Session` that creates a VM, evaluates the embedded SDK, evaluates enabled user scripts, and tears down on `Close`. No hook entrypoints yet.

**Files:**
- Create: `pkg/jsx/engine.go`
- Create: `pkg/jsx/session.go`
- Create: `pkg/jsx/store.go`
- Create: `pkg/jsx/sdk.go`
- Create: `pkg/jsx/sdk.js`
- Create: `pkg/jsx/engine_test.go`

**Acceptance Criteria:**
- [ ] `Engine.NewSession(ctx) (*Session, error)` constructs a VM, applies `SetMemoryLimit` and `SetEvalTimeout`, evaluates `sdk.js`, queries `ListEnabledScripts`, evaluates each in id-order
- [ ] `Session.Close()` calls `vm.Close()` exactly once and is safe to call multiple times
- [ ] `sdk.js` defines `globalThis.picotera = { hooks: { sortProviders: <Waterfall>, beforeRequest: <Waterfall>, rewriteRequest: <Waterfall> } }`
- [ ] Each Waterfall has `.tap(name, fn)` and an internal `.runWaterfall(input)` method that returns a Promise resolving to the final value (passthrough on `undefined`)
- [ ] A unit test loads two no-op scripts and asserts `picotera.hooks.sortProviders` exists with both taps registered

**Verify:** `go test ./pkg/jsx/ -run TestEngine_LoadsScripts -v`

**Steps:**

- [ ] **Step 1: Write `sdk.js`**

```js
// pkg/jsx/sdk.js
;(function () {
  'use strict'

  function Waterfall() {
    this._taps = []
  }
  Waterfall.prototype.tap = function (name, fn) {
    this._taps.push({ name: String(name || 'anonymous'), fn: fn })
  }
  Waterfall.prototype.runWaterfall = async function (input) {
    let value = input
    for (const tap of this._taps) {
      const out = await tap.fn(value)
      if (typeof out !== 'undefined') {
        value = out
      }
    }
    return value
  }

  globalThis.picotera = {
    hooks: {
      sortProviders: new Waterfall(),
      beforeRequest: new Waterfall(),
      rewriteRequest: new Waterfall(),
    },
  }
})()
```

- [ ] **Step 2: Write `sdk.go`**

```go
// pkg/jsx/sdk.go
package jsx

import _ "embed"

//go:embed sdk.js
var sdkSource string
```

- [ ] **Step 3: Write `store.go`**

```go
// pkg/jsx/store.go
package jsx

import (
	"context"

	"picotera/pkg/db"
)

// ScriptStore is the subset of db.Querier used by the engine.
type ScriptStore interface {
	ListEnabledScripts(ctx context.Context) ([]db.Script, error)
}
```

- [ ] **Step 4: Write `engine.go`**

```go
// pkg/jsx/engine.go
package jsx

import (
	"context"
	"time"
)

type Config struct {
	HookTimeout      time.Duration
	MemoryLimit      int64
	MaxTotalAttempts int
	MaxDelay         time.Duration
}

type Engine struct {
	cfg   Config
	store ScriptStore
}

func NewEngine(cfg Config, store ScriptStore) *Engine {
	return &Engine{cfg: cfg, store: store}
}

func (e *Engine) Config() Config { return e.cfg }

// NewSession creates a per-request session. The caller MUST call Close().
func (e *Engine) NewSession(ctx context.Context) (*Session, error) {
	return newSession(ctx, e)
}
```

- [ ] **Step 5: Write `session.go` skeleton**

```go
// pkg/jsx/session.go
package jsx

import (
	"context"
	"fmt"

	"modernc.org/quickjs"
)

type Session struct {
	engine *Engine
	vm     *quickjs.VM
	closed bool
}

func newSession(ctx context.Context, eng *Engine) (*Session, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, fmt.Errorf("jsx: NewVM: %w", err)
	}
	if eng.cfg.MemoryLimit > 0 {
		vm.SetMemoryLimit(eng.cfg.MemoryLimit)
	}
	if eng.cfg.HookTimeout > 0 {
		vm.SetEvalTimeout(eng.cfg.HookTimeout)
	}

	s := &Session{engine: eng, vm: vm}

	if _, err := vm.Eval(sdkSource, quickjs.EvalGlobal); err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: eval sdk: %w", err)
	}

	scripts, err := eng.store.ListEnabledScripts(ctx)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("jsx: list scripts: %w", err)
	}
	for _, sc := range scripts {
		if _, err := vm.Eval(sc.Source, quickjs.EvalGlobal); err != nil {
			s.Close()
			return nil, fmt.Errorf("jsx: eval script %s: %w", sc.ID, err)
		}
	}
	return s, nil
}

func (s *Session) Close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.vm != nil {
		s.vm.Close()
		s.vm = nil
	}
}

// VM exposes the underlying VM for hook implementations within this package.
func (s *Session) VM() *quickjs.VM { return s.vm }
```

- [ ] **Step 6: Write the test**

```go
// pkg/jsx/engine_test.go
package jsx

import (
	"context"
	"testing"
	"time"

	"picotera/pkg/db"
)

type fakeStore struct{ scripts []db.Script }

func (f *fakeStore) ListEnabledScripts(ctx context.Context) ([]db.Script, error) {
	return f.scripts, nil
}

func TestEngine_LoadsScripts(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{
		{ID: "a", Source: `picotera.hooks.sortProviders.tap("a", function (ctx) { return ctx; });`},
		{ID: "b", Source: `picotera.hooks.sortProviders.tap("b", function (ctx) { return ctx; });`},
	}}
	eng := NewEngine(Config{HookTimeout: time.Second, MemoryLimit: 64 * 1024 * 1024}, store)
	s, err := eng.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	v, err := s.VM().Eval(`picotera.hooks.sortProviders._taps.length`, 0)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	// quickjs may return int64 or float64 — accept either
	switch n := v.(type) {
	case int64:
		if n != 2 {
			t.Errorf("want 2 taps, got %d", n)
		}
	case float64:
		if n != 2 {
			t.Errorf("want 2 taps, got %v", n)
		}
	default:
		t.Errorf("unexpected type %T (%v)", v, v)
	}
}
```

- [ ] **Step 7: Run the test**

Run: `go test ./pkg/jsx/ -run TestEngine_LoadsScripts -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/jsx/
git commit -m "feat(jsx): engine and session skeleton with embedded sdk"
```

---

### Task 6: Promise pump — `awaitPromise`

**Goal:** Provide `awaitPromise(value any, timeout time.Duration) (any, error)` that drives QuickJS until a Promise settles or wall-clock timeout fires. Plain values are returned as-is.

**Files:**
- Create: `pkg/jsx/promise.go`
- Modify: `pkg/jsx/sdk.js` (add `__picotera` internals)
- Modify: `pkg/jsx/session.go` (add helpers `__picotera_resolve`/`__picotera_reject` registration in `newSession`)
- Modify: `pkg/jsx/engine_test.go` (add async test)

**Acceptance Criteria:**
- [ ] A JS function `picotera.hooks.sortProviders.runWaterfall({...})` that internally `await`s resolves correctly
- [ ] A test where a tap returns `new Promise(r => setTimeout(() => r(...), 5))` resolves through `awaitPromise`
- [ ] `awaitPromise` returns a non-nil error if the wall-clock timeout fires
- [ ] `awaitPromise` returns a non-nil error if the JS promise rejects, with the JS error message preserved

**Verify:** `go test ./pkg/jsx/ -run TestSession_Promise -v`

**Steps:**

- [ ] **Step 1: Augment `sdk.js`**

Add at the top of the IIFE (before the Waterfall definition):

```js
// minimal microtask pump — Go drives by calling __picotera_pump
const __picoteraPending = new Map()
let __picoteraNextID = 1

globalThis.__picotera_run = function (jsExpr) {
  // helper used by Go: returns the awaited result via __picotera_resolve(id, value)
  const id = __picoteraNextID++
  Promise.resolve()
    .then(() => eval(jsExpr))
    .then(
      (v) => globalThis.__picotera_resolve(id, v),
      (e) => globalThis.__picotera_reject(id, String(e && e.message || e))
    )
  return id
}
```

(Yes, `eval` is intentional and contained — only Go-formatted JS expressions reach it.)

- [ ] **Step 2: Implement Go-side resolution registration in `newSession`**

In `pkg/jsx/session.go`, before `vm.Eval(sdkSource, ...)`:

```go
type pendingSlot struct {
	value any
	err   error
	done  bool
}

type Session struct {
	engine  *Engine
	vm      *quickjs.VM
	closed  bool
	pending map[int64]*pendingSlot
}
```

After creating `s`, register:

```go
vm.RegisterFunc("__picotera_resolve", func(id int64, v any) {
	if slot, ok := s.pending[id]; ok {
		slot.value = v
		slot.done = true
	}
}, false)
vm.RegisterFunc("__picotera_reject", func(id int64, msg string) {
	if slot, ok := s.pending[id]; ok {
		slot.err = fmt.Errorf("jsx: %s", msg)
		slot.done = true
	}
}, false)
```

Initialize `s.pending = map[int64]*pendingSlot{}`.

- [ ] **Step 3: Write `promise.go`**

```go
// pkg/jsx/promise.go
package jsx

import (
	"errors"
	"fmt"
	"time"
)

var ErrHookTimeout = errors.New("jsx: hook timeout")

// awaitJSExpr evaluates a JS expression that may return a Promise, polling
// until it settles or timeout fires. The expression is evaluated inside
// __picotera_run, which schedules resolution via __picotera_resolve/reject.
func (s *Session) awaitJSExpr(expr string, timeout time.Duration) (any, error) {
	idVal, err := s.vm.Eval(fmt.Sprintf("__picotera_run(%q)", expr), 0)
	if err != nil {
		return nil, fmt.Errorf("jsx: schedule: %w", err)
	}
	id, ok := toInt64(idVal)
	if !ok {
		return nil, fmt.Errorf("jsx: __picotera_run returned non-int %T", idVal)
	}

	slot := &pendingSlot{}
	s.pending[id] = slot
	defer delete(s.pending, id)

	deadline := time.Now().Add(timeout)
	for !slot.done {
		if time.Now().After(deadline) {
			return nil, ErrHookTimeout
		}
		// Pump microtasks: evaluate a no-op which gives QuickJS a chance to run pending jobs.
		if _, err := s.vm.Eval("0", 0); err != nil {
			return nil, fmt.Errorf("jsx: pump: %w", err)
		}
		time.Sleep(100 * time.Microsecond)
	}
	if slot.err != nil {
		return nil, slot.err
	}
	return slot.value, nil
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
```

- [ ] **Step 4: Add async test**

```go
// pkg/jsx/engine_test.go (append)
func TestSession_Promise_ResolvesValue(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{
		{ID: "a", Source: `globalThis.giveMe42 = function () { return Promise.resolve(42); }`},
	}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, err := eng.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	got, err := s.awaitJSExpr("giveMe42()", time.Second)
	if err != nil {
		t.Fatalf("awaitJSExpr: %v", err)
	}
	switch n := got.(type) {
	case int64:
		if n != 42 {
			t.Errorf("want 42, got %d", n)
		}
	case float64:
		if n != 42 {
			t.Errorf("want 42, got %v", n)
		}
	default:
		t.Errorf("unexpected %T (%v)", got, got)
	}
}

func TestSession_Promise_PropagatesRejection(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{
		{ID: "a", Source: `globalThis.boom = function () { return Promise.reject(new Error("boom")); }`},
	}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background())
	defer s.Close()

	_, err := s.awaitJSExpr("boom()", time.Second)
	if err == nil || err.Error() == "" {
		t.Fatalf("want rejection error, got %v", err)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/jsx/ -run TestSession_Promise -v`
Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/jsx/promise.go pkg/jsx/session.go pkg/jsx/sdk.js pkg/jsx/engine_test.go
git commit -m "feat(jsx): promise pump with go-driven resolution"
```

---

### Task 7: ctx marshaling — Go ↔ JS types

**Goal:** Convert Go inputs (Endpoint, Model, candidate list, request shapes) to JS objects in a stable shape, and convert JS return values back. Use JSON as the marshaling boundary — `vm.Eval(fmt.Sprintf("globalThis.__ctx = %s; <runner>(globalThis.__ctx)", jsonBytes))` — to avoid value-by-value FFI.

**Files:**
- Create: `pkg/jsx/types.go`
- Modify: `pkg/jsx/engine_test.go`

**Acceptance Criteria:**
- [ ] `marshalCtx(v any) (string, error)` — JSON encodes a Go struct/map; returns the JS literal text (i.e. JSON, valid JS literal)
- [ ] `unmarshalJSON(jsRet any) (json.RawMessage, error)` — given the value returned by `vm.Eval` (which may be a string/number/object via QuickJS marshaling), produce JSON bytes Go can `json.Unmarshal` into a typed struct
  - Strategy: have JS stringify the result before returning. Pump emits `JSON.stringify(value)` from inside `__picotera_resolve`'s wrapper before delivering to Go. (Update `__picotera_run` to JSON-stringify on the resolve side.)
- [ ] An end-to-end test passes a `{ providers: [...] }` Go struct to JS, JS reverses the array, Go receives the reversed struct

**Verify:** `go test ./pkg/jsx/ -run TestSession_CtxRoundTrip -v`

**Steps:**

- [ ] **Step 1: Update `__picotera_run` in `sdk.js` to stringify on resolve**

Replace the body of `__picotera_run`:

```js
globalThis.__picotera_run = function (jsExpr) {
  const id = __picoteraNextID++
  Promise.resolve()
    .then(() => (0, eval)(jsExpr))
    .then(
      (v) => globalThis.__picotera_resolve(id, JSON.stringify(typeof v === 'undefined' ? null : v)),
      (e) => globalThis.__picotera_reject(id, String((e && e.message) || e))
    )
  return id
}
```

(Now `__picotera_resolve` in Go always receives a JSON string.)

- [ ] **Step 2: Update `pendingSlot.value` typing**

In `session.go`, change the resolve registration to take a string:

```go
vm.RegisterFunc("__picotera_resolve", func(id int64, jsonStr string) {
	if slot, ok := s.pending[id]; ok {
		slot.value = jsonStr
		slot.done = true
	}
}, false)
```

`pendingSlot.value` becomes `string`.

In `awaitJSExpr`, on success return `slot.value.(string)`.

Update `TestSession_Promise_ResolvesValue` to expect `"42"` (the JSON literal).

- [ ] **Step 3: Write `types.go`**

```go
// pkg/jsx/types.go
package jsx

import (
	"encoding/json"
	"fmt"
)

// marshalToJSLiteral encodes v to JSON text — valid as a JS literal expression.
func marshalToJSLiteral(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("jsx: marshal: %w", err)
	}
	return string(b), nil
}

// unmarshalJSON decodes a JSON string returned by JS into out.
func unmarshalJSON(s string, out any) error {
	if s == "" || s == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), out); err != nil {
		return fmt.Errorf("jsx: unmarshal: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Add roundtrip test**

```go
// pkg/jsx/engine_test.go (append)
func TestSession_CtxRoundTrip(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{
		{ID: "rev", Source: `picotera.hooks.sortProviders.tap("rev", function (ctx) {
			return { providers: ctx.providers.slice().reverse() };
		});`},
	}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background())
	defer s.Close()

	in := map[string]any{"providers": []map[string]any{
		{"id": 1}, {"id": 2}, {"id": 3},
	}}
	lit, _ := marshalToJSLiteral(in)
	expr := fmt.Sprintf("picotera.hooks.sortProviders.runWaterfall(%s)", lit)
	got, err := s.awaitJSExpr(expr, time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	var out struct {
		Providers []struct{ ID int } `json:"providers"`
	}
	if err := unmarshalJSON(got.(string), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Providers) != 3 || out.Providers[0].ID != 3 || out.Providers[2].ID != 1 {
		t.Errorf("want reversed order, got %+v", out.Providers)
	}
}
```

(Add `import "fmt"` to the test file if not already imported.)

- [ ] **Step 5: Run the test**

Run: `go test ./pkg/jsx/ -run TestSession_CtxRoundTrip -v`
Expected: PASS. Also re-run all jsx tests: `go test ./pkg/jsx/ -v`. All PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/jsx/
git commit -m "feat(jsx): json-based ctx marshaling between go and js"
```

---

### Task 8: Hook entrypoints — `RunSortHook`, `RunBeforeRequestHook`, `RunRewriteHook`

**Goal:** Implement the three Go-callable hook functions that take Go inputs, build the JS ctx, run the corresponding Waterfall, and unmarshal back to Go.

**Files:**
- Modify: `pkg/jsx/session.go` (add the three methods)
- Modify: `pkg/jsx/types.go` (add request-side struct literals)
- Modify: `pkg/jsx/engine_test.go` (cover all three hooks)

**Acceptance Criteria:**
- [ ] `Session.RunSortHook(in SortInput) (SortOutput, error)` — `in.Endpoint`, `in.Model`, `in.Request`, `in.Providers` (`[]Candidate{ Provider db.Provider, MPE db.ModelProviderEndpoint }`); returns reordered/filtered list. JS returning `undefined` keeps order. JS returning empty array yields empty list (loop returns 502 NO_PROVIDER_AVAILABLE).
- [ ] `Session.RunBeforeRequestHook(in BeforeRequestInput) (BeforeRequestDecision, error)` — fields per spec. Defaults: `Next=false`, `Delay=0`. JS returning `undefined` is treated as defaults.
- [ ] `Session.RunRewriteHook(in RewriteInput) (RewriteOutput, error)` — fields per spec. JS-omitted fields are kept from `in.UpstreamRequest`. `body` may be string or object; objects are auto-stringified before reaching Go.
- [ ] All three hooks honor `engine.cfg.HookTimeout`
- [ ] All three hooks return `ErrHookTimeout` (or wrap it) on timeout

**Verify:** `go test ./pkg/jsx/ -run TestSession_Hooks -v`

**Steps:**

- [ ] **Step 1: Add input/output types to `types.go`**

```go
// pkg/jsx/types.go (append)

// Candidate is the JS-visible shape for a provider candidate.
type Candidate struct {
	Provider any `json:"provider"` // marshaled db.Provider with credentials redacted? No — spec says full row including credentials.
	MPE      any `json:"mpe"`
}

type RequestShape struct {
	Path    string              `json:"path"`
	Method  string              `json:"method"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
	Model   string              `json:"model"`
}

type SortInput struct {
	Endpoint  any           `json:"endpoint"`
	Model     any           `json:"model"`
	Request   RequestShape  `json:"request"`
	Providers []Candidate   `json:"providers"`
}

type SortOutput struct {
	Providers []Candidate `json:"providers"`
}

type LastError struct {
	ProviderID int    `json:"providerId"`
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

type BeforeRequestInput struct {
	Endpoint           any          `json:"endpoint"`
	Model              any          `json:"model"`
	Request            RequestShape `json:"request"`
	Provider           any          `json:"provider"`
	MPE                any          `json:"mpe"`
	CurrentRetryCount  int          `json:"currentRetryCount"`
	TotalAttemptCount  int          `json:"totalAttemptCount"`
	LastError          *LastError   `json:"lastError"`
}

type BeforeRequestDecision struct {
	Next  bool `json:"next"`
	Delay int  `json:"delay"`
}

type UpstreamRequestShape struct {
	URL     string              `json:"url"`
	Method  string              `json:"method"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type RewriteInput struct {
	Endpoint          any                  `json:"endpoint"`
	Model             any                  `json:"model"`
	Request           RequestShape         `json:"request"`
	Provider          any                  `json:"provider"`
	MPE               any                  `json:"mpe"`
	CurrentRetryCount int                  `json:"currentRetryCount"`
	TotalAttemptCount int                  `json:"totalAttemptCount"`
	UpstreamRequest   UpstreamRequestShape `json:"upstreamRequest"`
	ClientRequest     RequestShape         `json:"clientRequest"`
}

type RewriteOutput struct {
	URL     *string              `json:"url"`
	Method  *string              `json:"method"`
	Headers *map[string][]string `json:"headers"`
	Body    json.RawMessage      `json:"body"`
}
```

- [ ] **Step 2: Add hook entrypoints to `session.go`**

```go
// pkg/jsx/session.go (append)

import (
	"encoding/json"
)

func (s *Session) runHook(hookName string, in any, out any) error {
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return err
	}
	expr := fmt.Sprintf("picotera.hooks.%s.runWaterfall(%s)", hookName, lit)
	raw, err := s.awaitJSExpr(expr, s.engine.cfg.HookTimeout)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return unmarshalJSON(raw.(string), out)
}

func (s *Session) RunSortHook(in SortInput) ([]Candidate, error) {
	var ret SortInput // sortProviders waterfall returns the ctx; spec says JS returns the new array directly.
	// Adjust: JS returns the array, not the full ctx — but waterfall passthrough on undefined means
	// we need the *array* to flow through. Use a wrapper expression.
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return nil, err
	}
	// sortProviders taps return an array (or undefined). Initial value is in.Providers.
	// To match the spec we treat the runWaterfall input as the providers array, and the ctx is exposed
	// to taps via globalThis.__sortCtx.
	expr := fmt.Sprintf(
		`(function(){ globalThis.__sortCtx = %s; return picotera.hooks.sortProviders.runWaterfall(globalThis.__sortCtx); })()`,
		lit,
	)
	raw, err := s.awaitJSExpr(expr, s.engine.cfg.HookTimeout)
	if err != nil {
		return nil, err
	}
	// JS returns the full ctx (passthrough) OR a tap may have returned an array directly.
	// Try both shapes:
	rawStr := raw.(string)
	if rawStr == "" || rawStr == "null" {
		return in.Providers, nil
	}
	// Try { providers: [...] } first
	if err := json.Unmarshal([]byte(rawStr), &ret); err == nil && ret.Providers != nil {
		return ret.Providers, nil
	}
	// Try direct array
	var arr []Candidate
	if err := json.Unmarshal([]byte(rawStr), &arr); err == nil {
		return arr, nil
	}
	return in.Providers, nil
}

func (s *Session) RunBeforeRequestHook(in BeforeRequestInput) (BeforeRequestDecision, error) {
	var dec BeforeRequestDecision
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return dec, err
	}
	expr := fmt.Sprintf(
		`(function(){ globalThis.__brCtx = %s; return picotera.hooks.beforeRequest.runWaterfall(globalThis.__brCtx); })()`,
		lit,
	)
	raw, err := s.awaitJSExpr(expr, s.engine.cfg.HookTimeout)
	if err != nil {
		return dec, err
	}
	rawStr := raw.(string)
	// passthrough (returns ctx) → defaults; or returns {next?, delay?}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawStr), &probe); err != nil {
		return dec, nil
	}
	if v, ok := probe["next"]; ok {
		_ = json.Unmarshal(v, &dec.Next)
	}
	if v, ok := probe["delay"]; ok {
		_ = json.Unmarshal(v, &dec.Delay)
	}
	return dec, nil
}

func (s *Session) RunRewriteHook(in RewriteInput) (RewriteOutput, error) {
	var out RewriteOutput
	lit, err := marshalToJSLiteral(in)
	if err != nil {
		return out, err
	}
	expr := fmt.Sprintf(
		`(function(){
			globalThis.__rwCtx = %s;
			return Promise.resolve(picotera.hooks.rewriteRequest.runWaterfall(globalThis.__rwCtx))
				.then(function(r){
					if (r === globalThis.__rwCtx || typeof r === 'undefined') return null;
					if (r && typeof r.body === 'object' && r.body !== null) {
						r = Object.assign({}, r, { body: JSON.stringify(r.body) });
					}
					return r;
				});
		})()`,
		lit,
	)
	raw, err := s.awaitJSExpr(expr, s.engine.cfg.HookTimeout)
	if err != nil {
		return out, err
	}
	rawStr := raw.(string)
	if rawStr == "null" || rawStr == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(rawStr), &out); err != nil {
		return out, fmt.Errorf("jsx: rewriteRequest unmarshal: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 3: Write tests**

```go
// pkg/jsx/engine_test.go (append)
func TestSession_Hooks_Sort(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx) {
			return ctx.providers.slice().reverse();
		});
	`}}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background())
	defer s.Close()

	out, err := s.RunSortHook(SortInput{
		Providers: []Candidate{
			{Provider: map[string]any{"id": 1}, MPE: map[string]any{"providerId": 1}},
			{Provider: map[string]any{"id": 2}, MPE: map[string]any{"providerId": 2}},
		},
	})
	if err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	pm := out[0].Provider.(map[string]any)
	if int(pm["id"].(float64)) != 2 {
		t.Errorf("want first provider id=2 after reverse, got %v", pm["id"])
	}
}

func TestSession_Hooks_BeforeRequest_Defaults(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function () {});
	`}}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background())
	defer s.Close()

	dec, err := s.RunBeforeRequestHook(BeforeRequestInput{})
	if err != nil {
		t.Fatalf("RunBeforeRequestHook: %v", err)
	}
	if dec.Next || dec.Delay != 0 {
		t.Errorf("want defaults, got %+v", dec)
	}
}

func TestSession_Hooks_BeforeRequest_NextAndDelay(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{{ID: "a", Source: `
		picotera.hooks.beforeRequest.tap("a", function () { return { next: true, delay: 100 }; });
	`}}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background())
	defer s.Close()

	dec, err := s.RunBeforeRequestHook(BeforeRequestInput{})
	if err != nil {
		t.Fatalf("RunBeforeRequestHook: %v", err)
	}
	if !dec.Next || dec.Delay != 100 {
		t.Errorf("want {next:true, delay:100}, got %+v", dec)
	}
}

func TestSession_Hooks_Rewrite_BodyObjectStringified(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{{ID: "a", Source: `
		picotera.hooks.rewriteRequest.tap("a", function (ctx) {
			return { body: { hello: "world" } };
		});
	`}}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background())
	defer s.Close()

	out, err := s.RunRewriteHook(RewriteInput{
		UpstreamRequest: UpstreamRequestShape{URL: "https://x", Method: "POST"},
	})
	if err != nil {
		t.Fatalf("RunRewriteHook: %v", err)
	}
	if string(out.Body) != `"{\"hello\":\"world\"}"` {
		t.Errorf("want json-stringified body, got %s", string(out.Body))
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/jsx/ -run TestSession_Hooks -v`
Expected: 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/jsx/
git commit -m "feat(jsx): hook entrypoints for sort/before/rewrite"
```

---

### Task 9: Helpers — `picotera.fetch`, `setTimeout`/`clearTimeout`, `console.*`

**Goal:** Register host helpers needed by user scripts. Wire them through Go and into JS via `RegisterFunc` + a thin SDK wrapper.

**Files:**
- Create: `pkg/jsx/helpers.go`
- Modify: `pkg/jsx/sdk.js`
- Modify: `pkg/jsx/session.go` (call helpers registrar in newSession)
- Modify: `pkg/jsx/engine_test.go`

**Acceptance Criteria:**
- [ ] `picotera.fetch(url, init?)` returns a Promise resolving to `{ status, headers, body }` (body as string). Uses Go `http.Client` with 5s timeout per call. No allowlist.
- [ ] `setTimeout(fn, ms)` returns an integer id; `clearTimeout(id)` cancels.
- [ ] `console.log/error/warn/info` writes via `logx` with fields `script_id="*"` (we don't track which script logged — leave `"*"`) and `request_id` if available on the session
- [ ] Tests for each helper

**Verify:** `go test ./pkg/jsx/ -run TestSession_Helpers -v`

**Steps:**

- [ ] **Step 1: Add `RequestID` field to Session**

In `Engine.NewSession`, accept `requestID string`:

```go
// engine.go
func (e *Engine) NewSession(ctx context.Context, requestID string) (*Session, error) {
	return newSession(ctx, e, requestID)
}
```

In `session.go`, store it:

```go
type Session struct {
	engine    *Engine
	vm        *quickjs.VM
	closed    bool
	pending   map[int64]*pendingSlot
	requestID string

	timers   map[int64]*time.Timer
	timerSeq int64
}

func newSession(ctx context.Context, eng *Engine, requestID string) (*Session, error) {
	// ... existing body ...
	s.requestID = requestID
	s.timers = map[int64]*time.Timer{}
	registerHelpers(s)
	// ...
}
```

(Update Task 5/6/7/8 callers in tests to pass `""`.)

- [ ] **Step 2: Write `helpers.go`**

```go
// pkg/jsx/helpers.go
package jsx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"picotera/pkg/logx"
)

func registerHelpers(s *Session) {
	registerFetch(s)
	registerTimers(s)
	registerConsole(s)
}

var fetchClient = &http.Client{Timeout: 5 * time.Second}

func registerFetch(s *Session) {
	// JS-side wrapper turns picotera.fetch(url, init) into __picotera_fetch(url, init) via Promise.
	s.vm.RegisterFunc("__picotera_fetch", func(url, initJSON string) string {
		var init struct {
			Method  string              `json:"method"`
			Headers map[string]string   `json:"headers"`
			Body    string              `json:"body"`
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
			return errorJSON(err)
		}
		for k, v := range init.Headers {
			req.Header.Set(k, v)
		}
		resp, err := fetchClient.Do(req)
		if err != nil {
			return errorJSON(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		out := map[string]any{
			"status":  resp.StatusCode,
			"headers": resp.Header,
			"body":    string(body),
		}
		b, _ := json.Marshal(out)
		return string(b)
	}, false)
}

func errorJSON(err error) string {
	b, _ := json.Marshal(map[string]any{"__error": err.Error()})
	return string(b)
}

func registerTimers(s *Session) {
	s.vm.RegisterFunc("__picotera_setTimeout", func(callbackID int64, ms int64) int64 {
		id := atomic.AddInt64(&s.timerSeq, 1)
		t := time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
			// fire by evaluating the dispatch
			_, _ = s.vm.Eval(fmt.Sprintf("globalThis.__picotera_fireTimer(%d)", callbackID), 0)
		})
		s.timers[id] = t
		return id
	}, false)
	s.vm.RegisterFunc("__picotera_clearTimeout", func(id int64) {
		if t, ok := s.timers[id]; ok {
			t.Stop()
			delete(s.timers, id)
		}
	}, false)
}

func registerConsole(s *Session) {
	emit := func(level, msg string) {
		entry := logx.WithField("script_id", "*").WithField("request_id", s.requestID)
		switch level {
		case "error":
			entry.Error(msg)
		case "warn":
			entry.Warn(msg)
		case "info":
			entry.Info(msg)
		default:
			entry.Info(msg)
		}
	}
	s.vm.RegisterFunc("__picotera_console", func(level, msg string) {
		emit(level, msg)
	}, false)

	_ = bytes.MinRead // silence unused if we drop bytes import later
}
```

(Confirm `pkg/logx` exposes `WithField`. If not, adapt.)

- [ ] **Step 3: Add SDK wrappers in `sdk.js`**

Add inside the IIFE, after the Waterfall setup:

```js
// console
const consoleEmit = (lvl) => (...args) => {
  globalThis.__picotera_console(lvl, args.map(String).join(' '))
}
globalThis.console = {
  log: consoleEmit('info'),
  info: consoleEmit('info'),
  warn: consoleEmit('warn'),
  error: consoleEmit('error'),
}

// timers
const __timerCallbacks = new Map()
let __timerNext = 1
globalThis.setTimeout = function (fn, ms) {
  const cbId = __timerNext++
  __timerCallbacks.set(cbId, fn)
  const id = globalThis.__picotera_setTimeout(cbId, ms | 0)
  return id
}
globalThis.clearTimeout = function (id) {
  globalThis.__picotera_clearTimeout(id)
}
globalThis.__picotera_fireTimer = function (cbId) {
  const fn = __timerCallbacks.get(cbId)
  if (fn) {
    __timerCallbacks.delete(cbId)
    try { fn() } catch (_e) {}
  }
}

// fetch
globalThis.picotera = globalThis.picotera || {}
globalThis.picotera.fetch = function (url, init) {
  return new Promise((resolve) => {
    const initJSON = init ? JSON.stringify(init) : ''
    const out = globalThis.__picotera_fetch(String(url), initJSON)
    const parsed = JSON.parse(out)
    resolve(parsed)
  })
}
```

Note: the picotera.fetch wrapper is synchronous-on-host (Go blocks); to keep JS non-blocking, wrap in Promise so user code can `await`. (This is intentional simplification — no concurrent fetches in v1.)

- [ ] **Step 4: Update existing tests for the new `NewSession` signature**

Replace all `eng.NewSession(context.Background())` with `eng.NewSession(context.Background(), "")` in `pkg/jsx/engine_test.go`.

- [ ] **Step 5: Add helper tests**

```go
// pkg/jsx/engine_test.go (append)
func TestSession_Helpers_Console(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", function (ctx) {
			console.log("hello", "world");
			return ctx;
		});
	`}}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background(), "req-123")
	defer s.Close()
	_, err := s.RunSortHook(SortInput{})
	if err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
	// log capture omitted; pass on no panic
}

func TestSession_Helpers_SetTimeout(t *testing.T) {
	store := &fakeStore{scripts: []db.Script{{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("a", async function (ctx) {
			await new Promise(r => setTimeout(r, 5));
			return ctx;
		});
	`}}}
	eng := NewEngine(Config{HookTimeout: time.Second}, store)
	s, _ := eng.NewSession(context.Background(), "")
	defer s.Close()
	if _, err := s.RunSortHook(SortInput{}); err != nil {
		t.Fatalf("RunSortHook: %v", err)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./pkg/jsx/ -v`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/jsx/
git commit -m "feat(jsx): console, setTimeout/clearTimeout, picotera.fetch helpers"
```

---

### Task 10: Syntax validation on submit

**Goal:** `ValidateSyntax(source string) error` boots a throwaway VM and runs `Eval`, returning a non-nil error on syntax errors.

**Files:**
- Create: `pkg/jsx/validate.go`
- Create: `pkg/jsx/validate_test.go`

**Acceptance Criteria:**
- [ ] `ValidateSyntax("var x = 1;")` returns nil
- [ ] `ValidateSyntax("var x = ;")` returns non-nil error
- [ ] `ValidateSyntax("undefined_func();")` returns nil (runtime errors are not caught at submit time, per spec)

**Verify:** `go test ./pkg/jsx/ -run TestValidateSyntax -v`

**Steps:**

- [ ] **Step 1: Write `validate.go`**

```go
// pkg/jsx/validate.go
package jsx

import (
	"fmt"
	"strings"

	"modernc.org/quickjs"
)

// ValidateSyntax checks JS source for syntax errors.
// Runtime errors are NOT caught; only parse-time errors fail validation.
func ValidateSyntax(source string) error {
	vm, err := quickjs.NewVM()
	if err != nil {
		return fmt.Errorf("jsx: NewVM: %w", err)
	}
	defer vm.Close()
	// Wrap in a never-called function so undefined references at runtime don't fire.
	wrapped := "(function(){" + source + "\n})"
	if _, err := vm.Eval(wrapped, quickjs.EvalGlobal); err != nil {
		// QuickJS reports SyntaxError vs other errors via the message text.
		if strings.Contains(err.Error(), "SyntaxError") || strings.Contains(strings.ToLower(err.Error()), "syntax") {
			return fmt.Errorf("syntax error: %w", err)
		}
		return fmt.Errorf("syntax error: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Write tests**

```go
// pkg/jsx/validate_test.go
package jsx

import "testing"

func TestValidateSyntax_Valid(t *testing.T) {
	if err := ValidateSyntax("var x = 1;"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateSyntax_BadSyntax(t *testing.T) {
	if err := ValidateSyntax("var x = ;"); err == nil {
		t.Fatalf("want error, got nil")
	}
}

func TestValidateSyntax_RuntimeReferenceAllowed(t *testing.T) {
	// Runtime errors are not caught by validation per spec.
	if err := ValidateSyntax("undefined_func();"); err != nil {
		t.Fatalf("want nil (runtime not validated), got %v", err)
	}
}
```

- [ ] **Step 3: Run**

Run: `go test ./pkg/jsx/ -run TestValidateSyntax -v`
Expected: 3 PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/jsx/validate.go pkg/jsx/validate_test.go
git commit -m "feat(jsx): syntax validation for script submissions"
```

---

### Task 11: Script CRUD contract

**Goal:** Define the Huma operations and view types for `/scripts`.

**Files:**
- Create: `pkg/contract/script.go`

**Acceptance Criteria:**
- [ ] `ScriptView` with `id`, `name`, `source`, `enabled`, `createdAt`, `updatedAt` (RFC3339 strings)
- [ ] `ListScriptsResponse`, `GetScriptRequest`, `GetScriptResponse`
- [ ] `CreateScriptRequest` (no id; server generates xid), `CreateScriptResponse`
- [ ] `UpdateScriptRequest` (id in path), `UpdateScriptResponse`
- [ ] `DeleteScriptRequest` (id in body, matching existing convention)
- [ ] Operation declarations: `OperationListScripts`, `OperationGetScript`, `OperationCreateScript`, `OperationUpdateScript`, `OperationDeleteScript`
- [ ] `ToScriptView(*db.Script) *ScriptView`

**Verify:** `go build ./...`

**Steps:**

- [ ] **Step 1: Write `pkg/contract/script.go`**

```go
package contract

import (
	"net/http"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

type ScriptView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func ToScriptView(s *db.Script) *ScriptView {
	v := &ScriptView{
		ID:      s.ID,
		Name:    s.Name,
		Source:  s.Source,
		Enabled: s.Enabled,
	}
	if s.CreatedAt.Valid {
		v.CreatedAt = s.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if s.UpdatedAt.Valid {
		v.UpdatedAt = s.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return v
}

type ListScriptsResponse struct {
	Body []ScriptView
}

type GetScriptRequest struct {
	ID string `path:"id"`
}
type GetScriptResponse struct{ Body ScriptView }

type CreateScriptRequest struct {
	Body struct {
		Name    string `json:"name"`
		Source  string `json:"source"`
		Enabled bool   `json:"enabled"`
	}
}
type CreateScriptResponse struct{ Body ScriptView }

type UpdateScriptRequest struct {
	ID   string `path:"id"`
	Body struct {
		Name    string `json:"name"`
		Source  string `json:"source"`
		Enabled bool   `json:"enabled"`
	}
}
type UpdateScriptResponse struct{ Body ScriptView }

type DeleteScriptRequest struct {
	Body struct {
		ID string `json:"id"`
	}
}

var OperationListScripts = huma.Operation{
	OperationID: "listScripts",
	Method:      http.MethodGet,
	Path:        "/scripts",
	Summary:     "List all scripts",
}

var OperationGetScript = huma.Operation{
	OperationID: "getScript",
	Method:      http.MethodGet,
	Path:        "/scripts/{id}",
	Summary:     "Get a script",
}

var OperationCreateScript = huma.Operation{
	OperationID: "createScript",
	Method:      http.MethodPost,
	Path:        "/scripts",
	Summary:     "Create a script",
}

var OperationUpdateScript = huma.Operation{
	OperationID: "updateScript",
	Method:      http.MethodPut,
	Path:        "/scripts/{id}",
	Summary:     "Update a script",
}

var OperationDeleteScript = huma.Operation{
	OperationID: "deleteScript",
	Method:      http.MethodPost,
	Path:        "/scripts/delete",
	Summary:     "Delete a script",
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add pkg/contract/script.go
git commit -m "feat(contract): script CRUD operations and views"
```

---

### Task 12: Script CRUD handlers (with syntax validation)

**Goal:** Implement the five handlers, calling `jsx.ValidateSyntax` on Create/Update and rejecting with HTTP 400 on syntax error.

**Files:**
- Create: `pkg/server/handle_script.go`
- Modify: `pkg/server/server.go` (register operations)

**Acceptance Criteria:**
- [ ] `handleListScripts`, `handleGetScript`, `handleCreateScript`, `handleUpdateScript`, `handleDeleteScript`
- [ ] Create generates a server-side `xid.New().String()` for the id
- [ ] Create + Update both call `jsx.ValidateSyntax`; on error return `huma.Error400BadRequest("invalid script syntax", err)`
- [ ] Get returns 404 on `pgx.ErrNoRows`
- [ ] All five operations registered in `registerOperations()`

**Verify:** `go build ./... && curl -X POST http://localhost:9898/api/picotera/scripts -d '{"name":"t","source":"var x =;","enabled":true}' -H 'Content-Type: application/json'` → 400. With `var x = 1;` → 200.

**Steps:**

- [ ] **Step 1: Write `handle_script.go`**

```go
package server

import (
	"context"
	"errors"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"
)

func (s *Server) handleListScripts(ctx context.Context, _ *struct{}) (*contract.ListScriptsResponse, error) {
	rows, err := s.queries.ListScripts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list scripts", err)
	}
	out := make([]contract.ScriptView, len(rows))
	for i, r := range rows {
		out[i] = *contract.ToScriptView(&r)
	}
	return &contract.ListScriptsResponse{Body: out}, nil
}

func (s *Server) handleGetScript(ctx context.Context, in *contract.GetScriptRequest) (*contract.GetScriptResponse, error) {
	r, err := s.queries.GetScript(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("script not found")
		}
		return nil, huma.Error500InternalServerError("failed to get script", err)
	}
	return &contract.GetScriptResponse{Body: *contract.ToScriptView(&r)}, nil
}

func (s *Server) handleCreateScript(ctx context.Context, in *contract.CreateScriptRequest) (*contract.CreateScriptResponse, error) {
	if err := jsx.ValidateSyntax(in.Body.Source); err != nil {
		return nil, huma.Error400BadRequest("invalid script syntax", err)
	}
	r, err := s.queries.InsertScript(ctx, db.InsertScriptParams{
		ID:      xid.New().String(),
		Name:    in.Body.Name,
		Source:  in.Body.Source,
		Enabled: in.Body.Enabled,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create script", err)
	}
	return &contract.CreateScriptResponse{Body: *contract.ToScriptView(&r)}, nil
}

func (s *Server) handleUpdateScript(ctx context.Context, in *contract.UpdateScriptRequest) (*contract.UpdateScriptResponse, error) {
	if err := jsx.ValidateSyntax(in.Body.Source); err != nil {
		return nil, huma.Error400BadRequest("invalid script syntax", err)
	}
	r, err := s.queries.UpdateScript(ctx, db.UpdateScriptParams{
		ID:      in.ID,
		Name:    in.Body.Name,
		Source:  in.Body.Source,
		Enabled: in.Body.Enabled,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("script not found")
		}
		return nil, huma.Error500InternalServerError("failed to update script", err)
	}
	return &contract.UpdateScriptResponse{Body: *contract.ToScriptView(&r)}, nil
}

func (s *Server) handleDeleteScript(ctx context.Context, in *contract.DeleteScriptRequest) (*struct{}, error) {
	if err := s.queries.DeleteScript(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete script", err)
	}
	return &struct{}{}, nil
}
```

- [ ] **Step 2: Register in `server.go`**

In `registerOperations()`, append after the request operations:

```go
huma.Register(mgmt, contract.OperationListScripts, s.handleListScripts)
huma.Register(mgmt, contract.OperationGetScript, s.handleGetScript)
huma.Register(mgmt, contract.OperationCreateScript, s.handleCreateScript)
huma.Register(mgmt, contract.OperationUpdateScript, s.handleUpdateScript)
huma.Register(mgmt, contract.OperationDeleteScript, s.handleDeleteScript)
```

- [ ] **Step 3: Build and smoke-test**

Run: `go build ./...`
Expected: success.

Run: `docker compose up -d && go run ./cmd/picotera/main.go &`
Wait for `serving API`. Then in another terminal:

```bash
curl -s -X POST http://localhost:9898/api/picotera/scripts \
  -H 'Content-Type: application/json' \
  -d '{"name":"bad","source":"var x =;","enabled":true}' -o /dev/null -w "%{http_code}\n"
```
Expected: `400`.

```bash
curl -s -X POST http://localhost:9898/api/picotera/scripts \
  -H 'Content-Type: application/json' \
  -d '{"name":"ok","source":"picotera.hooks.sortProviders.tap(\"x\", function(c){return c;});","enabled":true}'
```
Expected: 200, returns the new ScriptView with `id` populated.

```bash
curl -s http://localhost:9898/api/picotera/scripts | jq
```
Expected: array containing the inserted script.

Kill server.

- [ ] **Step 4: Commit**

```bash
git add pkg/server/handle_script.go pkg/server/server.go
git commit -m "feat(server): script crud with syntax validation"
```

---

### Task 13: Wire Engine into Server boot

**Goal:** Construct `*jsx.Engine` in `NewServer` and store it on `*Server`. The gateway handler uses it.

**Files:**
- Modify: `pkg/server/server.go`

**Acceptance Criteria:**
- [ ] `Server` struct gains `jsxEngine *jsx.Engine`
- [ ] `NewServer` constructs the engine after `queries` is built, passing config
- [ ] Build succeeds

**Verify:** `go build ./...`

**Steps:**

- [ ] **Step 1: Add field**

```go
// pkg/server/server.go
type Server struct {
	queries    *db.Queries
	router     *chi.Mux
	api        huma.API
	config     *configx.Config
	httpClient *http.Client
	artifacts  artifacts.Sink
	jsxEngine  *jsx.Engine
}
```

Add `import "picotera/pkg/jsx"`.

- [ ] **Step 2: Construct engine in `NewServer`**

After `queries := db.New(conn)`:

```go
jsxEngine := jsx.NewEngine(jsx.Config{
	HookTimeout:      config.JSHookTimeout,
	MemoryLimit:      config.JSMemoryLimit,
	MaxTotalAttempts: config.JSMaxTotalAttempts,
	MaxDelay:         config.JSMaxDelay,
}, queries)
```

Pass it into the struct literal:

```go
server := &Server{config: config, queries: queries, router: router, api: api, httpClient: httpClient, artifacts: sink, jsxEngine: jsxEngine}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add pkg/server/server.go
git commit -m "feat(server): wire jsx engine into server boot"
```

---

### Task 14: Gateway integration — call hooks in retry loop

**Goal:** Replace the existing for-range provider loop with the spec's index-based loop. Call `RunSortHook` once before the loop, `RunBeforeRequestHook` on each iteration, and `RunRewriteHook` between `buildUpstreamRequest` and `forwardRequest`. Honor `Next`, `Delay`, and `MaxTotalAttempts`. Failures of any hook fail the meta request with the appropriate code.

**Files:**
- Modify: `pkg/server/handle_gateway.go`
- Modify: `pkg/server/gateway_helpers.go` (add `lookupModel` helper that returns `*db.Model` or nil)

**Acceptance Criteria:**
- [ ] Session is created after `resolveProviders` and `model` lookup; `defer session.Close()` immediately after construction
- [ ] If session creation fails (script eval error / VM init error) → 502 with the error text in `error_message`
- [ ] `RunSortHook` is called once; on error → 502
- [ ] Loop variables: `i := 0`, `currentRetryCount := 0`, `totalAttemptCount := 0`, `lastError *jsx.LastError = nil`
- [ ] On each iteration: if `i >= len(providers)` → 502 NO_PROVIDER_AVAILABLE; if `totalAttemptCount >= cfg.MaxTotalAttempts` → 502
- [ ] `RunBeforeRequestHook` called every iteration (including after a failed attempt on the same provider)
- [ ] On `Delay > 0` → `time.Sleep(min(delay, MaxDelay))` ms
- [ ] On `Next == true` → `i++; currentRetryCount = 0; continue`
- [ ] After `buildUpstreamRequest`, call `RunRewriteHook` and apply `URL`/`Method`/`Headers`/`Body` overrides; if Body changed, also update `req.ContentLength`
- [ ] On 200 → stream as before; on non-200 → set `lastError = &jsx.LastError{ProviderID: ..., StatusCode: resp.StatusCode, Message: string(respBody)}`, increment `currentRetryCount` and `totalAttemptCount`, continue
- [ ] On hook timeout → 503 with the message `hook timeout`
- [ ] On any other hook error → 502 with the error text

**Verify:**
- [ ] Manual: with no scripts present, gateway behavior is unchanged from baseline (one request through providers in priority order).
- [ ] With a script that taps `sortProviders` and reverses, observable order changes.

**Steps:**

- [ ] **Step 1: Add `lookupModel` helper**

```go
// pkg/server/gateway_helpers.go (append)

// lookupModel returns the model row by name, or nil if not found.
// Errors other than "not found" are logged and treated as nil — model metadata
// is non-essential to routing.
func (s *Server) lookupModel(ctx context.Context, name string) *db.Model {
	m, err := s.queries.GetModelByName(ctx, name)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logx.WithContext(ctx).WithError(err).WithField("model", name).Warn("lookup model failed")
		}
		return nil
	}
	return &m
}
```

- [ ] **Step 2: Rewrite the retry loop in `handle_gateway.go`**

Open `pkg/server/handle_gateway.go`. After step "7. Resolve providers" (line 138) and before step "8. Try each provider with failover" (line 141), insert session setup. Then replace the entire `for _, provider := range providers { ... }` block plus the post-loop fallthrough error block (lines 142–337) with the new logic.

The replacement (full body, well-commented):

```go
	// 8. Build jsx session for this request
	model := h.lookupModel(r.Context(), modelName /* extracted earlier */)
	session, err := h.jsxEngine.NewSession(r.Context(), metaID)
	if err != nil {
		errMsg := "failed to load js hooks: " + err.Error()
		failMeta(http.StatusBadGateway, errMsg)
		respBody := writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
		h.uploadResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody)
		return
	}
	defer session.Close()

	// 8a. Build candidate list and an upstream URL sidecar map.
	candidates := make([]jsx.Candidate, 0, len(providers))
	upstreamURLs := make(map[int32]string, len(providers))
	providerByID := make(map[int32]db.Provider, len(providers))
	for _, row := range providers {
		// fetch full provider once; cache by ID
		if _, ok := providerByID[row.ProviderID]; !ok {
			full, err := h.queries.GetProviderByID(r.Context(), row.ProviderID)
			if err == nil {
				providerByID[row.ProviderID] = full
			}
		}
		provFull := providerByID[row.ProviderID]
		candidates = append(candidates, jsx.Candidate{
			Provider: provFull,
			MPE: db.ModelProviderEndpoint{
				ModelName:         row.ModelName,
				ProviderID:        row.ProviderID,
				EndpointPath:      row.EndpointPath,
				UpstreamModelName: row.UpstreamModelName,
				Priority:          row.Priority,
				Annotations:       row.Annotations,
			},
		})
		upstreamURLs[row.ProviderID] = row.UpstreamUrl.String
	}

	// 8b. Build the JS request shape (read-only view of the client request).
	jsRequest := jsx.RequestShape{
		Path:    r.URL.Path,
		Method:  r.Method,
		Headers: r.Header.Clone(),
		Body:    string(body),
		Model:   modelName,
	}

	// 8c. Run sortProviders.
	sortedCandidates, err := session.RunSortHook(jsx.SortInput{
		Endpoint:  endpoint,
		Model:     model,
		Request:   jsRequest,
		Providers: candidates,
	})
	if err != nil {
		failHookErr(h, w, bgCtx, metaID, metaCreatedAt, gatewayStart, err, failMeta)
		return
	}

	// 8d. Retry loop — index-based.
	var lastErr error
	var lastJSErr *jsx.LastError
	i := 0
	currentRetryCount := 0
	totalAttemptCount := 0

	for {
		if i >= len(sortedCandidates) {
			break
		}
		if totalAttemptCount >= h.config.JSMaxTotalAttempts {
			break
		}
		cand := sortedCandidates[i]

		// Extract DB rows back from JS-marshaled candidate. Both fields are db.* structs
		// originally; after JSON roundtrip they survive as the same shape.
		provJSON, _ := json.Marshal(cand.Provider)
		var prov db.Provider
		_ = json.Unmarshal(provJSON, &prov)
		mpeJSON, _ := json.Marshal(cand.MPE)
		var mpe db.ModelProviderEndpoint
		_ = json.Unmarshal(mpeJSON, &mpe)

		dec, err := session.RunBeforeRequestHook(jsx.BeforeRequestInput{
			Endpoint:          endpoint,
			Model:             model,
			Request:           jsRequest,
			Provider:          prov,
			MPE:               mpe,
			CurrentRetryCount: currentRetryCount,
			TotalAttemptCount: totalAttemptCount,
			LastError:         lastJSErr,
		})
		if err != nil {
			failHookErr(h, w, bgCtx, metaID, metaCreatedAt, gatewayStart, err, failMeta)
			return
		}

		if dec.Delay > 0 {
			d := time.Duration(dec.Delay) * time.Millisecond
			if d > h.config.JSMaxDelay {
				d = h.config.JSMaxDelay
			}
			time.Sleep(d)
		}
		if dec.Next {
			i++
			currentRetryCount = 0
			continue
		}

		// Build upstream request
		attemptStart := time.Now()
		ctx, cancel := context.WithCancel(r.Context())

		upstreamID := xid.New().String()
		upstreamCreatedAt := h.insertRequest(bgCtx, db.InsertRequestParams{
			ID:           upstreamID,
			SpanID:       pgtype.Text{String: metaID, Valid: true},
			ParentSpanID: pgtype.Text{Valid: false},
			Type:         db.RequestTypeUpstream,
			Status:       db.RequestStatusPending,
			ProviderID:   pgtype.Int4{Int32: prov.ID, Valid: true},
			EndpointPath: pgtype.Text{String: endpoint.Path, Valid: true},
			ApiKeyID:     pgtype.Int4{Valid: false},
			Model:        pgtype.Text{String: modelName, Valid: true},
			StatusCode:   pgtype.Int4{Valid: false},
			ErrorMessage: pgtype.Text{Valid: false},
			TimeSpentMs:  pgtype.Int4{Valid: false},
		})

		upstreamModel := ""
		if mpe.UpstreamModelName.Valid {
			upstreamModel = mpe.UpstreamModelName.String
		}
		upstreamURL := upstreamURLs[prov.ID]
		req, reqBody, err := buildUpstreamRequest(ctx, r, body, upstreamURL, upstreamModel, prov.Credentials, authTyp)
		if err != nil {
			cancel()
			h.completeFailedAttempt(bgCtx, upstreamID, attemptStart, 0, err.Error())
			lastErr = err
			lastJSErr = &jsx.LastError{ProviderID: int(prov.ID), StatusCode: 0, Message: err.Error()}
			currentRetryCount++
			totalAttemptCount++
			continue
		}

		// Run rewriteRequest hook
		rw, err := session.RunRewriteHook(jsx.RewriteInput{
			Endpoint:          endpoint,
			Model:             model,
			Request:           jsRequest,
			Provider:          prov,
			MPE:               mpe,
			CurrentRetryCount: currentRetryCount,
			TotalAttemptCount: totalAttemptCount,
			UpstreamRequest: jsx.UpstreamRequestShape{
				URL:     req.URL.String(),
				Method:  req.Method,
				Headers: req.Header.Clone(),
				Body:    string(reqBody),
			},
			ClientRequest: jsRequest,
		})
		if err != nil {
			cancel()
			failHookErr(h, w, bgCtx, metaID, metaCreatedAt, gatewayStart, err, failMeta)
			return
		}
		if rw.URL != nil {
			parsed, perr := http.NewRequestWithContext(ctx, req.Method, *rw.URL, nil)
			if perr == nil {
				req.URL = parsed.URL
				req.Host = parsed.Host
			}
		}
		if rw.Method != nil {
			req.Method = *rw.Method
		}
		if rw.Headers != nil {
			req.Header = http.Header{}
			for k, vv := range *rw.Headers {
				for _, v := range vv {
					req.Header.Add(k, v)
				}
			}
		}
		if len(rw.Body) > 0 {
			// rw.Body is a JSON-encoded value; if it's a JSON string, unwrap it.
			var asString string
			if jerr := json.Unmarshal(rw.Body, &asString); jerr == nil {
				reqBody = []byte(asString)
			} else {
				reqBody = []byte(rw.Body)
			}
			req.Body = io.NopCloser(bytes.NewReader(reqBody))
			req.ContentLength = int64(len(reqBody))
		}

		// Upload upstream request artifact
		h.uploadRequestArtifact(bgCtx, upstreamID, upstreamCreatedAt, req.Method, req.URL.String(), req.Header.Clone(), reqBody)

		upstreamStartTime := time.Now()
		resp, err := h.forwardRequest(req)
		if err != nil {
			cancel()
			h.completeFailedAttempt(bgCtx, upstreamID, attemptStart, 0, err.Error())
			lastErr = err
			lastJSErr = &jsx.LastError{ProviderID: int(prov.ID), StatusCode: 0, Message: err.Error()}
			currentRetryCount++
			totalAttemptCount++
			continue
		}

		if resp.StatusCode == http.StatusOK {
			// (existing success-path code: backfill header, stream, capture metrics, commit DB rows)
			// ... reuse existing block verbatim from old loop ...
			// On success → return.
			// IMPORTANT: copy the old success block unchanged here, replacing `provider` with `prov`
			// and `provider.ProviderID` with `prov.ID`, and `provider.UpstreamUrl.String` is no longer needed.
			handleSuccess200(h, w, r, ctx, cancel, resp, upstreamID, upstreamCreatedAt, attemptStart, metaID, metaCreatedAt, gatewayStart, prov.ID, modelName, endpoint.Path, upstreamStartTime, bgCtx)
			return
		}

		// Non-200 → record + try next iteration on same provider
		cancel()
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		h.uploadResponseArtifact(bgCtx, upstreamID, upstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), respBody)
		errMsg := string(respBody)
		h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
			ID:           upstreamID,
			StatusCode:   pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
			ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
			TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(attemptStart).Milliseconds()), Valid: true},
			Status:       db.RequestStatusFailed,
		})
		lastErr = fmt.Errorf("upstream returned %d: %s", resp.StatusCode, errMsg)
		lastJSErr = &jsx.LastError{ProviderID: int(prov.ID), StatusCode: resp.StatusCode, Message: errMsg}
		currentRetryCount++
		totalAttemptCount++
	}

	// 9. All providers exhausted (or attempts cap reached) — fail meta
	errMsg := "all providers failed"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	failMeta(http.StatusBadGateway, errMsg)
	respBody := writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
	h.uploadResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody)
}
```

Notes for the implementer:
- The `modelName` variable in the new block corresponds to the `model` string variable in the existing code (line 115) — rename if needed (variable name was just `model`; rename to `modelName` to avoid shadowing the new `model *db.Model`).
- Add helper `completeFailedAttempt` and `handleSuccess200` to keep the loop body short (next sub-step).
- Add a helper `failHookErr` that classifies `jsx.ErrHookTimeout` → 503; everything else → 502.

- [ ] **Step 3: Add helpers**

In `gateway_helpers.go`:

```go
func (s *Server) completeFailedAttempt(ctx context.Context, upstreamID string, attemptStart time.Time, statusCode int32, errMsg string) {
	s.updateRequestOnComplete(ctx, db.UpdateRequestOnCompleteParams{
		ID:           upstreamID,
		StatusCode:   pgtype.Int4{Int32: statusCode, Valid: true},
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
		TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(attemptStart).Milliseconds()), Valid: true},
		Status:       db.RequestStatusFailed,
	})
}
```

In `handle_gateway.go` (top-level):

```go
func failHookErr(
	h *gatewayHandler, w http.ResponseWriter, bgCtx context.Context,
	metaID string, metaCreatedAt time.Time, gatewayStart time.Time,
	err error,
	failMeta func(int32, string),
) {
	status := http.StatusBadGateway
	if errors.Is(err, jsx.ErrHookTimeout) {
		status = http.StatusServiceUnavailable
	}
	errMsg := err.Error()
	failMeta(int32(status), errMsg)
	respBody := writeGatewayError(w, status, errMsg, errorx.UpstreamError.Error())
	h.uploadResponseArtifact(bgCtx, metaID, metaCreatedAt, status, w.Header().Clone(), respBody)
}

// handleSuccess200 is the original success block extracted into a helper.
// Copy the body of the existing `if resp.StatusCode == http.StatusOK { ... return }` block
// verbatim, replacing the captured variables with parameters.
func handleSuccess200(
	h *gatewayHandler, w http.ResponseWriter, r *http.Request,
	ctx context.Context, cancel context.CancelFunc, resp *http.Response,
	upstreamID string, upstreamCreatedAt time.Time, attemptStart time.Time,
	metaID string, metaCreatedAt time.Time, gatewayStart time.Time,
	providerID int32, modelName, endpointPath string,
	upstreamStartTime time.Time,
	bgCtx context.Context,
) {
	// header backfill
	h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
		ID:           metaID,
		ProviderID:   pgtype.Int4{Int32: providerID, Valid: true},
		Model:        pgtype.Text{String: modelName, Valid: true},
		EndpointPath: pgtype.Text{String: endpointPath, Valid: true},
		ApiKeyID:     pgtype.Int4{Valid: false},
		Status:       db.RequestStatusHeaderReceived,
	})
	h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
		ID:           upstreamID,
		ProviderID:   pgtype.Int4{Int32: providerID, Valid: true},
		Model:        pgtype.Text{String: modelName, Valid: true},
		EndpointPath: pgtype.Text{String: endpointPath, Valid: true},
		ApiKeyID:     pgtype.Int4{Valid: false},
		Status:       db.RequestStatusHeaderReceived,
	})

	for key, values := range resp.Header {
		if strings.ToLower(key) == "content-length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	metaRespHeader := w.Header().Clone()
	w.WriteHeader(http.StatusOK)

	extractor := NewResponseExtractor(resp.Body, resp.Header.Get("Content-Type"), upstreamStartTime)
	reader := newIdleTimeoutReader(extractor, h.config.GatewayReadTimeout, cancel)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var captureBuf bytes.Buffer
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			captureBuf.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	cancel()
	resp.Body.Close()

	respBytes := captureBuf.Bytes()
	h.uploadResponseArtifact(bgCtx, upstreamID, upstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), respBytes)
	h.uploadResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusOK, metaRespHeader, respBytes)

	m := extractor.Metrics()
	ttftMs, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens := metricsToPG(m)

	upstreamTimeSpent := int32(time.Since(attemptStart).Milliseconds())
	h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
		ID:               upstreamID,
		StatusCode:       pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
		ErrorMessage:     pgtype.Text{Valid: false},
		TimeSpentMs:      pgtype.Int4{Int32: upstreamTimeSpent, Valid: true},
		Status:           db.RequestStatusCompleted,
		TtftMs:           ttftMs,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
	})

	metaTimeSpent := int32(time.Since(gatewayStart).Milliseconds())
	h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
		ID:               metaID,
		StatusCode:       pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
		ErrorMessage:     pgtype.Text{Valid: false},
		TimeSpentMs:      pgtype.Int4{Int32: metaTimeSpent, Valid: true},
		Status:           db.RequestStatusCompleted,
		TtftMs:           ttftMs,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
	})
}
```

Add imports: `"encoding/json"`, `"picotera/pkg/jsx"`, ensure `bytes`/`io`/`strings` already imported.

- [ ] **Step 4: Build and smoke-test**

Run: `go build ./...`
Expected: success.

Run server. With no scripts in DB, send a baseline gateway request to a configured endpoint. Confirm it routes through providers as before.

Insert a script that taps `sortProviders` and reverses the array (using `curl` from Task 12). Send another gateway request. Confirm via `request` table that the `provider_id` chosen is the lowest-priority one (i.e. order was reversed).

Insert a script that taps `beforeRequest` and returns `{ next: true }`. Send a request; confirm meta returns 502 NO_PROVIDER_AVAILABLE because all providers were skipped.

Disable the script and confirm baseline restored.

- [ ] **Step 5: Commit**

```bash
git add pkg/server/handle_gateway.go pkg/server/gateway_helpers.go
git commit -m "feat(gateway): integrate jsx hooks into retry loop"
```

---

### Task 15: Regenerate OpenAPI spec

**Goal:** Refresh `openapi.yaml` so the dashboard's typed client picks up Script operations.

**Files:**
- Modify: `openapi.yaml`

**Acceptance Criteria:**
- [ ] `openapi.yaml` contains paths `/scripts`, `/scripts/{id}`, `/scripts/delete`
- [ ] `ScriptView` schema present
- [ ] `git diff openapi.yaml` shows the new operations

**Verify:** `mise run openapi && grep -c '/scripts' openapi.yaml`

**Steps:**

- [ ] **Step 1: Regenerate**

Run: `mise run openapi`
Expected: `openapi.yaml` updated.

- [ ] **Step 2: Sanity-check**

Run: `grep '/scripts' openapi.yaml | head`
Expected: matches found.

- [ ] **Step 3: Commit**

```bash
git add openapi.yaml
git commit -m "chore: regenerate openapi.yaml for script operations"
```

---

### Task 16: Regenerate dashboard OpenAPI types

**Goal:** Refresh the dashboard's typed OpenAPI client so it knows about `/scripts` endpoints.

**Files:**
- Modify: `dashboard/src/api.d.ts` or `dashboard/src/openapi-types.ts` (whichever is the configured output)

**Acceptance Criteria:**
- [ ] Generated types include `paths['/api/picotera/scripts']`, `ScriptView`
- [ ] `pnpm --dir dashboard type-check` passes

**Verify:** `pnpm --dir dashboard type-check`

**Steps:**

- [ ] **Step 1: Find the generation command**

Run: `cat dashboard/package.json | grep -A2 scripts`
Look for an `openapi` / `gen:api` script. Run it. (If absent, run `pnpm --dir dashboard exec openapi-typescript ../openapi.yaml -o src/openapi-types.ts` — adjust output path to match `dashboard/src/api/plugin.ts`'s import.)

- [ ] **Step 2: Type-check**

Run: `pnpm --dir dashboard type-check`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add dashboard/
git commit -m "chore(dashboard): regenerate openapi types for scripts"
```

---

### Task 17: Dashboard — Scripts route + sidebar entry

**Goal:** Wire a `/scripts` route into the router and add a nav entry to the sidebar.

**Files:**
- Modify: `dashboard/src/router/index.ts`
- Modify: `dashboard/src/components/AppSidebar.vue`

**Acceptance Criteria:**
- [ ] Route `/scripts` lazy-loads `ScriptsView.vue`
- [ ] Sidebar shows a "脚本" (Scripts) entry with a sensible icon (e.g. `code` if present in `paths.ts`, otherwise `branch` or `cpu` as fallback)
- [ ] Active styling works

**Verify:** `pnpm --dir dashboard build` (vue-tsc + vite) succeeds; manually load `/scripts` in dev.

**Steps:**

- [ ] **Step 1: Add the route**

```ts
// dashboard/src/router/index.ts
{ path: '/scripts', name: 'scripts', component: () => import('@/views/ScriptsView.vue') },
```
(Place after `/requests`.)

- [ ] **Step 2: Check available icons**

Run: `grep "'code'" dashboard/src/ui/icons/paths.ts || echo "no code icon"`
If no `code` icon, use `'cpu'` and add a TODO to add an icon later.

- [ ] **Step 3: Add sidebar entry**

```ts
// dashboard/src/components/AppSidebar.vue (script setup, in `nav` array)
{ name: 'scripts', label: '脚本', icon: 'code' },
```

(Use the icon name confirmed in step 2.)

- [ ] **Step 4: Stub the view temporarily**

Create `dashboard/src/views/ScriptsView.vue` with placeholder so dev server doesn't 404:

```vue
<template>
  <div class="text-ink">Scripts coming soon.</div>
</template>
```

- [ ] **Step 5: Verify**

Run: `pnpm --dir dashboard build`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add dashboard/src/router/index.ts dashboard/src/components/AppSidebar.vue dashboard/src/views/ScriptsView.vue
git commit -m "feat(dashboard): add scripts route and sidebar entry"
```

---

### Task 18: Dashboard — ScriptForm component

**Goal:** Side-panel form for create/edit. Single textarea for `source`, name field, enabled toggle.

**Files:**
- Create: `dashboard/src/components/ScriptForm.vue`

**Acceptance Criteria:**
- [ ] Reuses `EndpointForm` pattern: `SidePanel`, `Field`, `Input`, `Textarea`, `Button`
- [ ] In edit mode, `id` is shown read-only at the top
- [ ] Enabled toggle uses an HTML `<input type="checkbox">` styled minimally (or `SegmentedControl` with on/off)
- [ ] Source uses `<Textarea>` with `rows=20` and `font-mono`
- [ ] Submit calls `POST /scripts` (create) or `PUT /scripts/{id}` (edit)
- [ ] On 400 (syntax error), shows `error.message` in the form's `#error` slot

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint`

**Steps:**

- [ ] **Step 1: Write the component**

```vue
<!-- dashboard/src/components/ScriptForm.vue -->
<script setup lang="ts">
import { ref } from 'vue'
import { useApi } from '@/composables/useApi'
import { SidePanel, Button, Input, Field, Textarea } from '@/ui'
import type { components } from '@/openapi-types'

type ScriptView = components['schemas']['ScriptView']

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{ script?: ScriptView; onSave?: () => void }>()
const api = useApi()

const isEdit = !!props.script
const form = ref({
  name: props.script?.name ?? '',
  source: props.script?.source ?? '',
  enabled: props.script?.enabled ?? true,
})
const saving = ref(false)
const error = ref('')

async function submit() {
  saving.value = true
  error.value = ''
  if (isEdit) {
    const { error: err } = await api.PUT('/api/picotera/scripts/{id}', {
      params: { path: { id: props.script!.id } },
      body: { name: form.value.name, source: form.value.source, enabled: form.value.enabled },
    })
    if (err) error.value = err.message ?? '操作失败'
    else { props.onSave?.(); emit('close') }
  } else {
    const { error: err } = await api.POST('/api/picotera/scripts', {
      body: { name: form.value.name, source: form.value.source, enabled: form.value.enabled },
    })
    if (err) error.value = err.message ?? '操作失败'
    else { props.onSave?.(); emit('close') }
  }
  saving.value = false
}
</script>

<template>
  <SidePanel
    :title="isEdit ? (form.name || '脚本') : '新增脚本'"
    :kicker="isEdit ? '编辑脚本' : '脚本'"
    @close="emit('close')"
  >
    <form id="script-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <Field v-if="isEdit" label="ID">
        <Input :model-value="props.script!.id" readonly />
      </Field>
      <Field label="名称">
        <Input v-model="form.name" required />
      </Field>
      <Field label="启用">
        <label class="inline-flex items-center gap-2">
          <input v-model="form.enabled" type="checkbox" class="rounded border-line" />
          <span class="text-sm text-ink-muted">enabled</span>
        </label>
      </Field>
      <Field label="源代码">
        <Textarea v-model="form.source" :rows="20" class="font-mono text-sm" required />
      </Field>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button type="submit" form="script-form" :disabled="saving">
        {{ saving ? '保存中…' : isEdit ? '更新' : '创建' }}
      </Button>
    </template>
  </SidePanel>
</template>
```

(Confirm the OpenAPI types path matches `EndpointForm`'s import — adjust if `@/api` is the indirection.)

- [ ] **Step 2: Type-check + lint**

Run: `pnpm --dir dashboard type-check && pnpm --dir dashboard lint`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/components/ScriptForm.vue
git commit -m "feat(dashboard): script form component"
```

---

### Task 19: Dashboard — ScriptsView list page

**Goal:** List page with new + edit + delete + enable toggle.

**Files:**
- Modify: `dashboard/src/views/ScriptsView.vue`

**Acceptance Criteria:**
- [ ] Mirrors `EndpointsView.vue` structure: count line, "新增脚本" button, `DataCard` + `DataTable`
- [ ] Columns: 名称 (name), 状态 (enabled tag), 创建时间, 更新时间, 操作 (edit/delete)
- [ ] "新增脚本" opens `ScriptForm`
- [ ] Row "编辑" opens `ScriptForm` with the row's data
- [ ] Row "删除" uses `useConfirm` and calls `POST /scripts/delete`
- [ ] Toggle column for enabled — clicking it issues a `PUT /scripts/{id}` with the flipped enabled value (other fields preserved). Use a `SegmentedControl` or a simple checkbox inside the row.

**Verify:** `pnpm --dir dashboard build`; manually verify in dev: create script, toggle enabled, edit, delete.

**Steps:**

- [ ] **Step 1: Replace the placeholder view**

```vue
<!-- dashboard/src/views/ScriptsView.vue -->
<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useConfirm } from '@/composables/useConfirm'
import { useApi } from '@/composables/useApi'
import { useSidePanel } from '@/composables/useSidePanel'
import ScriptForm from '@/components/ScriptForm.vue'
import {
  Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon,
} from '@/ui'
import type { components } from '@/openapi-types'

type ScriptView = components['schemas']['ScriptView']

const panel = useSidePanel()
const confirm = useConfirm()
const api = useApi()

const scripts = ref<ScriptView[]>([])
const loading = ref(true)
const count = computed(() => scripts.value.length)

async function fetchScripts() {
  loading.value = true
  const { data, error } = await api.GET('/api/picotera/scripts')
  if (!error && data) scripts.value = data as ScriptView[]
  loading.value = false
}
onMounted(fetchScripts)

function openCreate() {
  panel.open(ScriptForm, { onSave: fetchScripts }, { key: 'script:new' })
}
function openEdit(s: ScriptView) {
  panel.open(ScriptForm, { script: s, onSave: fetchScripts }, { key: `script:${s.id}` })
}
function confirmDelete(_e: Event, id: string) {
  confirm.require({
    message: `确定要删除脚本「${id}」吗？此操作不可撤销。`,
    accept: async () => {
      await api.POST('/api/picotera/scripts/delete', { body: { id } })
      fetchScripts()
    },
  })
}
async function toggle(s: ScriptView) {
  await api.PUT('/api/picotera/scripts/{id}', {
    params: { path: { id: s.id } },
    body: { name: s.name, source: s.source, enabled: !s.enabled },
  })
  fetchScripts()
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个脚本</span>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增脚本</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="scripts.length">
      <DataTable>
        <thead>
          <tr>
            <Th>名称</Th>
            <Th>状态</Th>
            <Th>创建时间</Th>
            <Th>更新时间</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr v-for="s in scripts" :key="s.id" :selected="panel.isActive(`script:${s.id}`)">
            <Td>
              <span class="font-medium">{{ s.name }}</span>
              <span class="block font-mono text-2xs text-ink-faint">{{ s.id }}</span>
            </Td>
            <Td>
              <button
                class="cursor-pointer"
                :title="s.enabled ? '已启用' : '已禁用'"
                @click="toggle(s)"
              >
                <Tag :variant="s.enabled ? 'ok' : 'muted'">
                  {{ s.enabled ? '启用' : '禁用' }}
                </Tag>
              </button>
            </Td>
            <Td><span class="font-mono text-ink-faint">{{ s.createdAt }}</span></Td>
            <Td><span class="font-mono text-ink-faint">{{ s.updatedAt }}</span></Td>
            <Td actions>
              <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                <IconButton :active="panel.isActive(`script:${s.id}`)" title="编辑" aria-label="编辑" @click="openEdit(s)">
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton variant="danger" title="删除" aria-label="删除" @click="(e: Event) => confirmDelete(e, s.id)">
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-else>暂无脚本，点击右上角按钮新增</StateText>
  </div>
</template>
```

- [ ] **Step 2: Build + lint**

Run: `pnpm --dir dashboard build && pnpm --dir dashboard lint`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/views/ScriptsView.vue
git commit -m "feat(dashboard): scripts list page with crud and enable toggle"
```

---

### Task 20: End-to-end smoke test

**Goal:** Verify the full stack: dashboard creates a script, the gateway picks it up on the next request, and the script's effects are observable.

**Files:** none modified

**Acceptance Criteria:**
- [ ] Server boots cleanly with the new migration
- [ ] Dashboard `/scripts` page lists/creates/toggles/deletes scripts
- [ ] A `sortProviders` reversing script measurably changes provider selection (verify in `request` table)
- [ ] A `beforeRequest` script returning `{ next: true }` causes 502 NO_PROVIDER_AVAILABLE
- [ ] A `rewriteRequest` script that overrides `headers.X-Test` is reflected in the upstream request artifact
- [ ] Disabling all scripts restores baseline behavior

**Verify:** Manual checklist below.

**Steps:**

- [ ] **Step 1: Boot the stack**

Run: `docker compose up -d && go run ./cmd/picotera/main.go &` (in one terminal) and `pnpm --dir dashboard dev` (in another).

- [ ] **Step 2: Configure baseline**

Through the dashboard, ensure at least: 2 providers (e.g. fake-1, fake-2 with priorities differing), 1 endpoint (e.g. `/v1/chat/completions`), 1 model, 2 mappings binding the model to both providers.

- [ ] **Step 3: Baseline gateway request**

```bash
curl -s -X POST http://localhost:9898/v1/chat/completions \
  -H 'Authorization: Bearer test' -H 'Content-Type: application/json' \
  -d '{"model":"<your-model>","messages":[{"role":"user","content":"hi"}]}'
```

In the dashboard's Requests view, confirm the request fired against the higher-priority provider.

- [ ] **Step 4: Reverse-sort script**

Through the dashboard, create a script:
```js
picotera.hooks.sortProviders.tap("reverse", function (ctx) {
  return ctx.providers.slice().reverse();
});
```
Send the same gateway request. Confirm it now hits the lower-priority provider.

- [ ] **Step 5: Skip-all script**

Disable the previous script. Create:
```js
picotera.hooks.beforeRequest.tap("skip", function () {
  return { next: true };
});
```
Send the request. Confirm 502 with all providers exhausted.

- [ ] **Step 6: Header rewrite**

Disable the previous. Create:
```js
picotera.hooks.rewriteRequest.tap("hdr", function (ctx) {
  const h = Object.assign({}, ctx.upstreamRequest.headers);
  h["X-Picotera-Test"] = ["1"];
  return { headers: h };
});
```
Send the request. Pull the upstream request artifact from the meta record's child upstream and confirm `X-Picotera-Test: 1` is present.

- [ ] **Step 7: Disable all**

Toggle all scripts off. Confirm the gateway runs as in Step 3 again.

- [ ] **Step 8: Commit nothing**

This task only verifies. If any step revealed a bug, fix it under the relevant earlier task and re-verify.

---

## Self-Review Notes

**Spec coverage check:**
- `script` table + index → Task 1
- `ListEnabledScripts`, ordering by id ASC → Task 2
- Validation on submit → Tasks 10, 12
- Tapable Waterfall (passthrough on undefined) → Tasks 5 (sdk.js)
- ctx.endpoint/model/request fields → Tasks 7, 8 (types.go)
- sortProviders return semantics (undefined keeps, empty drops, missing element drops) → Task 8 (RunSortHook unmarshal handles undefined-as-passthrough; empty array yields empty slice; explicit element absence is implicit because we trust the JS array order)
- beforeRequest defaults / next / delay → Task 8 (RunBeforeRequestHook); retry loop honors them → Task 14
- rewriteRequest field omission preserves; body object → JSON.stringify → Task 8
- async/await, Promise.all → Task 6 (microtask pump)
- picotera.fetch / setTimeout / console → Task 9
- Per-VM lifecycle: NewVM, SetMemoryLimit, SetEvalTimeout, Close on defer → Tasks 5, 13, 14
- Configuration env vars → Task 3
- Failure modes: JS throw → 502; timeout → 503; memory → 502 (Go side wraps as generic 502); MaxTotalAttempts → 502 → Task 14 (failHookErr)
- Code layout in spec maps 1:1 to Tasks 5–9, 11–12, 14, 17–19
- Out-of-scope items intentionally NOT addressed: scope binding, after-response, KV/Redis, bytecode caching, ES modules, dry-run, console-to-dashboard, fail_mode, fetch allowlist, body size limits.

**Placeholder scan:** None. Each task has concrete code or a directive ("copy the existing X verbatim, replacing Y").

**Type consistency:** `Candidate`, `RequestShape`, `BeforeRequestInput`, `RewriteOutput` are defined once in Task 7/8 and consumed unchanged in Task 14. `jsx.LastError` defined in Task 8, used in Task 14. `pendingSlot.value` typing changed deliberately in Task 7 (string after stringification).

**Known compromise — JSON marshaling of db.* structs into JS:** The implementer should verify that `db.Provider` (with `json` tags via sqlc `emit_json_tags: true`) round-trips through JSON cleanly. Fields like `ProviderModels []byte` will encode as base64 JSON strings. If this causes problems for user scripts, add a Provider→JS-friendly wrapper struct in `pkg/jsx/types.go` that decodes the bytes upfront. Out of scope for v1 unless found broken in Task 20.

---

## Revision (2026-04-26): JS engine swap to `github.com/fastschema/qjs`

A spike in Task 4 found that `modernc.org/quickjs` does **not expose** `JS_ExecutePendingJob`. Promise jobs cannot be drained from Go — Promises stay pending forever, async/await never settles, `setTimeout` callbacks never fire. The plan's microtask pump (Task 6) is therefore impossible against that library.

We have switched to **`github.com/fastschema/qjs`** (CGO-free, wazero-based QuickJS port) which has first-class async support. Per-VM startup is ~2–5ms warm (acceptable for per-request lifecycle).

### API differences (cheat-sheet)

| Old (`modernc.org/quickjs`)                    | New (`github.com/fastschema/qjs`)                                            |
|------------------------------------------------|-------------------------------------------------------------------------------|
| `vm, _ := quickjs.NewVM()`                     | `rt, _ := qjs.New(qjs.Option{...})`; `c := rt.Context()`                       |
| `vm.Eval(src, quickjs.EvalGlobal)`             | `c.Eval("name.js", qjs.Code(src))`                                             |
| `vm.SetMemoryLimit(n)`                         | `qjs.Option{MemoryLimit: n}` at runtime construction                            |
| `vm.SetEvalTimeout(d)`                         | `MaxExecutionTime` is non-functional. Use `Option{Context, CloseOnContextDone:true}` and recover from panic |
| `vm.RegisterFunc(name, fn, false)`             | `c.SetFunc(name, func(*qjs.This)(*qjs.Value, error){...})`                      |
| (no async support)                             | `c.SetAsyncFunc(name, func(*qjs.This){...; this.Promise().Resolve(v)})`         |
| Manual microtask pump (`__picotera_run`, ...)  | **DELETED** — `Eval` auto-awaits in-flight promises; for explicit Promise return values use `value.Await()` |
| Compile/validate via `Eval` + parse error sniff | `c.Compile("name.js", qjs.Code(src))` returns syntax error directly             |
| (no JSON helpers)                              | `c.ParseJSON(s)`, `value.JSONStringify()`                                      |

### Tasks affected

- **Task 4** — package: `github.com/fastschema/qjs` (added; smoke test passed; runtime startup ~2-5ms warm).
- **Task 5** — `Session` wraps `*qjs.Runtime` (per-request via `qjs.New`). Memory/stack/ctx-deadline configured at `qjs.New`. `sdk.js` simplified — no `__picotera_*` plumbing.
- **Task 6** — **DELETED**. No microtask pump needed. The "promise pump" capability becomes a thin helper: `runHookExpr(c, expr) (jsonStr, error)` that calls `c.Eval`, checks if `v.Type()=="Promise"` then `v.Await()`, then `v.JSONStringify()`. Acceptance criteria fold into Tasks 7/8.
- **Task 7** — ctx marshaling: `marshalToJSLiteral` (Go → JSON text) unchanged. Unmarshaling: `value.JSONStringify()` returns Go string for `json.Unmarshal`. Test `TestSession_CtxRoundTrip` reverses an array — same shape, simpler implementation.
- **Task 8** — Hook entrypoints: same Go API surface. Inside, build expression `(async () => { return await picotera.hooks.<name>.runWaterfall(<jsonLit>); })()`, `c.Eval`, auto-await, `JSONStringify`, unmarshal. No `pendingSlot` map.
- **Task 9** — Helpers:
  - `picotera.fetch` → `c.SetAsyncFunc("__picotera_fetch", ...)` performing the HTTP call in a goroutine and resolving via `this.Promise().Resolve(...)`.
  - `setTimeout` → `c.SetAsyncFunc("__picotera_setTimeout", ...)` that schedules a `time.AfterFunc` and resolves the promise after the delay. JS-side `setTimeout(fn, ms)` becomes `__picotera_setTimeout(ms).then(fn)`. (Note: shape differs slightly — no `clearTimeout` for v1 since no host-side handle is exposed; can be added later if needed.)
  - `console.*` → `c.SetFunc("__picotera_console", ...)`.
- **Task 10** — `ValidateSyntax(src)` calls `c.Compile("submitted.js", qjs.Code(src))` — direct error on syntax problems.
- **Task 13** — `*jsx.Engine` field unchanged; constructor signature stays.
- **Task 14** — Gateway integration unchanged. `RunSortHook` / `RunBeforeRequestHook` / `RunRewriteHook` API is preserved; only their internal implementation changes.

### New invariants

1. **Per-request runtime.** `Session` owns one `*qjs.Runtime`. Created in `Engine.NewSession`. Closed in `Session.Close`. The runtime is **never reused** between requests (spec: per-request fresh state).
2. **Hook timeout via context.** The Engine's `HookTimeout` becomes a `context.WithTimeout(ctx, cfg.HookTimeout)` passed via `qjs.Option.Context` with `CloseOnContextDone:true`. Each Eval is wrapped in `func() (..., err error) { defer func() { if r := recover(); r != nil { err = jsx.ErrHookTimeout } }(); ... }`. The runtime is treated as dead after a timeout (already true since per-request).
3. **Auto-await semantics.** `c.Eval("(async () => Promise.resolve(42))()")` returns Number=42 directly, not a Promise. We only call `value.Await()` when `value.Type() == "Promise"`.
4. **Memory limit.** `qjs.Option{MemoryLimit: int(cfg.MemoryLimit)}` (int, not int64; capped at MaxInt32 if cfg ever exceeds).
5. **Pooling deferred.** v1 uses per-request `qjs.New`; pool optimization is a follow-up if profiling warrants.

### Validation

Spike tests run during Task 4 confirmed: Promise auto-await, async host fn resolution from goroutines, JSON parse/stringify, syntax validation via Compile, context-cancel-driven timeout (with panic recovery), and 2–5ms per-VM warm startup. Spike files removed (will be rebuilt in Task 5).
