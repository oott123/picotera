# 设计：Nix 打包

## 目标与边界

在仓库根目录新增一个只输出 `packages` 的 flake，使 `nix build` 能产出与 Dockerfile 等价的可运行产物：内嵌 dashboard 的 `picotera` 二进制 + `picotera-llmbridge-plugin` 二进制。

**不做**：nixos module、home-manager module、overlay、容器镜像（`dockerTools`）、CI 集成、`nix flake check`。

**不动**：`.nix/flake.nix`（devshell）与 `.envrc` 保持原样 —— 开发环境走 mise / 现有 devshell，打包走根 flake，两者互不引用。根 flake 不输出 `devShells`，所以 `nix develop` 仍需显式指向 `.nix/`（与现状一致）。

## flake 结构

单文件 `flake.nix`，四个 derivation 全部内联（不新建 `nix/` 目录 —— 与已有的 `.nix/` 目录名太接近，容易混淆）。

```nix
{
  inputs = {
    self.submodules = true;
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
  };
  outputs = { self, nixpkgs }: { packages = forAllSystems (...); };
}
```

**输入只有 nixpkgs。** 不引入 flake-utils，多系统展开用手写的 `genAttrs`（四个系统：`x86_64-linux` / `aarch64-linux` / `x86_64-darwin` / `aarch64-darwin`），少一个输入就少一处 lock 漂移。

nixpkgs 固定 `nixos-25.11`（与 `.nix/flake.lock` 同一分支）。该分支已验证具备本项目需要的全部工具链：

| 需求 | nixos-25.11 提供 |
| --- | --- |
| `go.mod` 声明 `go 1.26.1` | `go_1_26` = 1.26.2，构建器 `buildGo126Module` |
| `pnpm-lock.yaml` lockfileVersion 9.0 | `pnpm` = 10.28.0，含 `fetchDeps` / `configHook` |
| dashboard `engines.node >= 22.12` | `nodejs_24` = 24.15.0 |

### 子模块

`go.mod` 有三条指向 git submodule 的 `replace`：`third_party/go-sse`、`third_party/axonhub/llm`、`third_party/quickjs`。没有这三个目录 go build 直接失败。

用 **`inputs.self.submodules = true`**（Nix ≥ 2.27 的 flake 自引用特性）让 nix 在取自身源码时一并拉取子模块。已在本机 Nix 2.35 上验证：clean tree 与 dirty tree 下 `self + "/third/dep"` 均能读到子模块内容。

不采用"把三个子模块声明成 `flake = false` 的独立 input 再 copy 进去"的方案：那需要在 `flake.lock` 里第二次维护一份 rev，与 `.gitmodules` 之间没有任何机制保证一致。

代价：Nix < 2.27 的用户 build 会因 `third_party/` 为空而失败。这一点写进文档，不做兼容处理。

### 版本号

仓库没有版本号常量，二进制也不嵌版本。统一取 `version = "0-unstable-" + (self.shortRev or self.dirtyShortRev or "unknown")`，符合 nixpkgs 对无 release 上游的命名约定。

## 源码切片

用 `lib.fileset` 把源码切成两份，避免前端改动触发 Go 重编、反之亦然。

**`goSrc`**：`cmd/`、`pkg/`、`db/`、`go.mod`、`go.sum`、`LICENSE`、`THIRD_PARTY_NOTICES.md`，加上三个 `replace` 目标目录 `third_party/go-sse`、`third_party/axonhub/llm`、`third_party/quickjs`。

第三方只收这三个子目录而不是整个 `third_party/`：`third_party/axonhub` 是完整的上游仓库（含自己的 `go.mod`、前端、docs），而 `replace` 只指向其中的 `llm` 子模块，后者自带 `go.mod` 可独立解析。若将来新增 `replace`，构建会以"目录不存在"直接失败，改 fileset 即可。

`tools/` 无 Go 文件，不收。

