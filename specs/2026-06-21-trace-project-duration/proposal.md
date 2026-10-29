# 为追踪与项目增加「持续时间」

为「追踪」（Traces）和「项目」（Projects）两个列表各增加一列「持续时间」，其值为该行的最近出现时间减去首次出现时间：

- 追踪：`lastRequestAt - firstRequestAt`
- 项目：`lastSeenAt - firstSeenAt`

用 `date-fns` 配合中文语言包（`zh-CN`）实现持续时间的人类可读格式化。
