# 普通用户隐藏「标准」费用 — 改动记录

> 用途：在新版本中重新应用这份改动。
> 目标版本基线：sub2api 0.1.162（前端为 Vue 3 + vue-i18n，组件在 `frontend/src`）。

---

## 一、需求

普通用户界面中隐藏「标准（standard）」费用/列，只保留「实际（actual）」。**管理端保持不变。**

具体位置：

| 页面 | 位置 | 处理 |
| --- | --- | --- |
| 仪表盘 | 今日消费 | 去掉「/ 标准」，只留「实际」 |
| 仪表盘 | 累计消费 | 去掉「/ 标准」，只留「实际」 |
| 仪表盘 | 模型分布 | 删除「标准」列 |
| 仪表盘 | 最近使用 | 去掉「/ 标准」，只留「实际」 |
| 使用记录 | 总消费 | 去掉「标准」 |
| 使用记录 | 模型分布 | 删除「标准」列 |
| 使用记录 | 端点分布 | 删除「标准」列 |
| 使用记录 | 分组使用分布 | 删除「标准」列 |

---

## 二、实现思路

两类文件，两种改法：

1. **用户专属组件**（只在普通用户侧使用）→ 直接删除「标准」的 DOM 元素。
   - `UserDashboardStats.vue`、`UserDashboardCharts.vue`、`UserDashboardRecentUsage.vue`

2. **管理端 / 用户端共用组件** → 新增 `showStandardCost` 属性（默认 `true`），
   用户在 `frontend/src/views/user/UsageView.vue` 里传 `:show-standard-cost="false"`。
   这样管理端行为完全不变。
   - `UsageStatsCards.vue`、`ModelDistributionChart.vue`、`GroupDistributionChart.vue`、`EndpointDistributionChart.vue`

> ⚠️ 不要直接删除共用组件里的「标准」列，否则管理端也会一起消失。

---

## 三、涉及文件清单（共 8 个）

```
frontend/src/components/user/dashboard/UserDashboardStats.vue        (1)
frontend/src/components/user/dashboard/UserDashboardCharts.vue       (2)
frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue  (3)
frontend/src/components/admin/usage/UsageStatsCards.vue              (4)
frontend/src/components/charts/ModelDistributionChart.vue            (5)
frontend/src/components/charts/GroupDistributionChart.vue            (6)
frontend/src/components/charts/EndpointDistributionChart.vue         (7)
frontend/src/views/user/UsageView.vue                                (8)
```

涉及到的 i18n key（**不需要改语言包**，只是不再渲染）：

- `dashboard.standard` / `dashboard.actual`
- `usage.standardCost`
- `admin.dashboard.standard`

---

## 四、详细改动

### 1) `frontend/src/components/user/dashboard/UserDashboardStats.vue`

今日消费 + 累计消费，删掉「标准」的 `<span>`。

**改动前：**

```html
          <p class="text-xl font-bold text-gray-900 dark:text-white">
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actual')">${{ formatCost(stats?.today_actual_cost || 0) }}</span>
            <span class="text-sm font-normal text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(stats?.today_cost || 0) }}</span>
          </p>
          <p class="text-xs">
            <span class="text-gray-500 dark:text-gray-400">{{ t('common.total') }}: </span>
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actual')">${{ formatCost(stats?.total_actual_cost || 0) }}</span>
            <span class="text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(stats?.total_cost || 0) }}</span>
          </p>
```

**改动后：**

```html
          <p class="text-xl font-bold text-gray-900 dark:text-white">
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actual')">${{ formatCost(stats?.today_actual_cost || 0) }}</span>
          </p>
          <p class="text-xs">
            <span class="text-gray-500 dark:text-gray-400">{{ t('common.total') }}: </span>
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actual')">${{ formatCost(stats?.total_actual_cost || 0) }}</span>
          </p>
```

---

### 2) `frontend/src/components/user/dashboard/UserDashboardCharts.vue`

模型分布表格，删掉表头 `<th>标准</th>` 和对应 `<td>`（`formatCost(model.cost)`）。

**改动前（表头 + 单元格）：**

