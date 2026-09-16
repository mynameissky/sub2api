# 分组倍率「×0.1」显示改动说明

> 用途：在**新版 sub2api 代码**中重新应用这组「分组倍率 ×0.1 显示」的改动。
> 本改动**只影响前端显示**，不改任何数据（`rate_multiplier` / `user_rate_multiplier` 原值不动）。

---

## 一、需求

「API 密钥」菜单（路由 `/keys`，[KeysView.vue](../frontend/src/views/user/KeysView.vue)）中，凡是显示分组倍率的地方，
把倍率 **×0.1 后再显示**：例如 `2x` → `0.2x`，`1.5x` → `0.15x`。

涉及两处 UI：
1. 分组列里的倍率徽章（`GroupBadge`）
2. 「选择分组 / 更换分组」下拉菜单里的倍率（`GroupOptionItem` + 选中项 `GroupBadge`）

---

## 二、实现方案

不要在每个调用处逐个手写 `×0.1`，而是在两个公共显示组件上新增一个
**`rateScale` prop（默认 `1`）**，只有「API 密钥」这几个调用处传 `0.1`。
其它页面（可用渠道、账号分组等）不传，保持原样。

缩放时用 `Math.round(value * scale * 1e8) / 1e8` 消除浮点尾数（如 `1.5 * 0.1 = 0.15000000000000002`）。

共涉及 **3 个文件**。

---

## 三、逐文件改动

### 文件 1：`frontend/src/components/common/GroupBadge.vue`

**1.1 Props 接口末尾新增 `rateScale`**

```ts
  /**
   * 订阅分组默认在右侧 label 展示"订阅"或剩余天数；
   * 开启后订阅分组也改为显示倍率（保留订阅主题色 label，配合可用渠道这类
   * 只关心费率、不关心有效期的场景）。
   */
  alwaysShowRate?: boolean
  /**
   * 显示倍率的缩放系数（仅影响展示，不改变数据）。
   * 例如传入 0.1 时，倍率 2 会显示为 0.2x。
   */
  rateScale?: number          // ← 新增
}
```

**1.2 `withDefaults` 新增默认值 `rateScale: 1`**

```ts
const props = withDefaults(defineProps<Props>(), {
  subscriptionType: 'standard',
  showRate: true,
  daysRemaining: null,
  userRateMultiplier: null,
  peakRateEnabled: false,
  alwaysShowRate: false,
  rateScale: 1                // ← 新增
})
```

**1.3 在 `hasCustomRate` computed 之后，新增缩放工具与两个展示用 computed**

```ts
// 对倍率应用缩放系数，消除浮点误差（如 1.5 * 0.1 的尾数）
function scaleRate(value: number, scale: number): number {
  if (scale === 1) return value
  return Math.round(value * scale * 1e8) / 1e8
}

// 展示用的默认倍率（已应用 rateScale）
const displayRateMultiplier = computed(() => {
  if (props.rateMultiplier === undefined) return undefined
  return scaleRate(props.rateMultiplier, props.rateScale)
})

// 展示用的专属倍率（已应用 rateScale）
const displayUserRateMultiplier = computed(() => {
  if (props.userRateMultiplier === null || props.userRateMultiplier === undefined) return null
  return scaleRate(props.userRateMultiplier, props.rateScale)
})
```

**1.4 `labelText` computed 里改用缩放后的值**

```ts
// 改前
const rateLabel = props.rateMultiplier !== undefined ? `${props.rateMultiplier}x` : ''
// 改后
const rateLabel = displayRateMultiplier.value !== undefined ? `${displayRateMultiplier.value}x` : ''
```

**1.5 模板中「专属倍率」两处显示改用缩放后的值**

```html
<!-- 改前 -->
<span class="line-through opacity-50 mr-0.5">{{ rateMultiplier }}x</span>
<span class="font-bold">{{ userRateMultiplier }}x</span>

<!-- 改后 -->
<span class="line-through opacity-50 mr-0.5">{{ displayRateMultiplier }}x</span>
<span class="font-bold">{{ displayUserRateMultiplier }}x</span>
```

---

### 文件 2：`frontend/src/components/common/GroupOptionItem.vue`

> 下拉菜单里倍率由这个组件自己渲染（**不是** `GroupBadge`），所以必须单独改。

**2.1 Props 接口末尾新增 `rateScale`**

```ts
  description?: string | null
  selected?: boolean
  showCheckmark?: boolean
  /** 显示倍率的缩放系数（仅影响展示，不改变数据）。例如 0.1 时倍率 2 显示为 0.2x。 */
  rateScale?: number          // ← 新增
}
```

**2.2 `withDefaults` 新增默认值 `rateScale: 1`**

```ts
const props = withDefaults(defineProps<Props>(), {
  subscriptionType: 'standard',
  selected: false,
  showCheckmark: true,
  userRateMultiplier: null,
  peakRateEnabled: false,
  rateScale: 1                // ← 新增
})
```

**2.3 在 `hasCustomRate` computed 之后，新增与文件 1 相同的缩放工具与两个 computed**

```ts
// 对倍率应用缩放系数，消除浮点误差（如 1.5 * 0.1 的尾数）
function scaleRate(value: number, scale: number): number {
  if (scale === 1) return value
  return Math.round(value * scale * 1e8) / 1e8
}

// 展示用的默认倍率（已应用 rateScale）
const displayRateMultiplier = computed(() => {
  if (props.rateMultiplier === undefined) return undefined
  return scaleRate(props.rateMultiplier, props.rateScale)
})

// 展示用的专属倍率（已应用 rateScale）
const displayUserRateMultiplier = computed(() => {
  if (props.userRateMultiplier === null || props.userRateMultiplier === undefined) return null
  return scaleRate(props.userRateMultiplier, props.rateScale)
})
```

