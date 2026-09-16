package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// --- 支付宝轮询充值（智能体托管二维码 + 轮询查单） ---
//
// 该充值方式不依赖任何支付渠道实例（provider instance）：由配置好的智能体
// （蚂蚁百宝箱 / Tbox）生成支付宝收款二维码，随后按配置的开始时间/间隔轮询
// 查询该订单状态，查到支付成功即走既有履约管线（toPaid → executeFulfillment）。
//
// 凭证（ApiKey / 智能体ID）存放在全局支付配置中，且不下发给普通用户。
//
// 智能体端点（BaseURL / ChatPath）、调用方标识（UserID）与订单备注（Note）
// 同样是服务端集成细节，默认为 Tbox 公开接口，均可在管理端手动维护；
// 留空时由 parsePaymentConfig 回落到 defaultAlipayPoll* 默认值。

const (
	tboxRequestTimeout = 120 * time.Second
	tboxMaxBodyBytes   = 1 << 20

	// 智能体在新会话首轮可能只回文本、尚未产出二维码，因此生成二维码带重试。
	alipayPollQRMaxAttempts = 3
	alipayPollQRRetryDelay  = 3 * time.Second

	// tboxStatusPaid / tboxStatusClosed 是查单结果归一化后的终态。
	tboxStatusPaid   = "paid"
	tboxStatusClosed = "closed"
)

// AlipayPollCreateResult is returned after creating an Alipay poll order and
// generating its QR code.
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

// CreateAlipayPollOrder creates a pending Alipay poll order for the given tier
// and asks the configured agent to generate its payment QR code.
func (s *PaymentService) CreateAlipayPollOrder(ctx context.Context, userID int64, tierIndex int, clientIP, srcHost string) (*AlipayPollCreateResult, error) {
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	if !cfg.AlipayPollEnabled {
		return nil, infraerrors.Forbidden("ALIPAY_POLL_DISABLED", "alipay poll recharge is disabled")
	}
	if tierIndex < 0 || tierIndex >= len(cfg.AlipayPollTiers) {
		return nil, infraerrors.BadRequest("INVALID_TIER", "invalid alipay poll tier")
	}
	tier := cfg.AlipayPollTiers[tierIndex]
	creds, err := s.configService.AlipayPollCredentials(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user.Status != payment.EntityStatusActive {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}

	order, err := s.createAlipayPollOrderTx(ctx, user, cfg, tier, clientIP, srcHost)
	if err != nil {
		return nil, err
	}

	qrImage, err := s.generateAlipayPollQRCode(ctx, creds, cfg.Endpoint(), order.OutTradeNo, tier.Price)
	if err != nil {
		slog.Error("[AlipayPoll] generate QR code failed", "orderID", order.ID, "error", err)
		_, _ = s.entClient.PaymentOrder.UpdateOneID(order.ID).
			SetStatus(OrderStatusFailed).
			SetFailedReason(psTruncateReason(err.Error())).
			Save(ctx)
		return nil, infraerrors.ServiceUnavailable("ALIPAY_POLL_QR_FAILED", "failed to generate alipay payment QR code")
	}
	updated, err := s.entClient.PaymentOrder.UpdateOneID(order.ID).SetQrCode(qrImage).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("persist alipay poll qr code: %w", err)
	}
	s.writeAuditLog(ctx, updated.ID, "ORDER_CREATED", fmt.Sprintf("user:%d", userID), map[string]any{
		"paymentAmount":  tier.Price,
		"creditedAmount": tier.Credit,
		"paymentType":    payment.TypeAlipayPoll,
		"orderType":      payment.OrderTypeBalance,
		"outTradeNo":     updated.OutTradeNo,
	})

	return &AlipayPollCreateResult{
		OrderID:       updated.ID,
		OutTradeNo:    updated.OutTradeNo,
		Amount:        updated.Amount,
		PayAmount:     updated.PayAmount,
		QRImage:       qrImage,
		Status:        updated.Status,
		ExpiresAt:     updated.ExpiresAt,
		StartDelaySec: cfg.AlipayPollStartDelaySec,
		IntervalSec:   cfg.AlipayPollIntervalSec,
	}, nil
}

