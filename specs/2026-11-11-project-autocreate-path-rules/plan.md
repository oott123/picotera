# 执行计划

1. `pkg/server/project_extractor.go`
   - 新增包级变量 `worktreeParentDirs = []string{".claude", ".codex"}` 与 `worktreesComponentRe = regexp.MustCompile(`[/\\]worktrees[/\\]`)`，注释说明用途。
   - 新增 `autoCreateSeedPath(p string) string`：按 design.md 第 1 步做 `worktrees` 截断和白名单父目录上移，分隔符为 `/` 和 `\`。
   - 新增 `isExcludedAutoCreatePath(p string) bool`：按 design.md 第 2 步判断 `/root`、`/tmp`、`/home/<x>`、`<盘符>:\Users\<x>`。
   - 修改 `maybeAutoCreate` 的选种循环：对每个 `p` 先 `seed := autoCreateSeedPath(p)`，跳过 `isExcludedAutoCreatePath(seed)` 和长度不足的，取最长 seed 作为 `source`；其余逻辑（命名、重试、写入 `paths`）不变。
   - 更新文件头和 `maybeAutoCreate` 注释，说明截断与排除规则。
2. `pkg/server/project_extractor_test.go`
   - `TestAutoCreateSeedPath`：覆盖无 worktrees、Unix / Windows / 混合分隔符截断、多个 `worktrees` 组件取第一个、`.claude` / `.codex` 上移（Unix 与 Windows 各一）、`.git` 不上移、末尾 `worktrees` 不截断、`myworktrees` 这类非完整组件不截断。
   - `TestIsExcludedAutoCreatePath`：覆盖 design.md 示例表中的排除与不排除用例，另加 `/home/alice/`、`/home`、`/tmp/foo`、`/root/foo`、`C:\Users\alice\`、`C:\users\alice`（不排除）、`CD:\Users\alice`（不排除）。
3. `CLAUDE.md` 的 “Project matching” 条目补一句：自动创建时先截断 `worktrees` 组件（`.claude` / `.codex` 再上移一级），并排除 `/root`、`/tmp`、`/home/<x>`、`<盘符>:\Users\<x>`。
4. 验证：`go test ./pkg/server/ -run 'AutoCreate|AncestorPaths|LastPathComponent|ExtractProjectCandidates'` 与 `go build ./...`。
