# Fetch Models Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a feature to fetch model lists from upstream `/models` endpoints, with credentials resolver refactor to support Bearer Token and X-Api-Key auth styles.

**Architecture:** Refactor credentials resolver to support three types (generalApiKey, bearerToken, xApiKey) with a shared `setCredentialsHeaders` helper. Add a new `POST /provider-endpoints/fetch-models` API that uses this helper. Add a fetch button in the provider-endpoint binding panel UI.

**Tech Stack:** Go 1.26, Huma v2, sqlc, Vue 3, Tailwind v4, @tabler/icons-vue

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `pkg/contract/endpoint.go` | Modify | Add bearerToken/xApiKey constants, update To/From converters, update enum tag |
| `pkg/server/gateway_helpers.go` | Modify | Add `setCredentialsHeaders`, refactor `buildUpstreamRequest` to use it |
| `pkg/server/handle_gateway.go` | Modify | Remove `generalApiKey`-only guard, pass `credentials_resolver` to `buildUpstreamRequest` |
| `pkg/contract/provider_endpoint.go` | Modify | Add `FetchModelsRequest`, `FetchModelsResponse`, operation definition |
| `pkg/server/handle_provider_endpoint.go` | Modify | Add `handleFetchModels` handler |
| `pkg/server/server.go` | Modify | Register `OperationFetchModels` |
| `openapi.yaml` | Modify | Regenerated via `mise run openapi` |
| `dashboard/src/api.d.ts` | Modify | Regenerated via `openapi-typescript` |
| `dashboard/src/components/EndpointForm.vue` | Modify | Add bearerToken/xApiKey options to credentials resolver select |
| `dashboard/src/ui/icons/paths.ts` | Modify | Add `cloud-download`, `loader`, `check` icons from @tabler/icons-vue |
| `dashboard/src/components/ProviderEndpointsPanel.vue` | Modify | Add fetch button, loading/success/failure states |

---

### Task 1: Add credentials resolver types to contract

**Goal:** Add `bearerToken` and `xApiKey` resolver constants and update the endpoint contract.

**Files:**
- Modify: `pkg/contract/endpoint.go`

**Acceptance Criteria:**
- [ ] `CredentialsResolver_BearerToken` (2) and `CredentialsResolver_XApiKey` (3) constants exist
- [ ] `ToCredentialsResolver` handles `"bearerToken"` and `"xApiKey"`
- [ ] `FromCredentialsResolver` handles values 2 and 3
- [ ] `EndpointView.CredentialsResolver` enum tag includes all three values
- [ ] `go build ./...` passes

**Verify:** `go build ./...` → success

**Steps:**

- [ ] **Step 1: Add constants and update converters**

In `pkg/contract/endpoint.go`, replace the constants block and converter functions:

```go
const (
	CredentialsResolver_Unknown       int32 = 0
	CredentialsResolver_GeneralApiKey int32 = 1
	CredentialsResolver_BearerToken   int32 = 2
	CredentialsResolver_XApiKey       int32 = 3
)

func ToCredentialsResolver(s string) int32 {
	switch s {
	case "unknown":
		return CredentialsResolver_Unknown
	case "generalApiKey":
		return CredentialsResolver_GeneralApiKey
	case "bearerToken":
		return CredentialsResolver_BearerToken
	case "xApiKey":
		return CredentialsResolver_XApiKey
	default:
		return CredentialsResolver_Unknown
	}
}

func FromCredentialsResolver(cr int32) string {
	switch cr {
	case CredentialsResolver_Unknown:
		return "unknown"
	case CredentialsResolver_GeneralApiKey:
		return "generalApiKey"
	case CredentialsResolver_BearerToken:
		return "bearerToken"
	case CredentialsResolver_XApiKey:
		return "xApiKey"
	default:
		return "unknown"
	}
}
```

- [ ] **Step 2: Update EndpointView enum tag**

