package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentproviderinstance"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SettingPaymentEnabled      = "payment_enabled"
	SettingMinRechargeAmount   = "MIN_RECHARGE_AMOUNT"
	SettingMaxRechargeAmount   = "MAX_RECHARGE_AMOUNT"
	SettingDailyRechargeLimit  = "DAILY_RECHARGE_LIMIT"
	SettingOrderTimeoutMinutes = "ORDER_TIMEOUT_MINUTES"
	SettingMaxPendingOrders    = "MAX_PENDING_ORDERS"
	SettingEnabledPaymentTypes = "ENABLED_PAYMENT_TYPES"
	SettingLoadBalanceStrategy = "LOAD_BALANCE_STRATEGY"
	SettingBalancePayDisabled  = "BALANCE_PAYMENT_DISABLED"
	SettingBalanceRechargeMult = "BALANCE_RECHARGE_MULTIPLIER"
	// SettingSubscriptionUSDToCNYRate 是订阅 CNY 换算汇率（1 USD = X CNY）。
	// 0/未配置 = 关闭换算（订阅按 price 数值直付），显式配置后 CNY 通道订阅按 price × rate 收款。
	SettingSubscriptionUSDToCNYRate      = "SUBSCRIPTION_USD_TO_CNY_RATE"
	SettingRechargeFeeRate               = "RECHARGE_FEE_RATE"
	SettingProductNamePrefix             = "PRODUCT_NAME_PREFIX"
	SettingProductNameSuffix             = "PRODUCT_NAME_SUFFIX"
	SettingHelpImageURL                  = "PAYMENT_HELP_IMAGE_URL"
	SettingHelpText                      = "PAYMENT_HELP_TEXT"
	SettingCancelRateLimitOn             = "CANCEL_RATE_LIMIT_ENABLED"
	SettingCancelRateLimitMax            = "CANCEL_RATE_LIMIT_MAX"
	SettingCancelWindowSize              = "CANCEL_RATE_LIMIT_WINDOW"
	SettingCancelWindowUnit              = "CANCEL_RATE_LIMIT_UNIT"
	SettingCancelWindowMode              = "CANCEL_RATE_LIMIT_WINDOW_MODE"
	SettingAlipayForceQRCode             = "ALIPAY_FORCE_QRCODE"
	SettingAlipayMobilePrecreateDeepLink = "ALIPAY_MOBILE_PRECREATE_DEEP_LINK"

	// 支付宝轮询充值（agent 生成二维码 + 轮询查单）。
	// ApiKey / AgentID 是只写凭证：用户侧接口绝不返回，仅服务端通过
	// AlipayPollCredentials 读取，只有管理员设置接口会回显。
	SettingAlipayPollEnabled    = "ALIPAY_POLL_ENABLED"
	SettingAlipayPollTiers      = "ALIPAY_POLL_TIERS"
	SettingAlipayPollAPIKey     = "ALIPAY_POLL_API_KEY"
	SettingAlipayPollAgentID    = "ALIPAY_POLL_AGENT_ID"
	SettingAlipayPollStartDelay = "ALIPAY_POLL_START_DELAY_SEC"
	SettingAlipayPollInterval   = "ALIPAY_POLL_INTERVAL_SEC"
	SettingAlipayPollTimeout    = "ALIPAY_POLL_TIMEOUT_SEC"
	// 智能体端点与提示词参数，可在管理端手动维护。
	// 同样是服务端集成细节：不进用户侧接口（见 PaymentConfig 的 json:"-" 字段）。
	SettingAlipayPollBaseURL  = "ALIPAY_POLL_BASE_URL"
	SettingAlipayPollChatPath = "ALIPAY_POLL_CHAT_PATH"
	SettingAlipayPollUserID   = "ALIPAY_POLL_USER_ID"
	SettingAlipayPollNote     = "ALIPAY_POLL_NOTE"
)

// Default values for the Alipay poll recharge settings.
// 端点相关的默认值是 Tbox（蚂蚁百宝箱）的公开接口，留空时回落到这里。
const (
	defaultAlipayPollStartDelaySec = 30
	defaultAlipayPollIntervalSec   = 5
	defaultAlipayPollTimeoutSec    = 600
	defaultAlipayPollBaseURL       = "https://o.tbox.cn"
	defaultAlipayPollChatPath      = "/openapi/v1/chat/create"
	// defaultAlipayPollUserID 是调用智能体时固定的用户标识。
	defaultAlipayPollUserID = "sub2api_alipay_qr_client"
	// defaultAlipayPollNote 是生成二维码时写入支付宝订单的固定备注。
	defaultAlipayPollNote = "FaithAPI充值"
)

// AlipayPollTier is one selectable Alipay poll recharge tier.
type AlipayPollTier struct {
	Price  float64 `json:"price"`  // 实付金额（¥，作为支付宝收款金额传给智能体）
	Credit float64 `json:"credit"` // 到账额度（支付成功后充值到余额）
}

