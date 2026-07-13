# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working Conventions

- **No unsolicited compatibility layers.** When planning or making decisions, do not introduce compatibility shims, fallbacks, deprecated-path branches, or any code whose purpose is to preserve old behavior — unless the user explicitly asks for it. Prefer clean replacements that update all call sites. If you genuinely believe a compatibility layer is unavoidable or strongly warranted, **stop and ask the user before writing it**.
- **Fail fast on input; do not be lenient.** Do not add forgiving normalization of user/API input — no silent trimming of whitespace, no case-folding, no coercing empty strings to defaults, no "did you mean" guessing, no accepting near-miss formats. Validate strictly and reject invalid input with a clear error. Only relax this when the user explicitly asks for it.

## Build & Run Commands

Toolchain is pinned via `mise.toml` (go, node, pnpm, sqlc). The `[tasks]` block in `mise.toml` defines shortcuts; direct commands also work.

```bash
# Backend
mise run server                         # go run ./cmd/picotera/main.go (auto-builds llmbridge plugin first)
go build -o picotera ./cmd/picotera     # build binary

# llmbridge cross-format converter plugin
mise run llmbridge-plugin               # go build → dist/picotera-llmbridge-plugin

# Infra (Postgres on :34052, KeyDB/Redis on :34051, MinIO on :34050)
docker compose up -d

# sqlc — edit db/queries/*.sql, then regenerate pkg/db/
sqlc generate

# OpenAPI spec (checked in at openapi.yaml, consumed by dashboard via openapi-typescript)
mise run openapi                        # writes openapi.yaml from `picotera openapi` subcommand

# Dashboard (pnpm workspace, Vue beta pinned in pnpm-workspace.yaml)
mise run web                            # pnpm --dir dashboard dev
pnpm --dir dashboard build              # type-check + vite build
pnpm --dir dashboard lint               # oxlint + eslint, both with --fix
pnpm --dir dashboard type-check         # vue-tsc --build
pnpm --dir dashboard format             # oxfmt src/
pnpm --dir dashboard generate-openapi   # regenerate TS types from openapi.yaml
```

### OpenAPI → TypeScript SDK workflow

The dashboard does not call the API by hand-written client code; types and the fetch client are generated from the spec.

1. Edit Huma operations / contract types in `pkg/contract/` (and any handlers in `pkg/server/`).
2. Run `mise run openapi` — this invokes `go run ./cmd/picotera/main.go openapi` (the `openapi` subcommand on the `humacli` entry point) and writes the result to `openapi.yaml` at the repo root.
3. Run `pnpm --dir dashboard generate-openapi` — `openapi-typescript` consumes the YAML and emits `dashboard/src/openapi-types.d.ts`. `dashboard/src/api/index.ts` re-exports the schema types; `dashboard/src/api/plugin.ts` wires `openapi-fetch` against those types and installs it on the Vue app.

Always run both steps after touching a contract — backend changes are invisible to the dashboard until the TS types regenerate.

Go tests live under `pkg/llmbridge/` (bridge format conversions) and `pkg/server/` (gateway flow/helpers, overview, pricing match, project extraction, request/response decompression, web-search loops, unified-handler helpers, and more). They are pure unit tests over hand-built structs — there is no postgres-backed test harness, so DB-touching code paths are not exercised end-to-end. No Go linter is configured. Dashboard lints via oxlint+eslint.

## Bundling the dashboard

The Go binary embeds `pkg/server/static/dist/` via `//go:embed`. By default that directory only contains a placeholder `index.html` (committed) explaining how to bundle the real dashboard — `go build` works on a clean checkout without running any frontend build. Production assembly (Dockerfile / nix build) is responsible for overwriting the placeholder with `dashboard/dist/` output.

To test the bundled binary locally: `pnpm --dir dashboard build && find pkg/server/static/dist -mindepth 1 ! -name index.html ! -name .gitignore -delete && cp -r dashboard/dist/. pkg/server/static/dist/`. The directory has a local `.gitignore` (`pkg/server/static/dist/.gitignore`) that ignores everything except the placeholder, so populated assets won't accidentally get committed.

