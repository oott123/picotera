# PicoTera

一款偏好明确的个人 LLM 网关。vibe coding 产物，包含 99% 以上的 AI 生成代码。

项目尚在开发过程中，但主要功能已可用。

## 功能特点

* 通过脚本定义各类路由行为
* 默认透传；可集成 AxonHub 转换库并自由配置转换参数，供不时之需
* 基于工作目录自动识别项目，并分别统计成本
* 多币种费用统计
* 完整的请求/响应日志
* 基于反向代理认证的多用户系统

## 界面展示

### 概览页

流量结构桑基图

![](./docs/screenshots/overview1.webp)

各类堆叠图、饼图

![](./docs/screenshots/overview2.webp)

### 端点页

自由设置访问端点

![](./docs/screenshots/endpoints.webp)

### 脚本页

自定义行为

![](./docs/screenshots/scripts.webp)

### 请求详情页

可看当前元请求（客户端请求）和上游请求（服务端请求）的完整详细信息

![](./docs/screenshots/requests.webp)

## 安装

> [!CAUTION]
> 本项目的管理 API 默认没有鉴权，因此请勿将其暴露到公网使用！

### Docker

请使用如下镜像运行 picotera ，并发布 9898 端口：

```
ghcr.io/oott123/picotera:master
```

该镜像包含 LGPL 的请求转换插件，位于 `/app/picotera-llmbridge-plugin` 。

根据 LGPL 许可证的授权，你可以自由地通过挂载文件以替换该组件。

### Docker Compose

请参考 [docker-compose.yaml](./docs/deploy/docker-compose.yaml)。

特别注意，请修改 minio 的默认密码，防止 minio 被未授权访问。

### 外部依赖

* TimescaleDB - 必选
* Redis - 可选：用于脚本 KV 存储，如果没有则自动回落自带内存 KV 引擎
* S3 兼容的对象存储 - 可选：用于请求/响应存储，如果没有则不会记录请求响应头/体，只有部分元数据被记录

### 配置样例

```
PICOTERA_DATABASE_URL=postgres://picotera:picotera@localhost:34052/picotera
PICOTERA_S3_ENDPOINT=localhost:34050
PICOTERA_S3_REGION=us-east-1
PICOTERA_S3_ACCESS_KEY=picotera
PICOTERA_S3_SECRET_KEY=picotera-dev
PICOTERA_S3_USE_SSL=false
PICOTERA_S3_BUCKET=picotera-artifacts
PICOTERA_S3_PATH_STYLE=true
PICOTERA_S3_PUBLIC_URL=http://localhost:34050
```

### 请求转换插件

请求转换插件使用 AxonHub 的 LGPL 代码。

如果自行编译运行 PicoTera ，需要单独编译请求转换插件，通过 go-plugin 作为独立进程使用：

```bash
mise run llmbridge-plugin
```

生成的 `dist/picotera-llmbridge-plugin` 需要通过 `PICOTERA_LLMBRIDGE_PLUGIN_PATH` 提供给主程序。该路径会按原样执行；为空时跨格式转换被禁用，同格式请求仍保持透传。

### 优化 Timescaledb 参数

```bash
docker compose exec -it postgres timescaledb-tune --yes -cpus 1 -memory 512MB
```

## 多用户

### 单用户模式

提供如下环境变量，启动单用户模式：

```env
PICOTERA_AUTH_SINGLE_USER_MODE=true
```

单用户模式下，所有管理接口、控制台不做鉴权，默认归属于自动创建的、名为 root 的用户下。

### 多用户模式

如需多用户功能，需要在 PicoTera 之外，部署一层反向代理进行鉴权，常见的有 oauth2-proxy 或 traefik 的 forward auth。

提供如下变量以开启多用户模式：

```env
PICOTERA_AUTH_HEADER_ENABLED=true
PICOTERA_AUTH_HEADER_NAME=X-User-Identity # 用户唯一识别符的 Header
PICOTERA_AUTH_AUTO_CREATE_USER=true # 自动创建没有见过的用户
```

### 通过命令行绑定用户

如果没有使用自动创建用户，你应运行如下命令以绑定提供商到现存用户：

```bash
mise bind-identity -- <identity_provider> <identity> <user_id>
# 例如
mise bind-identity -- http-header root 1
```

## 初次使用

1. 创建各类端点。可以参考文档开头的端点界面截图。
2. 创建一个渠道。
3. 点击渠道右侧的模型按钮，拉取模型。
4. 点击渠道右侧的链接按钮，将上游绑定到支持的端点。
5. 在模型页面，展开未注册上游模型，将模型添加到支持列表。可以先不填写价格。
6. 在模型界面点击“匹配价格”，匹配一个合适的价格。
7. 在密钥界面创建一个新的密钥。
8. 在设置界面允许项目自动创建。
9. 设置你的 harness，将 `https://picotera/api/unified` 作为 Base URL，将刚才创建的密钥作为密钥。
   如果不需要请求转换（透传），则将 `https://picotera` 作为 Base URL。
10. 探索 <./docs/example-scripts> 目录，检查是否有需要的脚本。

## 协议

* `cmd/picotera-llmbridge-plugin`, `pkg/llmbridgeimpl`, `third_party/axonhub`: LGPLv3
* 其它： BSD 3-Clause
