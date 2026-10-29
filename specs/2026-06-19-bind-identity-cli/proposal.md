# bind-identity 命令

给主程序增加一个命令行子命令，用来把身份绑定到用户：输入 identity provider name、identity、user id，将对应的 (provider, identity) 身份映射绑定到该用户上。

## 需求

1. 新增 CLI 子命令 `bind-identity <provider> <identity> <user-id>`，在 `user_identity` 表中写入一条把 `(provider, identity)` 指向 `user-id` 的映射。
2. 必须先校验 `user-id` 对应的用户存在，不存在则报错退出（fail-fast）。
3. 当 `(provider, identity)` 已经绑定到某个用户时：
   - 默认报错并退出，提示当前所属用户；
   - 携带 `--force` 开关时则覆盖改绑到新的 `user-id`。
4. 若 `(provider, identity)` 已经绑定到目标 `user-id` 本身，视为幂等成功（无需 `--force`）。

## 补充确认（规划期间澄清）

- **命令命名**：使用 `bind-identity`（绑定的是身份），参数顺序为 `<provider> <identity> <user-id>`，与现有 `set-admin <user-id>` 同属顶层子命令。
- **冲突处理**：默认报错退出；`--force` 开关覆盖改绑。
