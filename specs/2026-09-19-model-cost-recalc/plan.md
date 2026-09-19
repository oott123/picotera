# 执行计划

后端一个 admin 端点 + 仪表盘一个侧栏。计价复用网关的 `computeCost`，按 `(created_at, id)` keyset 分批扫描并用 `unnest` 批量 UPDATE，结束时刷新一次 `request_overview_bucketed`。无表结构变更、无迁移。

## 1. 契约 `pkg/contract/model.go`

在 `OperationDeleteModel` 之后追加：

```go
type RecalculateModelCostsRequest struct {
	Body struct {
		Name  string `json:"name" required:"true" example:"gpt-5.6-luna"`
		Range string `json:"range,omitempty" example:"168h" doc:"Go duration parsed by time.ParseDuration; empty means the whole history"`
	}
}

type RecalculateModelCostsResponse struct {
	Body struct {
		Model   string  `json:"model"`
		Range   string  `json:"range"`
		StartAt *string `json:"startAt,omitempty" example:"2026-09-12T07:57:27Z"`
		EndAt   string  `json:"endAt" example:"2026-09-19T07:57:27Z"`
		Updated int64   `json:"updated" example:"1234"`
		TookMs  int64   `json:"tookMs" example:"812"`
	}
}

var OperationRecalculateModelCosts = huma.Operation{
	OperationID: "recalculateModelCosts",
	Method:      http.MethodPost,
	Path:        "/models/recalculate-cost",
	Summary:     "Recalculate the recorded cost of a model's historical requests",
}
```

## 2. sqlc 查询

### `db/queries/request.sql` 追加两条

```sql
-- name: ListRequestCostRecalcBatch :many
SELECT
  r.id,
  r.created_at,
  r.input_tokens,
  r.output_tokens,
  r.cache_read_tokens,
  r.cache_write_tokens,
  r.cache_write_1h_tokens
FROM request r
WHERE r.model = sqlc.arg('model')::text
  -- NULL 下界 = 「全部」；上界在处理开始时固定。
  AND (sqlc.narg('start_at')::timestamp IS NULL OR r.created_at >= sqlc.narg('start_at')::timestamp)
  AND r.created_at < sqlc.arg('end_at')::timestamp
  -- 只有终态行的 token 是最终值（见 design.md）。
  AND r.finish_reason IS NOT NULL
  AND (
    sqlc.narg('cursor_created_at')::timestamp IS NULL
    OR (r.created_at, r.id) > (sqlc.narg('cursor_created_at')::timestamp, sqlc.narg('cursor_id')::text)
  )
ORDER BY r.created_at ASC, r.id ASC
LIMIT sqlc.arg('limit')::int;

-- name: UpdateRequestCosts :exec
-- PostgreSQL only has single- and two-array unnest(), so four parallel arrays
-- go through ROWS FROM (pad-free by construction: equal-length slices).
-- A NULL element in `costs` means "not billable" and clears both columns; the
-- currency is a scalar because one recalculation bills one pricing's currency
-- (sqlc types a text[] arg as []string, which cannot carry SQL NULL anyway).
UPDATE request AS r
SET model_cost = v.model_cost,
    model_cost_currency = CASE WHEN v.model_cost IS NULL THEN NULL ELSE sqlc.arg('currency')::text END
FROM ROWS FROM (
  unnest(sqlc.arg('ids')::text[]),
  unnest(sqlc.arg('created_ats')::timestamp[]),
  unnest(sqlc.arg('costs')::numeric[])
) AS v(id, created_at, model_cost)
WHERE r.id = v.id AND r.created_at = v.created_at;
```

### `db/queries/overview.sql` 追加一条

```sql
-- name: RefreshRequestOverviewBucketed :exec
CALL refresh_continuous_aggregate('request_overview_bucketed', sqlc.narg('start_at')::timestamp, NULL);
```

