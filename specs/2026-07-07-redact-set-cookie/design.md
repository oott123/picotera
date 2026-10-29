# 设计：响应头 Set-Cookie 脱敏

## 背景

现有脱敏逻辑 `redactUpstreamCredentials`（`pkg/server/gateway_helpers.go`）仅对**请求头**的凭据（`Authorization`、`X-Api-Key`、`X-Goog-Api-Key`、`Cf-Access-*`、URL `key` 查询参数）在 artifact 副本上脱敏，实际发往上游的请求保留真实凭据。响应头目前**未做任何脱敏**，直接随 response artifact 持久化。

响应 artifact 通过 `pkg/server/gateway_flow_success.go` 中的 4 个 helper 上传，覆盖全部上传路径（path success/error、unified success/error、upstream/meta）：

- `uploadResponseArtifact`
- `uploadResponseArtifactWithAggregation`
- `uploadMetaResponseArtifact`
- `uploadMetaResponseArtifactWithAggregation`

`copyPathSuccessHeaders` 会把上游所有响应头（含 `Set-Cookie`）转发给客户端，因此 upstream 与 meta 两类 response artifact 都可能携带真实 cookie。

## 库选型：为何不复用 stdlib `http.ParseSetCookie`

Go 1.26 标准库提供 `http.ParseSetCookie(line) (*Cookie, error)` 与 `Cookie.String()`，公开 API 可用。但 `ParseSetCookie → 改写 Value → String()` 的往返对**脱敏场景有损**，违背"仅替换 value、其余字节不变"的契约：

- `Cookie.String()` 从结构体字段重新序列化，**不输出 `Unparsed`**：非标准/未来属性（如 `Priority`、厂商自定义属性）静默丢失。
- 属性顺序被规范化为固定顺序（Path, Domain, Expires, MaxAge, HttpOnly, Secure, SameSite, Partitioned），非原始顺序。
- `Expires` 被重格式化为 `Mon, 02 Jan 2006 15:04:05 GMT`；`Domain` 前导 `.` 被剥离；`Max-Age: 0` 被丢弃（解析为 `MaxAge=0` 即"未指定"）；`SameSite` 取未知值时归为 DefaultMode 不再输出。
- **最关键**：`ParseSetCookie` 对无效 name/value 返回 error，`readSetCookies` 直接丢弃该 cookie。脱敏的契约是"改写而非删除"——上游返回的 cookie 若含 stdlib 不接受的字节会被整条删除而非脱敏。

`Cookie.Raw` 保留原始行，可做混合方案（解析后回拼进 `Raw`），但 `ParseSetCookie` 会 trim name/value，从 `Raw` 重构原始 value token 去替换的脆弱度与手写解析相当，且仍需 malformed 兜底。无知名第三方库做无损 Set-Cookie 往返（`gorilla/securecookie` 是签名而非解析）。

stdlib 的 cookie 模型面向 **cookie-jar 语义**（该 cookie 对后续请求的含义），而非**头部保真**。脱敏需要后者。手写的 value 边界解析（首个 `=` 之后、首个 `;` 或闭合 `"` 之前）是一个边界清晰的小子问题，且能保真保留其余所有字节，正是脱敏契约所需。

## 方案

### 1. 新增 `redactResponseHeaders`

在 `pkg/server/gateway_helpers.go` 中、`redactUpstreamCredentials` 旁新增，复用现有 `redactedPlaceholder` 常量。与 `redactUpstreamCredentials` 同样约定：调用方传入 clone，函数原地修改并返回。

```go
// redactResponseHeaders redacts sensitive response headers in a cloned header
// (the caller passes a clone), returning the redacted header. It mutates the
// provided header in place and only touches fields that carry a secret:
//   - Set-Cookie: replaces each cookie's value with [REDACTED], preserving the
//     cookie name and all attributes (Path, Domain, HttpOnly, Secure, …).
func redactResponseHeaders(header http.Header) http.Header {
	values := header.Values("Set-Cookie")
	if len(values) == 0 {
		return header
	}
	redacted := make([]string, len(values))
	for i, v := range values {
		redacted[i] = redactSetCookieValue(v)
	}
	header.Del("Set-Cookie")
	for _, v := range redacted {
		header.Add("Set-Cookie", v)
	}
	return header
}
```

`Set-Cookie` 是多值头（一条响应可含多个），`http.Header.Set/Get` 会折叠多值，因此用 `Values` 读取、`Del`+`Add` 逐条写回。

### 2. cookie 值解析 `redactSetCookieValue`

`Set-Cookie` 形如 `name=value; attr1; attr2`。value 是第一个 `=` 之后、第一个 `;` 之前的内容；带引号的 value（`name="quoted"; …`）以闭合 `"` 为界，需识别 `\"` 转义，避免引号内含 `;` 时泄漏。

```go
// redactSetCookieValue replaces the cookie value in a single Set-Cookie header
// value with [REDACTED], keeping the cookie name and all attributes. A value
// with no '=' (malformed) is replaced wholesale.
func redactSetCookieValue(v string) string {
	name, rest, ok := strings.Cut(v, "=")
	if !ok {
		return redactedPlaceholder
	}
	var attrs string
	if strings.HasPrefix(rest, `"`) {
		// Quoted value: ends at the closing quote (respecting \" escapes);
		// the remainder is the attributes.
		i := 1
		for i < len(rest) {
			if rest[i] == '\\' && i+1 < len(rest) {
				i += 2
				continue
			}
			if rest[i] == '"' {
				break
			}
			i++
		}
		if i < len(rest) && rest[i] == '"' {
			attrs = rest[i+1:]
		} else {
			// No closing quote — malformed; redact the whole tail.
			return name + "=" + redactedPlaceholder
		}
	} else {
		// Unquoted value: ends at the first ';'.
		if _, tail, hasSemi := strings.Cut(rest, ";"); hasSemi {
			attrs = ";" + tail
		}
	}
	return name + "=" + redactedPlaceholder + attrs
}
```

边界处理：

- 无 `=`（畸形）：整条替换为 `[REDACTED]`。
- 空 value（`session=; Path=/`）：`session=[REDACTED]; Path=/`。
- value 含 `=`（如 base64，`s=a=b=c; Path=/`）：仅替换第一个 `=` 与第一个 `;` 之间 → `s=[REDACTED]; Path=/`。
- 引号内含 `;`（`foo="a;b"; Path=/`）：识别闭合引号 → `foo=[REDACTED]; Path=/`，不泄漏 `b"`。
- 无属性（`token=xyz`）：`token=[REDACTED]`。

### 3. 接入 4 个 response artifact helper

在 `gateway_flow_success.go` 的 4 个 helper 中，于 `artifacts.Enabled()` 早返之后、`artifacts.BuildResponse*` 之前，对入参 `header` 脱敏。所有调用方传入的都是 `.Clone()` 副本，原地修改安全：

```go
header = redactResponseHeaders(header)
```

这一处改动覆盖全部 response artifact 上传路径（upstream + meta、path + unified、success + error），无需逐个调用点修改。

## 范围与不变量

- 仅影响**存档副本**：实际下发给客户端的响应头不变，cookie 正常转发。
- 同时覆盖 upstream response artifact 与 meta response artifact：两类都可能携带真实 cookie，均为会话敏感信息，统一脱敏。
- 不改动 `pkg/artifacts` 包：脱敏在 server 包完成，与现有 `redactUpstreamCredentials` 的归属一致。
- 无 API 变更，无 schema 变更，无配置项。
