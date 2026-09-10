# Gateway Routing Engine — Design Spec

## Overview

Implement the request routing engine in the PicoTera gateway handler. The gateway receives incoming LLM inference requests, resolves the target endpoint and model, finds available providers, and forwards the request with failover logic.

## Request Flow

```
Client Request
  │
  ├─ 1. Match endpoint by r.URL.Path → DB: GetEndpointByPath
  │     └─ Not found → 404 {message, code, details}
  │
  ├─ 2. Extract model name from body → gjson.Get(body, endpoint.ModelPath)
  │     └─ Not found → 400 {message, code, details}
  │
  ├─ 3. Resolve providers → DB: GetProvidersByEndpointAndModel
  │     └─ None found → 404 {message, code, details}
  │
  ├─ 4. Sort by priority (provider.priority + mpe.priority, descending)
  │
  ├─ 5. For each provider in order:
  │     ├─ Build upstream request:
  │     │   - URL: provider_endpoint.upstream_url
  │     │   - Body: replace model via sjson (if upstream_model_name differs)
  │     │   - Credentials: mirror client header type with provider.credentials
  │     │   - Timeout: 300s read idle timeout per attempt
  │     ├─ Forward request (stream-through)
  │     └─ If HTTP 200 → stream response to client, log attempt, done
  │        If non-200 → log failed attempt, try next provider
  │
  ├─ 6. All providers failed → 502 {message, code, details}
  │
  └─ 7. Log each attempt to `request` table (sync, before final response)
```

## Credential Resolution (generalApiKey)

`endpoint.credentials_resolver = 1` means generalApiKey. Any other value is unsupported in v1 and results in a 500 error.

1. Check client request headers:
   - If `Authorization` starts with `Bearer ` → extract token, auth type = bearer
   - If `X-Api-Key` present → extract value, auth type = api_key
   - If neither → reject with 401
2. When forwarding to upstream, replace with provider's credentials:
   - If auth type = bearer → set `Authorization: Bearer <provider.credentials>`
   - If auth type = api_key → set `X-Api-Key: <provider.credentials>`
3. Remove the other header from the upstream request (don't leak client key)

## Error Handling

### Error Response Format

All gateway errors use a consistent structure:

```json
{
  "message": "human-readable description",
  "code": "MACHINE_CODE",
  "details": []
}
```

### Error Codes

| Scenario | HTTP Status | Code |
|---|---|---|
| Endpoint path not matched | 404 | `ROUTE_NOT_FOUND` |
| Model missing in request body | 400 | `MODEL_NOT_FOUND` |
| No providers for model | 404 | `NO_PROVIDER_AVAILABLE` |
| Missing credentials in request | 401 | `UNAUTHORIZED` |
| All providers failed | 502 | `UPSTREAM_ERROR` |

For `UPSTREAM_ERROR`, the message includes the error from the last provider attempt.

## Request Logging

Every attempt (success or failure) is logged to the `request` table synchronously before the final response is sent.

Fields populated:
- `id`: UUID
- `provider_id`: provider used for this attempt
- `endpoint_path`: matched endpoint
- `model`: requested model name
- `status_code`: upstream response status (0 if never reached upstream)
- `error_message`: error details if failed
- `time_spent_ms`: wall time for this attempt
- `ttft_ms`, token counts: extracted from upstream response if available, zero for v1

## Timeout Strategy

- `PICOTERA_GATEWAY_READ_TIMEOUT` — read idle timeout per attempt (default: 300s)
- Semantics: if no data arrives from upstream for 300 consecutive seconds, the attempt times out. As long as data keeps flowing, the request continues indefinitely.
- Implementation: `http.Transport.ResponseHeaderTimeout` (300s) for initial headers, then a custom `io.Reader` wrapper enforcing 300s per-chunk read idle deadline.
- No total request timeout.

## Architecture

Inline logic in `gatewayHandler.ServeHTTP` with helper methods on `*Server`:

- `resolveEndpoint(path string)` — DB lookup
- `extractModel(body []byte, modelPath string)` — gjson extraction
- `resolveProviders(endpointPath, model string)` — DB lookup + priority sort
- `buildUpstreamRequest(r *http.Request, upstreamURL, upstreamModel, creds string, authType authType)` — construct upstream HTTP request
- `forwardRequest(req *http.Request)` — execute with stream-through
- `logRequest(...)` — DB insert into request table

No middleware chain. No abstract interfaces. Direct method calls, linear flow.

## Configuration

Add to `pkg/configx/`:
- `GatewayReadTimeout` (env: `PICOTERA_GATEWAY_READ_TIMEOUT`, default: 300s)

No other config changes for v1.

## New Dependencies

- `github.com/tidwall/gjson` — extract model name from request body
- `github.com/tidwall/sjson` — replace model name in request body before forwarding

## Code Changes

1. **`pkg/server/handle_gateway.go`** — rewrite `ServeHTTP` with full routing pipeline + helper methods
2. **`pkg/configx/`** — add `GatewayReadTimeout` field
3. **`db/migrations/`** — new migration: make `request.api_key_id` nullable (v1 skips API key auth, so this column won't have a value)
4. **`db/queries/routing.sql`** — add query to get `provider_endpoint.upstream_url` by `provider_id` + `endpoint_path`; add query to insert into `request` table
5. **`pkg/db/`** — regenerate via `sqlc generate`
6. **`go.mod`** — add gjson, sjson

## Out of Scope for v1

- API key authentication against the `api_key` table
- Additional `credentials_resolver` types beyond generalApiKey
- Token count extraction from upstream responses
- TTFT measurement
- Rate limiting
- Circuit breaking / provider health tracking