Change the `CredentialsResolver` field tag from `enum:"generalApiKey,unknown"` to `enum:"generalApiKey,bearerToken,xApiKey,unknown"`.

- [ ] **Step 3: Build and verify**

Run: `go build ./...`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add pkg/contract/endpoint.go
git commit -m "feat(contract): add bearerToken and xApiKey credentials resolver types"
```

---

### Task 2: Add shared `setCredentialsHeaders` helper and refactor gateway

**Goal:** Extract a shared `setCredentialsHeaders` function and refactor the gateway to use `credentials_resolver` from the endpoint instead of inferring from client request.

**Files:**
- Modify: `pkg/server/gateway_helpers.go`
- Modify: `pkg/server/handle_gateway.go`

**Acceptance Criteria:**
- [ ] `setCredentialsHeaders` function exists with signature `func setCredentialsHeaders(headers http.Header, credentials string, resolver int32, sourceRequest *http.Request)`
- [ ] For `CredentialsResolver_GeneralApiKey` + non-nil `sourceRequest`: infers auth style from source request (existing behavior)
- [ ] For `CredentialsResolver_GeneralApiKey` + nil `sourceRequest`: sets both `Authorization: Bearer` and `X-Api-Key`
- [ ] For `CredentialsResolver_BearerToken`: always sets `Authorization: Bearer <creds>`
- [ ] For `CredentialsResolver_XApiKey`: always sets `X-Api-Key: <creds>`
- [ ] `buildUpstreamRequest` signature updated to accept `resolver int32` instead of `auth authType`
- [ ] Gateway handler passes `endpoint.CredentialsResolver` to `buildUpstreamRequest`
- [ ] The `generalApiKey`-only guard in gateway handler is removed (allow all three resolver types)
- [ ] `resolveAuthType` is still called for client auth validation (401 on missing credentials)
- [ ] `go build ./...` passes

**Verify:** `go build ./...` → success

**Steps:**

- [ ] **Step 1: Add constants and `setCredentialsHeaders` to `gateway_helpers.go`**

Remove `const credentialsResolverGeneralAPIKey = 1` from line 34 of `gateway_helpers.go`. This constant will be replaced by the contract constants `contract.CredentialsResolver_*` defined in Task 1. The `server` package already imports `picotera/pkg/contract` (see `handle_gateway.go`), so there's no circular import concern.

Add `setCredentialsHeaders` after `resolveAuthType`:

```go
// setCredentialsHeaders sets the appropriate authentication headers based on the
// credentials resolver type. For generalApiKey with a source request, it infers
// the auth style from the source request (preserving existing gateway behavior).
// For generalApiKey without a source request, both headers are sent as fallback.
// For bearerToken and xApiKey, the source request is ignored.
func setCredentialsHeaders(headers http.Header, credentials string, resolver int32, sourceRequest *http.Request) {
	if credentials == "" {
		return
	}
	switch resolver {
	case contract.CredentialsResolver_GeneralApiKey:
		if sourceRequest != nil {
			auth := sourceRequest.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				headers.Set("Authorization", "Bearer "+credentials)
			} else {
				apiKey := sourceRequest.Header.Get("X-Api-Key")
				if apiKey != "" {
					headers.Set("X-Api-Key", credentials)
				} else {
					headers.Set("Authorization", "Bearer "+credentials)
					headers.Set("X-Api-Key", credentials)
				}
			}
		} else {
			headers.Set("Authorization", "Bearer "+credentials)
			headers.Set("X-Api-Key", credentials)
		}
	case contract.CredentialsResolver_BearerToken:
		headers.Set("Authorization", "Bearer "+credentials)
	case contract.CredentialsResolver_XApiKey:
		headers.Set("X-Api-Key", credentials)
	}
}
```

Add `"picotera/pkg/contract"` to the imports in `gateway_helpers.go`.

- [ ] **Step 2: Update `buildUpstreamRequest` signature and body**

Replace the function signature from:

```go
func buildUpstreamRequest(ctx context.Context, original *http.Request, body []byte, upstreamURL, upstreamModel, creds string, auth authType) (*http.Request, []byte, error) {
```

to:

```go
func buildUpstreamRequest(ctx context.Context, original *http.Request, body []byte, upstreamURL, upstreamModel, creds string, resolver int32) (*http.Request, []byte, error) {
```

Replace the credential-setting switch block (lines 238-243):

```go
	// Set credentials based on auth type
	switch auth {
	case authTypeBearer:
		req.Header.Set("Authorization", "Bearer "+creds)
	case authTypeAPIKey:
		req.Header.Set("X-Api-Key", creds)
	}
```

with:

```go
	// Set credentials based on resolver type
	setCredentialsHeaders(req.Header, creds, resolver, original)
```

- [ ] **Step 3: Update `handle_gateway.go`**

In `handle_gateway.go`:

a) Remove the `generalApiKey`-only guard (lines 104-111). Since we now support all three resolver types, this guard is no longer needed:

```go
		// REMOVE THIS BLOCK:
		if endpoint.CredentialsResolver != credentialsResolverGeneralAPIKey {
			errMsg := fmt.Sprintf("unsupported credentials resolver: %d", endpoint.CredentialsResolver)
			failMeta(http.StatusInternalServerError, errMsg)
			respBody := writeGatewayError(w, http.StatusInternalServerError, errMsg, errorx.InternalError.Error())
			h.uploadResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusInternalServerError, w.Header().Clone(), respBody)
			return
		}
```

b) Keep the `resolveAuthType(r)` call (line 114) for client auth validation — it's still needed to verify the client sent credentials.

c) Change the `buildUpstreamRequest` call (line 298) from:

```go
req, reqBody, berr := buildUpstreamRequest(ctx, r, body, side.upstreamURL, upstreamModel, side.credentials, authTyp)
```

to:

```go
req, reqBody, ber := buildUpstreamRequest(ctx, r, body, side.upstreamURL, upstreamModel, side.credentials, endpoint.CredentialsResolver)
```

d) Remove the `authType` type and its constants from `gateway_helpers.go` (lines 27-32):

```go
type authType int

const (
	authTypeBearer authType = iota
	authTypeAPIKey
)
```

Keep `resolveAuthType` — it still returns `(authType, error)` for client auth validation. But since `authType` is removed, convert it to return a bool or simplify. Actually, the only caller uses it to check for error (missing credentials). The returned `authType` value is no longer consumed. Simplify `resolveAuthType` to just validate client auth:

```go
// validateClientAuth checks that the client request includes credentials.
// Returns a gatewayError if credentials are missing.
func validateClientAuth(r *http.Request) error {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return nil
	}
	apiKey := r.Header.Get("X-Api-Key")
	if apiKey != "" {
		return nil
	}
	return &gatewayError{
		status:  http.StatusUnauthorized,
		message: "missing credentials",
		code:    errorx.Unauthorized.Error(),
	}
}
```

Update the call site in `handle_gateway.go` from:

```go
authTyp, err := resolveAuthType(r)
```

to:

```go
err := validateClientAuth(r)
```

And remove the `authTyp` variable since it's no longer used.

- [ ] **Step 4: Build and verify**

Run: `go build ./...`
Expected: success

- [ ] **Step 5: Commit**

```bash
git add pkg/server/gateway_helpers.go pkg/server/handle_gateway.go
git commit -m "feat(gateway): add setCredentialsHeaders helper, refactor auth to use credentials_resolver"
```

---

### Task 3: Add fetch-models API endpoint

**Goal:** Add the `POST /provider-endpoints/fetch-models` API operation with request/response types and handler.

**Files:**
- Modify: `pkg/contract/provider_endpoint.go`
- Modify: `pkg/server/handle_provider_endpoint.go`
- Modify: `pkg/server/server.go`

**Acceptance Criteria:**
- [ ] `FetchModelsRequest` with `{ providerId, endpointPath }` body exists
- [ ] `FetchModelsResponse` with `{ providerId, models }` body exists
- [ ] `OperationFetchModels` operation definition exists
- [ ] Handler fetches provider and provider-endpoint binding from DB, returns 404 if not found
- [ ] Handler makes GET request to `upstream_url` with `setCredentialsHeaders` and 10s timeout
- [ ] Handler parses response with the priority chain (data[].id → data[].name → [].id → [].name)
- [ ] Handler updates `provider.provider_models` on success
- [ ] Handler returns 502 on upstream failure, 422 on parse failure
- [ ] Operation is registered in `registerOperations`
- [ ] `go build ./...` passes

**Verify:** `go build ./...` → success

**Steps:**

- [ ] **Step 1: Add contract types and operation to `provider_endpoint.go`**

Add after the existing `DeleteProviderEndpointRequest`:

```go
type FetchModelsRequest struct {
	Body struct {
		ProviderID   int32  `json:"providerId"`
		EndpointPath string `json:"endpointPath"`
	}
}

