# 扫码人工充值（启用扫码人工充值 + 充值/订阅 菜单影响）需求文档

> 版本：v1.0　·　用途：在其它版本中重新实现本功能时的依据
> 目标版本基线：sub2api 0.1.162（后端 Go + Gin + ent，前端 Vue 3 + vue-i18n）
> 相关文档：[zf.md](./zf.md)（支付宝轮询充值）在配置范式上**沿用本功能**，实现时可互相参照

---

## 1. 功能概述

在原有支付体系（支付宝 / 微信 / Stripe / Airwallex 等）之上，新增一种**线下扫码 + 客服人工到账**的充值方式：

1. 管理员在「项目设置 → 支付设置」中勾选**启用扫码人工充值**，并配置若干**固定档位**（实付金额 → 到账额度）+ 每个档位对应的**收款二维码**，以及一个**客服二维码**和客服提示文案。
2. 开启后，用户端「充值/订阅」页面进入**独占模式**：只显示「扫码充值」一个 Tab，**自动充值 Tab 与订阅 Tab 全部隐藏**。
3. 用户选择档位 → 页面展示该档位的收款二维码 → 用户扫码付款。
4. 用户付款后联系客服（客服二维码 + 提示文案）；客服在后台或支付页隐藏入口为其实施**手动充值**（写入余额 + 落一笔已支付订单）。
5. 用户在「我的订单」中可以看到这笔由客服写入的充值记录。

关键特性：

- **没有用户侧的订单创建流程**。用户不点击"下单"，系统不为扫码充值创建 PENDING 订单；订单由**管理员手动充值**时反向生成，状态直接为已支付。
- 档位二维码与客服二维码以 **base64 data URL** 存在 settings 表中，通过一个**免鉴权**的图片端点下发（原因见 §5）。
- 开启该开关会**独占**充值页，这是一条强产品约束，不只是"多一个 Tab"。

命名约定：

- 设置键前缀 `MANUAL_RECHARGE_*` / `MANUAL_CS_*`
- 请求字段前缀 `payment_manual_*`
- 用户端 Tab key `manual`；i18n 键前缀 `payment.manual*`
- 管理员手动充值写入的订单：`payment_type = alipay`、`order_type = balance`、`status = SUCCESS`、`src_host = admin-manual`

---

## 2. 配置项（管理端 Settings → 支付设置）

| 界面名称 | 设置键（DB） | 请求字段 | 类型 | 默认 |
|---|---|---|---|---|
| 启用扫码人工充值 | `MANUAL_RECHARGE_ENABLED` | `payment_manual_recharge_enabled` | bool | false |
| 档位列表 | `MANUAL_RECHARGE_TIERS` | `payment_manual_recharge_tiers` | JSON 数组 | `[]` |
| 客服二维码 | `MANUAL_CS_QR_IMAGE` | `payment_manual_cs_qr_image` | data URL | `""` |
| 客服提示文案 | `MANUAL_CS_HELP_TEXT` | `payment_manual_cs_help_text` | string | `""` |

档位结构 `ManualRechargeTier`（`internal/service/payment_config_service.go`）：

```go
// ManualRechargeTier is a fixed-price offline recharge option: user pays Price
// (in CNY) by scanning QRImage, and after CS confirmation gets Credit balance.
type ManualRechargeTier struct {
    Price   float64 `json:"price"`    // 实付金额（¥）
    Credit  float64 `json:"credit"`   // 到账额度
    QRImage string  `json:"qr_image"` // 该档位收款二维码（data URL 或自有图片端点 URL）
}
```

> **注意**：`ManualCSQRImage` 属于 `PaymentConfig` 并被下发给用户（用户端必须能显示客服二维码），
> 与 zf.md 中支付宝轮询的 ApiKey/AgentID 不同——那两个是敏感凭证，必须独立存储、不下发。
> 本功能**没有需要保密的凭证**。

### 2.1 解析与校验