`start_at` 为 NULL 时整表重物化（对应「全部历史」）。参数必须显式 `::timestamp`，且该 `CALL` 不能出现在事务块里（`design.md` 有实测记录）。

### 生成

`sqlc generate` → `pkg/db/request.sql.go`、`pkg/db/overview.sql.go`、`pkg/db/querier.go` 更新。生成的类型：`CursorCreatedAt pgtype.Timestamp`、`CursorID pgtype.Text`、`Currency string`、`Ids []string`、`CreatedAts []pgtype.Timestamp`、`Costs []pgtype.Numeric`（含 NULL 元素的 `Costs`/`CreatedAts` 已实测可编码：pgx 对 invalid 元素编码 SQL NULL）。

## 3. 处理函数 `pkg/server/handle_model_cost_recalc.go`（新增）

```go
package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// modelCostRecalcBatchSize bounds one scan+update round trip; the update
// statement carries four parallel arrays of this length.
const modelCostRecalcBatchSize = 2000

// recalcWindowStart parses the request's duration into the window's lower
// bound. An empty string means "no lower bound" (the whole history) and yields
// an invalid pgtype.Timestamp.
func recalcWindowStart(raw string, now time.Time) (pgtype.Timestamp, error) {
	if raw == "" {
		return pgtype.Timestamp{}, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return pgtype.Timestamp{}, fmt.Errorf("range must be a Go duration such as 24h or 168h: %w", err)
	}
	if d <= 0 {
		return pgtype.Timestamp{}, errors.New("range must be positive")
	}
	return pgtype.Timestamp{Time: now.Add(-d), Valid: true}, nil
}

func (s *Server) handleRecalculateModelCosts(ctx context.Context, input *contract.RecalculateModelCostsRequest) (*contract.RecalculateModelCostsResponse, error) {
	started := time.Now()
	model, err := s.queries.GetModelByName(ctx, input.Body.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("model not found")
		}
		return nil, huma.Error500InternalServerError("failed to get model", err)
	}
	pricing, err := contract.PricingFromJSONB(model.Pricing)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode model pricing", err)
	}
	if pricing == nil {
		return nil, huma.Error400BadRequest("model has no pricing")
	}

	end := time.Now().UTC()
	startAt, err := recalcWindowStart(input.Body.Range, end)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	var (
		updated  int64
		cursorAt pgtype.Timestamp
		cursorID pgtype.Text
	)
	for {
		rows, err := s.queries.ListRequestCostRecalcBatch(ctx, db.ListRequestCostRecalcBatchParams{
			Model:           input.Body.Name,
			StartAt:         startAt,
			EndAt:           pgtype.Timestamp{Time: end, Valid: true},
			CursorCreatedAt: cursorAt,
			CursorID:        cursorID,
			Limit:           modelCostRecalcBatchSize,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list requests for cost recalculation", err)
		}
		if len(rows) == 0 {
			break
		}
		params := db.UpdateRequestCostsParams{
			Currency:   pricing.Currency,
			Ids:        make([]string, len(rows)),
			CreatedAts: make([]pgtype.Timestamp, len(rows)),
			Costs:      make([]pgtype.Numeric, len(rows)),
		}
		for i, row := range rows {
			// computeCost already yields invalid (NULL) values when the row
			// cannot be priced — same behaviour as the gateway write path.
			cost, _, _ := computeCost(pricing,
				pgInt4ToPtr(row.InputTokens),
				pgInt4ToPtr(row.OutputTokens),
				pgInt4ToPtr(row.CacheReadTokens),
				pgInt4ToPtr(row.CacheWriteTokens),
				pgInt4ToPtr(row.CacheWrite1hTokens))
			params.Ids[i] = row.ID
			params.CreatedAts[i] = row.CreatedAt
			params.Costs[i] = cost
		}
		if err := s.queries.UpdateRequestCosts(ctx, params); err != nil {
			return nil, huma.Error500InternalServerError("failed to update request costs", err)
		}
		updated += int64(len(rows))
		last := rows[len(rows)-1]
		cursorAt = last.CreatedAt
		cursorID = pgtype.Text{String: last.ID, Valid: true}
		if len(rows) < modelCostRecalcBatchSize {
			break
		}
	}

	// The overview's cost cards read request_overview_bucketed; materialized
	// buckets never pick up rewritten rows on their own and the policy only
	// covers [now-35d, now-5m]. A failure here is logged, not fatal: the rows
	// are already committed and re-running the recalculation is idempotent.
	if updated > 0 {
		if err := s.queries.RefreshRequestOverviewBucketed(ctx, startAt); err != nil {
			logx.WithContext(ctx).WithError(err).WithField("model", input.Body.Name).
				Warn("failed to refresh overview continuous aggregate after cost recalculation")
		}
	}

	logx.WithContext(ctx).WithFields(map[string]any{
		"model":   input.Body.Name,
		"range":   input.Body.Range,
		"updated": updated,
		"tookMs":  time.Since(started).Milliseconds(),
	}).Info("recalculated model costs")

	resp := &contract.RecalculateModelCostsResponse{}
	resp.Body.Model = input.Body.Name
	resp.Body.Range = input.Body.Range
	if startAt.Valid {
		v := startAt.Time.UTC().Format(time.RFC3339)
		resp.Body.StartAt = &v
	}
	resp.Body.EndAt = end.Format(time.RFC3339)
	resp.Body.Updated = updated
	resp.Body.TookMs = time.Since(started).Milliseconds()
	return resp, nil
}
```