type FetchModelsResponse struct {
	Body struct {
		ProviderID int32    `json:"providerId"`
		Models     []string `json:"models"`
	}
}

var OperationFetchModels = huma.Operation{
	OperationID: "fetchModels",
	Method:      http.MethodPost,
	Path:        "/provider-endpoints/fetch-models",
	Summary:     "Fetch model list from upstream provider",
}
```

- [ ] **Step 2: Add handler to `handle_provider_endpoint.go`**

Add the handler function:

```go
func (s *Server) handleFetchModels(ctx context.Context, input *contract.FetchModelsRequest) (*contract.FetchModelsResponse, error) {
	// 1. Verify provider exists
	provider, err := s.queries.GetProviderByID(ctx, input.Body.ProviderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("provider not found")
		}
		return nil, huma.Error500InternalServerError("failed to get provider", err)
	}

	// 2. Verify provider-endpoint binding exists and get upstream URL
	pe, err := s.queries.GetProviderEndpoint(ctx, db.GetProviderEndpointParams{
		ProviderID:   input.Body.ProviderID,
		EndpointPath: input.Body.EndpointPath,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("provider-endpoint binding not found")
		}
		return nil, huma.Error500InternalServerError("failed to get provider endpoint", err)
	}

	// 3. Get endpoint for credentials_resolver
	endpoint, err := s.queries.GetEndpointByPath(ctx, input.Body.EndpointPath)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("endpoint not found")
		}
		return nil, huma.Error500InternalServerError("failed to get endpoint", err)
	}

	// 4. Build upstream request
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, pe.UpstreamUrl, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create upstream request", err)
	}

	setCredentialsHeaders(req.Header, provider.Credentials, endpoint.CredentialsResolver, nil)

	// 5. Execute request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, huma.Error502BadGateway("upstream request failed: " + err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, huma.Error502BadGateway(fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body)))
	}

	// 6. Parse response
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, huma.Error502BadGateway("failed to read upstream response: " + err.Error())
	}

	models, err := parseModelsResponse(body)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	// 7. Update provider.provider_models
	modelsJSON, err := json.Marshal(models)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to marshal models", err)
	}

	_, err = s.queries.UpdateProvider(ctx, db.UpdateProviderParams{
		ID:                provider.ID,
		SetName:           false,
		SetCredentials:    false,
		SetPriority:       false,
		SetProviderModels: true,
		ProviderModels:    modelsJSON,
		SetAnnotations:    false,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update provider models", err)
	}

	resp := &contract.FetchModelsResponse{}
	resp.Body.ProviderID = input.Body.ProviderID
	resp.Body.Models = models
	return resp, nil
}
```

Also add the `parseModelsResponse` helper. This can go in `handle_provider_endpoint.go` or a new file. Since it's specific to this handler, put it in the same file:

```go
// parseModelsResponse tries multiple response formats to extract model IDs.
// Priority: data[].id → data[].name → [].id → [].name
func parseModelsResponse(body []byte) ([]string, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	// Try data[].id
	if models := extractFieldFromData(raw, "id"); len(models) > 0 {
		return models, nil
	}
	// Try data[].name
	if models := extractFieldFromData(raw, "name"); len(models) > 0 {
		return models, nil
	}
	// Try top-level [].id
	if models := extractFieldFromTopLevel(raw, "id"); len(models) > 0 {
		return models, nil
	}
	// Try top-level [].name
	if models := extractFieldFromTopLevel(raw, "name"); len(models) > 0 {
		return models, nil
	}

	return nil, fmt.Errorf("could not parse models from upstream response")
}

