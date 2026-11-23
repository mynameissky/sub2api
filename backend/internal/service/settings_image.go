package service

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SettingsImageEndpointPath is the public (unauthenticated) endpoint that serves
// images stored as data URLs in settings. Browser <img> tags cannot attach the
// Authorization header, so this endpoint must remain public.
const SettingsImageEndpointPath = "/api/v1/settings/image"

// 图片键：标识一次请求要取的是哪一张存库图片。
const (
	SettingsImageKeySiteLogo     = "site_logo"
	SettingsImageKeyHelpImage    = "payment_help_image"
	SettingsImageKeyManualCSQR   = "payment_manual_cs_qr"
	SettingsImageKeyManualTierQR = "payment_manual_tier_qr"
)

// SettingsImageQueryKey 是承载图片键的查询参数名。
const SettingsImageQueryKey = "key"

// SettingsImageIndexQueryKey 是档位二维码专用的下标查询参数名。
const SettingsImageIndexQueryKey = "index"

// SettingsImageURL 返回读取 key 对应图片的端点地址。
func SettingsImageURL(key string) string {
	return fmt.Sprintf("%s?%s=%s", SettingsImageEndpointPath, SettingsImageQueryKey, url.QueryEscape(key))
}

// ManualTierQRImageURL 返回第 index 个档位二维码的端点地址。
func ManualTierQRImageURL(index int) string {
	return fmt.Sprintf("%s&%s=%d", SettingsImageURL(SettingsImageKeyManualTierQR), SettingsImageIndexQueryKey, index)
}

// SettingsImageTierIndex 从档位二维码端点 URL 反解出 index。
// 非本端点 URL、key 不匹配或 index 非法时返回 false。
func SettingsImageTierIndex(v string) (int, bool) {
	trimmed := strings.TrimSpace(v)
	if !IsOwnSettingsImageURL(trimmed) {
		return 0, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return 0, false
	}
	query := parsed.Query()
	if query.Get(SettingsImageQueryKey) != SettingsImageKeyManualTierQR {
		return 0, false
	}
	rawIndex := strings.TrimSpace(query.Get(SettingsImageIndexQueryKey))
	if rawIndex == "" {
		return 0, false
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

// IsOwnSettingsImageURL 判定 v 是否指向本服务的图片端点。
// 只比对 path：存库/回传的值可能是相对地址，也可能被浏览器补成绝对地址。
func IsOwnSettingsImageURL(v string) bool {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	return parsed.Path == SettingsImageEndpointPath
}

// SettingsImageOrDataURL 把 data URL 换成图片端点 URL；其它值原样返回。
func SettingsImageOrDataURL(value, key string) string {
	if strings.HasPrefix(strings.TrimSpace(value), "data:image/") {
		return SettingsImageURL(key)
	}
	return value
}

// StripPaymentConfigImages 把配置里的内联 base64 图片换成图片端点 URL。
//
// 上传的二维码是原图 data URL（手机拍的收款码动辄数 MB），直接进响应体会让
// JSON 膨胀到数 MB —— 而管理端设置页每次加载都要拉完整设置（侧边栏
// adminSettingsStore 与设置页各拉一次），用户侧 /payment/config 同理。
// 换成端点 URL 后前端渲染完全一致，管理端保存时再由
// IsOwnSettingsImageURL / SettingsImageTierIndex 映射回库中已存的原图。
func StripPaymentConfigImages(cfg *PaymentConfig) {
	if cfg == nil {
		return
	}
	cfg.HelpImageURL = SettingsImageOrDataURL(cfg.HelpImageURL, SettingsImageKeyHelpImage)
	cfg.ManualCSQRImage = SettingsImageOrDataURL(cfg.ManualCSQRImage, SettingsImageKeyManualCSQR)
	for i := range cfg.ManualRechargeTiers {
		if strings.HasPrefix(cfg.ManualRechargeTiers[i].QRImage, "data:image/") {
			cfg.ManualRechargeTiers[i].QRImage = ManualTierQRImageURL(i)
		}
	}
}

// DecodeDataURL 解码 RFC 2397 的 base64 data URL，返回 MIME 类型与原始字节。
func DecodeDataURL(s string) (string, []byte, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", nil, false
	}
	comma := strings.Index(trimmed, ",")
	if comma < 0 {
		return "", nil, false
	}
	meta := trimmed[len("data:"):comma]
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, false
	}
	mimeType := strings.TrimSuffix(meta, ";base64")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	data, err := base64.StdEncoding.DecodeString(trimmed[comma+1:])
	if err != nil {
		return "", nil, false
	}
	return mimeType, data, true
}
