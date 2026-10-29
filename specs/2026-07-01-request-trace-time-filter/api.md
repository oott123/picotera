# API — 请求与追踪页面时间筛选器

## GET /api/picotera/requests

### New Query Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `startAt` | string | no | RFC3339/RFC3339Nano timestamp. Includes requests whose `createdAt` is greater than or equal to this value. |
| `endAt` | string | no | RFC3339/RFC3339Nano timestamp. Includes requests whose `createdAt` is less than or equal to this value. |

### Validation

- Non-empty `startAt` and `endAt` must parse with `time.RFC3339Nano`.
- Inputs must include a timezone offset or `Z`.
- The server rejects invalid timestamps with HTTP 400.
- When both values are present, `startAt` must be earlier than or equal to `endAt`; otherwise the server returns HTTP 400.
- The server does not trim, normalize, case-fold, or infer missing timezone information.

### Example

```http
GET /api/picotera/requests?type=0&startAt=2026-07-01T00%3A00%3A00Z&endAt=2026-07-01T23%3A59%3A59Z&limit=30
```

## GET /api/picotera/request-traces

### New Query Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `startAt` | string | no | RFC3339/RFC3339Nano timestamp. Includes traces whose `lastRequestAt` is greater than or equal to this value. |
| `endAt` | string | no | RFC3339/RFC3339Nano timestamp. Includes traces whose `lastRequestAt` is less than or equal to this value. |

### Validation

Validation rules match `GET /api/picotera/requests`.

### Example

```http
GET /api/picotera/request-traces?startAt=2026-07-01T00%3A00%3A00Z&endAt=2026-07-01T23%3A59%3A59Z&limit=30
```

## Dashboard URL Query

Both dashboard routes persist the filter with the same names:

- `/requests?startAt=2026-07-01T00%3A00%3A00.000Z&endAt=2026-07-01T12%3A00%3A00.000Z`
- `/traces?startAt=2026-07-01T00%3A00%3A00.000Z&endAt=2026-07-01T12%3A00%3A00.000Z`

Changing either time value deletes `cursor` from the route query before reloading the list.