注意 `logx.WithFields` 的既有用法是 `logrus.Fields`（`logx.WithContext(ctx).WithFields(logrus.Fields{...})`），照抄库内写法。

## 4. 注册 `pkg/server/server.go`

admin 组内、`OperationDeleteModel` 之后：

```go
huma.Register(admin, contract.OperationRecalculateModelCosts, s.handleRecalculateModelCosts)
```

## 5. 单元测试 `pkg/server/model_cost_recalc_test.go`（新增）

表驱动测 `recalcWindowStart`（固定 `now`）：

| 输入 | 期望 |
| --- | --- |
| `""` | 无错误，返回 invalid `pgtype.Timestamp`（无下界） |
| `"24h"` / `"168h"` / `"720h"` | 无错误，`now - 24h/168h/720h` |
| `"90m"` | 无错误，`now - 90m`（单位不止小时） |
| `"7d"` / `"abc"` / `"24"` | 报错（`time.ParseDuration` 不认 `d`、非数字、缺单位） |
| `"0"` / `"0s"` / `"-24h"` | 报错（必须为正） |

## 6. 重新生成 OpenAPI 与前端类型

```bash
mise run openapi
pnpm --dir dashboard generate-openapi
```

## 7. 仪表盘

### 7.1 `dashboard/src/api/index.ts`

```ts
export type RecalculateModelCostsRequestBody = components['schemas']['RecalculateModelCostsRequestBody']
export type RecalculateModelCostsResponseBody = components['schemas']['RecalculateModelCostsResponseBody']
```

### 7.2 `dashboard/src/api/client.ts`

`deleteModel` 之后加 fetcher，文件末尾的 invalidate 区块加助手：

```ts
export async function recalculateModelCosts(
  body: RecalculateModelCostsRequestBody,
): Promise<RecalculateModelCostsResponseBody> {
  const { data, error } = await api.POST('/api/picotera/models/recalculate-cost', { body })
  if (error) fail(error, '重算历史费用失败')
  return data
}

// Request rows, traces and both overviews render model_cost.
export function invalidateRequestCosts(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.requests.all })
  client.invalidateQueries({ queryKey: queryKeys.requestTraces.all })
  client.invalidateQueries({ queryKey: queryKeys.overview.all })
  client.invalidateQueries({ queryKey: queryKeys.adminOverview.all })
}
```