// allocateAlipayPollOutTradeNo 为支付宝轮询订单分配 MR + 13位毫秒时间戳 的订单号。
// 与通用 allocateOutTradeNo 相同的唯一性兜底：同毫秒并发下单时重新取时间戳重试。
func (s *PaymentService) allocateAlipayPollOutTradeNo(ctx context.Context, tx *dbent.Tx) (string, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := generateAlipayPollOutTradeNo()
		exists, err := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNo(candidate)).Exist(ctx)
		if err != nil {
			return "", fmt.Errorf("check out_trade_no uniqueness: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("generate unique alipay poll out_trade_no: exhausted %d attempts", maxAttempts)
}

func (s *PaymentService) createAlipayPollOrderTx(ctx context.Context, user *User, cfg *PaymentConfig, tier AlipayPollTier, clientIP, srcHost string) (*dbent.PaymentOrder, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.checkPendingLimit(ctx, tx, user.ID, cfg.MaxPendingOrders); err != nil {
		return nil, err
	}
	if err := s.checkDailyLimit(ctx, tx, user.ID, tier.Price, cfg.DailyLimit); err != nil {
		return nil, err
	}
	timeout := cfg.AlipayPollTimeoutSec
	if timeout <= 0 {
		timeout = defaultAlipayPollTimeoutSec
	}
	exp := time.Now().Add(time.Duration(timeout) * time.Second)
	outTradeNo, err := s.allocateAlipayPollOutTradeNo(ctx, tx)
	if err != nil {
		return nil, err
	}
	order, err := tx.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetNillableUserNotes(psNilIfEmpty(user.Notes)).
		SetAmount(tier.Credit).
		SetPayAmount(tier.Price).
		SetFeeRate(0).
		SetRechargeCode("").
		SetOutTradeNo(outTradeNo).
		SetPaymentType(payment.TypeAlipayPoll).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(exp).
		SetClientIP(clientIP).
		SetSrcHost(srcHost).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create alipay poll order: %w", err)
	}
	code := fmt.Sprintf("PAY-%d-%d", order.ID, time.Now().UnixNano()%100000)
	order, err = tx.PaymentOrder.UpdateOneID(order.ID).SetRechargeCode(code).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("set recharge code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit order transaction: %w", err)
	}
	return order, nil
}

// VerifyAlipayPollOrder queries the agent for the order's payment status and
// fulfills it when paid. Query failures keep the order PENDING so the next
// poll can retry.
func (s *PaymentService) VerifyAlipayPollOrder(ctx context.Context, outTradeNo string, userID int64) (*dbent.PaymentOrder, error) {
	outTradeNo, err := normalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNo(outTradeNo)).Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	if o.Status != OrderStatusPending {
		return o, nil
	}

	creds, credErr := s.configService.AlipayPollCredentials(ctx)
	// 配置读取失败时 cfg 为 nil，Endpoint() 自带空值兜底，仍能拼出默认地址。
	cfg, _ := s.configService.GetPaymentConfig(ctx)
	ep := cfg.Endpoint()

	// 超时关单：先向上游做最后一次确认，避免刚好在超时前支付成功的订单被误关。
	if time.Now().After(o.ExpiresAt) {
		if credErr == nil {
			if status, qerr := s.queryAlipayPollOrder(ctx, creds, ep, o.OutTradeNo); qerr == nil && status == tboxStatusPaid {
				return s.fulfillAlipayPollOrder(ctx, o)
			}
		}
		if _, err := s.cancelCore(ctx, o, OrderStatusExpired, "system", "alipay poll order timed out"); err != nil {
			slog.Warn("[AlipayPoll] expire timed out order failed", "orderID", o.ID, "error", err)
		}
		return s.reloadPaymentOrder(ctx, o.ID)
	}

	if credErr != nil {
		// 凭证瞬时不可用：保持 PENDING，由下一次轮询重试。
		return o, nil
	}
	status, err := s.queryAlipayPollOrder(ctx, creds, ep, o.OutTradeNo)
	if err != nil {
		// 查单瞬时错误不应让整个轮询失败。
		slog.Warn("[AlipayPoll] query order failed", "orderID", o.ID, "error", err)
		return o, nil
	}
	switch status {
	case tboxStatusPaid:
		return s.fulfillAlipayPollOrder(ctx, o)
	case tboxStatusClosed:
		if _, err := s.cancelCore(ctx, o, OrderStatusExpired, "system", "alipay poll order closed upstream"); err != nil {
			slog.Warn("[AlipayPoll] close order failed", "orderID", o.ID, "error", err)
		}
		return s.reloadPaymentOrder(ctx, o.ID)
	default:
		return o, nil
	}
}

