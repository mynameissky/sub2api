package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const manualTierQRDataURL = "data:image/png;base64,aGVsbG8="

func TestParseManualRechargeTiersDegradesToEmpty(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty string", raw: "", want: 0},
		{name: "whitespace only", raw: "   ", want: 0},
		{name: "invalid json", raw: "{not json", want: 0},
		{name: "json object instead of array", raw: `{"price":1}`, want: 0},
		{name: "drops non-positive price", raw: `[{"price":0,"credit":10},{"price":5,"credit":5}]`, want: 1},
		{name: "drops non-positive credit", raw: `[{"price":10,"credit":-1},{"price":5,"credit":5}]`, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseManualRechargeTiers(tc.raw)
			if len(got) != tc.want {
				t.Fatalf("parseManualRechargeTiers(%q) len = %d, want %d", tc.raw, len(got), tc.want)
			}
			if got == nil {
				t.Fatalf("parseManualRechargeTiers(%q) returned nil, want non-nil slice", tc.raw)
			}
		})
	}
}

func TestParseManualRechargeTiersKeepsImageAndOrder(t *testing.T) {
	raw := `[{"price":10,"credit":12,"qr_image":"data:image/png;base64,AAA"},{"price":50,"credit":60,"qr_image":"data:image/png;base64,BBB"}]`
	tiers := parseManualRechargeTiers(raw)
	if len(tiers) != 2 {
		t.Fatalf("len = %d, want 2", len(tiers))
	}
	if tiers[0].QRImage != "data:image/png;base64,AAA" || tiers[1].QRImage != "data:image/png;base64,BBB" {
		t.Fatalf("qr images not preserved: %+v", tiers)
	}
}

func TestUpdatePaymentConfigRejectsInvalidManualTier(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := &PaymentConfigService{settingRepo: repo}

	tiers := []ManualRechargeTier{{Price: 0, Credit: 10, QRImage: manualTierQRDataURL}}
	err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{ManualRechargeTiers: &tiers})
	if err == nil {
		t.Fatal("expected an error for a non-positive tier price")
	}
	if !strings.Contains(err.Error(), "manual recharge tier price and credit must be greater than 0") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if _, ok := repo.updates[SettingManualRechargeTiers]; ok {
		t.Fatal("invalid tiers must not be persisted")
	}
}

// 管理端把未修改的档位二维码原样回传时，必须按 index 映射回库里的 base64，
// 否则保存后图片就被端点 URL 覆盖掉。
func TestUpdatePaymentConfigRestoresManualTierImagesByIndex(t *testing.T) {
	prev := []ManualRechargeTier{
		{Price: 10, Credit: 10, QRImage: manualTierQRDataURL},
		{Price: 50, Credit: 55, QRImage: "data:image/png;base64,BBBB"},
	}
	encoded, err := json.Marshal(prev)
	if err != nil {
		t.Fatalf("marshal prev tiers: %v", err)
	}
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingManualRechargeTiers: string(encoded),
	}}
	svc := &PaymentConfigService{settingRepo: repo}

	// 管理员把两个档位调换了顺序，二维码仍是首版回传的端点 URL。
	submitted := []ManualRechargeTier{
		{Price: 50, Credit: 55, QRImage: ManualTierQRImageURL(1)},
		{Price: 10, Credit: 10, QRImage: ManualTierQRImageURL(0)},
	}
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{ManualRechargeTiers: &submitted}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}

	var saved []ManualRechargeTier
	if err := json.Unmarshal([]byte(repo.values[SettingManualRechargeTiers]), &saved); err != nil {
		t.Fatalf("unmarshal saved tiers: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("saved len = %d, want 2", len(saved))
	}
	// index 映射而不是下标映射：重排后每个档位仍拿到各自原来的二维码。
	if saved[0].QRImage != "data:image/png;base64,BBBB" {
		t.Errorf("saved[0].QRImage = %q, want the second tier's image", saved[0].QRImage)
	}
	if saved[1].QRImage != manualTierQRDataURL {
		t.Errorf("saved[1].QRImage = %q, want the first tier's image", saved[1].QRImage)
	}
}

