# QuickJS Hook Engine — Design Spec

## Overview

Embed a QuickJS-based JavaScript hook engine into the gateway so user-supplied scripts can rewrite provider routing decisions at three points in the request lifecycle:

1. **sortProviders** — reorder the candidate provider list before retry loop begins
2. **beforeRequest** — decide, on each iteration, whether to try the current provider, skip to the next, and how long to delay
3. **rewriteRequest** — modify the upstream HTTP request (URL/method/headers/body) just before it is sent

Library: `modernc.org/quickjs` (pure Go, no cgo).

## Script Storage and Loading

### Schema

A new `script` table:

| column | type | notes |
|---|---|---|
| `id` | TEXT PRIMARY KEY | xid, generated server-side |
| `name` | TEXT NOT NULL | human label |
| `source` | TEXT NOT NULL | JS source, validated for syntax on insert/update |
| `enabled` | BOOLEAN NOT NULL DEFAULT TRUE | |
| `created_at` | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ NOT NULL DEFAULT now() | |

Index: partial `(enabled) WHERE enabled = TRUE`.

No scope binding (global / endpoint / model / mpe) in v1. Every enabled script is loaded into every gateway request. Scope binding may be added in a future iteration.

### Activation

For each gateway request:

1. Query `ListEnabledScripts` ordered by `id ASC`.
2. Load each script into the same QuickJS VM via `vm.Eval(source, EvalGlobal)`.
3. Each script registers hooks at load time by calling `picotera.hooks.<name>.tap(name, fn)`.

Scripts share `globalThis` within a single request — they may communicate with each other through globals if they want.

### Validation on Submit

`POST` and `PUT` operations on the script resource boot a throwaway QuickJS VM, evaluate the source, and reject the submission with HTTP 400 if QuickJS reports a syntax error. Runtime errors are not caught at submit time.

## Hook Model

### Tapable Waterfall

Every hook is a Waterfall: scripts tap in load-order; each tap receives the previous tap's return value (or the initial input for the first tap). The final return value is the output. A tap that returns `undefined` is treated as "passthrough" and does not change the value.

```js
picotera.hooks.sortProviders.tap("my-script", async (ctx) => {
  // return new array, or undefined to leave unchanged
});
```

### Common ctx fields

Every hook receives a `ctx` object with these fields:

```ts
{
  endpoint:      Endpoint   // full DB row, including annotations
  model:         Model      // full DB row, including annotations
  request: {                 // client request, read-only
    path:    string
    method:  string
    headers: Record<string, string[]>
    body:    string         // raw bytes as string; JS parses if it cares
    model:   string         // extracted via endpoint.modelPath
  }
}
```

`endpoint` and `model` are constant across hooks within a request.

### Hook-specific ctx and output

#### sortProviders

```ts
ctx.providers: Array<{ provider: Provider, mpe: ModelProviderEndpoint }>
```

`mpe` here is the per-candidate mapping row (with its own `annotations`, `priority`, `upstreamModelName`). The `provider` row contains the full DB record including `credentials`.

Return: new array of the same shape, or `undefined` to keep the order. Any element missing in the returned array is dropped from the candidate list. Returning an empty array is allowed and means "no provider should be tried" → request fails with 502 `NO_PROVIDER_AVAILABLE`.

#### beforeRequest

```ts
ctx.provider:           Provider
ctx.mpe:                ModelProviderEndpoint
ctx.currentRetryCount:  number    // attempts on this specific provider so far
ctx.totalAttemptCount:  number    // attempts across all providers so far
ctx.lastError:          null | { providerId: number, statusCode: number, message: string }
```

Return: `{ next?: boolean, delay?: number }`. Defaults: `next=false`, `delay=0`.

- `next: false, delay: 0` → request the current provider immediately.
- `delay > 0` → sleep `min(delay, MaxDelay)` ms, then request the current provider.
- `next: true` → skip current provider, advance pointer, reset `currentRetryCount` to 0; if no providers remain → 502.

After a failed attempt, the loop re-enters this hook with `lastError` populated and `currentRetryCount`/`totalAttemptCount` incremented (see Retry Semantics below).

#### rewriteRequest

```ts
ctx.provider:           Provider
ctx.mpe:                ModelProviderEndpoint
ctx.currentRetryCount:  number
ctx.totalAttemptCount:  number
ctx.upstreamRequest: {              // already pre-processed by gateway
  url:     string
  method:  string
  headers: Record<string, string[]>
  body:    string
}
ctx.clientRequest: {                // raw client request, read-only
  path: string, method: string, headers: ..., body: string
}
```

Return: `{ url?, method?, headers?, body? }`. Any field omitted is left unchanged. `body` may be a string or an object; objects are auto `JSON.stringify`-ed. No size limit.

## Retry Semantics

The retry loop in the gateway is driven entirely by Go. JS only signals decisions.

```
i := 0                           // index into candidate providers
currentRetryCount := 0
totalAttemptCount := 0

for {
  if i >= len(providers):                       → 502
  if totalAttemptCount >= MaxTotalAttempts:     → 502

  d := beforeRequest({ provider: providers[i], currentRetryCount, totalAttemptCount, lastError })

  if d.delay > 0:
    sleep min(d.delay, MaxDelay)

  if d.next:
    i++
    currentRetryCount = 0
    continue

  upstream := buildUpstreamRequest(providers[i])
  upstream = rewriteRequest({ ..., upstreamRequest: upstream })

  resp := forward(upstream)
  totalAttemptCount++

  if resp.OK:
    stream to client → return
  else:
    lastError = { providerId, statusCode, message }
    currentRetryCount++
    continue                     // re-enters beforeRequest on same provider
}
```

