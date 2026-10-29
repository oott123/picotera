# API 设计：多用户功能

第一期只新增一个 Huma operation。用户 / 身份的增删改第一期不暴露 REST API（无管理界面），仅由身份中间件与 CLI 写入。

## GET /api/picotera/me

获取当前已鉴权用户的信息。需鉴权（经过身份中间件）。

### 请求

无 body、无参数。身份由中间件从请求解析（header / 单用户模式）。

### 响应 200

```json
{
  "id": 1,
  "displayName": "alice",
  "isAdmin": false
}
```

### 契约类型（`pkg/contract/user.go`）

```go
type MeView struct {
    ID          int64  `json:"id"`
    DisplayName string `json:"displayName"`
    IsAdmin     bool   `json:"isAdmin"`
}

type GetMeResponse struct {
    Body MeView
}

var OperationGetMe = huma.Operation{
    OperationID: "getMe",
    Method:      http.MethodGet,
    Path:        "/me",
    Summary:     "Get current user",
    Tags:        []string{"User"},
}
```

handler：

```go
func (s *Server) handleGetMe(ctx context.Context, _ *struct{}) (*contract.GetMeResponse, error) {
    u := auth.UserFromContext(ctx)
    if u == nil {
        return nil, huma.Error500InternalServerError("no authenticated user")
    }
    return &contract.GetMeResponse{Body: contract.ToMeView(u)}, nil
}
```

在 `registerOperations()` 的 `mgmt` 组内注册：

```go
huma.Register(mgmt, contract.OperationGetMe, s.handleGetMe)
```

## 鉴权失败响应

身份中间件在解析失败时直接写响应，不进入 Huma：

- `401`，body `{"message":"unauthorized"}`，`Content-Type: application/json`。

## 迁移后的 unified 路由（非 Huma operation，不进 openapi.yaml）

| 旧路径 | 新路径 |
|---|---|
| `POST /api/picotera/v1/messages` | `POST /api/unified/v1/messages` |
| `POST /api/picotera/v1/responses` | `POST /api/unified/v1/responses` |
| `POST /api/picotera/v1/chat/completions` | `POST /api/unified/v1/chat/completions` |
| `POST /api/picotera/v1beta/models/{model}:generateContent` | `POST /api/unified/v1beta/models/{model}:generateContent` |
| `POST /api/picotera/v1beta/models/{model}:streamGenerateContent` | `POST /api/unified/v1beta/models/{model}:streamGenerateContent` |
