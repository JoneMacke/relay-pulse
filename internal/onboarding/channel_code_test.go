package onboarding

import (
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
