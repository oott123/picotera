# Nix 打包支持

## 原始需求

为本项目增加 nix 支持，只需要打 nix 包出来就行，不需要 nixos module 之类的。

## 澄清补充

规划期间与用户确认的细节：

1. **flake 位置**：新增根目录 `flake.nix`，只输出 `packages`。现有的 `.nix/flake.nix`（devshell）原样保留，`.envrc` 不动。两份 `flake.lock` 各自独立。
2. **前端**：nix 包内置编译好的 dashboard，即等价于 Dockerfile 的产物（`dashboard/dist` 塞进 `pkg/server/static/dist` 后再 `go build`）。
3. **包拆分**：拆成三个包 —— 主程序 `picotera-core`（裸 go 二进制）、`picotera-llmbridge-plugin`、以及 wrapper 主包 `picotera`（`packages.default`，给主程序设好 `PICOTERA_LLMBRIDGE_PLUGIN_PATH` 默认值）。
4. **测试**：构建时不跑 Go 单元测试（`doCheck = false`）。