（`RecalculateModelCostsRequestBody` / `ResponseBody` 加入 `@/api` 的 import 列表。）

### 7.3 `dashboard/src/components/ModelCostRecalcPanel.vue`（新增）

```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import type { ModelView, RecalculateModelCostsResponseBody } from '@/api'
import { invalidateRequestCosts, recalculateModelCosts } from '@/api/client'
import { Button, Field, Select, SidePanel } from '@/ui'

const props = defineProps<{ model: ModelView }>()
const emit = defineEmits<{ close: [] }>()

type CostRecalcRange = '24h' | '168h' | '720h' | ''

const queryClient = useQueryClient()
const range = ref<CostRecalcRange>('168h')
const result = ref<RecalculateModelCostsResponseBody | null>(null)

// Wire values are Go durations (time.ParseDuration): days are pre-converted to
// hours and the empty string means "the whole history".
const rangeOptions = [
  { value: '24h', label: '24 小时' },
  { value: '168h', label: '7 天' },
  { value: '720h', label: '30 天' },
  { value: '', label: '全部' },
]

const mutation = useMutation({
  mutationFn: () => recalculateModelCosts({ name: props.model.name, range: range.value }),
  onSuccess: (data) => {
    result.value = data
    invalidateRequestCosts(queryClient)
  },
})

const running = computed(() => mutation.isPending.value)
const error = computed(() => (mutation.error.value as Error | null)?.message ?? '')
</script>

<template>
  <SidePanel :title="model.name" kicker="重算历史费用" @close="emit('close')">
    <p class="m-0 text-xs text-ink-muted leading-[1.5]">
      按当前价格重新计算该模型在所选时间段内<strong>已结束</strong>请求的费用，覆盖原有的费用记录。区间越大耗时越久，重算期间请保持页面打开。
    </p>
    <Field label="时间区间" as="div">
      <Select
        :model-value="range"
        :options="rangeOptions"
        :searchable="false"
        @update:model-value="(v) => (range = String(v) as CostRecalcRange)"
      />
    </Field>
    <p v-if="running" class="m-0 bg-surface-50 text-ink-muted text-xs rounded-md px-3 py-2">
      重算中…请勿关闭页面。
    </p>
    <p v-else-if="result" class="m-0 bg-ok-faint text-ok-ink text-xs rounded-md px-3 py-2">
      已重算 {{ result.updated }} 条请求（耗时 {{ (result.tookMs / 1000).toFixed(1) }} 秒）。
    </p>
    <template #error>{{ error }}</template>
    <template #footer>
      <Button variant="ghost" :disabled="running" @click="emit('close')">关闭</Button>
      <Button :disabled="running" @click="mutation.mutate()">
        {{ running ? '重算中…' : '开始重算' }}
      </Button>
    </template>
  </SidePanel>
</template>
```

`bg-ok-faint text-ok-ink` 是 `DESIGN_SYSTEM.md` 里登记的状态色对（tinted surface + text），直接使用。

### 7.4 `dashboard/src/views/ModelsView.vue`

- import `ModelCostRecalcPanel`；
- 新增：
  ```ts
  function openCostRecalc(m: ModelView) {
    panel.open(ModelCostRecalcPanel, { model: m }, { key: `model-cost-recalc:${m.name}` })
  }
  ```
- `Td actions` 内「导出价格表达式」按钮之后插入（与导出按钮同样的「有价格才显示」条件）：

  ```vue
  <IconButton
    v-if="m.pricing?.tiers?.length"
    :active="panel.isActive(`model-cost-recalc:${m.name}`)"
    title="重算历史费用"
    aria-label="重算历史费用"
    @click="openCostRecalc(m)"
  >
    <Icon name="refresh" :size="13" />
  </IconButton>
  ```

`refresh` 已在 `src/ui/icons/paths.ts` 注册。

## 8. 验证

后端（本地 dev 库 `localhost:34052`；用一次性模型 + 两条合成请求行，不改动任何真实模型的价格）：