```html
                  <th class="pb-2 text-right">{{ t('dashboard.actual') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.standard') }}</th>
```

```html
                  <td class="py-1.5 text-right text-green-600 dark:text-green-400">${{ formatCost(model.actual_cost) }}</td>
                  <td class="py-1.5 text-right text-gray-400 dark:text-gray-500">${{ formatCost(model.cost) }}</td>
```

**改动后：**

```html
                  <th class="pb-2 text-right">{{ t('dashboard.actual') }}</th>
```

```html
                  <td class="py-1.5 text-right text-green-600 dark:text-green-400">${{ formatCost(model.actual_cost) }}</td>
```

---

### 3) `frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue`

最近使用卡片，删掉「标准」。

**改动前：**

```html
            <p class="text-sm font-semibold">
              <span class="text-green-600 dark:text-green-400" :title="t('dashboard.actual')">${{ formatCost(log.actual_cost) }}</span>
              <span class="font-normal text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(log.total_cost) }}</span>
            </p>
```

**改动后：**

```html
            <p class="text-sm font-semibold">
              <span class="text-green-600 dark:text-green-400" :title="t('dashboard.actual')">${{ formatCost(log.actual_cost) }}</span>
            </p>
```

---

### 4) `frontend/src/components/admin/usage/UsageStatsCards.vue`（共用，新增属性）

**模板改动前：**

```html
          <span>
            {{ t('usage.standardCost') }}
            <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
          </span>
```

**模板改动后（加 `v-if`）：**

```html
          <span v-if="showStandardCost">
            {{ t('usage.standardCost') }}
            <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
          </span>
```

**脚本改动（props 增加一项 + 默认值 + computed）：**

```ts
const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  showAccountCost?: boolean
  strikeStandardCost?: boolean
  showStandardCost?: boolean        // ← 新增
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
  showStandardCost: true,           // ← 新增，默认显示，保证管理端不变
})
```

```ts
const showAccountCost = computed(() => props.showAccountCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)
const showStandardCost = computed(() => props.showStandardCost)   // ← 新增
```

---

### 5) `frontend/src/components/charts/ModelDistributionChart.vue`（共用，新增属性）

**模板：** 表头加 `v-if`

```html
<!-- 改动前 -->
<th class="pb-2 text-right">{{ t('admin.dashboard.standard') }}</th>
<!-- 改动后 -->
<th v-if="showStandardCost" class="pb-2 text-right">{{ t('admin.dashboard.standard') }}</th>
```

单元格加 `v-if`

```html
<!-- 改动前 -->
<td class="py-1.5 text-right text-gray-400 dark:text-gray-500">
  ${{ formatCost(model.cost) }}
</td>
<!-- 改动后 -->
<td v-if="showStandardCost" class="py-1.5 text-right text-gray-400 dark:text-gray-500">
  ${{ formatCost(model.cost) }}
</td>
```

**脚本：** props 增加 `showStandardCost?: boolean`（默认 `true`），并新增 computed：

```ts
const showAccountCost = computed(() => props.showAccountCost)
const showStandardCost = computed(() => props.showStandardCost)
// ⚠️ 列数：模型 + 请求 + Token + 实际 = 4，再加可选的「成本」「标准」
const distributionColspan = computed(
  () => 4 + (showAccountCost.value ? 1 : 0) + (showStandardCost.value ? 1 : 0)
)
```

> 原实现是 `distributionColspan = showAccountCost ? 6 : 5`（写死 5/6），
> 因为「标准」列原来恒显示。改成动态计算后，隐藏列时展开行的 colspan 才不会错位。

---

### 6) `frontend/src/components/charts/GroupDistributionChart.vue`（共用，新增属性）

与 5) 完全同构，把 `model` 换成 `group`：

- 表头 `<th ...>{{ t('admin.dashboard.standard') }}</th>` 加 `v-if="showStandardCost"`
- 单元格 `${{ formatCost(group.cost) }}` 加 `v-if="showStandardCost"`
- props 增加 `showStandardCost?: boolean`，默认 `true`
- 新增 `const showStandardCost = computed(() => props.showStandardCost)`
- `distributionColspan` 改为 `4 + (showAccountCost ? 1 : 0) + (showStandardCost ? 1 : 0)`