`parseManualRechargeTiers(raw)`：

- 空串 / JSON 解析失败 → 返回 `[]ManualRechargeTier{}`（不报错，降级为空档位）。
- 逐项丢弃非法档位（`Price <= 0 || Credit <= 0`）。

`UpdatePaymentConfig` 保存档位时：

- 任一档位 `Price <= 0 || Credit <= 0` → 返回 `400 INVALID_MANUAL_RECHARGE_TIER`
  （提示语 `manual recharge tier price and credit must be greater than 0`）。
- 通过后再逐项处理二维码回传（见 §5.3），最后 `json.Marshal` 写库。

---

## 3. 数据模型

### 3.1 配置存储

全部存在既有 settings 表，**不新建表、不改表**。

- `MANUAL_RECHARGE_TIERS` 为一个 JSON 字符串，例如：
  ```json
  [{"price":10,"credit":10,"qr_image":"data:image/png;base64,iVBORw0..."}]
  ```
- 图片以 base64 data URL 原样存库。

### 3.2 订单（由管理员手动充值反向生成）

用户侧不建单；订单在 `admin_user.go` 的 `createManualRechargeOrder` 中创建，
让用户在「我的订单」可见：

| 字段 | 取值 |
|---|---|
| `out_trade_no` | `"MR" + strconv(unix秒) + strconv(userID)`，如 `MR1758000000123` |
| `payment_type` | `alipay`（默认，无真实第三方） |
| `order_type` | `balance` |
| `amount` | 到账额度 `creditAmount` |
| `pay_amount` | 实付金额 `payAmount` |
| `fee_rate` | `(creditAmount - payAmount) / payAmount`（差额即"手续费/优惠"，可正可负） |
| `status` | `SUCCESS`（已支付，直接落库，不经过履约管线） |
| `recharge_code` | `""` |
| `payment_trade_no` | `""` |
| `client_ip` | `127.0.0.1`（占位） |
| `src_host` | `admin-manual`（标识管理员手动充值） |
| `paid_at` / `created_at` / `updated_at` | `now` |
| `expires_at` | `now + 24h`（已支付，字段无意义） |
| `user_notes` | 取自 `user.Notes` |
| `refund_reason` | 复用该字段存管理员的 `notes` 备注 |

> ⚠️ **前缀冲突提示**：`payment_service.go` 中 `orderIDPrefix = "MR"`（格式 `MR + YYYYMMDD + 8位随机`），
> 而本处手动充值用 `MR + unix + userID`。两者都以 `MR` 开头但格式不同。
> 重新实现时如果解析侧对订单号有格式假设，需一并确认；
> 若无格式假设（仅当不透明字符串用），保持现状即可。

---

## 4. 接口设计