// AlipayPollCredentials holds the write-only agent credentials used to
// generate Alipay QR codes and query order status.
type AlipayPollCredentials struct {
	APIKey  string
	AgentID string
}

// Default values for payment configuration settings.
const (
	defaultOrderTimeoutMin  = 30
	defaultMaxPendingOrders = 3
)

// PaymentConfig holds the payment system configuration.
type PaymentConfig struct {
	Enabled                   bool     `json:"enabled"`
	MinAmount                 float64  `json:"min_amount"`
	MaxAmount                 float64  `json:"max_amount"`
	DailyLimit                float64  `json:"daily_limit"`
	OrderTimeoutMin           int      `json:"order_timeout_minutes"`
	MaxPendingOrders          int      `json:"max_pending_orders"`
	EnabledTypes              []string `json:"enabled_payment_types"`
	BalanceDisabled           bool     `json:"balance_disabled"`
	BalanceRechargeMultiplier float64  `json:"balance_recharge_multiplier"`
	// SubscriptionUSDToCNYRate 为 0 时订阅换算关闭（兼容存量行为）。
	SubscriptionUSDToCNYRate float64 `json:"subscription_usd_to_cny_rate"`
	RechargeFeeRate          float64 `json:"recharge_fee_rate"`
	LoadBalanceStrategy      string  `json:"load_balance_strategy"`
	ProductNamePrefix        string  `json:"product_name_prefix"`
	ProductNameSuffix        string  `json:"product_name_suffix"`
	HelpImageURL             string  `json:"help_image_url"`
	HelpText                 string  `json:"help_text"`
	StripePublishableKey     string  `json:"stripe_publishable_key,omitempty"`

	// Cancel rate limit settings
	CancelRateLimitEnabled bool   `json:"cancel_rate_limit_enabled"`
	CancelRateLimitMax     int    `json:"cancel_rate_limit_max"`
	CancelRateLimitWindow  int    `json:"cancel_rate_limit_window"`
	CancelRateLimitUnit    string `json:"cancel_rate_limit_unit"`
	CancelRateLimitMode    string `json:"cancel_rate_limit_window_mode"`

	// Force Alipay mobile users to use QR code instead of mobile redirect
	AlipayForceQRCode bool `json:"alipay_force_qrcode"`
	// Use Alipay face-to-face precreate and an app deep link on mobile clients.
	AlipayMobilePrecreateDeepLink bool `json:"alipay_mobile_precreate_deep_link"`

	// 支付宝轮询充值。注意：ApiKey / AgentID 不属于本结构体（也不下发），
	// 仅通过 AlipayPollCredentials 在服务端读取。
	AlipayPollEnabled       bool             `json:"alipay_poll_enabled"`
	AlipayPollTiers         []AlipayPollTier `json:"alipay_poll_tiers"`
	AlipayPollStartDelaySec int              `json:"alipay_poll_start_delay_sec"`
	AlipayPollIntervalSec   int              `json:"alipay_poll_interval_sec"`
	AlipayPollTimeoutSec    int              `json:"alipay_poll_timeout_sec"`

	// 智能体端点与提示词参数（管理端可维护）。
	// 用 json:"-" 显式排除：本结构体会被 GET /payment/config 原样序列化给用户，
	// 这些属于服务端集成细节，不应下发（管理端设置接口单独回显）。
	AlipayPollBaseURL  string `json:"-"`
	AlipayPollChatPath string `json:"-"`
	AlipayPollUserID   string `json:"-"`
	AlipayPollNote     string `json:"-"`
}

// AlipayPollEndpoint 是调用智能体所需的端点与提示词参数。
// 所有字段都可在管理端手动维护，留空时由 parsePaymentConfig 回落到默认值。
type AlipayPollEndpoint struct {
	BaseURL  string
	ChatPath string
	UserID   string
	Note     string
}

// Endpoint 返回当前配置对应的智能体端点参数。
func (c *PaymentConfig) Endpoint() AlipayPollEndpoint {
	if c == nil {
		return AlipayPollEndpoint{
			BaseURL:  defaultAlipayPollBaseURL,
			ChatPath: defaultAlipayPollChatPath,
			UserID:   defaultAlipayPollUserID,
			Note:     defaultAlipayPollNote,
		}
	}
	return AlipayPollEndpoint{
		BaseURL:  c.AlipayPollBaseURL,
		ChatPath: c.AlipayPollChatPath,
		UserID:   c.AlipayPollUserID,
		Note:     c.AlipayPollNote,
	}
}