func extractFieldFromData(raw any, field string) []string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	data, ok := obj["data"]
	if !ok {
		return nil
	}
	arr, ok := data.([]any)
	if !ok {
		return nil
	}
	return extractStrings(arr, field)
}

func extractFieldFromTopLevel(raw any, field string) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	return extractStrings(arr, field)
}

func extractStrings(arr []any, field string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		val, ok := obj[field].(string)
		if !ok || val == "" {
			continue
		}
		if !seen[val] {
			seen[val] = true
			result = append(result, val)
		}
	}
	sort.Strings(result)
	return result
}
```

Add necessary imports to `handle_provider_endpoint.go`:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)
```

- [ ] **Step 3: Add `GetProviderEndpoint` sqlc query**

In `db/queries/provider_endpoint.sql`, add:

```sql
-- name: GetProviderEndpoint :one
SELECT * FROM provider_endpoint
WHERE provider_id = $1 AND endpoint_path = $2;
```

Run `sqlc generate` to generate the Go code and types.

- [ ] **Step 4: Register the operation in `server.go`**

Add after the existing provider-endpoint registrations in `registerOperations`:

```go
huma.Register(mgmt, contract.OperationFetchModels, s.handleFetchModels)
```

- [ ] **Step 5: Build and verify**

Run: `go build ./...`
Expected: success

