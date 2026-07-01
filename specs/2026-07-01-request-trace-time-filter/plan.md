# Plan — 请求与追踪页面时间筛选器

## 1. Backend contract and SQL

1. Add `StartAt string \`query:"startAt,omitempty"\`` and `EndAt string \`query:"endAt,omitempty"\`` to `contract.ListRequestsRequest`.
2. Add the same fields to `contract.ListRequestTracesRequest`.
3. Update `db/queries/request.sql`:
   - `ListRequests` filters `r.created_at >= start_at` and `r.created_at <= end_at` when provided.
   - `ListRequestTraces` filters `traces.last_request_at >= start_at` and `traces.last_request_at <= end_at` when provided.
4. Run `sqlc generate`.

## 2. Backend handlers and validation

1. Add a strict time window parser in `pkg/server/handle_requests.go`.
2. Call the parser in `handleListRequests` and pass `StartAt` / `EndAt` into `db.ListRequestsParams`.
3. Call the parser in `handleListRequestTraces` and pass `StartAt` / `EndAt` into `db.ListRequestTracesParams`.
4. Add focused Go unit tests for:
   - valid RFC3339 and RFC3339Nano values.
   - invalid timestamps.
   - `startAt > endAt`.
   - empty values producing invalid `pgtype.Timestamp` filters.

## 3. OpenAPI and generated dashboard types

1. Run `mise run openapi`.
2. Run `pnpm --dir dashboard generate-openapi`.
3. Confirm generated `dashboard/src/openapi-types.d.ts` exposes `startAt` and `endAt` query parameters for both list endpoints.

## 4. Dashboard time range primitive

1. Create `dashboard/src/ui/TimeRangeFilter.vue`.
2. Implement trigger styling consistent with `ColumnFilter`.
3. Implement a floating panel with:
   - start datetime-local input.
   - end datetime-local input.
   - inline validation message.
   - `应用` and `清除` buttons.
4. Convert between local `datetime-local` values and UTC RFC3339 strings on apply/open.
5. Export the primitive from `dashboard/src/ui/index.ts`.
6. Update `dashboard/DESIGN_SYSTEM.md` to document `TimeRangeFilter` under filtering primitives.

## 5. RequestsView integration

1. Add `startAt` and `endAt` to the reactive filters from `route.query`.
2. Include the time range in `requestFilters`, `queryKeys.requests.list`, and `listRequests` calls.
3. Add `TimeRangeFilter` beside the `类型` segmented control.
4. Include the time range in the existing filter watcher.
5. Sync `startAt` and `endAt` to URL query in `syncFiltersToQuery` and delete `cursor` on changes.
6. Include the time range in `activeFilterCount` and `clearAllFilters`.
7. Add route query watchers for `startAt` and `endAt`.

## 6. TracesView integration

1. Add a local `filters` object with `startAt` and `endAt` initialized from `route.query`.
2. Add a computed trace list filter object with `limit`, `cursor`, `startAt`, and `endAt`.
3. Pass that object to `queryKeys.requestTraces.list` and `listRequestTraces`.
4. Add `TimeRangeFilter` in the page header.
5. Watch time filters to reset pagination and sync URL query.
6. Add route query watchers for `startAt` and `endAt`.
7. Keep row/project navigation unchanged, except cursor remains omitted when creating filtered list URLs.

## 7. Verification

1. Run `go test ./pkg/server ./pkg/llmbridge/...`.
2. Run `pnpm --dir dashboard type-check`.
3. Run `pnpm --dir dashboard lint`.
4. Run `pnpm --dir dashboard build`.
5. Manually verify in the dashboard:
   - requests page filters by the chosen time range.
   - traces page filters by recent request time.
   - clearing filters removes `startAt`, `endAt`, and `cursor` from the URL.
   - invalid ranges cannot be applied in the UI.