1. 建一次性模型 `recaltest-model`：`PUT /api/picotera/models`，body 为
   `{"name":"recaltest-model","disabled":false,"pricing":{"currency":"USD","tiers":[{"minInputTokens":0,"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"cacheWrite1h":0,"implicitCacheRead":0}]},"annotations":{}}`。
   单档下每行预期费用 = `1*1000000/1e6 + 2*500000/1e6 = 2.000000`。
2. 插入两条 40 天前的合成请求行（type 0 / type 1 各一条，同一 `span_id`，`model_cost` 留 NULL）：
   ```sql
   INSERT INTO request (id, span_id, type, model, endpoint_path, user_id, created_at, input_tokens, output_tokens, finish_reason, status_code)
   VALUES ('recaltest-m', 'recaltest', 0, 'recaltest-model', '<completion endpoint path>', 1, now()::timestamp - interval '40 days', 1000000, 500000, 3, 200),
          ('recaltest-u', 'recaltest', 1, 'recaltest-model', '<completion endpoint path>', 1, now()::timestamp - interval '40 days', 1000000, 500000, 3, 200);
   ```
3. 手工把连续聚合物化一遍（40 天前的桶早已超出策略窗口，只能显式物化），确认基线是 0：
   ```sql
   CALL refresh_continuous_aggregate('request_overview_bucketed', NULL, NULL);
   SELECT COALESCE(SUM(cost), 0) FROM request_overview_bucketed WHERE model = 'recaltest-model';  -- 0
   ```
4. 调端点（仪表盘点按钮，或在浏览器里复制登录 cookie 后 curl）：
   - `range = "24h"` → `updated = 0`，两行 `model_cost` 仍为 NULL（40 天前在窗口外，验证下界生效）；
   - `range = "7d"` → 400（`time.ParseDuration` 不认 `d`，验证 fail-fast）；
   - `range = ""`（或不带该字段）→ `updated = 2`，`startAt = null`。
5. 断言：
   ```sql
   SELECT id, model_cost, model_cost_currency FROM request WHERE id IN ('recaltest-m','recaltest-u');  -- 两行都是 2.000000 / USD
   SELECT COALESCE(SUM(cost), 0) FROM request_overview_bucketed WHERE model = 'recaltest-model';       -- 2.000000
   ```
   第 3 步的 0 → 第 5 步的 2.000000 就是端点内部的连续聚合刷新在起作用（已物化的桶不会随底层行自己变化）。
6. 再调一次 `range = ""` → `updated = 2`，两处值不变（幂等）。
7. 清理：`DELETE FROM request WHERE id IN ('recaltest-m','recaltest-u');`、`POST /api/picotera/models/delete`（body `{"name":"recaltest-model"}`）、再 `CALL refresh_continuous_aggregate('request_overview_bucketed', NULL, NULL)`。

前端：

- `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint` 通过；
- `mise run server` + `mise run web`，模型页点「重算历史费用」图标 → 侧栏出现、四档区间可切换、提交后显示「重算中…」并在完成后显示条数；请求列表与概览的费用随之更新；
- 没有价格的模型（价格列为「匹配价格」按钮的那些）不显示该图标。

## 涉及文件

- 修改：`pkg/contract/model.go`、`db/queries/request.sql`、`db/queries/overview.sql`、`pkg/db/*`（sqlc 生成）、`pkg/server/server.go`、`openapi.yaml`（生成）、`dashboard/src/api/index.ts`、`dashboard/src/api/client.ts`、`dashboard/src/openapi-types.d.ts`（生成）、`dashboard/src/views/ModelsView.vue`
- 新增：`pkg/server/handle_model_cost_recalc.go`、`pkg/server/model_cost_recalc_test.go`、`dashboard/src/components/ModelCostRecalcPanel.vue`
- 不动：数据库结构/迁移、`AGENTS.md`、`README.md`