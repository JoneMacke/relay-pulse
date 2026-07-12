package displayname

import (
	"strings"
	"testing"
)

// 不可见字符全部用显式 \u 转义，避免源码里字面隐形字符的维护风险与歧义。
//   BOM  = \ufeff (Cf)   NEL = \u0085 (Cc)   ZWSP = \u200b (Cf)
//   RLO  = \u202e (Cf)   Zl  = \u2028        Zp   =

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
		{"arabic rtl ok", "سحابة", "سحابة", false},
		{"mixed cjk+ascii", "赛博AI Cloud", "赛博AI Cloud", false},
		{"trims surrounding space", "  赛博AI  ", "赛博AI", false},
		{"strips edge BOM", "\ufeff赛博AI\ufeff", "赛博AI", false},
		{"strips edge NEL", "\u0085Sai AI\u0085", "Sai AI", false},
		{"strips edge ZWSP", "\u200bSai\u200b", "Sai", false},
		{"strips edge RLO then valid", "\u202eSaiAI", "SaiAI", false},
		{"empty", "", "", true},
		{"all whitespace", "   ", "", true},
		{"pure BOM -> empty -> required err", "\ufeff\ufeff", "", true},
		{"interior tab (Cc)", "Sai\tAI", "", true},
		{"interior newline (Cc)", "Sai\nAI", "", true},
		{"interior zero width (Cf)", "Sai\u200bAI", "", true},
		{"interior bidi override (Cf)", "Sai\u202eAI", "", true},
		{"interior line separator (Zl)", "Sai\u2028AI", "", true},
		{"interior paragraph separator (Zp)", "Sai\u2029AI", "", true},
		{"over 100 runes", strings.Repeat("赛", 101), "", true},
		{"exactly 100 runes ok", strings.Repeat("赛", 100), strings.Repeat("赛", 100), false},
		{"invalid utf-8", "Sai\xffAI", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateProviderName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateProviderName(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ValidateProviderName(%q)=%q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateChannelName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty is valid (optional)", "", "", false},
		{"all whitespace -> empty", "   ", "", false},
		{"pure edge Cf -> empty valid", "\u200b\ufeff", "", false},
		{"chinese ok", "线路一", "线路一", false},
		{"trims + strips edge BOM", "\ufeff线路一\ufeff", "线路一", false},
		{"interior zero width rejected", "线\u200b路", "", true},
		{"interior bidi rejected", "线\u202e路", "", true},
		{"over 40 runes", strings.Repeat("赛", 41), "", true},
		{"exactly 40 runes ok", strings.Repeat("赛", 40), strings.Repeat("赛", 40), false},
		{"invalid utf-8", "a\xffb", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateChannelName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateChannelName(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ValidateChannelName(%q)=%q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
