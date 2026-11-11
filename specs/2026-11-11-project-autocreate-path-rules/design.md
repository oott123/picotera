# 设计

## 范围

只改 `pkg/server/project_extractor.go` 中自动创建项目时的选种逻辑（`maybeAutoCreate`）。已有项目的匹配（`ancestorPaths` + `MatchProjectByPaths`）不变：worktree 路径的祖先本就包含截断后的路径，因此按截断路径自动创建的项目会被后续来自同一 worktree 的请求命中。

## 分隔符

与 `ancestorPaths` / `lastPathComponent` 一致，`/` 和 `\` 都是分隔符，二者可混用，Unix 与 Windows 路径走同一套逻辑。

## 选种流程

对 `extractProjectCandidates` 返回的每个路径 `p` 依次：

1. **worktree 截断** `autoCreateSeedPath(p)`：
   - 用正则 `[/\\]worktrees[/\\]` 找 `p` 中**第一次**出现的 `worktrees` 组件（两侧都必须是分隔符），取匹配起点之前的部分；不存在则原样返回。末尾的 `/worktrees` 或 `\worktrees`（其后无分隔符）不算出现。
   - 截断后若最后一个组件（`lastPathComponent`）恰为 `.claude` 或 `.codex`，再去掉这一级（取最后一个分隔符之前的部分）。白名单为包级变量 `worktreeParentDirs`，只含这两项，大小写严格匹配。
2. **排除检查** `isExcludedAutoCreatePath(seed)`：去掉末尾分隔符后按 `/`、`\` 切分为组件，命中以下任一即排除：
   - `["", "root"]` 或 `["", "tmp"]`，即 `/root`、`/tmp`；
   - `["", "home", x]`，`x` 非空，即 `/home/<x>`；
   - `[d, "Users", x]`，`d` 为一个 ASCII 字母加 `:`（盘符），`x` 非空，即 `<盘符>:\Users\<x>`。`Users` 大小写严格匹配。
   组件数不等于上述形状的（如 `/home/alice/proj`、`/tmp/foo`、`C:\Users\alice\proj`、`/home//alice`）不排除。
3. **长度检查**：沿用 `len(seed) > autoCreateMinPathLen`。

通过 2、3 的 seed 中取最长者作为项目路径与名称来源（`lastPathComponent(seed)`），写入 `paths` 的也是 seed。被排除的 seed 只是跳过，其他合格 seed 仍可创建项目；全部不合格则不创建。

## 示例

| 提取到的路径 | seed | 结果 |
| --- | --- | --- |
| `/home/alice` | `/home/alice` | 排除 |
| `/home/alice/proj` | `/home/alice/proj` | 创建 `proj` |
| `/root`、`/tmp`、`/root/` | 同左 | 排除 |
| `/srv/proj/worktrees/feat/sub` | `/srv/proj` | 创建 `proj` |
| `/home/alice/proj/.claude/worktrees/feat` | `/home/alice/proj` | 创建 `proj` |
| `/home/alice/.codex/worktrees/ab12/proj` | `/home/alice` | 排除 |
| `/home/alice/worktrees/x` | `/home/alice` | 排除 |
| `/srv/proj/.git/worktrees/x` | `/srv/proj/.git` | 创建 `.git`（不在白名单） |
| `C:\Users\alice` | `C:\Users\alice` | 排除 |
| `C:\Users\alice\proj` | `C:\Users\alice\proj` | 创建 `proj` |
| `D:\src\proj\.claude\worktrees\feat` | `D:\src\proj` | 创建 `proj` |
| `C:\Users\alice\.codex\worktrees\ab12\proj` | `C:\Users\alice` | 排除 |