func TestResolveManualRechargeTierImagesClearsOutOfRangeIndex(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := &PaymentConfigService{settingRepo: repo}

	tiers := []ManualRechargeTier{{Price: 10, Credit: 10, QRImage: ManualTierQRImageURL(3)}}
	resolved := svc.resolveManualRechargeTierImages(context.Background(), tiers)
	if resolved[0].QRImage != "" {
		t.Fatalf("out-of-range index QRImage = %q, want empty", resolved[0].QRImage)
	}
}

// 客服二维码回传自有图片端点 URL 时必须跳过写入；显式空串才清空。
func TestUpdatePaymentConfigKeepsManualCSQRImage(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingManualCSQRImage: manualTierQRDataURL,
	}}
	svc := &PaymentConfigService{settingRepo: repo}

	roundTripped := SettingsImageURL(SettingsImageKeyManualCSQR)
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{ManualCSQRImage: &roundTripped}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if _, ok := repo.updates[SettingManualCSQRImage]; ok {
		t.Fatalf("own image endpoint URL must not overwrite the stored data URL (got %q)", repo.updates[SettingManualCSQRImage])
	}

	// nil = 不改：不含该字段的局部更新不能抹掉已存图片。
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if repo.values[SettingManualCSQRImage] != manualTierQRDataURL {
		t.Fatalf("nil field wiped the stored QR image: %q", repo.values[SettingManualCSQRImage])
	}

	empty := ""
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{ManualCSQRImage: &empty}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if repo.values[SettingManualCSQRImage] != "" {
		t.Fatalf("explicit empty string must clear the stored QR image, got %q", repo.values[SettingManualCSQRImage])
	}
}

// 回显（用户侧与管理端）必须把内联 base64 换成图片端点 URL，
// 否则上传的二维码原图会让 /admin/settings 这类高频接口的响应体膨胀到数 MB。
func TestStripPaymentConfigImages(t *testing.T) {
	cfg := &PaymentConfig{
		HelpImageURL:    "data:image/png;base64,aGVsbG8=",
		ManualCSQRImage: "data:image/png;base64,aGVsbG8=",
		ManualRechargeTiers: []ManualRechargeTier{
			{Price: 10, Credit: 12, QRImage: "data:image/png;base64,aGVsbG8="},
			{Price: 50, Credit: 60, QRImage: "https://cdn.example.com/qr.png"},
		},
	}
	StripPaymentConfigImages(cfg)

	if cfg.HelpImageURL != SettingsImageURL(SettingsImageKeyHelpImage) {
		t.Errorf("HelpImageURL = %q, want the help image endpoint URL", cfg.HelpImageURL)
	}
	if cfg.ManualCSQRImage != SettingsImageURL(SettingsImageKeyManualCSQR) {
		t.Errorf("ManualCSQRImage = %q, want the CS QR endpoint URL", cfg.ManualCSQRImage)
	}
	if cfg.ManualRechargeTiers[0].QRImage != ManualTierQRImageURL(0) {
		t.Errorf("tier[0].QRImage = %q, want the tier endpoint URL", cfg.ManualRechargeTiers[0].QRImage)
	}
	// 非 data URL（外部链接 / 已是端点 URL）保持原样
	if cfg.ManualRechargeTiers[1].QRImage != "https://cdn.example.com/qr.png" {
		t.Errorf("tier[1].QRImage = %q, want passthrough", cfg.ManualRechargeTiers[1].QRImage)
	}

	// nil 配置不应 panic
	StripPaymentConfigImages(nil)
}