- [ ] **Step 6: Commit**

```bash
git add db/queries/provider_endpoint.sql pkg/db/ pkg/contract/provider_endpoint.go pkg/server/handle_provider_endpoint.go pkg/server/server.go
git commit -m "feat(api): add POST /provider-endpoints/fetch-models endpoint"
```

---

### Task 4: Regenerate OpenAPI spec and dashboard types

**Goal:** Regenerate `openapi.yaml` and the dashboard's typed API client to include the new endpoint and updated credentials resolver enum.

**Files:**
- Modify: `openapi.yaml` (regenerated)
- Modify: `dashboard/src/api.d.ts` (regenerated)

**Acceptance Criteria:**
- [ ] `openapi.yaml` includes the `fetchModels` operation and updated `credentialsResolver` enum
- [ ] `dashboard/src/api.d.ts` includes the `FetchModelsRequest` and `FetchModelsResponse` types
- [ ] Dashboard type-check passes

**Verify:** `pnpm --dir dashboard type-check` → success

**Steps:**

- [ ] **Step 1: Regenerate OpenAPI spec**

Run: `mise run openapi`

- [ ] **Step 2: Regenerate dashboard API types**

Run: `pnpm --dir dashboard exec openapi-typescript ../../openapi.yaml -o src/api.d.ts`

- [ ] **Step 3: Verify**

