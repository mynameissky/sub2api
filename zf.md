# 支付宝轮询充值（agent 生成二维码 + 轮询查单）需求文档

> 版本：v1.0　·　用途：在最新版本中重新添加本功能时的实现依据
> 参考实现工程：`/home/idriver/Downloads/zhifuTest`（.NET，TboxApi 查单调用）

---

## 1. 功能概述

在原有支付体系（支付宝 / 微信 / Stripe / Airwallex / 扫码人工充值等）之外，新增一种**由智能体（蚂蚁百宝箱 / Tbox）托管**的支付宝充值方式：

1. 用户在充值页选择固定档位（档位 = 实付金额 + 到账额度）。
2. 系统用本地生成的订单号、档位金额和固定备注，请求配置好的智能体**生成支付宝收款二维码**。
3. 用户扫码支付；系统按配置的**开始时间 / 间隔**轮询智能体查询该订单状态。
4. 查到支付成功后，走既有履约管线（`toPaid` → `executeFulfillment`）为用户**充值到账**。
5. 超过配置的**订单超时时间**仍未支付 → 订单关闭（EXPIRED）。

关键特性：**不依赖任何支付渠道实例（provider instance）**，凭证（ApiKey / 智能体ID）存放在全局支付配置中，且**不下发给普通用户**。

命名约定：

- 支付类型 `payment_type` = `alipay_poll`（`payment.TypeAlipayPoll`）
- 订单类型 `order_type` = `balance`（`payment.OrderTypeBalance`）
- 智能体固定备注 `alipayPollNote` = `FaithAPI充值`
- 订单号统一前缀 `MR`（不带下划线），格式 `MR + YYYYMMDD + 8位随机串`，例如 `MR20260829aB3kX9mQ`

---

## 2. 配置项（管理端 Settings → 支付设置）

沿用「启用扫码人工充值」的配置范式，新增一组设置。开启后充值页出现独立 Tab。

| 界面名称 | 设置键（DB） | 请求字段 | 类型 | 默认 |
|---|---|---|---|---|
| 启用支付宝轮询充值 | `ALIPAY_POLL_ENABLED` | `payment_alipay_poll_enabled` | bool | false |
| 档位列表 | `ALIPAY_POLL_TIERS` | `payment_alipay_poll_tiers` | JSON 数组 | `[]` |
| ApiKey | `ALIPAY_POLL_API_KEY` | `payment_alipay_poll_api_key` | string | — |
| 智能体ID | `ALIPAY_POLL_AGENT_ID` | `payment_alipay_poll_agent_id` | string | — |
| 轮询开始时间（秒） | `ALIPAY_POLL_START_DELAY_SEC` | `payment_alipay_poll_start_delay_sec` | int | 30 |
| 轮询间隔（秒） | `ALIPAY_POLL_INTERVAL_SEC` | `payment_alipay_poll_interval_sec` | int | 5 |
| 订单超时时间（秒） | `ALIPAY_POLL_TIMEOUT_SEC` | `payment_alipay_poll_timeout_sec` | int | 600 |

档位结构 `AlipayPollTier`：

```go
type AlipayPollTier struct {
    Price  float64 `json:"price"`  // 实付金额（¥，作为支付宝收款金额传给智能体）
    Credit float64 `json:"credit"` // 到账额度（支付成功后充值到余额）
}
```

**安全约束（必须保持）**：`ApiKey` / `AgentID` 是**只写**字段。

- 它们是独立设置键，**不属于** `PaymentConfig` 结构体。
- 用户侧接口（`GET /payment/config`、`GET /payment/checkout-info`）**绝不返回**它们；仅通过 `AlipayPollCredentials(ctx)` 在服务端读取。
- 仅管理员设置接口（`GET /admin/settings`）会回显，用于编辑。

---

## 3. 数据模型

订单复用既有 `PaymentOrder` 实体，无需改表（`qr_image` 字段已存在）：