// 回显换成端点 URL 后，管理端原样回传不能把帮助图片的 base64 覆盖掉。
func TestUpdatePaymentConfigKeepsHelpImage(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingHelpImageURL: manualTierQRDataURL,
	}}
	svc := &PaymentConfigService{settingRepo: repo}

	roundTripped := SettingsImageURL(SettingsImageKeyHelpImage)
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{HelpImageURL: &roundTripped}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if repo.values[SettingHelpImageURL] != manualTierQRDataURL {
		t.Fatalf("own image endpoint URL overwrote the stored help image: %q", repo.values[SettingHelpImageURL])
	}

	// nil = 不改
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if repo.values[SettingHelpImageURL] != manualTierQRDataURL {
		t.Fatalf("nil field wiped the stored help image: %q", repo.values[SettingHelpImageURL])
	}

	// 显式空串 / 外部链接都要照常写入
	external := "https://cdn.example.com/help.png"
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{HelpImageURL: &external}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if repo.values[SettingHelpImageURL] != external {
		t.Fatalf("external URL not persisted: %q", repo.values[SettingHelpImageURL])
	}

	empty := ""
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{HelpImageURL: &empty}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if repo.values[SettingHelpImageURL] != "" {
		t.Fatalf("explicit empty string must clear the help image, got %q", repo.values[SettingHelpImageURL])
	}
}

func TestSettingsImageTierIndexRoundTrip(t *testing.T) {
	for _, index := range []int{0, 1, 42} {
		url := ManualTierQRImageURL(index)
		got, ok := SettingsImageTierIndex(url)
		if !ok || got != index {
			t.Fatalf("SettingsImageTierIndex(%q) = (%d, %v), want (%d, true)", url, got, ok, index)
		}
	}

	for _, invalid := range []string{
		"",
		"/api/v1/settings/image?key=payment_manual_cs_qr",
		"/api/v1/settings/image?key=payment_manual_tier_qr",
		"/api/v1/settings/image?key=payment_manual_tier_qr&index=-1",
		"/api/v1/settings/image?key=payment_manual_tier_qr&index=abc",
		"data:image/png;base64,iVBORw0KGgo=",
	} {
		if _, ok := SettingsImageTierIndex(invalid); ok {
			t.Errorf("SettingsImageTierIndex(%q) unexpectedly resolved", invalid)
		}
	}

	// 其它 key 的自有端点 URL 也不是档位二维码
	if !IsOwnSettingsImageURL(SettingsImageURL(SettingsImageKeyManualCSQR)) {
		t.Error("manual CS QR endpoint URL should be recognised as our own")
	}
	if IsOwnSettingsImageURL(manualTierQRDataURL) {
		t.Error("data URL must not be recognised as our own image endpoint")
	}
	// 只比对 path：浏览器可能把相对地址补成绝对地址，主机名差异不能导致漏判
	if _, ok := SettingsImageTierIndex("https://example.com" + ManualTierQRImageURL(7)); !ok {
		t.Error("absolute URL on our own path should still resolve its tier index")
	}
}

func TestSettingsImageOrDataURL(t *testing.T) {
	if got, want := SettingsImageOrDataURL(manualTierQRDataURL, SettingsImageKeyManualCSQR), SettingsImageURL(SettingsImageKeyManualCSQR); got != want {
		t.Errorf("data URL = %q, want %q", got, want)
	}
	for _, passthrough := range []string{"", "https://cdn.example.com/qr.png", "/api/v1/settings/image?key=payment_manual_cs_qr"} {
		if got := SettingsImageOrDataURL(passthrough, SettingsImageKeyManualCSQR); got != passthrough {
			t.Errorf("SettingsImageOrDataURL(%q) = %q, want passthrough", passthrough, got)
		}
	}
}