**`webSrc`**：`dashboard/`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`。仓库没有根 `package.json`（pnpm workspace 仅靠 `pnpm-workspace.yaml` 工作，Dockerfile 已验证），不需要额外收文件。

## 包 1：`picotera-dashboard`（中间产物）

`stdenv.mkDerivation` + `pnpm.fetchDeps` + `pnpm.configHook`，产出 `$out` = `dashboard/dist` 的内容。

它是内置前端的必要中间步骤，顺带作为 `packages.picotera-dashboard` 暴露出去（零成本，便于单独调试前端构建）。

### `storeDir` 必须 patch 掉

`pnpm-workspace.yaml` 里有 `storeDir: ./.pnpm-store`。已实测确认：pnpm 10 中 `pnpm-workspace.yaml` 的 `storeDir` **优先级高于** `~/.npmrc`，而 nixpkgs 的 `fetchPnpmDeps` 与 `pnpmConfigHook` 都是靠 `pnpm config set store-dir`（写入 `$HOME/.npmrc`）来指定 store 位置的。不处理的话 store 会落到构建目录里，`$out` 为空，fixed-output hash 恒不匹配。

处理方式：两个 derivation 共用同一段 `postPatch`：

```nix
webPostPatch = "sed -i '/^storeDir:/d' pnpm-workspace.yaml";
```

`fetchPnpmDeps` 是普通的 `stdenvNoCC.mkDerivation`，`patchPhase` 正常执行，把 `postPatch` 透传给它即可。`storeDir` 不参与 `--frozen-lockfile` 的 settings 校验，删掉不影响锁文件一致性检查。不修改仓库里的 `pnpm-workspace.yaml` —— 那是开发者有意把 pnpm store 放进项目目录的选择。

### 依赖获取

```nix
pnpmDeps = pnpm.fetchDeps {
  inherit (finalAttrs) pname version src;
  postPatch = webPostPatch;
  fetcherVersion = 3;
  hash = "sha256-...";
};
```

不设 `pnpmWorkspaces`，即安装整个 workspace（只有 `dashboard` 一个成员）。

**postinstall 脚本无需补跑。** `fetchDeps` 与 `configHook` 都带 `--ignore-scripts`，唯一在 `pnpm-workspace.yaml` 的 `allowBuilds` 里声明过的包是 `vue-demi`。已拉取 `vue-demi@0.14.10` 的原始 tarball 确认：其 `lib/index.mjs` 与 `lib/index.cjs` 出厂即为 Vue 3 变体（`isVue3 = true`），postinstall 的 `vue-demi-fix` 对 Vue 3 是空操作。因此不加 `pnpm rebuild`。

### 构建命令

```
pnpm --dir dashboard build-only
```

用 `build-only`（纯 `vite build`）而非 `build`（`run-p type-check build-only`）。类型检查是开发流程的职责，与"构建时不跑 Go 测试"的取舍一致；`vue-tsc --build` 在打包路径上只是白白增加耗时与内存峰值。

## 包 2：`picotera-core`

`buildGo126Module`，`subPackages = [ "cmd/picotera" ]`。

```nix
env.CGO_ENABLED = 0;
ldflags = [ "-s" "-w" ];
doCheck = false;
vendorHash = "sha256-...";
```

`CGO_ENABLED = 0` 是安全的：JS 运行时走 `modernc.org/quickjs`（transpile 成纯 Go 的 libquickjs，已确认无 cgo、无 `.c` 文件），整个依赖树无 cgo 需求。产物为静态二进制。

`preBuild` 注入前端：

```sh
rm -rf pkg/server/static/dist
mkdir -p pkg/server/static/dist
cp -r ${picotera-dashboard}/. pkg/server/static/dist/
```

即 Dockerfile 里那步 `find ... -delete && cp -r dashboard/dist/.` 的等价物 —— 占位 `index.html` 与 `.gitignore` 一并清掉，`//go:embed all:dist` 嵌入真实资源（store 里的只读文件对 embed 没有影响）。

`postInstall` 装入 `share/doc/picotera/{LICENSE,THIRD_PARTY_NOTICES.md}`。

`meta`：`license = lib.licenses.bsd3`（仓库 LICENSE 为 BSD-3-Clause），`mainProgram = "picotera"`，`platforms` 为上述四个系统。

## 包 3：`picotera-llmbridge-plugin`

同样的 `buildGo126Module` 参数（共用 `goSrc` 与 `vendorHash`），`subPackages = [ "cmd/picotera-llmbridge-plugin" ]`，**不注入前端** —— 插件不引用 `pkg/server`，前端改动因此不会触发插件重编。

同样装入 `share/doc/`：插件是 `github.com/looplj/axonhub/llm`（LGPL-3.0）的实际链接方，`THIRD_PARTY_NOTICES.md` 必须随包分发。

两个 Go 包是独立 derivation，依赖会各编译一次。这是拆包的既定代价，换来的是"改前端不必重编插件"。

## 包 4：`picotera`（wrapper，`packages.default`）

`symlinkJoin` 合并前两个包，再 `wrapProgram`：

```nix
wrapProgram $out/bin/picotera \
  --set-default PICOTERA_LLMBRIDGE_PLUGIN_PATH ${picotera-llmbridge-plugin}/bin/picotera-llmbridge-plugin
```

用 `--set-default` 而非 `--set`：部署方仍可用环境变量覆盖插件路径。`$out/bin` 同时含 `picotera`（wrapper）与 `picotera-llmbridge-plugin`（软链）。

配置项对应 `configx.Config.LLMBridgePluginPath`（`mapstructure:"llmbridge_plugin_path"`）。

## 输出总览

```
packages.<system>.default                   = picotera        (wrapper)
packages.<system>.picotera                  = 同上
packages.<system>.picotera-core             = 裸主程序（含内嵌 dashboard）
packages.<system>.picotera-llmbridge-plugin = 插件
packages.<system>.picotera-dashboard        = 前端静态资源
```

## 需要维护的 hash

| hash | 何时失效 |
| --- | --- |
| `pnpmDeps.hash` | `pnpm-lock.yaml` 变动 |
| `vendorHash` | `go.mod` / `go.sum` 变动，或三个子模块 rev 变动 |

两者都是 fixed-output，失效时构建报 hash mismatch 并给出正确值，照抄即可。这是 nix 打包的固有成本，不做自动化。

## 已知风险

- **Nix 版本**：`inputs.self.submodules` 需要 Nix ≥ 2.27，更低版本会因 `third_party/` 为空而构建失败。
- **pnpm 大版本**：本机开发用 pnpm 11，nixpkgs 25.11 只有 pnpm 10。当前 `pnpm-lock.yaml` 仍是 lockfileVersion 9.0，pnpm 10 可读；若将来 pnpm 11 升级了 lockfile 格式，nix 侧需同步换 nixpkgs 分支。
- **darwin 未验证**：设计上纯 Go + 纯 JS 构建应当可跨平台，但本次只在 `x86_64-linux` 上实测。
