package automove

import (
	"testing"
	"time"
)

func TestIsSponsorExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		nowUTC    time.Time
		want      bool
	}{
		{
			name:      "空字符串不过期",
			expiresAt: "",
			nowUTC:    time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:      false,
		},
		{
			name:      "解析失败不过期",
			expiresAt: "not-a-date",
			nowUTC:    time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:      false,
		},
		{
			name:      "到期日当天仍有效（CST 视角）",
			expiresAt: "2026-07-07",
			nowUTC:    time.Date(2026, 7, 7, 15, 59, 59, 0, time.UTC), // CST 2026-07-07 23:59:59
			want:      false,
		},
		{
			name:      "CST 次日零点整即判过期",
			expiresAt: "2026-07-07",
			nowUTC:    time.Date(2026, 7, 7, 16, 0, 0, 0, time.UTC), // CST 2026-07-08 00:00:00
			want:      true,
		},
		{
			name:      "生产实例复现：CST 次日凌晨已过期，UTC 当天日期仍是到期日当天",
			expiresAt: "2026-07-07",
			nowUTC:    time.Date(2026, 7, 7, 16, 13, 0, 0, time.UTC), // CST 2026-07-08 00:13
			want:      true,
		},
		{
			name:      "远期未到期",
			expiresAt: "2099-01-01",
			nowUTC:    time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:      false,
		},
		{
			name:      "早已过期",
			expiresAt: "2020-01-01",
			nowUTC:    time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSponsorExpired(tt.expiresAt, tt.nowUTC)
			if got != tt.want {
				t.Errorf("isSponsorExpired(%q, %v) = %v, want %v", tt.expiresAt, tt.nowUTC, got, tt.want)
			}
		})
	}
}