## Architecture

PicoTera is an API gateway that routes LLM inference requests across multiple providers. It exposes a management API for configuring providers, models, and endpoints.

**Stack**: Go 1.26, Huma v2 (REST framework) + Chi router, PostgreSQL via pgx, sqlc for type-safe queries, goose for migrations, Viper for config.

**Startup flow**: Parse config → run goose migrations → connect PostgreSQL → register Huma operations → mount gateway handler → serve HTTP.

### Package Layout

- `cmd/picotera/` — CLI entry point (humacli + cobra). Subcommands: `openapi` (prints spec to stdout), `set-admin <user-id>` (flips `is_admin`), `bind-identity <provider> <identity> <user-id>` (maps an auth identity to an existing user — e.g. `bind-identity http-header root 1`).
- `db/migrations/` — goose SQL migrations (embedded in binary). Schema is the source of truth.
- `db/queries/` — sqlc query definitions. **Edit these, then run `sqlc generate`**.
- `pkg/db/` — **Generated by sqlc. Never edit manually.** Contains models, queries, and `Querier` interface.
- `pkg/contract/` — Huma request/response types and operation definitions. Each resource has `To*View`/`From*View` conversion functions between DB models and API views.
- `pkg/server/` — HTTP server, operation handlers (methods on `*Server`), gateway handler.
- `pkg/server/static/` — embedded dashboard SPA (`//go:embed all:dist`). Assets are gzip+brotli precompressed at startup; served as a fallback when the LLM gateway can't match the request path. See "Bundling the dashboard" below.
- `pkg/configx/` — Viper config parsing (env vars with `PICOTERA_` prefix).
- `pkg/errorx/` — Custom error types with structured codes.
- `pkg/logx/` — logrus wrapper.
- `pkg/jsx/` — embedded JavaScript runtime (built on `github.com/fastschema/qjs`) that runs user-supplied scripts as request-lifecycle hooks. See "Scripts" below.
- `pkg/llmbridge/` — adapter types and interfaces for cross-format LLM payload conversion (Anthropic Messages, OpenAI Chat Completions, OpenAI Responses, Gemini GenerateContent). The host talks to the converter through a HashiCorp go-plugin gRPC process.
- `pkg/llmbridgeimpl/` — concrete implementations of `llmbridge` converters, compiled into the plugin target (`cmd/picotera-llmbridge-plugin`). Depends on `github.com/looplj/axonhub/llm` (LGPL-3.0; attribution in `THIRD_PARTY_NOTICES.md`).
- `pkg/kv/` — key-value store abstraction with memory and Redis (KeyDB) backends. Used by user-supplied scripts. CRUD exposed at `/api/picotera/kv`.
- `pkg/artifacts/` — request/response payload serialization. Stores bodies as zstd-compressed JSON with optional line-by-line SSE timings. MinIO-backed via the `picotera-artifacts` bucket.
- `pkg/pricing/` — model pricing calculation and matching logic.
- `pkg/annotations/` — request annotation parsing and handling.
- `pkg/transform/` — generic data transformation utilities.
- `pkg/auth/` — management-API authentication middleware + identity resolver. See "User authentication" and "User isolation & authorization" below.

### Scripts (user JS hooks)

Operators store JS source in the `script` table (CRUD via `/api/picotera/scripts`, dashboard at `ScriptsView.vue` + `ScriptForm.vue`). On each gateway request, `Server.jsxEngine` (configured in `pkg/server/server.go` from `PICOTERA_JS_*` env vars: hook timeout, memory limit, max total attempts, max delay) opens a per-request `jsx.Session`, loads the embedded `pkg/jsx/sdk.js` to install `globalThis.picotera`, then evaluates every enabled script. Scripts call `picotera.hooks.<name>.tap(name, fn, priority)` to register handlers on seven waterfalls:

- `sortProviders` — reorder/filter provider candidates before dispatch.
- `rewriteModel` — rewrite the requested model name once, between extraction and provider resolution.
- `beforeRequest` — inspect / delay / skip an attempt; can override `upstreamModel` for that attempt.
- `beforeTransform` — choose or customize the outbound llmbridge profile before cross-format request conversion.
- `rewriteRequest` — mutate the pending upstream URL/headers/body just before send.
- `rewriteProviderModels` — rewrite a provider's `providerModels` array based on an upstream `/models` response (used by the "fetch models" flow in `ProviderForm`).
- `afterUpstreamError` — runs after **every** failed upstream attempt (HTTP non-200, connection/network failure, decode/bridge failure, and in-stream SSE errors). Input `{ break, statusCode, message, streamed }`; output `{ break, statusCode, message }`. When `break=true` **and** `streamed=false` (the response hasn't started writing yet), the gateway stops trying further providers and writes the downstream response: `statusCode<=0` follows the upstream's original status (falling back to `502`), `message==""` follows the upstream's original body + `Content-Type` (overrides write the `message` bytes as `application/json`). For in-stream errors (HTTP 200 + SSE error event) the response has already streamed, so `streamed=true` and `break` is ignored — the hook still runs for observation (logging/kv). When the upstream status code is exactly `400` and `streamed=false`, the input `break` seed defaults to `true` (so an unhandled upstream 400 is passed through to the client by default); every other status defaults to `false`. The hook reads this default in `input.break` and can return `{ break: false }` to keep trying further providers. `statusCode` is integer-cast; a non-string `message` becomes `""`. The hook is advisory: if it errors/times out it is logged and treated as `break=false`. `ctx.attempt.lastError` (`{providerId, statusCode, message}`) is set before the hook runs and, absent a `break`, carries into the next attempt's `beforeRequest`. The hook's `rewriteRequest`/`beforeRequest` JS errors (including timeout-tainted sessions) do **not** trigger it — those keep the existing `failHook` behavior.

Hooks are run as priority-sorted waterfalls (higher priority first); each tap may return a value that becomes the input to the next tap, or `undefined` to pass through. JS-visible context shapes are defined in `pkg/jsx/types.go` (`Candidate`, `RequestShape`, `BeforeRequestInput`, `RewriteInput`, `ProviderSummary`, etc.) — provider credentials are deliberately stripped before crossing the JS boundary. Scripts also get `picotera.fetch(url, init)` (host-side fetch returning parsed JSON) and a `console` shim that captures up to 1000 entries / 256 KiB per session into `LogEntry` records persisted alongside the request.

**Body Proxies (`pkg/jsx/objects.go` + `sdk.js`).** The two large request bodies a hook can touch — `ctx.request.body` and rewriteRequest's `pending.body` — are NOT eagerly embedded as JS objects. They live as `pkg/jsonast` `*Node` trees on the Go side; `sdk.js` wraps each accessed object/array node in a `Proxy` keyed by an integer id whose get/set/enumerate/delete traps forward through the synchronous `__picotera_obj_*` host functions. So a scalar (e.g. a multi-MiB data-url) crosses into QuickJS only when a script reads that field, and writes land straight on the Go tree — keeping memory flat for large bodies that would otherwise blow the JS memory limit on an embed→parse→re-stringify round-trip. The session installs `ctx.request.body` via `Session.SetClientBody([]byte)` (call it after any PatchContext that sets `request`; re-calling it after the model rewrite changes the body invalidates the prior Proxy); `RunRewriteRequest(initial, body []byte)` installs `pending.body` and returns the final upstream bytes (nil = fall back to pre-hook bytes, including the byte-identical clean-passthrough case). A hook that never touches the body never parses it. Script-visible semantics: nested reads return cached child Proxies (`body.a === body.a`); `Array.isArray`/`map`/`filter`/`slice`/spread/`Object.keys`/`JSON.stringify` and the array mutators all work; assigning a plain object or another Proxy deep-copies it into the tree (no aliasing); writing `undefined`/functions, out-of-range array writes, deleting a non-last array element, or growing `length` all throw; a stale Proxy (used after its body tree was replaced) throws. Array Proxies carry a `ProxiedArray` prototype (`target → ProxiedArrayProto → Array.prototype`) whose mutators — `splice`/`push`/`pop`/`shift`/`unshift`/`reverse` — route through `__picotera_arr_splice` / `__picotera_arr_reverse` so the Go side reorders the `[]*Node` slice by pointer: existing elements that merely shift position are **relocated, never cloned** (a Proxy passed as an inserted item is still deep-copied, matching the set path). `sort` (needs a JS comparator) keeps the native `Array.prototype` implementation and so still clones via per-index set traps. There is **no** data-url masking at the JS boundary — scripts read originals. (The `pkg/datamask` package still exists on disk but is unwired: nothing imports it, and no `PICOTERA_JS_DATA_URL_MASK_*` env var is read.)

**Request annotations (`ctx.metaRequest.annotations` / `ctx.upstreamRequest.annotations`).** Distinct from `ctx.annotations` (the read-only *merged config-layer* view of model/provider/apiKey annotations), these are two writable string KV maps a hook uses to tag the current gateway request; they persist to the `request.annotations` JSONB column (see Schema). Backed not by jsonast trees but by flat `map[string]string` accumulators on the Go side (`qjsSession.metaAnnotations` / `upstreamAnnotations`, guarded by `logsMu`); `sdk.js` `__picotera_makeAnnotationsProxy(slot)` wraps each as a `Proxy` forwarding get/set/delete/has/enumerate through the `__picotera_anno_*` host functions, routed by slot (`"meta"` / `"upstream"`). **Strict typing (fail fast):** the set trap throws `TypeError` unless the key is a non-empty string and the value is a string (no implicit `String()` coercion); the Go side re-checks the key defensively. `Object.keys`/`JSON.stringify`/`in`/spread all work; a missing key reads as `undefined` (distinguished from an empty-string value via JSON-encoded host returns). `ctx.metaRequest` is installed at session init (available from `sortProviders`/`rewriteModel` on); `ctx.upstreamRequest` is `undefined` until the **first attempt**, installed by `Session.ResetUpstreamAnnotations()` — called at the top of every `runSingleAttempt` so each attempt's upstream annotations start empty (writes from a `next=true`-skipped attempt are discarded; `afterUpstreamError` and in-stream `runStreamErrorHook` writes belong to the current attempt). Persistence (`gatewayFlow.persistRequestAnnotations`): meta annotations are written once in `run()`'s `defer session.Close()` closure; upstream annotations are written per attempt in a `runSingleAttempt` defer (a closure, so the snapshot is taken at return time). Reading the accumulators never touches the VM, so a timeout-tainted session still yields already-written labels. **Not gated by OTR** — annotations are operator-authored operational labels, not client content. `Session.{Meta,Upstream}Annotations()` return copies; `qjsSession` is the sole implementer of the interface (no fakes to sync).

### Key Patterns

- **Endpoint matching**: request paths are matched against `endpoint.path` patterns (which may contain `{name}` placeholders matching any non-empty string, including `/`). The matcher is an in-memory cache (`pkg/server/endpoint_router.go`) loaded lazily from `GetEndpoints` and sorted by literal-character specificity. Any mutation of the `endpoint` table **must** call `Server.endpointRouter.Invalidate()`. Do not reintroduce `GetEndpointByPath` for gateway routing — it only remains for exact-path validation in `handle_provider_endpoint.go`.
- **Project matching**: every gateway request body is scanned by `pkg/server/project_extractor.go` against some fixed regexes. Captures are JSON-string unescaped, expanded into separator-aligned ancestors, and matched via the synchronous `MatchProjectByPaths` SQL query; there is no project-router memory cache. Projects are **per-user** (`project.user_id`, unique `(user_id, name)`): `Extract`/`MatchProjectByPaths` take the resolved `userID` and scope matching to that user, and auto-create is gated on that user's `project.autoCreate` user-setting (absent ⇒ off). Extraction happens **after authentication** in `authenticateAndBackfill` (`gateway_flow.go`): the meta row is inserted with `project_id = NULL`, then `UpdateRequestOnHeader` backfills `project_id` alongside `user_id`. The matched `project_id` is written onto every upstream `request` row and triggers an asynchronous `UpsertProjectSeen`.
- **sqlc workflow**: Write queries in `db/queries/*.sql` → run `sqlc generate` → use generated code in `pkg/db/`. The `Querier` interface in `pkg/db/querier.go` lists all available DB methods. sqlc is configured in `sqlc.yaml` (pgx/v5 driver, `emit_interface: true`, camelCase JSON tags).
- **Adding an API operation**: Define operation + request/response types in `pkg/contract/`, add handler method on `*Server` in `pkg/server/`, register in `registerOperations()`. After the change, regenerate `openapi.yaml` so the dashboard's typed client picks it up.
- **Config**: All settings via env vars with `PICOTERA_` prefix (e.g., `PICOTERA_DATABASE_URL`, `PICOTERA_PORT`, `PICOTERA_APP_TITLE`). Default port is 9898. The dashboard title comes from `PICOTERA_APP_TITLE` and is exposed read-only via `GET /api/picotera/config` (`{ "title": ... }`).
- **API base path**: All management operations are under `/api/picotera`.
- **Database**: TimescaleDB (`timescale/timescaledb:2.26.4-pg17` image) on port 34052 via docker-compose. Migrations auto-run on startup. A MinIO instance on 34050 (bucket `picotera-artifacts`, bootstrapped by `minio-init`) backs the artifact sink (`pkg/artifacts/`).
- **`request` is a TimescaleDB hypertable** (migration 017): partitioned by `created_at` with composite primary key `(id, created_at)`. `created_at` has no column default — every insert/update/delete must supply it (see the `id_created_at` / `created_at` args in `db/queries/request.sql`). Cursor pagination tuples are `(created_at, id)`, not just `id`.

### Unified generation routes

Five chi routes are registered as runtime constants in `server.go` BEFORE the catch-all gateway mount and back the cross-format dispatch. They live under `/api/unified` (NOT `/api/picotera`) so the user-auth middleware — which guards only the `/api/picotera` prefix — leaves them open; like the gateway they authenticate via API key:

- `POST /api/unified/v1/messages` — Anthropic Messages source.
- `POST /api/unified/v1/responses` — OpenAI Responses source.
- `POST /api/unified/v1/chat/completions` — OpenAI Chat Completions source.
- `POST /api/unified/v1beta/models/{model}:generateContent` — Gemini GenerateContent source (non-stream).
- `POST /api/unified/v1beta/models/{model}:streamGenerateContent` — Gemini GenerateContent source (stream).

These are NOT rows in the `endpoint` table — operators only configure the underlying upstream `endpoint` rows (`anthropicMessages`, `openaiChatCompletions`, `openaiResponses`, `geminiGenerateContent`, `geminiStreamGenerateContent`). The unified handler (`pkg/server/handle_unified_gateway.go`) collects every candidate MPE that supports the requested model+stream tuple via `GetProvidersByEndpointTypesAndModel`, runs the gateway-generation JS hooks (same shapes as the path-based gateway, including `beforeTransform` for outbound profile selection), and per attempt: if the chosen upstream's `endpoint_type` differs from the source format, runs the body through `pkg/llmbridge/` **before** `rewriteRequest` (the hook sees and mutates the upstream-format body) and the response back before writing to the client. Identity (1:1) attempts are byte-for-byte passthrough so token/TTFT extraction is unaffected.

### User authentication (`pkg/auth/`)

A chi middleware (`auth.Middleware`) authenticates the internal management API. It does **not** match a path prefix internally; instead `server.go` derives `mgmtRouter := router.With(auth.Middleware(...))` (after `decompressRequest`) and registers every `/api/picotera` route on it — the Huma management operations (`humachi.New(mgmtRouter, ...)`, which also carries Huma's own `/openapi.*` and `/docs`) plus the raw `test/direct` route. The gateway catch-all and `/api/unified` stay on the bare `router` and authenticate via API key; static assets fall through the gateway. (Because Huma's docs routes live on `mgmtRouter` too, they require auth — the dashboard generates types from the checked-in `openapi.yaml`, so nothing depends on them being open.) Resolution (`auth.Resolver.Resolve`) follows a fixed precedence, no implicit defaults:

1. **single-user-mode** (`PICOTERA_AUTH_SINGLE_USER_MODE=true`) — ignores all headers, fixes identity `(single-user-mode, root)`, and **unconditionally** bootstraps that user as `is_admin=true` (independent of auto-create).
2. **http-header** (`PICOTERA_AUTH_HEADER_ENABLED=true`, `PICOTERA_AUTH_HEADER_NAME=<header>`) — reads the configured header; empty/missing → 401. Non-empty value is the `identity` under provider `http-header`; unknown identity creates a user only when `PICOTERA_AUTH_AUTO_CREATE_USER=true` (else 401). Startup fails fast if `HEADER_ENABLED` is set with an empty `HEADER_NAME`.
3. **neither configured** → every `/api/picotera` request is 401.

On success the resolved `*db.AppUser` is stored on the request context (`auth.WithUser`); humachi passes it through so handlers read it via `auth.UserFromContext(ctx)` (see `GET /api/picotera/me`). On failure the middleware writes `401 {"message":"unauthorized"}` directly. Two tables back this (`db/migrations/033_users.sql`): `app_user` and `user_identity` (unique `(provider, identity)`, no FK on `user_id`); auto-create is a single transaction with `ON CONFLICT DO NOTHING` + reread as a concurrency guard. A user carries a `disabled` flag: the resolver rejects a disabled user with `ErrUnauthorized` (401) on **every** provider including single-user-mode root; the gateway separately rejects requests whose API-key owner is disabled (or missing) with 403 (`gateway_helpers.go`).

### User isolation & authorization

Two orthogonal layers sit on top of the resolved user:

**1. Per-resource ownership (data isolation).** Three resource classes are scoped to a `user_id` with **no admin bypass** — even admins see only their own:

- `api_key.user_id` — owner = creator (taken from context, never the request body).
- `request.user_id` (nullable) — owner = the API key's user; backfilled post-auth by `UpdateRequestOnHeader` (meta rows are inserted NULL pre-auth).
- `traces.user_id` — trace identity is the composite `(parent_span_id, user_id)`, not `parent_span_id` alone, so a client-controlled `parent_span_id` can't merge two users' traces. `UpsertTrace` runs post-auth (anchored on the meta row's `created_at`); every `ListRequestTraces` LATERAL also constrains `request.user_id = traces.user_id`.
- `project.user_id` — see "Project matching".

Read paths inject the current user as a **mandatory** SQL filter (`WHERE user_id = $N` on list; `AND user_id = $N` on single-get/filter/cross-span — a miss is a 404, never a leak). Live-progress / interrupt first re-check ownership via the user-scoped `GetRequest` before touching the in-memory `liveRequests` map. The `user_id` columns are NOT NULL with no default (except nullable `request.user_id`, which is NULL on the pre-auth meta row until backfill), so every insert must supply `user_id`; the schema assumes a user with id 1 exists (single-user-mode root). Both continuous aggregates (`request_overview_hourly`, `request_speed_hourly`) carry `user_id` in their `GROUP BY`.

**2. `is_admin` capability gate (admin vs user features).** `registerOperations` (`server.go`) registers every Huma op on one of two groups sharing the `/api/picotera` prefix: `mgmt` (all authenticated users) and `admin` (`admin.UseMiddleware(s.requireAdmin)` → 403 for non-admins, 500 if context user is somehow nil). The split — see `register(mgmt, admin)`:

- **mgmt (user):** `me`, `config`, overview ×4, api-key ×5, request/trace ×6, **label** endpoints, **project** CRUD ×5, **user-setting** CRUD, exchange-rate *list* (read).
- **admin:** providers, models, endpoints, provider-endpoints, scripts, kv, exchange-rate writes + match-pricing, fetch-models, user/user-identity CRUD, and the **admin overview** endpoints.

The raw chi route `POST /api/picotera/test/direct` is not a Huma op, so it does its own admin check inside `handleTestDirect`. `NewHuma()` (openapi generator) calls the same `register()` so the spec never drifts from the live server. Both groups sit behind the chi-level `auth.Middleware`, so `requireAdmin` always sees a non-nil context user.

**Label endpoints** (`pkg/contract/label.go`, `handle_label.go`, mgmt group): `GET /api/picotera/labels/{providers,models,endpoints,projects}` (+ upstream-models) return minimal id/name/path projections of admin-only resources so user-facing views (overview filters, request views, gateway test) can render names without reading full configs (notably provider `credentials`). They reuse the existing list queries and project in the handler — no new sqlc.

**Admin global overview** (`pkg/contract/admin_overview.go`, `handle_admin_overview.go`, `db/queries/admin_overview.sql`, admin group, prefix `/api/picotera/admin/overview`): a parallel, cross-user copy of the user overview. Its queries mirror `overview.sql` one-for-one, the difference being they have no mandatory `user_id` filter (instead an *optional* `userId` narg) and offer a `user` dimension in place of apiKey/project. It is a separate endpoint set with its own SQL rather than a conditional on the shared user-scoped queries, so no single query carries two scope semantics.

**Request credential hygiene** (`gateway_helpers.go`): `buildUpstreamRequest` strips the local auth header (`config.Auth.HeaderName`) in addition to the fixed credential headers so a client/proxy can't leak it upstream; `redactRequestCredentials` rewrites `Authorization`/`X-Api-Key`/`X-Goog-Api-Key`/`Cf-Access-Client-Id`/`Cf-Access-Client-Secret`/`?key=` to `[REDACTED]` on the **artifact copy only** (the real requests keep live creds). It is applied at both request-artifact upload call sites, not in the `artifacts` package: the meta request (client → PicoTera, `gateway_flow.go`) redacts the client's PicoTera key, and the upstream request (PicoTera → provider, `gateway_flow_attempts.go`) redacts the provider credential. Response-artifact cookie redaction is separate (`redactResponseHeaders`, inside `uploadResponseArtifact`).

### Database Schema

Core tables: `provider`, `endpoint`, `provider_endpoint`, `model`, `model_provider_endpoint`, `api_key`, `request` (hypertable), `script`, `traces`, `project`, `app_user`, `user_identity`, `user_setting`. Uses JSONB for flexible fields (provider models, annotations, project paths, setting values). Upsert pattern via `ON CONFLICT DO UPDATE`. `api_key` / `request` / `traces` / `project` all carry a `user_id` (see "User isolation & authorization"); `app_user` has a `disabled` flag; `user_setting` is keyed `(user_id, key)` and holds per-user prefs (e.g. `project.autoCreate`). There is no global-settings table — the dashboard title is an env var and all other settings are per-user in `user_setting`. The `request` hypertable also carries a nullable `project_id` (no FK) populated by the project extractor post-auth. It also has a nullable `annotations` JSONB column (migration 044) written only by the script layer (see "Request annotations") — `GET /api/picotera/requests?annotations=<url-encoded JSON object>` filters by JSONB containment (`@>`, AND across pairs, exact match) via a **partial** GIN index (`jsonb_path_ops`, `WHERE annotations IS NOT NULL`) so index size scales with tagged rows, not total rows, and INSERT cost is zero (rows are inserted NULL, annotated only by a terminal UPDATE). The annotations filter shares the `filterLookback` (30-day default when `startAt` omitted) with the requestId search. Compression compatibility: columnstore has no GIN, so a future `compress_after` must exceed the annotations query window (e.g. 90d vs 30d) to keep the filter on indexed uncompressed chunks. TimescaleDB continuous aggregates (`request_overview_hourly`, `request_speed_hourly`) power the overview metrics and group by `user_id`.

## Dashboard

See `dashboard/CLAUDE.md` for all dashboard-specific documentation (architecture, components, composables, UI primitives, data layer, design context).