// fulfillAlipayPollOrder 走既有履约管线（toPaid → executeFulfillment）为用户充值到账。
func (s *PaymentService) fulfillAlipayPollOrder(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentOrder, error) {
	if err := s.toPaid(ctx, o, o.OutTradeNo, o.PayAmount, payment.TypeAlipayPoll); err != nil {
		slog.Error("[AlipayPoll] fulfill paid order failed", "orderID", o.ID, "error", err)
	}
	return s.reloadPaymentOrder(ctx, o.ID)
}

func (s *PaymentService) reloadPaymentOrder(ctx context.Context, orderID int64) (*dbent.PaymentOrder, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("reload payment order: %w", err)
	}
	return o, nil
}

// generateAlipayPollQRCode asks the agent to produce a QR code, retrying a few
// times because a fresh agent session may reply with text only on the first turn.
func (s *PaymentService) generateAlipayPollQRCode(ctx context.Context, creds *AlipayPollCredentials, ep AlipayPollEndpoint, outTradeNo string, price float64) (string, error) {
	question := fmt.Sprintf(
		"请生成一个支付宝收款二维码，收款金额 %s 元，商户订单号：%s，备注：%s。",
		formatAlipayPollAmount(price), outTradeNo, ep.Note,
	)
	var lastErr error = fmt.Errorf("alipay poll qr code generation failed")
	for attempt := 1; attempt <= alipayPollQRMaxAttempts; attempt++ {
		answer, err := callTboxAgent(ctx, creds, ep, question)
		if err != nil {
			lastErr = err
		} else if qr := extractQRCode(answer); qr != "" {
			return qr, nil
		} else {
			lastErr = fmt.Errorf("agent response contained no QR code")
		}
		if attempt < alipayPollQRMaxAttempts {
			if err := sleepAlipayPoll(ctx, alipayPollQRRetryDelay); err != nil {
				return "", err
			}
		}
	}
	return "", lastErr
}

// queryAlipayPollOrder returns "paid" / "closed" / "" (still waiting).
func (s *PaymentService) queryAlipayPollOrder(ctx context.Context, creds *AlipayPollCredentials, ep AlipayPollEndpoint, outTradeNo string) (string, error) {
	question := fmt.Sprintf("请查询支付宝订单，订单号：%s，并返回该订单的最新支付状态。", outTradeNo)
	answer, err := callTboxAgent(ctx, creds, ep, question)
	if err != nil {
		return "", err
	}
	return tboxStatusFromAnswer(answer), nil
}

// callTboxAgent posts a question to the configured Tbox agent and returns the
// concatenated text content of the response.
func callTboxAgent(ctx context.Context, creds *AlipayPollCredentials, ep AlipayPollEndpoint, question string) (string, error) {
	if creds == nil || creds.APIKey == "" || creds.AgentID == "" {
		return "", fmt.Errorf("alipay poll credentials are not configured")
	}
	payload, err := json.Marshal(map[string]any{
		"agent_id": creds.AgentID,
		"question": question,
		"user_id":  ep.UserID,
		"stream":   false,
	})
	if err != nil {
		return "", fmt.Errorf("marshal tbox request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build tbox request: %w", err)
	}
	req.Header.Set("Authorization", creds.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: tboxRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call tbox agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, tboxMaxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("read tbox response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("tbox agent returned status %d: %s", resp.StatusCode, psTruncateReason(string(raw)))
	}
	return collectTboxContent(raw), nil
}

// collectTboxContent 递归收集 JSON 中所有 content 字段并拼接为文本。
func collectTboxContent(raw []byte) string {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw)
	}
	var parts []string
	collectTboxContentInto(decoded, &parts)
	return strings.Join(parts, "\n")
}

