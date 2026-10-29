# 执行计划：Nix 打包

## 1. 编写根 `flake.nix`

新建仓库根目录 `flake.nix`，内容按 `design.md` 组织：

1. `inputs`：`self.submodules = true`，`nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11"`。
2. `forAllSystems` 辅助函数，展开 `x86_64-linux` / `aarch64-linux` / `x86_64-darwin` / `aarch64-darwin`。
3. `let` 层：`version`、`goSrc`、`webSrc`（`lib.fileset.toSource`）、`webPostPatch`。
4. 四个 derivation：`picotera-dashboard`、`picotera-core`、`picotera-llmbridge-plugin`、`picotera`。
5. `packages.<system>` 暴露五个 attr（含 `default`）。

两个 hash 先写成空串 `""`。

## 2. 求 `pnpmDeps.hash`

```bash
nix build .#picotera-dashboard --no-link 2>&1 | tail -20
```

从 hash mismatch 报错里取 `got:` 的值填回 `flake.nix`，重跑至前端构建通过。

若报 `ERR_PNPM_NO_OFFLINE_TARBALL` 或 `$out` 为空，检查 `webPostPatch` 是否确实删掉了 `pnpm-workspace.yaml` 的 `storeDir` 行（`fetchDeps` 与主 derivation 都要带上）。

验收：`ls $(nix build .#picotera-dashboard --print-out-paths)` 有 `index.html` 与 `assets/`。

## 3. 求 `vendorHash`

```bash
nix build .#picotera-llmbridge-plugin --no-link 2>&1 | tail -20
```

先建插件（不依赖前端，反馈快）。同样从报错取 hash 填回。

若报三个 `replace` 目录缺失，说明子模块没被带进源码 —— 确认 `nix --version` ≥ 2.27 且 `git submodule status` 无未初始化项。

验收：`nix build .#picotera-llmbridge-plugin --print-out-paths` 下有 `bin/picotera-llmbridge-plugin`。

## 4. 构建主程序与 wrapper

```bash
nix build .#picotera-core --no-link
nix build . --print-out-paths
```

验收：

- `file result/bin/picotera` 显示静态链接的 ELF。
- `grep -c picotera-llmbridge-plugin result/bin/picotera` 命中（wrapper 脚本里含 `--set-default` 的路径）。
- `result/bin/picotera-llmbridge-plugin` 存在。
- `result/share/doc/picotera/THIRD_PARTY_NOTICES.md` 存在。

## 5. 冒烟验证

1. `nix run . -- --help` 正常输出。
2. 确认前端真的嵌进去了：`nix run . -- openapi | head -3` 能跑通说明二进制可用；再用 `strings result/bin/picotera | grep -c 'assets/index-'` 或直接起服务后 `curl -I http://127.0.0.1:9898/` 看是否返回真实 SPA（而非占位 `index.html`）。占位页含 "placeholder" 字样，可用它区分。
3. 用 `.env` 里的现成数据库连接实起一次服务，确认 llmbridge 插件被默认路径拉起（日志无 plugin path 相关报错）。

## 6. 收尾

1. `.gitignore` 增加 `/result`（`nix build` 的默认软链）。
2. `git add flake.nix flake.lock`（`flake.lock` 由 `nix build` 自动生成，需一并提交）。
3. `CLAUDE.md` 的 **Build & Run Commands** 一节增加 Nix 小节：`nix build` 的五个 attr、两个 hash 何时需要更新、`inputs.self.submodules` 对 Nix ≥ 2.27 的要求。
4. 不改 `README.md`，不改 `.nix/`、`.envrc`、`mise.toml`、`Dockerfile`。