Run: `pnpm --dir dashboard type-check`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add openapi.yaml dashboard/src/api.d.ts
git commit -m "chore: regenerate openapi.yaml and dashboard API types for fetch-models"
```

---

### Task 5: Update endpoint form with new credentials resolver options

**Goal:** Add `bearerToken` and `xApiKey` options to the credentials resolver select in the endpoint form.

**Files:**
- Modify: `dashboard/src/components/EndpointForm.vue`

**Acceptance Criteria:**
- [ ] Credentials resolver dropdown shows four options: 通用 API Key, Bearer Token, X-Api-Key, unknown
- [ ] Default is `generalApiKey` for new endpoints
- [ ] Existing endpoint's resolver is preserved on edit
- [ ] Dashboard builds and type-checks

**Verify:** `pnpm --dir dashboard type-check` → success

**Steps:**

- [ ] **Step 1: Update the Select options**

In `EndpointForm.vue`, replace the current select:

```html
<Select v-model="form.credentialsResolver">
  <option value="generalApiKey">generalApiKey</option>
  <option value="unknown">unknown</option>
</Select>
```

with:

```html
<Select v-model="form.credentialsResolver">
  <option value="generalApiKey">通用 API Key</option>
  <option value="bearerToken">Bearer Token</option>
  <option value="xApiKey">X-Api-Key</option>
</Select>
```

Remove the `unknown` option — it shouldn't be user-selectable.

- [ ] **Step 2: Build and verify**

Run: `pnpm --dir dashboard type-check`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/components/EndpointForm.vue
git commit -m "feat(dashboard): add bearerToken and xApiKey to endpoint credentials resolver"
```

---

### Task 6: Add fetch-models icons and UI to binding panel

**Goal:** Add the cloud-download, loader, and check icons, and implement the fetch-models button in `ProviderEndpointsPanel.vue`.

**Files:**
- Modify: `dashboard/src/ui/icons/paths.ts`
- Modify: `dashboard/src/components/ProviderEndpointsPanel.vue`

**Acceptance Criteria:**
- [ ] `cloud-download`, `loader`, `check` icons available in the Icon component
- [ ] Fetch button appears only on binding rows where `endpointPath` ends with `/models`
- [ ] Button uses Tabler icons for all states (cloud-download for idle, loader for loading, check for success)
- [ ] Button is placed inline after the endpoint path text
- [ ] Clicking the button calls `POST /api/picotera/provider-endpoints/fetch-models`
- [ ] Loading state shows spinner + "拉取中…", disables the button
- [ ] Success state shows check + "N 个模型" in green for ~2s
- [ ] Failure shows error in panel's `#error` slot
- [ ] After success, emits `models-fetched` event with `{ providerId }`
- [ ] Dashboard builds and type-checks

**Verify:** `pnpm --dir dashboard type-check` → success

**Steps:**

- [ ] **Step 1: Add icons to `paths.ts`**

Add imports from `@tabler/icons-vue`:

```ts
import {
  IconActivity,
  IconAlignJustified,
  IconBraces,
  IconCheck,
  IconChevronDown,
  IconCloudDownload,
  IconCpu,
  IconDatabase,
  IconEdit,
  IconGitBranch,
  IconLink,
  IconList,
  IconLoader2,
  IconPlug,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconSettings,
  IconTrash,
  IconX,
} from '@tabler/icons-vue'
```

Add to `IconName` type: `'cloud-download' | 'loader' | 'check'`

Add to `iconComponents` map:

```ts
'cloud-download': IconCloudDownload,
loader: IconLoader2,
check: IconCheck,
```

- [ ] **Step 2: Update `ProviderEndpointsPanel.vue`**

Add reactive state for fetch:

```ts
const fetchState = reactive<Record<string, { loading: boolean; count: number | null }>>({})
```

Add the `fetchModels` function:

```ts
function isModelsEndpoint(path: string): boolean {
  return path.endsWith('/models')
}

async function fetchModels(endpointPath: string) {
  fetchState[endpointPath] = { loading: true, count: null }
  error.value = ''
  const { data, error: err } = await api.POST('/api/picotera/provider-endpoints/fetch-models', {
    body: { providerId: props.providerId, endpointPath },
  })
  if (err) {
    error.value = err.message ?? '拉取模型失败'
    fetchState[endpointPath] = { loading: false, count: null }
    return
  }
  const count = (data as any)?.models?.length ?? 0
  fetchState[endpointPath] = { loading: false, count }
  emit('modelsFetched', { providerId: props.providerId })
  setTimeout(() => {
    if (fetchState[endpointPath]) {
      fetchState[endpointPath].count = null
    }
  }, 2000)
}
```