| 字段 | 取值 |
|---|---|
| `out_trade_no` | `MR` + `YYYYMMDD` + 8位随机串（唯一性由 `allocateOutTradeNo` 5 次重试保证） |
| `payment_type` | `alipay_poll` |
| `order_type` | `balance` |
| `amount` | `tier.Credit`（到账额度） |
| `pay_amount` | `tier.Price`（实付金额） |
| `fee_rate` | 0 |
| `qr_code` | 智能体返回的二维码（base64 data URL 或图片 URL），生成后回填 |
| `recharge_code` | `PAY-{orderID}-{nano%100000}` |
| `expires_at` | 创建时间 + `ALIPAY_POLL_TIMEOUT_SEC` 秒 |
| `status` | `PENDING` → 支付成功走 `toPaid` 履约 → `COMPLETED`；超时/上游关闭 → `EXPIRED`；创建二维码失败 → `FAILED` |

---

## 4. 接口设计

路由注册见 `backend/internal/server/routes/payment.go`，均在已认证分组 `/api/v1/payment` 下：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/payment/alipay-poll/orders` | 创建订单并生成二维码 |
| POST | `/payment/alipay-poll/orders/verify` | 查询订单状态（前端轮询调用） |
| POST | `/payment/orders/:id/cancel` | **复用既有接口**，用户切换页面/离开时取消未完成订单 |

### 4.1 创建订单

请求：`{ "tier_index": number }`

响应 `AlipayPollCreateResult`：

```go
type AlipayPollCreateResult struct {
    OrderID       int64     `json:"order_id"`
    OutTradeNo    string    `json:"out_trade_no"`
    Amount        float64   `json:"amount"`     // 到账额度
    PayAmount     float64   `json:"pay_amount"` // 实付金额
    QRImage       string    `json:"qr_image"`
    Status        string    `json:"status"`
    ExpiresAt     time.Time `json:"expires_at"`
    StartDelaySec int       `json:"poll_start_delay_sec"`
    IntervalSec   int       `json:"poll_interval_sec"`
}
```

校验：支付总开关开启 → 轮询充值开关开启 → `tier_index` 合法 → 凭证已配置 → 用户为 active → 待支付单数 / 当日限额校验。

### 4.2 查询订单

请求：`{ "out_trade_no": string }`（复用 `VerifyOrderRequest`）
响应：`sanitizePaymentOrderForResponse(order)`（订单对象，前端读取 `status`）。

校验：订单存在 → 属于当前用户 → 非 PENDING 直接返回现状。

---

## 5. 智能体（Tbox）调用规范

常量：

```go
tboxBaseURL  = "https://o.tbox.cn"
tboxChatPath = "/openapi/v1/chat/create"
tboxUserID   = "sub2api_alipay_qr_client"
alipayPollNote = "FaithAPI充值"
```

请求：

```
POST https://o.tbox.cn/openapi/v1/chat/create
Header: Authorization: <ApiKey>
Header: Content-Type: application/json
Body:   {"agent_id":"<智能体ID>","question":"...","user_id":"sub2api_alipay_qr_client","stream":false}
```

`http.Client{Timeout: 120s}`；非 2xx 视为错误（响应体截断 500 字符）。

**生成二维码的提问**：

```
请生成一个支付宝收款二维码，收款金额 {金额} 元，商户订单号：{订单号}，备注：FaithAPI充值。
```

**查单的提问**：

```
请查询支付宝订单，订单号：{订单号}，并返回该订单的最新支付状态。
```

### 5.1 二维码提取（`extractQRCode`）

按优先级：内联 base64 图片（`data:image/...;base64,...`）→ 图片 URL（`.png/.jpg/.jpeg/.gif/.bmp`）→ 任意 URL。返回值可直接用于 `<img src>`。

响应解析：递归收集 JSON 中所有 `content` 字段拼接为文本（`collectTboxContent`）。

### 5.2 状态判定（`tboxStatusFromAnswer`）— ⚠️ 关键坑位

只匹配**精确的终态短语**，返回 `paid` / `closed` / `""`（未知，视作仍在等待）：

```go
// 成功
"交易成功","支付成功","已支付","支付完成","付款成功","trade_success","trade_finished"
// 终态失败/关闭
"交易关闭","订单已关闭","已关闭","已取消","交易失败","支付失败","trade_closed"
```

**严禁**使用 `failed` / `closed` / `cancelled` / `success` 等裸英文子串，也**不得**把 `交易不存在` 归入关闭。

> 原因：订单尚未被扫码时，智能体回传的是支付宝 `TRADE_NOT_EXIST`（“交易不存在”，报文含 `Business Failed` / `40004` / `ACQ.TRADE_NOT_EXIST`）。若用裸 `failed` 匹配，订单会被误判为已关闭而**提前过期**（远早于配置的超时时间）。该场景必须视作“仍在等待支付”。

---

## 6. 后端实现要点

核心文件：`backend/internal/service/alipay_poll.go`

### 6.1 创建流程 `CreateAlipayPollOrder`

1. 读配置、校验开关 / 档位 / 凭证 / 用户状态。
2. `createAlipayPollOrderTx`：事务内校验待支付单数与当日限额 → `allocateOutTradeNo` → 建单（PENDING）→ 生成并回填 `recharge_code`。
3. **生成二维码（带重试）**：
   ```go
   const (
       alipayPollQRMaxAttempts = 3
       alipayPollQRRetryDelay  = 3 * time.Second
   )
   ```
   最多 3 次、每次间隔 3 秒（`sleepAlipayPoll` 支持 ctx 取消）。**原因**：智能体在新会话首轮可能只回文本、尚未产出二维码，重试即可成功（对应“第一次生成二维码失败，后续就可以”的问题）。
4. 成功后回填 `qr_code` 并写审计日志 `ORDER_CREATED`。
5. 二维码始终生成失败 → 将订单置 `FAILED` 并写 `failed_reason`，返回错误。

### 6.2 查单流程 `VerifyAlipayPollOrder`

1. 按 `out_trade_no` 查单 → 校验归属。
2. 非 `PENDING` 直接返回。
3. **超时关单**：`time.Now().After(o.ExpiresAt)` → `cancelCore(..., OrderStatusExpired, "system", ...)`。
4. 读凭证 → `queryAlipayPollOrder` 查单。
5. 查单**瞬时错误不应失败整个轮询**：留 `PENDING`，由下一次轮询重试。
6. 状态分支：
   - `paid` → `s.toPaid(ctx, o, o.OutTradeNo, o.PayAmount, payment.TypeAlipayPoll)` 履约；
   - `closed` → `cancelCore(..., OrderStatusExpired, ...)`；
   - 其他 → 保持 `PENDING`。

### 6.3 履约管线兼容（`payment_order_lifecycle.go`）

`alipay_poll` 订单**没有** provider 实例，`getOrderProvider` 可能返回 `(nil, nil)`。`checkPaidWithOptions` 必须加空值保护，否则空指针 panic：

```go
prov, err := s.getOrderProvider(ctx, o)
if err != nil { return "" }
if prov == nil {
    // 无上游 provider 实例（如 alipay_poll），无可对账对象，视为未支付
    return ""
}
```

### 6.4 订单号前缀

```go
// 新订单号统一前缀（不带下划线）
orderIDPrefix = "MR"
// 仅用于解析历史 "sub2_<DBID>" 旧订单号的回退逻辑
legacyOrderIDPrefix = "sub2_"
```

`generateOutTradeNo()` → `"MR" + time.Now().Format("20060102") + 8位随机`。
`parseLegacyPaymentOrderID` 必须继续使用 `legacyOrderIDPrefix`，以保证历史订单仍可回退解析。

---

## 7. 前端实现要点

### 7.1 API 层 `frontend/src/api/payment.ts`

```ts
createAlipayPollOrder(tierIndex: number) {
  return apiClient.post<AlipayPollCreateResult>('/payment/alipay-poll/orders', { tier_index: tierIndex })
},
verifyAlipayPollOrder(outTradeNo: string) {
  return apiClient.post<PaymentOrder>('/payment/alipay-poll/orders/verify', { out_trade_no: outTradeNo })
},
```

类型：`frontend/src/types/payment.ts` 增加 `AlipayPollTier`、`AlipayPollCreateResult`，并在 checkout 类型上加 `alipay_poll_enabled / alipay_poll_tiers / alipay_poll_start_delay_sec / alipay_poll_interval_sec / alipay_poll_timeout_sec`。

### 7.2 用户页 `frontend/src/views/user/PaymentView.vue`

新增 Tab `alipayPoll`（仅当 `checkout.alipay_poll_enabled === true` 时显示）。

**状态机**：

```
alipayPollPhase: 'select' | 'polling'
alipayPollStatus: 'pending' | 'paid' | 'expired'
```

**交互**：

1. **选择档位**：档位按钮网格，显示 `¥实付金额` 与 `得 {credit} 额度`。
2. **确认支付**：点击 → `createAlipayPollOrder` → 展示二维码（`qr_image`，可点击放大）→ 按 `startDelaySec` 延迟后开始轮询 → 间隔 `intervalSec` 轮询 `verifyAlipayPollOrder`。
3. **轮询结果**：`COMPLETED` → 状态「支付成功，充值已到账」并 `authStore.refreshUser()`；`EXPIRED/CANCELLED/FAILED` → 状态「订单已超时或关闭」；请求异常 → 忽略，继续轮询。
4. **取消未完成订单（重要）**：轮询中且未结算时——
   - 切换到其它 Tab：调用 `paymentAPI.cancelOrder(order_id)` 并提示「已取消本次未完成的支付订单」，然后复位；
   - 组件卸载（`onBeforeUnmount`）：同样取消订单；
   - 已结算（paid/expired）时仅复位，不再取消。

关键函数：`submitAlipayPoll` / `scheduleAlipayPollPoll` / `pollAlipayPollOrder` / `resetAlipayPoll` / `cancelPendingAlipayPoll` / `clearAlipayPollTimer`。

### 7.3 管理端 `frontend/src/views/admin/SettingsView.vue`

在支付设置「扫码人工充值」之后，新增「支付宝轮询充值」区块（Row 7）：

- 启用复选框 + 说明；
- 开启后展示：档位编辑器（增删，字段 `实付金额（¥）` / `到账额度`）、ApiKey、智能体ID、轮询开始时间、轮询间隔、订单超时时间（后三者带 hint）；
- 表单模型：`form.payment_alipay_poll_*`（默认 `enabled=false`、`tiers=[]`、`start_delay=30`、`interval=5`、`timeout=600`）；
- 加载走通用 `loadSettings()` 逐键赋值；保存时随设置一起提交（见 `saveSettings` 中 `payment_alipay_poll_*` 组装）。
- 类型定义：`frontend/src/api/admin/settings.ts`（请求体与响应体两处）。

### 7.4 i18n

- 管理端：`frontend/src/i18n/locales/{zh,en}/admin/settings.ts` → `admin.settings.payment.alipayPoll*`
  （`alipayPollEnabled`、`alipayPollHint`、`alipayPollTierPrice`、`alipayPollTierCredit`、`alipayPollTierAdd`、`alipayPollApiKey`、`alipayPollAgentId`、`alipayPollStartDelaySec[Hint]`、`alipayPollIntervalSec[Hint]`、`alipayPollTimeoutSec[Hint]`）
- 用户端：`frontend/src/i18n/locales/{zh,en}/misc.ts` → `payment.`
  （`tabAlipayPoll`、`alipayPollSelectTier`、`alipayPollTierCredit`、`alipayPollConfirm`、`alipayPollScanToPay`、`alipayPollNoQR`、`alipayPollPolling`、`alipayPollPaid`、`alipayPollExpired`、`alipayPollBack`、`alipayPollOrderNo`、`alipayPollCreateFailed`、`alipayPollCancelled`）

---

## 8. 关键边界与已修复坑位（重新实现时务必保留）

1. **二维码首次生成失败** → `generateAlipayPollQRCode` 3 次重试、间隔 3 秒。
2. **订单被提前判「已过期」** → `tboxStatusFromAnswer` 仅匹配精确终态短语，禁用裸英文子串，`交易不存在 / TRADE_NOT_EXIST / Business Failed` 视为等待中。
3. **alipay_poll 订单空 provider panic** → `checkPaidWithOptions` 增加 `prov == nil` 保护。
4. **未支付时切页/离开未取消订单** → 前端 `watch(activeTab)` + `onBeforeUnmount` 调 `cancelOrder` 并提示。
5. **订单号前缀统一** → `MR`（不带下划线），`sub2_` 仅保留给历史订单回退解析。
6. **凭证不下发** → ApiKey/AgentID 独立设置键，仅服务端 `AlipayPollCredentials` 读取，用户接口不返回。

---

## 9. 重新添加时的改动文件清单

**后端**

- `internal/service/alipay_poll.go`（新增，核心逻辑）
- `internal/service/payment_config_service.go`（设置键、`AlipayPollTier`、默认值、解析、`AlipayPollCredentials`、更新写入）
- `internal/service/payment_service.go`（`orderIDPrefix` / `legacyOrderIDPrefix`）
- `internal/service/payment_order_lifecycle.go`（`checkPaidWithOptions` nil provider 保护）
- `internal/payment/types.go`（`TypeAlipayPoll`）
- `internal/handler/payment_handler.go`（`GetCheckoutInfo` 增加字段、`CreateAlipayPollOrder`、`VerifyAlipayPollOrder`）
- `internal/handler/dto/settings.go`（`PaymentAlipayPoll*` 响应字段）
- `internal/handler/admin/setting_handler.go`（回显含 ApiKey/AgentID）
- `internal/handler/admin/setting_handler_update.go`（请求体字段 + 组装 + 是否涉及支付配置判断）
- `internal/server/routes/payment.go`（`/payment/alipay-poll` 路由）

**前端**

- `src/api/payment.ts`、`src/types/payment.ts`
- `src/api/admin/settings.ts`
- `src/views/user/PaymentView.vue`
- `src/views/admin/SettingsView.vue`
- `src/i18n/locales/{zh,en}/admin/settings.ts`、`src/i18n/locales/{zh,en}/misc.ts`

---

## 10. 验收用例

| # | 场景 | 预期 |
|---|---|---|
| 1 | 首次点击「确认支付」 | 能拿到二维码（首次失败会自动重试，不报错） |
| 2 | 扫码并支付 | 轮询到成功，状态显示已到账，余额增加 `credit` |
| 3 | 未支付等待超过 `timeout` 秒 | 订单 `EXPIRED`，前端显示已超时 |
| 4 | 未扫码时（订单尚未生成于支付宝侧） | 保持等待，**不得**提前判过期 |
| 5 | 轮询中切换到其它 Tab | 调用取消接口，提示「已取消本次未完成的支付订单」 |
| 6 | 轮询中直接离开页面 | 订单被取消 |
| 7 | 关闭轮询充值开关 | 充值页不显示该 Tab |
| 8 | 查看用户侧 `/payment/config`、`/payment/checkout-info` | **不包含** ApiKey / AgentID |
| 9 | 新订单号 | 形如 `MR20260829aB3kX9mQ`，无下划线前缀 |
| 10 | 历史 `sub2_<ID>` 订单回退解析 | 仍可正确解析（回归） |
