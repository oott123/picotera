# Plan

纯前端改动。全部位于 `dashboard/`。

## 1. 导出算法工具 `dashboard/src/utils/pricingExpression.ts`

新建纯函数模块(无 Vue 依赖,便于后续单测):

- 签名:
  ```ts
  export function buildPricingExpression(
    pricing: Pricing,
    targetCurrency: string,
    byCode: Map<string, ExchangeRateView>,
  ): { expression: string; warnings: string[]; error: string | null }
  ```
- 步骤:
  1. 校验 `pricing.currency` 与 `targetCurrency` 在 `byCode` 中存在且 `unitsPerUsd > 0`;否则返回 `{ expression: '', warnings: [], error: <说明> }`。
  2. 计算 `factor = unitsPerUsd(target) / unitsPerUsd(source)`(相等币种直接 `1`)。
  3. 系数格式化函数 `fmt(v)`:`v * factor` → 最多 6 位小数、去尾零、整数不带小数点。
  4. 逐 tier 生成成本表达式(见 design「单个 tier 的成本表达式」),收集 `cacheRead !== implicitCacheRead` 的警告。
  5. 按 tier 数拼接:单档 `tier("base", …)`;多档嵌套三元 + `len < {nextMin}`,命名 `tier_{i}`,多行缩进。
- 类型从 `@/api` 导入(`Pricing`、`PricingTier`、`ExchangeRateView`)。

## 2. 面板组件 `dashboard/src/components/PricingExportPanel.vue`

- `defineProps<{ model: ModelView }>()`,`defineEmits<{ close: [] }>()`。
- `const { rates, byCode } = useExchangeRates()`。
- `targetCurrency = ref(props.model.pricing?.currency ?? 'USD')`。
- `currencyOptions = computed(...)`(同 `PricingEditor.vue` 的映射:`{ value: code, label: \`${code} ${symbol}\` }`)。
- `result = computed(() => buildPricingExpression(props.model.pricing!, targetCurrency.value, byCode.value))`。
- 复制:`copied = ref(false)`;`async function copy()` → `navigator.clipboard.writeText(result.value.expression)`,成功后 `copied=true` 并用 `setTimeout` 复位,失败静默忽略(参照 `ApiKeysView.vue`)。
- 模板:`SidePanel`(`title=model.name`、`kicker="导出价格"`),币种 `Select`、警告列表(`StateText`)、只读 `<pre>` 表达式块;`#error` 显示 `result.error`;`#footer` 放「复制表达式」(`error` 存在或表达式为空时禁用,`copied` 时显示"已复制")与「关闭」。

## 3. 接入 `dashboard/src/views/ModelsView.vue`

- import `PricingExportPanel`。
- 新增 `function openPricingExport(m: ModelView)` → `panel.open(PricingExportPanel, { model: m }, { key: \`model-pricing-export:${m.name}\` })`。
- 在 `Td actions` 内、「编辑」`IconButton` 之前,新增:
  ```vue
  <IconButton
    v-if="m.pricing?.tiers?.length"
    :active="panel.isActive(`model-pricing-export:${m.name}`)"
    title="导出价格表达式"
    aria-label="导出价格表达式"
    @click="openPricingExport(m)"
  >
    <Icon name="braces" :size="13" />
  </IconButton>
  ```

## 4. 验证

- `pnpm --dir dashboard type-check` 通过。
- `pnpm --dir dashboard lint` 通过。
- 手动核对样例:
  - 单档、仅 input/output → `tier("base", p * X + c * Y)`。
  - 含缓存且 `cacheRead === implicitCacheRead`、`cacheWrite === cacheWrite1h` → 只出现单个 `cr` 项、单个 `cc` 项(无 `cc1h`)。
  - `cacheWrite !== cacheWrite1h` → 同时出现 `cc` 与 `cc1h`。
  - `cacheRead !== implicitCacheRead` → `cr` 取较大值 + 面板出现警告。
  - 多档 → 嵌套三元、`len < {nextMin}`、`tier_0/tier_1/...`。
  - 切换目标币种 → 系数按汇率换算,`Pricing.Currency` 缺失汇率时报错并禁用复制。

## 涉及文件

- 新增:`dashboard/src/utils/pricingExpression.ts`
- 新增:`dashboard/src/components/PricingExportPanel.vue`
- 修改:`dashboard/src/views/ModelsView.vue`

无后端 / OpenAPI / sqlc / 迁移改动。