本功能**没有新增任何专用业务接口**，完全复用既有接口 + 一个新增的公共图片端点。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/settings/image?key=...&index=...` | **新增**，免鉴权图片端点，见 §5 |
| GET | `/api/v1/payment/config` | 用户侧支付配置（含 `manual_recharge_*` 字段） |
| GET | `/api/v1/payment/checkout-info` | 充值页聚合数据（含 `manual_recharge_*` 字段） |
| GET | `/api/v1/admin/settings` | 管理端设置回显 |
| PUT | `/api/v1/admin/settings` | 管理端设置保存 |
| POST | `/api/v1/admin/users/:id/balance` | **复用**，管理员手动充值（`operation=add` + `pay_amount`） |

### 4.1 `/payment/checkout-info` 新增响应字段

```go
ManualRechargeEnabled bool                        `json:"manual_recharge_enabled"`
ManualRechargeTiers   []service.ManualRechargeTier `json:"manual_recharge_tiers"`
ManualCSQRImage       string                      `json:"manual_cs_qr_image"`
ManualCSHelpText      string                      `json:"manual_cs_help_text"`
```

下发前会经 `stripPaymentConfigImages(cfg)` 处理，把内联 base64 替换成图片端点 URL（见 §5.2）。

### 4.2 管理员手动充值

复用 `UpdateUserBalance`（`admin_user.go`）。当满足
**`operation == "add"` && `balance > 0` && `payAmount != nil && *payAmount > 0`** 时，
额外调用 `createManualRechargeOrder` 落一笔 SUCCESS 订单并写余额调整记录
（`RedeemCode`，`Type = AdjustmentTypeAdminBalance`）。

---

## 5. 图片存储与下发（本功能最容易漏掉的部分）

核心文件：`backend/internal/service/settings_image.go`

### 5.1 为什么需要免鉴权端点

管理员上传的二维码是 base64 data URL，直接塞进 `/payment/config` 响应会让 JSON 极大。
因此响应中把它替换为图片端点 URL。**但浏览器 `<img>` 标签无法携带 `Authorization` 头**，
所以该端点必须是**公共（免鉴权）**的，这是硬约束，不能"顺手"挪进认证分组。

```go
// SettingsImageEndpointPath is the public (unauthenticated) endpoint that serves
// images stored as data URLs in settings. Browser <img> tags cannot attach the
// Authorization header, so this endpoint must remain public.
const SettingsImageEndpointPath = "/api/v1/settings/image"
```

### 5.2 图片键与 URL 生成

```go
const (
    SettingsImageKeySiteLogo     = "site_logo"
    SettingsImageKeyHelpImage    = "payment_help_image"
    SettingsImageKeyManualCSQR   = "payment_manual_cs_qr"
    SettingsImageKeyManualTierQR = "payment_manual_tier_qr"
)