// URL 返回拼接好的智能体接口地址（容忍两端多余或缺失的斜杠）。
func (e AlipayPollEndpoint) URL() string {
	base := strings.TrimRight(strings.TrimSpace(e.BaseURL), "/")
	path := strings.TrimSpace(e.ChatPath)
	if path == "" {
		path = defaultAlipayPollChatPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// UpdatePaymentConfigRequest contains fields to update payment configuration.
type UpdatePaymentConfigRequest struct {
	Enabled                   *bool    `json:"enabled"`
	MinAmount                 *float64 `json:"min_amount"`
	MaxAmount                 *float64 `json:"max_amount"`
	DailyLimit                *float64 `json:"daily_limit"`
	OrderTimeoutMin           *int     `json:"order_timeout_minutes"`
	MaxPendingOrders          *int     `json:"max_pending_orders"`
	EnabledTypes              []string `json:"enabled_payment_types"`
	BalanceDisabled           *bool    `json:"balance_disabled"`
	BalanceRechargeMultiplier *float64 `json:"balance_recharge_multiplier"`
	SubscriptionUSDToCNYRate  *float64 `json:"subscription_usd_to_cny_rate"`
	RechargeFeeRate           *float64 `json:"recharge_fee_rate"`
	LoadBalanceStrategy       *string  `json:"load_balance_strategy"`
	ProductNamePrefix         *string  `json:"product_name_prefix"`
	ProductNameSuffix         *string  `json:"product_name_suffix"`
	HelpImageURL              *string  `json:"help_image_url"`
	HelpText                  *string  `json:"help_text"`

	// Cancel rate limit settings
	CancelRateLimitEnabled *bool   `json:"cancel_rate_limit_enabled"`
	CancelRateLimitMax     *int    `json:"cancel_rate_limit_max"`
	CancelRateLimitWindow  *int    `json:"cancel_rate_limit_window"`
	CancelRateLimitUnit    *string `json:"cancel_rate_limit_unit"`
	CancelRateLimitMode    *string `json:"cancel_rate_limit_window_mode"`

	// Force Alipay mobile users to use QR code instead of mobile redirect
	AlipayForceQRCode *bool `json:"alipay_force_qrcode"`
	// Use Alipay face-to-face precreate and an app deep link on mobile clients.
	AlipayMobilePrecreateDeepLink *bool `json:"alipay_mobile_precreate_deep_link"`

	VisibleMethodAlipaySource  *string `json:"payment_visible_method_alipay_source"`
	VisibleMethodWxpaySource   *string `json:"payment_visible_method_wxpay_source"`
	VisibleMethodAlipayEnabled *bool   `json:"payment_visible_method_alipay_enabled"`
	VisibleMethodWxpayEnabled  *bool   `json:"payment_visible_method_wxpay_enabled"`

	// 支付宝轮询充值
	AlipayPollEnabled       *bool             `json:"payment_alipay_poll_enabled"`
	AlipayPollTiers         *[]AlipayPollTier `json:"payment_alipay_poll_tiers"`
	AlipayPollAPIKey        *string           `json:"payment_alipay_poll_api_key"`
	AlipayPollAgentID       *string           `json:"payment_alipay_poll_agent_id"`
	AlipayPollStartDelaySec *int              `json:"payment_alipay_poll_start_delay_sec"`
	AlipayPollIntervalSec   *int              `json:"payment_alipay_poll_interval_sec"`
	AlipayPollTimeoutSec    *int              `json:"payment_alipay_poll_timeout_sec"`
	// 智能体端点与提示词参数（留空即回落到默认值）
	AlipayPollBaseURL  *string `json:"payment_alipay_poll_base_url"`
	AlipayPollChatPath *string `json:"payment_alipay_poll_chat_path"`
	AlipayPollUserID   *string `json:"payment_alipay_poll_user_id"`
	AlipayPollNote     *string `json:"payment_alipay_poll_note"`
}

// MethodLimits holds per-payment-type limits.
type MethodLimits struct {
	PaymentType string  `json:"payment_type"`
	DisplayName string  `json:"display_name,omitempty"`
	Currency    string  `json:"currency"`
	FeeRate     float64 `json:"fee_rate"`
	DailyLimit  float64 `json:"daily_limit"`
	SingleMin   float64 `json:"single_min"`
	SingleMax   float64 `json:"single_max"`
}

// MethodLimitsResponse is the full response for the user-facing /limits API.
// It includes per-method limits and the global widest range (union of all methods).
type MethodLimitsResponse struct {
	Methods   map[string]MethodLimits `json:"methods"`
	GlobalMin float64                 `json:"global_min"` // 0 = no minimum
	GlobalMax float64                 `json:"global_max"` // 0 = no maximum
}

type CreateProviderInstanceRequest struct {
	ProviderKey     string            `json:"provider_key"`
	Name            string            `json:"name"`
	Config          map[string]string `json:"config"`
	SupportedTypes  []string          `json:"supported_types"`
	Enabled         bool              `json:"enabled"`
	PaymentMode     string            `json:"payment_mode"`
	SortOrder       int               `json:"sort_order"`
	Limits          string            `json:"limits"`
	RefundEnabled   bool              `json:"refund_enabled"`
	AllowUserRefund bool              `json:"allow_user_refund"`
}

type UpdateProviderInstanceRequest struct {
	Name            *string           `json:"name"`
	Config          map[string]string `json:"config"`
	SupportedTypes  []string          `json:"supported_types"`
	Enabled         *bool             `json:"enabled"`
	PaymentMode     *string           `json:"payment_mode"`
	SortOrder       *int              `json:"sort_order"`
	Limits          *string           `json:"limits"`
	RefundEnabled   *bool             `json:"refund_enabled"`
	AllowUserRefund *bool             `json:"allow_user_refund"`
}
type CreatePlanRequest struct {
	GroupID       int64    `json:"group_id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Price         float64  `json:"price"`
	OriginalPrice *float64 `json:"original_price"`
	Currency      string   `json:"currency"`
	ValidityDays  int      `json:"validity_days"`
	ValidityUnit  string   `json:"validity_unit"`
	Features      string   `json:"features"`
	ProductName   string   `json:"product_name"`
	ForSale       bool     `json:"for_sale"`
	SortOrder     int      `json:"sort_order"`
}

type UpdatePlanRequest struct {
	GroupID       *int64   `json:"group_id"`
	Name          *string  `json:"name"`
	Description   *string  `json:"description"`
	Price         *float64 `json:"price"`
	OriginalPrice *float64 `json:"original_price"`
	Currency      *string  `json:"currency"`
	ValidityDays  *int     `json:"validity_days"`
	ValidityUnit  *string  `json:"validity_unit"`
	Features      *string  `json:"features"`
	ProductName   *string  `json:"product_name"`
	ForSale       *bool    `json:"for_sale"`
	SortOrder     *int     `json:"sort_order"`
}

// PaymentConfigService manages payment configuration and CRUD for
// provider instances, channels, and subscription plans.
type PaymentConfigService struct {
	entClient     *dbent.Client
	settingRepo   SettingRepository
	encryptionKey []byte
}

// NewPaymentConfigService creates a new PaymentConfigService.
func NewPaymentConfigService(entClient *dbent.Client, settingRepo SettingRepository, encryptionKey []byte) *PaymentConfigService {
	return &PaymentConfigService{entClient: entClient, settingRepo: settingRepo, encryptionKey: encryptionKey}
}

// IsPaymentEnabled returns whether the payment system is enabled.
func (s *PaymentConfigService) IsPaymentEnabled(ctx context.Context) bool {
	val, err := s.settingRepo.GetValue(ctx, SettingPaymentEnabled)
	if err != nil {
		return false
	}
	return val == "true"
}

// GetPaymentConfig returns the full payment configuration.
func (s *PaymentConfigService) GetPaymentConfig(ctx context.Context) (*PaymentConfig, error) {
	keys := []string{
		SettingPaymentEnabled, SettingMinRechargeAmount, SettingMaxRechargeAmount,
		SettingDailyRechargeLimit, SettingOrderTimeoutMinutes, SettingMaxPendingOrders,
		SettingEnabledPaymentTypes, SettingBalancePayDisabled, SettingBalanceRechargeMult, SettingSubscriptionUSDToCNYRate, SettingRechargeFeeRate, SettingLoadBalanceStrategy,
		SettingProductNamePrefix, SettingProductNameSuffix,
		SettingHelpImageURL, SettingHelpText,
		SettingCancelRateLimitOn, SettingCancelRateLimitMax,
		SettingCancelWindowSize, SettingCancelWindowUnit, SettingCancelWindowMode,
		SettingAlipayForceQRCode, SettingAlipayMobilePrecreateDeepLink,
		SettingPaymentVisibleMethodAlipayEnabled, SettingPaymentVisibleMethodAlipaySource,
		SettingPaymentVisibleMethodWxpayEnabled, SettingPaymentVisibleMethodWxpaySource,
		SettingAlipayPollEnabled, SettingAlipayPollTiers,
		SettingAlipayPollStartDelay, SettingAlipayPollInterval, SettingAlipayPollTimeout,
		SettingAlipayPollBaseURL, SettingAlipayPollChatPath, SettingAlipayPollUserID, SettingAlipayPollNote,
	}
	vals, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get payment config settings: %w", err)
	}
	cfg := s.parsePaymentConfig(vals)
	// Load Stripe publishable key from the first enabled Stripe provider instance
	cfg.StripePublishableKey = s.getStripePublishableKey(ctx)
	return cfg, nil
}

func (s *PaymentConfigService) parsePaymentConfig(vals map[string]string) *PaymentConfig {
	cfg := &PaymentConfig{
		Enabled:                   vals[SettingPaymentEnabled] == "true",
		MinAmount:                 pcParseFloat(vals[SettingMinRechargeAmount], 1),
		MaxAmount:                 pcParseFloat(vals[SettingMaxRechargeAmount], 0),
		DailyLimit:                pcParseFloat(vals[SettingDailyRechargeLimit], 0),
		OrderTimeoutMin:           pcParseInt(vals[SettingOrderTimeoutMinutes], defaultOrderTimeoutMin),
		MaxPendingOrders:          pcParseInt(vals[SettingMaxPendingOrders], defaultMaxPendingOrders),
		BalanceDisabled:           vals[SettingBalancePayDisabled] == "true",
		BalanceRechargeMultiplier: normalizeBalanceRechargeMultiplier(pcParseFloat(vals[SettingBalanceRechargeMult], defaultBalanceRechargeMultiplier)),
		SubscriptionUSDToCNYRate:  normalizeSubscriptionUSDToCNYRate(pcParseFloat(vals[SettingSubscriptionUSDToCNYRate], 0)),
		RechargeFeeRate:           pcParseFloat(vals[SettingRechargeFeeRate], 0),
		LoadBalanceStrategy:       vals[SettingLoadBalanceStrategy],
		ProductNamePrefix:         vals[SettingProductNamePrefix],
		ProductNameSuffix:         vals[SettingProductNameSuffix],
		HelpImageURL:              vals[SettingHelpImageURL],
		HelpText:                  vals[SettingHelpText],

		CancelRateLimitEnabled: vals[SettingCancelRateLimitOn] == "true",
		CancelRateLimitMax:     pcParseInt(vals[SettingCancelRateLimitMax], 10),
		CancelRateLimitWindow:  pcParseInt(vals[SettingCancelWindowSize], 1),
		CancelRateLimitUnit:    vals[SettingCancelWindowUnit],
		CancelRateLimitMode:    vals[SettingCancelWindowMode],

		AlipayForceQRCode:             vals[SettingAlipayForceQRCode] == "true",
		AlipayMobilePrecreateDeepLink: vals[SettingAlipayMobilePrecreateDeepLink] == "true",

		AlipayPollEnabled:       vals[SettingAlipayPollEnabled] == "true",
		AlipayPollTiers:         parseAlipayPollTiers(vals[SettingAlipayPollTiers]),
		AlipayPollStartDelaySec: pcParseInt(vals[SettingAlipayPollStartDelay], defaultAlipayPollStartDelaySec),
		AlipayPollIntervalSec:   pcParseInt(vals[SettingAlipayPollInterval], defaultAlipayPollIntervalSec),
		AlipayPollTimeoutSec:    pcParseInt(vals[SettingAlipayPollTimeout], defaultAlipayPollTimeoutSec),

		AlipayPollBaseURL:  pcParseStrOrDefault(vals[SettingAlipayPollBaseURL], defaultAlipayPollBaseURL),
		AlipayPollChatPath: pcParseStrOrDefault(vals[SettingAlipayPollChatPath], defaultAlipayPollChatPath),
		AlipayPollUserID:   pcParseStrOrDefault(vals[SettingAlipayPollUserID], defaultAlipayPollUserID),
		AlipayPollNote:     pcParseStrOrDefault(vals[SettingAlipayPollNote], defaultAlipayPollNote),
	}
	if cfg.AlipayPollStartDelaySec <= 0 {
		cfg.AlipayPollStartDelaySec = defaultAlipayPollStartDelaySec
	}
	if cfg.AlipayPollIntervalSec <= 0 {
		cfg.AlipayPollIntervalSec = defaultAlipayPollIntervalSec
	}
	if cfg.AlipayPollTimeoutSec <= 0 {
		cfg.AlipayPollTimeoutSec = defaultAlipayPollTimeoutSec
	}
	cfg.AlipayMobilePrecreateDeepLink = pcEnvBoolOverride(
		SettingAlipayMobilePrecreateDeepLink,
		cfg.AlipayMobilePrecreateDeepLink,
	)
	if cfg.LoadBalanceStrategy == "" {
		cfg.LoadBalanceStrategy = payment.DefaultLoadBalanceStrategy
	}
	if raw := vals[SettingEnabledPaymentTypes]; raw != "" {
		types := make([]string, 0, len(strings.Split(raw, ",")))
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				types = append(types, t)
			}
		}
		cfg.EnabledTypes = NormalizeVisibleMethods(types)
	}
	return cfg
}

// parseAlipayPollTiers 解析档位 JSON 数组；非法内容一律视为空档位列表。
func parseAlipayPollTiers(raw string) []AlipayPollTier {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var tiers []AlipayPollTier
	if err := json.Unmarshal([]byte(raw), &tiers); err != nil {
		return nil
	}
	out := make([]AlipayPollTier, 0, len(tiers))
	for _, t := range tiers {
		if t.Price <= 0 || t.Credit <= 0 {
			continue
		}
		out = append(out, t)
	}
	return out
}

// AlipayPollCredentials returns the write-only agent credentials required to
// create Alipay QR codes and query order status. It is never exposed to users.
func (s *PaymentConfigService) AlipayPollCredentials(ctx context.Context) (*AlipayPollCredentials, error) {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingAlipayPollAPIKey, SettingAlipayPollAgentID})
	if err != nil {
		return nil, fmt.Errorf("get alipay poll credentials: %w", err)
	}
	creds := &AlipayPollCredentials{
		APIKey:  strings.TrimSpace(vals[SettingAlipayPollAPIKey]),
		AgentID: strings.TrimSpace(vals[SettingAlipayPollAgentID]),
	}
	if creds.APIKey == "" || creds.AgentID == "" {
		return nil, infraerrors.ServiceUnavailable("ALIPAY_POLL_NOT_CONFIGURED", "alipay poll recharge is not configured")
	}
	return creds, nil
}

// AlipayPollConfigured reports whether both credential fields are present,
// without failing when they are missing.
func (s *PaymentConfigService) AlipayPollConfigured(ctx context.Context) bool {
	_, err := s.AlipayPollCredentials(ctx)
	return err == nil
}

// AlipayPollCredentialValues returns the stored credentials verbatim (empty
// strings when unset). Only the admin settings endpoint may use this — it exists
// so admins can edit the write-only ApiKey / AgentID fields.
func (s *PaymentConfigService) AlipayPollCredentialValues(ctx context.Context) AlipayPollCredentials {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingAlipayPollAPIKey, SettingAlipayPollAgentID})
	if err != nil {
		return AlipayPollCredentials{}
	}
	return AlipayPollCredentials{
		APIKey:  strings.TrimSpace(vals[SettingAlipayPollAPIKey]),
		AgentID: strings.TrimSpace(vals[SettingAlipayPollAgentID]),
	}
}

func pcEnvBoolOverride(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

// getStripePublishableKey finds the publishable key from the first enabled Stripe provider instance.
func (s *PaymentConfigService) getStripePublishableKey(ctx context.Context) string {
	if s.entClient == nil {
		return ""
	}
	instances, err := s.entClient.PaymentProviderInstance.Query().
		Where(
			paymentproviderinstance.EnabledEQ(true),
			paymentproviderinstance.ProviderKeyEQ(payment.TypeStripe),
		).Limit(1).All(ctx)
	if err != nil || len(instances) == 0 {
		return ""
	}
	cfg, err := s.decryptConfig(instances[0].Config)
	if err != nil || cfg == nil {
		return ""
	}
	return cfg[payment.ConfigKeyPublishableKey]
}

// UpdatePaymentConfig updates the payment configuration settings.
// NOTE: This function exceeds 30 lines because each field requires an independent
// nil-check before serialisation — this is inherent to patch-style update patterns
// and cannot be meaningfully decomposed without introducing unnecessary abstraction.
func (s *PaymentConfigService) UpdatePaymentConfig(ctx context.Context, req UpdatePaymentConfigRequest) error {
	if req.BalanceRechargeMultiplier != nil {
		if math.IsNaN(*req.BalanceRechargeMultiplier) || math.IsInf(*req.BalanceRechargeMultiplier, 0) || *req.BalanceRechargeMultiplier <= 0 {
			return infraerrors.BadRequest("INVALID_BALANCE_RECHARGE_MULTIPLIER", "balance recharge multiplier must be greater than 0")
		}
	}
	if req.SubscriptionUSDToCNYRate != nil {
		v := *req.SubscriptionUSDToCNYRate
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return infraerrors.BadRequest("INVALID_SUBSCRIPTION_USD_TO_CNY_RATE", "subscription USD to CNY rate must be 0 (disabled) or a positive number")
		}
	}
	if req.RechargeFeeRate != nil {
		v := *req.RechargeFeeRate
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return infraerrors.BadRequest("INVALID_RECHARGE_FEE_RATE", "recharge fee rate must be between 0 and 100")
		}
		// Enforce max 2 decimal places
		if math.Round(v*100) != v*100 {
			return infraerrors.BadRequest("INVALID_RECHARGE_FEE_RATE", "recharge fee rate allows at most 2 decimal places")
		}
	}
	m := make(map[string]string)
	if req.Enabled != nil {
		m[SettingPaymentEnabled] = formatBoolOrEmpty(req.Enabled)
	}
	if req.MinAmount != nil {
		m[SettingMinRechargeAmount] = formatPositiveFloat(req.MinAmount)
	}
	if req.MaxAmount != nil {
		m[SettingMaxRechargeAmount] = formatPositiveFloat(req.MaxAmount)
	}
	if req.DailyLimit != nil {
		m[SettingDailyRechargeLimit] = formatPositiveFloat(req.DailyLimit)
	}
	if req.OrderTimeoutMin != nil {
		m[SettingOrderTimeoutMinutes] = formatPositiveInt(req.OrderTimeoutMin)
	}
	if req.MaxPendingOrders != nil {
		m[SettingMaxPendingOrders] = formatPositiveInt(req.MaxPendingOrders)
	}
	if req.EnabledTypes != nil {
		m[SettingEnabledPaymentTypes] = strings.Join(req.EnabledTypes, ",")
	}
	if req.BalanceDisabled != nil {
		m[SettingBalancePayDisabled] = formatBoolOrEmpty(req.BalanceDisabled)
	}
	if req.BalanceRechargeMultiplier != nil {
		m[SettingBalanceRechargeMult] = formatPositiveFloat(req.BalanceRechargeMultiplier)
	}
	if req.SubscriptionUSDToCNYRate != nil {
		m[SettingSubscriptionUSDToCNYRate] = formatPositiveFloatExact(req.SubscriptionUSDToCNYRate)
	}
	if req.RechargeFeeRate != nil {
		m[SettingRechargeFeeRate] = formatNonNegativeFloat(req.RechargeFeeRate)
	}
	if req.LoadBalanceStrategy != nil {
		m[SettingLoadBalanceStrategy] = derefStr(req.LoadBalanceStrategy)
	}
	if req.ProductNamePrefix != nil {
		m[SettingProductNamePrefix] = derefStr(req.ProductNamePrefix)
	}
	if req.ProductNameSuffix != nil {
		m[SettingProductNameSuffix] = derefStr(req.ProductNameSuffix)
	}
	if req.HelpImageURL != nil {
		m[SettingHelpImageURL] = derefStr(req.HelpImageURL)
	}
	if req.HelpText != nil {
		m[SettingHelpText] = derefStr(req.HelpText)
	}
	if req.CancelRateLimitEnabled != nil {
		m[SettingCancelRateLimitOn] = formatBoolOrEmpty(req.CancelRateLimitEnabled)
	}
	if req.CancelRateLimitMax != nil {
		m[SettingCancelRateLimitMax] = formatPositiveInt(req.CancelRateLimitMax)
	}
	if req.CancelRateLimitWindow != nil {
		m[SettingCancelWindowSize] = formatPositiveInt(req.CancelRateLimitWindow)
	}
	if req.CancelRateLimitUnit != nil {
		m[SettingCancelWindowUnit] = derefStr(req.CancelRateLimitUnit)
	}
	if req.CancelRateLimitMode != nil {
		m[SettingCancelWindowMode] = derefStr(req.CancelRateLimitMode)
	}
	if req.AlipayForceQRCode != nil {
		m[SettingAlipayForceQRCode] = formatBoolOrEmpty(req.AlipayForceQRCode)
	}
	if req.AlipayMobilePrecreateDeepLink != nil {
		m[SettingAlipayMobilePrecreateDeepLink] = formatBoolOrEmpty(req.AlipayMobilePrecreateDeepLink)
	}
	if req.AlipayPollEnabled != nil {
		m[SettingAlipayPollEnabled] = formatBoolOrEmpty(req.AlipayPollEnabled)
	}
	if req.AlipayPollTiers != nil {
		encoded, err := formatAlipayPollTiers(*req.AlipayPollTiers)
		if err != nil {
			return err
		}
		m[SettingAlipayPollTiers] = encoded
	}
	if req.AlipayPollAPIKey != nil {
		m[SettingAlipayPollAPIKey] = strings.TrimSpace(*req.AlipayPollAPIKey)
	}
	if req.AlipayPollAgentID != nil {
		m[SettingAlipayPollAgentID] = strings.TrimSpace(*req.AlipayPollAgentID)
	}
	if req.AlipayPollStartDelaySec != nil {
		m[SettingAlipayPollStartDelay] = formatPositiveInt(req.AlipayPollStartDelaySec)
	}
	if req.AlipayPollIntervalSec != nil {
		m[SettingAlipayPollInterval] = formatPositiveInt(req.AlipayPollIntervalSec)
	}
	if req.AlipayPollTimeoutSec != nil {
		m[SettingAlipayPollTimeout] = formatPositiveInt(req.AlipayPollTimeoutSec)
	}
	// 端点参数留空即落回默认值，避免存成空串导致调用地址拼不出来。
	if req.AlipayPollBaseURL != nil {
		m[SettingAlipayPollBaseURL] = strings.TrimSpace(*req.AlipayPollBaseURL)
	}
	if req.AlipayPollChatPath != nil {
		m[SettingAlipayPollChatPath] = strings.TrimSpace(*req.AlipayPollChatPath)
	}
	if req.AlipayPollUserID != nil {
		m[SettingAlipayPollUserID] = strings.TrimSpace(*req.AlipayPollUserID)
	}
	if req.AlipayPollNote != nil {
		m[SettingAlipayPollNote] = strings.TrimSpace(*req.AlipayPollNote)
	}
	if req.VisibleMethodAlipaySource != nil {
		m[SettingPaymentVisibleMethodAlipaySource] = derefStr(req.VisibleMethodAlipaySource)
	}
	if req.VisibleMethodWxpaySource != nil {
		m[SettingPaymentVisibleMethodWxpaySource] = derefStr(req.VisibleMethodWxpaySource)
	}
	if req.VisibleMethodAlipayEnabled != nil {
		m[SettingPaymentVisibleMethodAlipayEnabled] = formatBoolOrEmpty(req.VisibleMethodAlipayEnabled)
	}
	if req.VisibleMethodWxpayEnabled != nil {
		m[SettingPaymentVisibleMethodWxpayEnabled] = formatBoolOrEmpty(req.VisibleMethodWxpayEnabled)
	}
	return s.settingRepo.SetMultiple(ctx, m)
}

// formatAlipayPollTiers 校验并序列化档位列表。每个档位必须实付金额、到账额度均为正数。
func formatAlipayPollTiers(tiers []AlipayPollTier) (string, error) {
	if len(tiers) == 0 {
		return "[]", nil
	}
	for _, t := range tiers {
		if math.IsNaN(t.Price) || math.IsInf(t.Price, 0) || t.Price <= 0 {
			return "", infraerrors.BadRequest("INVALID_ALIPAY_POLL_TIER", "tier price must be a positive number")
		}
		if math.IsNaN(t.Credit) || math.IsInf(t.Credit, 0) || t.Credit <= 0 {
			return "", infraerrors.BadRequest("INVALID_ALIPAY_POLL_TIER", "tier credit must be a positive number")
		}
	}
	encoded, err := json.Marshal(tiers)
	if err != nil {
		return "", infraerrors.BadRequest("INVALID_ALIPAY_POLL_TIER", "alipay poll tiers are not serialisable")
	}
	return string(encoded), nil
}

func formatBoolOrEmpty(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}

func formatPositiveFloat(v *float64) string {
	if v == nil || *v <= 0 {
		return "" // empty → parsePaymentConfig uses default
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

// formatPositiveFloatExact 保留完整精度，用于汇率等对小数位敏感的配置。
func formatPositiveFloatExact(v *float64) string {
	if v == nil || *v <= 0 {
		return "" // empty → parsePaymentConfig 视为未配置（换算关闭）
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func formatNonNegativeFloat(v *float64) string {
	if v == nil || *v < 0 {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

func formatPositiveInt(v *int) string {
	if v == nil || *v <= 0 {
		return ""
	}
	return strconv.Itoa(*v)
}

func derefStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func splitTypes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func joinTypes(types []string) string {
	return strings.Join(types, ",")
}

func pcParseFloat(s string, defaultVal float64) float64 {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func pcParseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// pcParseStrOrDefault 返回去空白后的值，为空时回落到默认值。
func pcParseStrOrDefault(s, defaultVal string) string {
	if v := strings.TrimSpace(s); v != "" {
		return v
	}
	return defaultVal
}

func buildVisibleMethodSourceAvailability(instances []*dbent.PaymentProviderInstance) map[string]bool {
	available := make(map[string]bool, 4)
	for _, inst := range instances {
		switch inst.ProviderKey {
		case payment.TypeAlipay:
			if inst.SupportedTypes == "" || payment.InstanceSupportsType(inst.SupportedTypes, payment.TypeAlipay) || payment.InstanceSupportsType(inst.SupportedTypes, payment.TypeAlipayDirect) {
				available[VisibleMethodSourceOfficialAlipay] = true
			}
		case payment.TypeWxpay:
			if inst.SupportedTypes == "" || payment.InstanceSupportsType(inst.SupportedTypes, payment.TypeWxpay) || payment.InstanceSupportsType(inst.SupportedTypes, payment.TypeWxpayDirect) {
				available[VisibleMethodSourceOfficialWechat] = true
			}
		case payment.TypeEasyPay:
			for _, supportedType := range splitTypes(inst.SupportedTypes) {
				switch NormalizeVisibleMethod(supportedType) {
				case payment.TypeAlipay:
					available[VisibleMethodSourceEasyPayAlipay] = true
				case payment.TypeWxpay:
					available[VisibleMethodSourceEasyPayWechat] = true
				}
			}
		}
	}
	return available
}

func applyVisibleMethodRoutingToEnabledTypes(base []string, vals map[string]string, available map[string]bool) []string {
	shouldExpose := map[string]bool{
		payment.TypeAlipay: visibleMethodShouldBeExposed(payment.TypeAlipay, vals, available),
		payment.TypeWxpay:  visibleMethodShouldBeExposed(payment.TypeWxpay, vals, available),
	}

	seen := make(map[string]struct{}, len(base)+2)
	out := make([]string, 0, len(base)+2)
	appendType := func(paymentType string) {
		paymentType = NormalizeVisibleMethod(paymentType)
		if paymentType == "" {
			return
		}
		if _, ok := seen[paymentType]; ok {
			return
		}
		seen[paymentType] = struct{}{}
		out = append(out, paymentType)
	}

	for _, paymentType := range base {
		visibleMethod := NormalizeVisibleMethod(paymentType)
		switch visibleMethod {
		case payment.TypeAlipay, payment.TypeWxpay:
			if shouldExpose[visibleMethod] {
				appendType(visibleMethod)
			}
		default:
			appendType(visibleMethod)
		}
	}

	for _, visibleMethod := range []string{payment.TypeAlipay, payment.TypeWxpay} {
		if shouldExpose[visibleMethod] {
			appendType(visibleMethod)
		}
	}
	return out
}

func visibleMethodShouldBeExposed(method string, vals map[string]string, available map[string]bool) bool {
	enabledKey := visibleMethodEnabledSettingKey(method)
	sourceKey := visibleMethodSourceSettingKey(method)
	if enabledKey == "" || sourceKey == "" || vals[enabledKey] != "true" {
		return false
	}
	source := NormalizeVisibleMethodSource(method, vals[sourceKey])
	return source != "" && available[source]
}
