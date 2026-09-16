package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// PaymentConfig 会被 GET /payment/config 原样序列化给普通用户，
// 因此支付宝轮询充值的端点与提示词参数必须显式排除。
func TestPaymentConfigDoesNotLeakAlipayPollEndpoint(t *testing.T) {
	cfg := &PaymentConfig{
		AlipayPollEnabled:  true,
		AlipayPollBaseURL:  "https://o.tbox.cn",
		AlipayPollChatPath: "/openapi/v1/chat/create",
		AlipayPollUserID:   "sub2api_alipay_qr_client",
		AlipayPollNote:     "FaithAPI充值",
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal PaymentConfig: %v", err)
	}
	serialized := string(raw)

	for _, leak := range []string{
		"tbox.cn",
		"chat/create",
		"sub2api_alipay_qr_client",
		"FaithAPI",
	} {
		if strings.Contains(serialized, leak) {
			t.Errorf("user-facing PaymentConfig leaks %q: %s", leak, serialized)
		}
	}
	// 展示类字段仍应正常下发，否则前端开关会失效。
	if !strings.Contains(serialized, `"alipay_poll_enabled"`) {
		t.Errorf("alipay_poll_enabled missing from user-facing payload: %s", serialized)
	}
}

func TestAlipayPollEndpointFallsBackToDefaults(t *testing.T) {
	var nilCfg *PaymentConfig
	ep := nilCfg.Endpoint()
	if got, want := ep.URL(), "https://o.tbox.cn/openapi/v1/chat/create"; got != want {
		t.Errorf("nil config URL = %q, want %q", got, want)
	}
	if ep.UserID != defaultAlipayPollUserID {
		t.Errorf("nil config UserID = %q, want %q", ep.UserID, defaultAlipayPollUserID)
	}
	if ep.Note != defaultAlipayPollNote {
		t.Errorf("nil config Note = %q, want %q", ep.Note, defaultAlipayPollNote)
	}
}

func TestAlipayPollEndpointJoinsURL(t *testing.T) {
	cases := []struct {
		name     string
		endpoint AlipayPollEndpoint
		want     string
	}{
		{
			name:     "plain",
			endpoint: AlipayPollEndpoint{BaseURL: "https://example.com", ChatPath: "/api/x"},
			want:     "https://example.com/api/x",
		},
		{
			name:     "trailing slash on base",
			endpoint: AlipayPollEndpoint{BaseURL: "https://example.com/", ChatPath: "/api/x"},
			want:     "https://example.com/api/x",
		},
		{
			name:     "path without leading slash",
			endpoint: AlipayPollEndpoint{BaseURL: "https://example.com", ChatPath: "api/x"},
			want:     "https://example.com/api/x",
		},
		{
			name:     "surrounding whitespace",
			endpoint: AlipayPollEndpoint{BaseURL: "  https://example.com  ", ChatPath: "  /api/x  "},
			want:     "https://example.com/api/x",
		},
		{
			name:     "blank path falls back to default",
			endpoint: AlipayPollEndpoint{BaseURL: "https://example.com"},
			want:     "https://example.com" + defaultAlipayPollChatPath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.endpoint.URL(); got != tc.want {
				t.Errorf("URL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPCParseStrOrDefault(t *testing.T) {
	if got := pcParseStrOrDefault("   ", defaultAlipayPollNote); got != defaultAlipayPollNote {
		t.Errorf("blank input = %q, want default %q", got, defaultAlipayPollNote)
	}
	if got := pcParseStrOrDefault("  自定义备注  ", defaultAlipayPollNote); got != "自定义备注" {
		t.Errorf("custom input = %q, want trimmed %q", got, "自定义备注")
	}
}