func TestDecodeDataURL(t *testing.T) {
	mimeType, data, ok := DecodeDataURL("data:image/png;base64,aGVsbG8=")
	if !ok {
		t.Fatal("expected a successful decode")
	}
	if mimeType != "image/png" {
		t.Errorf("mimeType = %q, want image/png", mimeType)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want hello", string(data))
	}

	for _, invalid := range []string{
		"",
		"https://example.com/a.png",
		"data:image/png,notbase64",
		"data:image/png;base64,!!!not-base64!!!",
		"data:image/png;base64",
	} {
		if _, _, ok := DecodeDataURL(invalid); ok {
			t.Errorf("DecodeDataURL(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestPaymentConfigServiceSettingsImage(t *testing.T) {
	tiers, err := json.Marshal([]ManualRechargeTier{{Price: 10, Credit: 10, QRImage: manualTierQRDataURL}})
	if err != nil {
		t.Fatalf("marshal tiers: %v", err)
	}
	svc := &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
		SettingManualCSQRImage:       manualTierQRDataURL,
		SettingManualRechargeTiers:   string(tiers),
		SettingHelpImageURL:          "data:image/jpeg;base64,aGVsbG8=",
		SettingKeySiteLogo:           "data:image/svg+xml;base64,aGVsbG8=",
	}}}

	ctx := context.Background()
	cases := []struct {
		key       string
		index     string
		wantMime  string
		wantFound bool
	}{
		{key: SettingsImageKeyManualCSQR, wantMime: "image/png", wantFound: true},
		{key: SettingsImageKeyManualTierQR, index: "0", wantMime: "image/png", wantFound: true},
		{key: SettingsImageKeyHelpImage, wantMime: "image/jpeg", wantFound: true},
		{key: SettingsImageKeySiteLogo, wantMime: "image/svg+xml", wantFound: true},
		// index 越界 / 非法 / 缺省都应视为未命中
		{key: SettingsImageKeyManualTierQR, index: "1"},
		{key: SettingsImageKeyManualTierQR, index: "-1"},
		{key: SettingsImageKeyManualTierQR, index: "abc"},
		{key: SettingsImageKeyManualTierQR},
		{key: "unknown_key"},
		{key: ""},
	}
	for _, tc := range cases {
		mimeType, data, ok := svc.SettingsImage(ctx, tc.key, tc.index)
		if ok != tc.wantFound {
			t.Errorf("SettingsImage(%q, %q) found = %v, want %v", tc.key, tc.index, ok, tc.wantFound)
			continue
		}
		if !tc.wantFound {
			continue
		}
		if mimeType != tc.wantMime || string(data) != "hello" {
			t.Errorf("SettingsImage(%q, %q) = (%q, %q), want (%q, hello)", tc.key, tc.index, mimeType, string(data), tc.wantMime)
		}
	}
}

// 图片端点必须能取到这个「未保存二维码」的服务：命中失败只回 404，不 panic。
func TestSettingsImageMissingValuesReturnNotFound(t *testing.T) {
	svc := &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{}}}
	for _, key := range []string{
		SettingsImageKeySiteLogo,
		SettingsImageKeyHelpImage,
		SettingsImageKeyManualCSQR,
	} {
		if _, _, ok := svc.SettingsImage(context.Background(), key, ""); ok {
			t.Errorf("SettingsImage(%q) unexpectedly succeeded on an empty setting", key)
		}
	}
}

// 用户侧下发的配置不能带内联 base64：档位与客服二维码都必须换成图片端点 URL。
func TestPaymentConfigManualRechargeJSONShape(t *testing.T) {
	cfg := &PaymentConfig{
		ManualRechargeEnabled: true,
		ManualRechargeTiers: []ManualRechargeTier{
			{Price: 10, Credit: 12, QRImage: ManualTierQRImageURL(0)},
		},
		ManualCSQRImage:  SettingsImageURL(SettingsImageKeyManualCSQR),
		ManualCSHelpText: "付款后联系客服",
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal PaymentConfig: %v", err)
	}
	serialized := string(raw)
	for _, want := range []string{
		`"manual_recharge_enabled":true`,
		`"manual_recharge_tiers"`,
		`"qr_image":"/api/v1/settings/image?key=payment_manual_tier_qr\u0026index=0"`,
		`"manual_cs_qr_image":"/api/v1/settings/image?key=payment_manual_cs_qr"`,
		`"manual_cs_help_text":"付款后联系客服"`,
	} {
		if !strings.Contains(serialized, want) {
			t.Errorf("user-facing payload missing %s: %s", want, serialized)
		}
	}
}