So the example flow from the brainstorm transcript holds:

| step | beforeRequest input | beforeRequest return | action |
|---|---|---|---|
| 1 | `{provider: a, currentRetry: 0, totalAttempt: 0}` | `{next: false}` | request a, fails |
| 2 | `{provider: a, currentRetry: 1, totalAttempt: 1, lastError: ...}` | `{next: true}` | move to b |
| 3 | `{provider: b, currentRetry: 0, totalAttempt: 1}` | ... | ... |

## Async Model

Hooks may return Promises. JS may use `async`/`await`, `Promise.all`, etc. Go-side helpers exposed to JS:

| API | semantics |
|---|---|
| `picotera.fetch(url, init?)` | HTTP fetch, no allowlist, internal 5s timeout per call |
| `setTimeout(fn, ms)` / `clearTimeout(id)` | global, backed by Go timers |
| `console.log` / `console.error` / `console.warn` / `console.info` | logs via `logx` with fields `script_id`, `request_id` |

No KV / Redis access in v1. No `import` / module system in v1.

### Promise pump

When a hook returns a Promise, the engine drives QuickJS microtasks until the Promise settles or the per-hook timeout fires. Go-side timers and fetch callbacks resolve into JS by being scheduled onto the QuickJS job queue (or, if the library does not expose direct microtask control, by polling on a known global slot — to be confirmed during implementation).

## Failure Modes

A failure in any hook fails the entire client request:

| event | response |
|---|---|
| JS throws (sync or async rejection) | HTTP 502, meta record marked `failed`, `error_message` set to the exception text |
| Hook wall-clock exceeds `JSHookTimeout` (default 5s) | `vm.Interrupt()` + `vm.Close()`, HTTP 503 |
| QuickJS memory limit exceeded (default 64 MiB) | HTTP 502 |
| `totalAttemptCount` exceeds `MaxTotalAttempts` (default 50) | HTTP 502 with last upstream error message |

There is no per-script `fail_mode` — failures always abort the request.

## VM Lifecycle

- One `*quickjs.VM` per gateway (meta) request. Created on first hook call, closed via `defer` when the request handler exits (success, failure, or panic).
- `SetMemoryLimit(JSMemoryLimit)` and `SetEvalTimeout(JSHookTimeout)` set immediately after `NewVM()`.
- All scripts loaded once into that VM at the start of the session, before `sortProviders` runs.

## Configuration

| env var | default | meaning |
|---|---|---|
| `PICOTERA_JS_HOOK_TIMEOUT` | `5s` | per-hook wall-clock budget |
| `PICOTERA_JS_MEMORY_LIMIT` | `64MiB` | per-VM memory cap |
| `PICOTERA_JS_MAX_TOTAL_ATTEMPTS` | `50` | retry safety net |
| `PICOTERA_JS_MAX_DELAY` | `60s` | clamp for `beforeRequest.delay` |

## Code Layout

```
pkg/jsx/
  engine.go        // Engine constructed at server boot; holds config, ScriptStore
  session.go       // *Session created per request; RunSortHook / RunBeforeRequestHook / RunRewriteHook / Close
  store.go         // ScriptStore: ListEnabledScripts wrapper
  sdk.go           // //go:embed sdk.js
  sdk.js           // tapable Waterfall implementation, picotera global setup
  helpers.go       // RegisterFunc for fetch / setTimeout / clearTimeout / console.*
  promise.go       // awaitPromise: drives QuickJS until settled or timeout
  types.go         // Go ↔ JS ctx marshalling

pkg/contract/
  script.go        // ScriptView, CreateScriptInput, UpdateScriptInput, operations

pkg/server/
  handle_script.go // CRUD on script
  handle_gateway.go // integrate engine.NewSession + RunXxxHook calls
  server.go        // wire engine into Server

pkg/configx/
  config.go        // add JS* fields

db/queries/script.sql        // CRUD + ListEnabledScripts
db/migrations/004_script.sql // CREATE TABLE script

dashboard/src/views/ScriptsView.vue
dashboard/src/components/ScriptForm.vue
dashboard/src/router/index.ts        // route entry
dashboard/src/components/AppSidebar.vue // nav entry
```

## Gateway Integration Points

In `pkg/server/handle_gateway.go`:

1. After `resolveProviders` and after looking up the matched `endpoint` and `model` rows: build the per-request `*jsx.Session`. (`endpoint` is already in scope; `model` may need an additional fetch by name + endpoint — add `GetModelByEndpointAndName` query.)
2. Replace the priority-sorted `providers` slice with `session.RunSortHook(providers)`.
3. Inside the retry loop, just before any candidate is tried, call `session.RunBeforeRequestHook(...)`. Honor `next` and `delay`.
4. After `buildUpstreamRequest`, call `session.RunRewriteHook(upstreamReq, clientReq)` and use its output as the actual outgoing request.
5. `defer session.Close()` at the top of the handler.

## Dependencies

- `modernc.org/quickjs` (new)

No other Go dependencies needed. SDK code is embedded via `//go:embed`.

## Out of Scope for v1

- Script scope binding (global / endpoint / model / provider / mpe filtering)
- after-response hook
- KV / Redis helpers
- Bytecode caching
- ES module system / `import` between scripts
- Script dry-run / test endpoint
- console.* output surfaced into dashboard (logs only via logx)
- Per-script `fail_mode` (lenient vs strict)
- fetch allowlist
- Body size limit on rewriteRequest output