func SettingsImageURL(key string) string { ... }                     // ?key=<key>
func ManualTierQRImageURL(index int) string { ... }                  // ?key=payment_manual_tier_qr&index=N
func SettingsImageTierIndex(v string) (int, bool) { ... }            // 反解 index（见 §5.3）
func IsOwnSettingsImageURL(v string) bool { ... }                    // 前缀判定
func SettingsImageOrDataURL(value, key string) string { ... }        // data:image/ 前缀 → 端点 URL
func DecodeDataURL(s string) (mimeType string, data []byte, ok bool) // RFC 2397 解码
```

`stripPaymentConfigImages`（`payment_handler.go`）在下发前做替换：

```go
cfg.HelpImageURL      = service.SettingsImageOrDataURL(cfg.HelpImageURL, service.SettingsImageKeyHelpImage)
cfg.ManualCSQRImage   = service.SettingsImageOrDataURL(cfg.ManualCSQRImage, service.SettingsImageKeyManualCSQR)
for i := range cfg.ManualRechargeTiers {
    if strings.HasPrefix(cfg.ManualRechargeTiers[i].QRImage, "data:image/") {
        cfg.ManualRechargeTiers[i].QRImage = service.ManualTierQRImageURL(i)
    }
}
```

### 5.3 档位二维码的"原样回传"保护（关键坑位）

管理端设置页加载后会把**未修改**的图片原样回传（即回传我们自己的端点 URL，而非 base64）。
如果不特殊处理，保存后 base64 就被这个 URL 字符串覆盖，图片丢失。

因此保存时按 **index 映射回上一版档位的原图**：

```go
if req.ManualRechargeTiers != nil {
    var prevTiers []ManualRechargeTier
    if raw, err := s.settingRepo.GetValue(ctx, SettingManualRechargeTiers); err == nil {
        prevTiers = parseManualRechargeTiers(raw)
    }
    for _, t := range *req.ManualRechargeTiers {
        if IsOwnSettingsImageURL(t.QRImage) {
            if idx, ok := SettingsImageTierIndex(t.QRImage); ok && idx >= 0 && idx < len(prevTiers) {
                t.QRImage = prevTiers[idx].QRImage   // 按 index 映射，容忍管理员重排/删减后仍能对上
            } else {
                t.QRImage = ""
            }
        }
        ...
    }
}
```

同理，客服二维码走单键版本：

```go
if req.ManualCSQRImage == nil || !IsOwnSettingsImageURL(*req.ManualCSQRImage) {
    m[SettingManualCSQRImage] = derefStr(req.ManualCSQRImage)
}
```

即：**入参是自有图片端点 URL → 跳过写入**（保留库中已存 base64）；
`nil` / 空串 → 照常写入（实现清空语义）。

### 5.4 端点实现

`PaymentHandler.GetImage`（`payment_handler.go`），注册于 `internal/server/router.go`：

```go
v1.GET("/settings/image", h.Payment.GetImage)   // 无认证中间件
```

行为：

- `key = payment_manual_cs_qr` → 取 `cfg.ManualCSQRImage`
- `key = payment_manual_tier_qr` → 需带 `index`，越界或非法则视为未命中
- `key = site_logo` / `payment_help_image` → 分别取站点 logo / 帮助图片
- 取不到或 `DecodeDataURL` 失败 → `404`
- 命中 → `Cache-Control: public, max-age=300` + 原始 MIME 类型返回

---

## 6. 后端实现要点

核心文件：`backend/internal/service/payment_config_service.go`

### 6.1 常量与结构体

```go
// Manual (offline) recharge: fixed price tiers + customer-service QR flow.
SettingManualRechargeEnabled = "MANUAL_RECHARGE_ENABLED"
SettingManualRechargeTiers   = "MANUAL_RECHARGE_TIERS" // JSON array of ManualRechargeTier
SettingManualCSQRImage       = "MANUAL_CS_QR_IMAGE"    // customer-service QR image (data URL)
SettingManualCSHelpText      = "MANUAL_CS_HELP_TEXT"
```

- `PaymentConfig` 增加 4 个字段（下发给用户）：`manual_recharge_enabled` / `manual_recharge_tiers` / `manual_cs_qr_image` / `manual_cs_help_text`
- `UpdatePaymentConfigRequest` 增加 4 个指针字段（patch 语义，nil = 不改）

### 6.2 改动清单（后端）

1. `GetPaymentConfig` 的 `keys` 列表加入 4 个设置键，否则读不到。
2. `parsePaymentConfig` 中赋值 4 个字段。
3. `UpdatePaymentConfig` 的 `m` map 中加入 `SettingManualRechargeEnabled` / `SettingManualCSHelpText`；两个图片/档位字段按 §5.3 单独处理。
4. `GetCheckoutInfo` 的 `checkoutInfoResponse` 增加 4 个字段。

### 6.3 管理端设置链路

设置要在**四处**同时补齐，漏一处就会出现"保存了但读不到"或"读到了但保存不上"：

| 位置 | 改动 |
|---|---|
| `internal/handler/dto/settings.go` | `PaymentManualRechargeEnabled/Tiers/CSQRImage/CSHelpText` 响应字段 |
| `internal/handler/admin/setting_handler.go` | 从 `paymentCfg` 回显（管理端可见 base64 原图，供编辑） |
| `internal/handler/admin/setting_handler_update.go` | 请求体 4 字段 + 组装进 `UpdatePaymentConfigRequest` + 加入 `isPaymentConfigTouched` 判定 |
| `internal/service/payment_config_service.go` | 见 §6.1 / §6.2 |

> `isPaymentConfigTouched` 的判定必须包含这 4 个字段，否则只改这一项时不会触发支付配置更新。

---

## 7. 前端实现要点

### 7.1 用户端 `frontend/src/views/user/PaymentView.vue` ⭐ 本功能核心

#### 7.1.1 充值/订阅 菜单/Tab 的影响（**独占模式**）

这是本功能最容易被误解为"只是加一个 Tab"的地方，实际行为是**独占**：

```ts
const tabs = computed(() => {
  const result: { key: 'recharge' | 'subscription' | 'manual' | 'alipayPoll'; label: string }[] = []
  // 启用扫码人工充值后，充值页只保留「扫码充值」，隐藏自动充值与订阅 Tab。
  if (manualRechargeEnabled.value) {
    result.push({ key: 'manual', label: t('payment.tabManual') })
    return result          // ← 直接返回，recharge / subscription / alipayPoll 全部不加入
  }
  if (alipayPollEnabled.value) result.push({ key: 'alipayPoll', label: t('payment.tabAlipayPoll') })
  if (!checkout.value.balance_disabled) result.push({ key: 'recharge', label: t('payment.tabTopUp') })
  result.push({ key: 'subscription', label: t('payment.tabSubscribe') })
  return result
})
```

连锁影响：

1. **Tab 切换条整体消失**：模板中切换条的显示条件是 `tabs.length > 1`，独占模式下长度为 1，切换条不渲染。
2. **「充值」Tab 消失**：用户无法再走在线支付充值（支付宝/微信/Stripe…），只能扫码 + 找客服。
3. **「订阅」Tab 消失**：用户**无法再自助购买/续订套餐**。
4. **`?tab=subscription` 深度链接失效**：续订跳转（`/purchase?tab=subscription&group=123`）被显式忽略：

   ```ts
   if (!manualRechargeEnabled.value && route.query.tab === 'subscription') { ... }
   ```

   即续订入口点进来也会落到扫码充值页。**这是有意的**，重新实现时不要"顺手修好"。

5. **挂载时强制切换**：

   ```ts
   if (manualRechargeEnabled.value) {
     // 独占模式：只显示扫码充值，忽略其它 Tab 跳转逻辑。
     activeTab.value = 'manual'
   } else if (alipayPollEnabled.value) { ... }
   ```

6. **侧边栏菜单本身不变**：`充值/订阅`（`/purchase`，`nav.buySubscription`）与 `我的订单`（`/orders`）两个菜单项
   仍由支付总开关 `FeatureFlags.payment`（`payment_enabled`）控制，**不因本开关而隐藏**。
   变化的是点进去之后页面里只剩「扫码充值」。
7. **后端不强制**：`manual_recharge_enabled` **仅在服务端用于下发配置**，
   在线下单接口（`POST /payment/orders` 等）**不会**因该开关而拒绝请求。
   独占是纯前端行为——若需要真正的后端硬约束，需另行增加校验（当前版本没有）。

#### 7.1.2 Tab 内容

`activeTab === 'manual'` 时渲染三段：

1. **账户卡片**：用户名 + 当前余额（与「充值」Tab 一致）。
2. **档位选择**：
   - `manualTiers.length === 0` → 显示「充值功能暂未开放」（`payment.notAvailable`），不渲染后续区块。
   - 否则 3 列网格按钮，每项显示 `¥{price}` 与 `得 {credit} 额度`；选中项高亮。
3. **所选档位收款二维码**：`selectedTier.qr_image`，点击放大（`previewImage` lightbox）；
   未配置二维码时显示 `payment.manualNoQR`；下方固定显示 `payment.manualPayHint`（"付款后联系下方客服…"）。
4. **客服区块**：`payment.manualCSLabel`（"客服"）标题 + `manualCSQRImage` + `manualCSHelpText`
   （为空时回退到 i18n 默认文案 `payment.manualCSHelpTextDefault`）。

#### 7.1.3 管理员隐藏入口（双击客服标题）

客服区块标题上挂了 `@dblclick="openAdminRecharge"`，且仅在 `isAdmin` 时生效
（非管理员时标题加 `cursor-default select-none`，看不出可点）。

```ts
function openAdminRecharge() {
  if (!isAdmin.value) return
  adminRechargeSearchQuery.value = ''
  adminRechargeSearchResults.value = []
  adminRechargeSelectedUser.value = null
  adminRechargePayAmount.value = selectedTier.value?.price ?? null   // 预填当前档位
  adminRechargeCredit.value = selectedTier.value?.credit ?? null
  adminRechargeError.value = ''
  adminRechargeSuccess.value = ''
  adminRechargeOpen.value = true
}
```

弹窗流程：搜索用户（防抖 400ms，按用户名/邮箱）→ 选择用户 →
**实付金额 / 到账额度为只读**（取自当前选中档位）→ 提交：

```ts
await adminUsersAPI.updateBalance(target.id, credit, 'add', t('payment.adminRecharge.note'), payAmount)
```

即调用管理端余额接口（`operation='add'` + `pay_amount`），触发 §3.2 的订单落库。
若给自己充值则 `authStore.refreshUser()`。

#### 7.1.4 相关状态

```ts
const manualRechargeEnabled = computed(() => checkout.value.manual_recharge_enabled === true)
const manualTiers           = computed(() => checkout.value.manual_recharge_tiers ?? [])
const manualCSQRImage       = computed(() => checkout.value.manual_cs_qr_image ?? '')
const manualCSHelpText      = computed(() => checkout.value.manual_cs_help_text?.trim() || t('payment.manualCSHelpTextDefault'))
const selectedTierIndex     = ref(0)
const selectedTier          = computed(() => manualTiers.value[selectedTierIndex.value] ?? null)
const isAdmin               = computed(() => authStore.isAdmin)
```

### 7.2 管理端 `frontend/src/views/admin/SettingsView.vue`

支付设置中的 **Row 6：Manual (offline) recharge**（紧随帮助图片/帮助文案之后，支付宝轮询区块之前）：

- 复选框 `form.payment_manual_recharge_enabled` + 说明文案 `admin.settings.payment.manualRechargeHint`；
- **仅当启用时**（`v-if="form.payment_manual_recharge_enabled"`）展示：
  - **档位编辑器**：每项一个卡片，字段 `实付金额（¥）`（`tier.price`，number，min=0 step=0.01）、
    `到账额度`（`tier.credit`）、`收款二维码`（`<ImageUpload v-model="tier.qr_image">`）+ 删除按钮；下方「添加档位」按钮。
  - **客服二维码**（`<ImageUpload v-model="form.payment_manual_cs_qr_image">`）
  - **客服提示文案**（textarea，`form.payment_manual_cs_help_text`）

辅助函数：

```ts
function addManualTier() {
  if (!Array.isArray(form.payment_manual_recharge_tiers)) form.payment_manual_recharge_tiers = [];
  form.payment_manual_recharge_tiers.push({ price: 0, credit: 0, qr_image: "" });
}
function removeManualTier(idx: number) { form.payment_manual_recharge_tiers.splice(idx, 1); }
```

表单模型与保存：

- 类型声明与默认值：`payment_manual_recharge_enabled: false`、`payment_manual_recharge_tiers: []`、
  `payment_manual_cs_qr_image: ""`、`payment_manual_cs_help_text: ""`。
- 加载：走通用 `loadSettings()` 逐键赋值。
- 保存：随设置一起提交 `payment_manual_recharge_enabled` / `payment_manual_recharge_tiers` /
  `payment_manual_cs_qr_image` / `payment_manual_cs_help_text`。

> `ImageUpload` 组件负责把本地图片转成 data URL，这是"图片以 base64 存 settings"这个设计成立的前提。

### 7.3 API 与类型

- `frontend/src/api/admin/settings.ts`：**请求体与响应体两处**都要加 4 个字段。
- `frontend/src/types/payment.ts`：`CheckoutInfoResponse` 加 `manual_recharge_enabled` /
  `manual_recharge_tiers` / `manual_cs_qr_image` / `manual_cs_help_text`；新增 `ManualRechargeTier` 接口。
- 管理员手动充值复用 `frontend/src/api/admin/users.ts` 的 `updateBalance`。

### 7.4 i18n

管理端 `frontend/src/i18n/locales/{zh,en}/admin/settings.ts` → `admin.settings.payment.*`：

```
manualRechargeEnabled, manualRechargeHint,
manualTierPrice, manualTierCredit, manualTierQR, manualTierAdd,
manualCSQR, manualCSHelpText, manualCSHelpTextPlaceholder
```

用户端 `frontend/src/i18n/locales/{zh,en}/misc.ts` → `payment.*`：

```
tabManual, manualSelectTier, manualTierCredit, manualScanToPay,
manualNoQR, manualPayHint, manualCSLabel, manualCSHelpTextDefault,
adminRecharge.{title,subtitle,searchLabel,searchPlaceholder,noResults,currentBalance,
               payAmount,credit,note,success,errorNoUser,errorBadAmount,errorBadPayAmount,errorGeneric}