func collectTboxContentInto(node any, parts *[]string) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "content" {
				if s, ok := child.(string); ok && strings.TrimSpace(s) != "" {
					*parts = append(*parts, s)
					continue
				}
			}
			collectTboxContentInto(child, parts)
		}
	case []any:
		for _, child := range v {
			collectTboxContentInto(child, parts)
		}
	}
}

// extractQRCode 按优先级提取可直接用于 <img src> 的二维码内容：
// 内联 base64 图片 → 图片 URL → 任意 URL。
func extractQRCode(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	if data := extractDataURLImage(answer); data != "" {
		return data
	}
	lower := strings.ToLower(answer)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".bmp"} {
		if u := findURLWithExtension(answer, lower, ext); u != "" {
			return u
		}
	}
	return findAnyURL(answer)
}

func extractDataURLImage(text string) string {
	const marker = "data:image/"
	idx := strings.Index(strings.ToLower(text), marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	end := len(rest)
	for i, ch := range rest {
		if ch == ')' || ch == '"' || ch == '\'' || ch == '<' || ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' {
			end = i
			break
		}
	}
	candidate := strings.TrimSpace(rest[:end])
	if !strings.Contains(candidate, ";base64,") {
		return ""
	}
	return candidate
}

func findURLWithExtension(text, lower, ext string) string {
	idx := 0
	for {
		pos := strings.Index(lower[idx:], ext)
		if pos < 0 {
			return ""
		}
		pos += idx
		start := strings.LastIndexAny(text[:pos], " \n\r\t\"'()<>[")
		start++
		u := strings.TrimSpace(text[start : pos+len(ext)])
		// 去掉总是紧跟在 URL 后面的标点
		u = strings.TrimRight(u, ".,;:")
		if isHTTPURL(u) {
			return u
		}
		idx = pos + len(ext)
		if idx >= len(lower) {
			return ""
		}
	}
}

func findAnyURL(text string) string {
	for _, prefix := range []string{"https://", "http://"} {
		lower := strings.ToLower(text)
		pos := strings.Index(lower, prefix)
		if pos < 0 {
			continue
		}
		rest := text[pos:]
		end := len(rest)
		for i, ch := range rest {
			if ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' || ch == '"' || ch == '\'' || ch == ')' || ch == '<' || ch == '>' || ch == '[' {
				end = i
				break
			}
		}
		u := strings.TrimRight(strings.TrimSpace(rest[:end]), ".,;:")
		if isHTTPURL(u) {
			return u
		}
	}
	return ""
}

func isHTTPURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}

// tboxStatusFromAnswer 把智能体的自然语言答复归一化为 paid / closed / ""（等待中）。
//
// ⚠️ 只匹配精确的终态短语，严禁使用 failed / closed / cancelled / success 等
// 裸英文子串，也不得把「交易不存在」归入关闭：订单尚未被扫码时智能体回传的是
// 支付宝 TRADE_NOT_EXIST（报文含 Business Failed / 40004 / ACQ.TRADE_NOT_EXIST），
// 用裸 failed 匹配会导致订单被误判关闭而提前过期。
func tboxStatusFromAnswer(answer string) string {
	text := strings.TrimSpace(answer)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)

	paidPhrases := []string{
		"交易成功", "支付成功", "已支付", "支付完成", "付款成功",
		"trade_success", "trade_finished",
	}
	for _, phrase := range paidPhrases {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			return tboxStatusPaid
		}
	}

	closedPhrases := []string{
		"交易关闭", "订单已关闭", "已关闭", "已取消", "交易失败", "支付失败",
		"trade_closed",
	}
	for _, phrase := range closedPhrases {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			return tboxStatusClosed
		}
	}
	return ""
}

func sleepAlipayPoll(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// formatAlipayPollAmount 输出不带多余小数位的金额（12.00 → "12"，12.50 → "12.5"）。
func formatAlipayPollAmount(amount float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", amount), "0"), ".")
}

func psTruncateReason(reason string) string {
	reason = strings.TrimSpace(reason)
	const maxReasonLen = 500
	if len(reason) <= maxReasonLen {
		return reason
	}
	return reason[:maxReasonLen]
}
