package onboarding

import (
	"strings"
	"testing"
)

func TestDeriveChannelCode(t *testing.T) {
	cases := []struct {
		name                       string
		cType, source, group, want string
	}{
		{"three segments", "O", "max", "main", "o-max-main"},
		{"uppercase normalized", "O", "AWS", "US", "o-aws-us"},
		{"empty group falls back to two segments", "R", "kiro", "", "r-kiro"},
		{"spaces stripped from source", "M", "mi x", "pool", "m-mix-pool"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deriveChannelCode(c.cType, c.source, c.group); got != c.want {
				t.Fatalf("deriveChannelCode(%q,%q,%q) = %q, want %q", c.cType, c.source, c.group, got, c.want)
			}
		})
	}
}

func TestValidateProviderName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"ascii unchanged", "SaiAI", "SaiAI", false},
		{"ascii with spaces", "Sai AI Cloud", "Sai AI Cloud", false},
		{"ascii punctuation", "WorldBase.ai", "WorldBase.ai", false},
		{"chinese", "赛博AI", "赛博AI", false},
		{"japanese", "さくらクラウド", "さくらクラウド", false},
		{"russian", "Яндекс", "Яндекс", false},
		{"arabic", "سحابة", "سحابة", false},
		{"mixed cjk+ascii", "赛博AI Cloud", "赛博AI Cloud", false},
		{"trims surrounding space", "  赛博AI  ", "赛博AI", false},
		{"empty", "", "", true},
		{"all whitespace", "   ", "", true},
		{"invalid utf-8", "\xff\xfe", "", true},
		{"control char tab", "Sai\tAI", "", true},
		{"newline", "Sai\nAI", "", true},
		{"zero width space (Cf)", "Sai​AI", "", true},
		{"bidi override (Cf)", "Sai‮AI", "", true},
		{"line separator (Zl)", "Sai AI", "", true},
		{"paragraph separator (Zp)", "Sai AI", "", true},
		{"over 100 runes", strings.Repeat("赛", providerNameMaxRunes+1), "", true},
		{"exactly 100 runes ok", strings.Repeat("赛", providerNameMaxRunes), strings.Repeat("赛", providerNameMaxRunes), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateProviderName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateProviderName(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("validateProviderName(%q)=%q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateChannelName(t *testing.T) {
	// 展示名允许中文/日文/emoji 等任意语言的普通文本
	for _, good := range []string{"Claude Max 华东线路", "东京リレー", "US-East ⚡", "main"} {
		if got, err := validateChannelName(good); err != nil || got != good {
			t.Fatalf("channel name %q should pass as-is, got %q err=%v", good, got, err)
		}
	}
	// 留空/纯空白视为未填写；首尾空白（含粘贴带入的换行）剪除后取规范值
	if got, err := validateChannelName("   "); err != nil || got != "" {
		t.Fatalf("blank name should normalize to empty, got %q err=%v", got, err)
	}
	if got, err := validateChannelName("  华东线路\n"); err != nil || got != "华东线路" {
		t.Fatalf("expected trimmed %q, got %q err=%v", "华东线路", got, err)
	}
	// 40 rune 上限：40 个汉字放行，41 个拒绝
	long := strings.Repeat("名", channelNameMaxRunes)
	if _, err := validateChannelName(long); err != nil {
		t.Fatalf("%d-rune name should pass, got %v", channelNameMaxRunes, err)
	}
	if _, err := validateChannelName(long + "超"); err == nil {
		t.Fatalf("%d-rune name should be rejected", channelNameMaxRunes+1)
	}
	// 内部控制字符 / bidi 控制符 / 零宽字符 / BOM / 非法 UTF-8 一律拒绝
	for _, bad := range []string{"a\tb", "a\x00b", "a\u202eb", "a\u200bb", "a\ufeffb", "a\u2028b", "\xff\xfe"} {
		if _, err := validateChannelName(bad); err == nil {
			t.Fatalf("channel name %q should be rejected", bad)
		}
	}
}

func TestValidateChannelSource(t *testing.T) {
	if got, err := validateChannelSource("cc", "MAX"); err != nil || got != "max" {
		t.Fatalf("cc/MAX should normalize to max, got %q err=%v", got, err)
	}
	if _, err := validateChannelSource("cx", "kiro"); err == nil {
		t.Fatalf("kiro is not a cx source, should be rejected")
	}
	if _, err := validateChannelSource("gm", "antg"); err != nil {
		t.Fatalf("antg is a valid gm source, got %v", err)
	}
	if _, err := validateChannelSource("zz", "api"); err == nil {
		t.Fatalf("unknown service should be rejected")
	}
	if _, err := validateChannelSource("cc", "toolongsrc"); err == nil {
		t.Fatalf("over-length source should be rejected")
	}
}

func TestNormalizeGroup(t *testing.T) {
	if got, _ := normalizeGroup(""); got != channelGroupDefault {
		t.Fatalf("empty group should fall back to %q, got %q", channelGroupDefault, got)
	}
	if got, _ := normalizeGroup("  US "); got != "us" {
		t.Fatalf("group should be trimmed+lowered to %q, got %q", "us", got)
	}
	for _, bad := range []string{"has-dash", "toolonggroup", "空格 x", "UPPERTOOLONG"} {
		if _, err := normalizeGroup(bad); err == nil {
			t.Fatalf("group %q should be rejected", bad)
		}
	}
}

// TestChannelSourceCatalogValuesWellFormed 守护词表自身：每个 source 必须满足校验正则，
// 否则人工新增非法词后 validateChannelSource 会永远拒绝它。
func TestChannelSourceCatalogValuesWellFormed(t *testing.T) {
	for service, opts := range ChannelSourceCatalog {
		for _, opt := range opts {
			if !channelSourcePattern.MatchString(opt.Value) {
				t.Errorf("service %q source %q 不满足 channelSourcePattern (2-5 位小写字母/数字)", service, opt.Value)
			}
			if opt.Label == "" {
				t.Errorf("service %q source %q 缺少 Label", service, opt.Value)
			}
		}
	}
}
