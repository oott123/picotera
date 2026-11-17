# 需求：KV 内存引擎改为数据库引擎

把当前的内存 KV 引擎改为落到数据库上，redis 引擎不变。

## 补充说明

- 新驱动在 `PICOTERA_KV_DRIVER` 中的取值为 `database`，并成为默认值。
- 内存驱动保留，仍可通过 `PICOTERA_KV_DRIVER=memory` 选用。
- 数据库驱动不检查 key / value 是否为合法文本，含 NUL 或非法 UTF-8 时交给数据库直接报错。
- 数据库驱动的 `setex` 秒数 ≤ 0 时当作永不过期写入，与内存驱动一致。