**2.4 模板里的倍率 pill 改用缩放后的值**

```html
<!-- 改前 -->
<template v-if="hasCustomRate">
  <span class="mr-1 line-through opacity-50">{{ rateMultiplier }}x</span>
  <span class="font-bold">{{ userRateMultiplier }}x</span>
</template>
<template v-else>
  {{ rateMultiplier }}x {{ t('admin.groups.rateLabel') }}
</template>

<!-- 改后 -->
<template v-if="hasCustomRate">
  <span class="mr-1 line-through opacity-50">{{ displayRateMultiplier }}x</span>
  <span class="font-bold">{{ displayUserRateMultiplier }}x</span>
</template>
<template v-else>
  {{ displayRateMultiplier }}x {{ t('admin.groups.rateLabel') }}
</template>
```

---

### 文件 3：`frontend/src/views/user/KeysView.vue`

在下面 **4 处**组件上增加 `:rate-scale="0.1"`（位置以最新代码为准，按上下文匹配）：

| # | 位置 | 组件 | 说明 |
|---|------|------|------|
| 1 | 分组列 `#cell-group` 里（`@click="openGroupSelector(row)"` 之后那个） | `GroupBadge` | 列表「分组」列显示的徽章 |
| 2 | 创建/编辑弹窗「选择分组」`Select` 的 `#selected` 插槽 | `GroupBadge` | 下拉选中项的显示 |
| 3 | 创建/编辑弹窗「选择分组」`Select` 的 `#option` 插槽 | `GroupOptionItem` | 下拉选项 |
| 4 | 点击分组列后弹出的「更换分组」自定义下拉列表 | `GroupOptionItem` | 更换分组下拉选项 |

示例（每处都是在 `:user-rate-multiplier="..."` 与 `:peak-rate-enabled="..."` 之间插入一行）：

```html
<GroupBadge
  v-if="row.group"
  :name="row.group.name"
  :platform="row.group.platform"
  :subscription-type="row.group.subscription_type"
  :rate-multiplier="row.group.rate_multiplier"
  :user-rate-multiplier="userGroupRates[row.group.id]"
  :rate-scale="0.1"                       <!-- ← 新增 -->
  :peak-rate-enabled="row.group.peak_rate_enabled"
  ...
/>
```

```html
<GroupOptionItem
  :name="(option as unknown as GroupOption).label"
  :platform="(option as unknown as GroupOption).platform"
  :subscription-type="(option as unknown as GroupOption).subscriptionType"
  :rate-multiplier="(option as unknown as GroupOption).rate"
  :user-rate-multiplier="(option as unknown as GroupOption).userRate"
  :rate-scale="0.1"                       <!-- ← 新增 -->
  :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
  ...
/>
```

> 提示：先 `grep -n "GroupBadge\|GroupOptionItem" KeysView.vue` 找到全部调用点，
> 凡是出现在「分组」相关 UI 上的都加 `:rate-scale="0.1"`。
> 若还用了 `GroupSelector.vue`，同样给它内部传下去（视新版实现而定）。

---

## 四、在新版代码中重新应用 — 步骤清单

1. `grep -rn "GroupBadge\|GroupOptionItem" frontend/src` 找出这两个组件的所有使用点。
2. 按「三、文件 1」「文件 2」给 **`GroupBadge.vue` / `GroupOptionItem.vue`** 加 `rateScale` prop + 缩放逻辑。
3. 找到「API 密钥」页面（`KeysView.vue`，路由 `/keys`），在**分组列**和**两个下拉菜单**的所有相关组件上补 `:rate-scale="0.1"`。
4. 前端类型检查：`cd frontend && pnpm run typecheck`。
5. 重新构建 + 部署（见下）。

---

## 五、注意事项 / 坑

- **这是纯显示改动**：数据层 `rate_multiplier`、`user_rate_multiplier` 保持不变。
  若本意是改真实计费倍率，则不是改前端，而是改后端计费逻辑 —— 务必确认。
- **下拉菜单容易被漏掉**：下拉的倍率由 `GroupOptionItem.vue` 自己渲染，
  改 `GroupBadge.vue` 不会影响它，两处都要改。
- **其它页面不要误伤**：这两个组件在「可用渠道」「账号分组」「管理员-用户密钥」等处也被复用；
  本需求只针对「API 密钥」菜单，因此用 prop（默认 1）而非全局写死，别处保持原值。
- **`rateScale` 默认必须是 `1`**，否则会影响所有未显式传参的调用处。
- **浮点误差**：务必用 `Math.round(value * scale * 1e8) / 1e8`，
  否则 `1.5 × 0.1` 会显示成 `0.15000000000000002`。
- 前端改动需**重新编译进二进制**才生效（前端 dist 被 `-tags embed` 打进 Go 二进制）。
  本仓库可用根目录 `./build.sh` 一键「Docker 构建 + 拷贝二进制到当前目录」，
  再替换 `/opt/sub2api/sub2api` 并 `systemctl restart sub2api`。

---

## 六、附：涉及文件一览

| 文件 | 改动 |
|------|------|
| `frontend/src/components/common/GroupBadge.vue` | 新增 `rateScale` prop + 缩放显示（3 处模板/计算） |
| `frontend/src/components/common/GroupOptionItem.vue` | 新增 `rateScale` prop + 缩放显示（倍率 pill） |
| `frontend/src/views/user/KeysView.vue` | 分组列 + 下拉菜单共 4 处传 `:rate-scale="0.1"` |