```

中文参考文案：

| 键 | 中文 |
|---|---|
| `tabManual` | 扫码充值 |
| `manualSelectTier` | 选择充值档位 |
| `manualTierCredit` | 得 {credit} 额度 |
| `manualScanToPay` | 扫码支付 ¥{price} |
| `manualNoQR` | 该档位暂未配置收款二维码 |
| `manualPayHint` | 付款后联系下方客服，提供账户和付款记录，完成充值。 |
| `manualCSLabel` | 客服 |
| `manualCSHelpTextDefault` | 付款后联系客服，提供账户和付款记录，完成充值。 |
| `manualRechargeEnabled` | 启用扫码人工充值 |
| `manualRechargeHint` | 开启后，充值页会显示固定档位+收款二维码，用户付款后由客服人工到账。 |
| `manualTierPrice` / `manualTierCredit` / `manualTierQR` / `manualTierAdd` | 实付金额（¥） / 到账额度 / 收款二维码 / 添加档位 |
| `manualCSQR` / `manualCSHelpText` | 客服二维码 / 客服提示文案 |

---

## 8. 关键边界与已修复坑位（重新实现时务必保留）

1. **图片端点必须免鉴权** → `<img>` 无法带 `Authorization` 头；挪进认证分组会导致二维码全部 404。
2. **未修改图片回传导致 base64 丢失** → `IsOwnSettingsImageURL` 判定 + 档位按 `SettingsImageTierIndex` 映射回原图（§5.3）。
3. **档位重排/删减后图片错位** → 映射用 URL 里的 `index`，而不是数组下标位置，容忍管理端重排。
4. **`GetPaymentConfig` 的 key 列表漏加** → 表现为"保存成功但读回为空"，四处链路（§6.3）需同步补齐。
5. **`isPaymentConfigTouched` 漏加** → 只改扫码人工充值这一项时保存不生效。
6. **独占模式是有意设计** → `tabs` 提前 `return`；`?tab=subscription` 续订跳转被忽略；不要"顺手修好"。
7. **后端无硬约束** → 独占是前端行为，在线下单接口仍可被直接调用；若产品要求强约束需另行加校验。
8. **订单号 `MR` 前缀与常规充值订单前缀相同但格式不同** → 见 §3.2 提示。
9. **二维码为空时降级** → 档位列表为空显示「充值功能暂未开放」；单档位未配二维码显示 `manualNoQR`；
   客服二维码为空则不渲染 `<img>`，客服文案为空回退 i18n 默认文案。

---

## 9. 重新添加时的改动文件清单

**后端**

- `internal/service/payment_config_service.go`（设置键、`ManualRechargeTier`、`PaymentConfig` / `UpdatePaymentConfigRequest` 字段、`parseManualRechargeTiers`、key 列表、更新写入、档位校验与二维码回传映射）
- `internal/service/settings_image.go`（图片键常量、`ManualTierQRImageURL`、`SettingsImageTierIndex`、`IsOwnSettingsImageURL`、`SettingsImageOrDataURL`、`DecodeDataURL`）
- `internal/handler/payment_handler.go`（`stripPaymentConfigImages`、`GetImage`、`GetCheckoutInfo` 响应字段 + DTO）
- `internal/handler/dto/settings.go`（`PaymentManual*` 响应字段）
- `internal/handler/admin/setting_handler.go`（回显）
- `internal/handler/admin/setting_handler_update.go`（请求体字段 + 组装 + `isPaymentConfigTouched`）
- `internal/server/router.go`（`v1.GET("/settings/image", h.Payment.GetImage)`，**免鉴权**）
- `internal/service/admin_user.go`（`createManualRechargeOrder` 及 `UpdateUserBalance` 中的触发条件）

**前端**

- `src/views/user/PaymentView.vue`（**核心**：tabs 独占逻辑、manual Tab 内容、挂载强制切换、`?tab=subscription` 忽略、管理员双击隐藏入口 + 手动充值弹窗）
- `src/views/admin/SettingsView.vue`（Row 6 区块、类型/默认值、`addManualTier`/`removeManualTier`、保存组装）
- `src/api/admin/settings.ts`（请求体 + 响应体字段）
- `src/types/payment.ts`（`CheckoutInfoResponse` 字段 + `ManualRechargeTier`）
- `src/i18n/locales/{zh,en}/admin/settings.ts`、`src/i18n/locales/{zh,en}/misc.ts`

**复用（无需改动）**

- `src/components/common/ImageUpload.vue`、`src/api/admin/users.ts` 的 `updateBalance`、
  `src/components/layout/AppSidebar.vue`（菜单不因本开关变化）

---

## 10. 验收用例

| # | 场景 | 预期 |
|---|---|---|
| 1 | 关闭「启用扫码人工充值」 | 充值页恢复原有「充值 / 订阅」Tab（及可能存在的其它 Tab） |
| 2 | 开启该开关 + 配置 2 个档位 | 充值页**只剩「扫码充值」一个 Tab**，切换条隐藏，自动充值/订阅 Tab 均不可见 |
| 3 | 开启后访问 `/purchase?tab=subscription&group=X` | 落在扫码充值页，**不**进入续订流程 |
| 4 | 侧边栏 | 「充值/订阅」「我的订单」菜单仍在（由 `payment_enabled` 控制）；点「充值/订阅」进入扫码充值页 |
| 5 | 选择档位 | 展示该档位收款二维码；点击可放大 |
| 6 | 档位未配二维码 | 显示「该档位暂未配置收款二维码」，不报错 |
| 7 | 开启开关但档位为空 | 显示「充值功能暂未开放」 |
| 8 | 客服二维码为空 | 不渲染客服图片；客服文案为空时显示 i18n 默认文案 |
| 9 | 管理员双击「客服」标题 | 打开手动充值弹窗，实付金额/到账额度预填为当前档位且**只读** |
| 10 | 非管理员点击「客服」标题 | 无任何反应（无入口提示） |
| 11 | 管理员提交手动充值 | 用户余额增加 `credit`；「我的订单」出现 `src_host=admin-manual` 的已支付订单；`fee_rate = (credit-pay)/pay` |
| 12 | 管理端只修改客服提示文案后保存 | 保存生效（验证 `isPaymentConfigTouched` 与四处链路） |
| 13 | 管理端保存**未修改**的档位二维码 | 图片仍然可见（验证 base64 未被端点 URL 覆盖） |
| 14 | 管理端调整档位顺序后保存 | 每个档位仍显示各自原来的二维码（验证 index 映射） |
| 15 | 填写 `price <= 0` 或 `credit <= 0` 的档位并保存 | 返回 400 `INVALID_MANUAL_RECHARGE_TIER` |
| 16 | 用户侧 `/payment/config`、`/payment/checkout-info` | 含 `manual_*` 字段；图片字段为 `/api/v1/settings/image?...` 端点 URL 而非 base64 |
| 17 | 未带 token 直接 GET `/api/v1/settings/image?key=payment_manual_cs_qr` | 返回图片（200），**不得**返回 401 |
| 18 | `index` 越界访问档位二维码 | 返回 404 |