---

### 7) `frontend/src/components/charts/EndpointDistributionChart.vue`（共用，新增属性）

该组件**没有** `showAccountCost`，列固定为：端点 + 请求 + Token + 实际 + 标准 = 5。

- 表头 `<th ...>{{ t('admin.dashboard.standard') }}</th>` 加 `v-if="showStandardCost"`
- 单元格 `${{ formatCost(item.cost) }}` 加 `v-if="showStandardCost"`
- 展开行 colspan 由写死的 `colspan="5"` 改为动态：

```html
<!-- 改动前 -->
<td colspan="5" class="p-0">
<!-- 改动后 -->
<td :colspan="distributionColspan" class="p-0">
```

- props 增加 `showStandardCost?: boolean`，默认 `true`
- 新增 computed：

```ts
const showStandardCost = computed(() => props.showStandardCost)
const distributionColspan = computed(() => showStandardCost.value ? 5 : 4)
```

---

### 8) `frontend/src/views/user/UsageView.vue`（用户端传参）

**第 4 行：**

```html
<!-- 改动前 -->
<UsageStatsCards :stats="usageStats" :show-account-cost="false" :strike-standard-cost="true" />
<!-- 改动后 -->
<UsageStatsCards :stats="usageStats" :show-account-cost="false" :show-standard-cost="false" />
```

> 隐藏「标准」后，原先的 `:strike-standard-cost="true"` 已无意义，一并去掉。

**三个分布图各加一行属性：**

```html
<ModelDistributionChart
  ...
  :show-account-cost="false"
  :show-standard-cost="false"   <!-- ← 新增 -->
  ...
/>

<GroupDistributionChart
  ...
  :show-account-cost="false"
  :show-standard-cost="false"   <!-- ← 新增 -->
  ...
/>

<EndpointDistributionChart
  ...
  :enable-breakdown="false"
  :show-standard-cost="false"   <!-- ← 新增（该组件无 show-account-cost） -->
  ...
/>
```

---

## 五、易错点 checklist

- [ ] 共用组件（4/5/6/7）**只加属性**，不要删 DOM —— 否则管理端也会丢「标准」。
- [ ] 三个分布图的 `distributionColspan` 必须跟着改，否则展开明细行（breakdown）列数错位。
- [ ] `EndpointDistributionChart` 没有 `showAccountCost`，colspan 规则是 `5 → 4`。
- [ ] `UsageStatsCards` 的新属性默认值必须是 `true`，确保管理端 `views/admin/UsageView.vue`（无传参）行为不变。
- [ ] 用户端传参用 kebab-case：`:show-standard-cost="false"`。

---

## 六、验证

```bash
cd frontend
npx vue-tsc --noEmit                      # 类型检查
npx vitest run \
  src/components/admin/usage/__tests__/UsageStatsCards.spec.ts \
  src/views/user/__tests__/UsageView.spec.ts \
  src/components/charts/__tests__/            # 相关单测
```

人工核对：

1. 普通用户登录 → 仪表盘：今日消费、累计消费、模型分布、最近使用 均无「标准」。
2. 普通用户 → 使用记录：总消费无「标准」；模型分布 / 端点分布 / 分组使用分布 表格无「标准」列。
3. 管理员登录 → 使用记录：**仍能看到**「标准」列（回归验证，确认没误伤管理端）。

---

## 七、编译（可选）

```bash
# 仅编译 backend-builder 阶段，产出 /app/sub2api
docker build --target backend-builder -t sub2api:build \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  --build-arg GOSUMDB=sum.golang.google.cn \
  -f Dockerfile .

# 从镜像中取出二进制
docker create --name sub2api-extract sub2api:build
docker cp sub2api-extract:/app/sub2api ./sub2api
docker rm sub2api-extract
```

> 说明：Dockerfile 前端安装用的是 `pnpm install --no-frozen-lockfile`（不锁版本），
> 两次构建产物大小可能不同，属预期行为。若需可复现构建，可改为 `--frozen-lockfile`。