Update `defineEmits`:

```ts
const emit = defineEmits<{ close: []; modelsFetched: [payload: { providerId: number }] }>()
```

Update the template — in the binding row `<li>`, after the endpoint path `<div>`, add the fetch button:

Replace the current path display:
```html
<div class="font-mono text-sm text-ink overflow-hidden text-ellipsis whitespace-nowrap">
  {{ pe.endpointPath }}
</div>
```

with:
```html
<div class="flex items-center gap-1.5">
  <span class="font-mono text-sm text-ink overflow-hidden text-ellipsis whitespace-nowrap">
    {{ pe.endpointPath }}
  </span>
  <button
    v-if="isModelsEndpoint(pe.endpointPath)"
    type="button"
    class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-xs border cursor-pointer shrink-0"
    :class="fetchState[pe.endpointPath]?.count !== null
      ? 'text-emerald-600 bg-emerald-50 border-emerald-200'
      : 'text-blue-600 bg-blue-50 border-blue-200 hover:bg-blue-100'"
    :disabled="fetchState[pe.endpointPath]?.loading"
    @click="fetchModels(pe.endpointPath)"
  >
    <Icon
      :name="fetchState[pe.endpointPath]?.loading ? 'loader' : fetchState[pe.endpointPath]?.count !== null ? 'check' : 'cloud-download'"
      :size="12"
      :class="fetchState[pe.endpointPath]?.loading ? 'animate-spin' : ''"
    />
    <span>{{ fetchState[pe.endpointPath]?.loading ? '拉取中…' : fetchState[pe.endpointPath]?.count !== null ? `${fetchState[pe.endpointPath]!.count} 个模型` : '拉取' }}</span>
  </button>
</div>
```

- [ ] **Step 3: Build and verify**

Run: `pnpm --dir dashboard type-check`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/ui/icons/paths.ts dashboard/src/components/ProviderEndpointsPanel.vue
git commit -m "feat(dashboard): add fetch-models button to provider-endpoint binding panel"
```

---

### Task 7: End-to-end verification

**Goal:** Verify the full feature works end-to-end — backend builds, frontend builds, OpenAPI types are consistent.

**Files:** None (verification only)

**Acceptance Criteria:**
- [ ] `go build ./...` succeeds
- [ ] `pnpm --dir dashboard build` succeeds
- [ ] `mise run openapi` produces consistent spec
- [ ] Manual test plan documented

**Verify:** `go build ./... && pnpm --dir dashboard build` → success

**Steps:**

- [ ] **Step 1: Full backend build**

Run: `go build ./...`

- [ ] **Step 2: Full frontend build**

Run: `pnpm --dir dashboard build`

- [ ] **Step 3: Regenerate OpenAPI and verify consistency**

Run: `mise run openapi && pnpm --dir dashboard exec openapi-typescript ../../openapi.yaml -o src/api.d.ts`

Verify no type errors: `pnpm --dir dashboard type-check`

- [ ] **Step 4: Manual test checklist**

1. Start the server: `mise run server` (with docker compose up for DB)
2. Create an endpoint with path `/models` and credentials resolver `generalApiKey`
3. Create a provider with credentials set to a valid API key
4. Bind the provider to the `/models` endpoint with a real upstream URL (e.g. `https://api.openai.com/v1/models`)
5. Open the provider's binding panel, verify the "拉取" button appears next to `/models`
6. Click "拉取" — verify loading spinner appears, then success state shows model count
7. Check provider detail — provider_models should be populated
8. Test with a bad upstream URL — verify error appears in panel
9. Test credentials resolver: create endpoint with `bearerToken`, verify auth header is correct
