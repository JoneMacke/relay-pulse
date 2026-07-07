package api

import (
	"testing"
	"time"
)

func TestListedDaysSince(t *testing.T) {
	tests := []struct {
		name        string
		listedSince string
		nowUTC      time.Time
		want        *int
	}{
		{
			name:        "空字符串不计算",
			listedSince: "",
			nowUTC:      time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:        nil,
		},
		{
			name:        "解析失败不计算",
			listedSince: "not-a-date",
			nowUTC:      time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:        nil,
		},
		{
			name:        "收录当天为 0 天",
			listedSince: "2026-07-01",
			nowUTC:      time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), // CST 2026-07-01 18:00，仍是收录当天
			want:        intPtr(0),
		},
		{
			name:        "未来日期钳制为 0",
			listedSince: "2099-01-01",
			nowUTC:      time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			want:        intPtr(0),
		},
		{
			name:        "生产实例复现：CST 已跨入次日但 UTC 当天日期仍是收录当天，应算满 1 天",
			listedSince: "2026-07-01",
			nowUTC:      time.Date(2026, 7, 1, 16, 13, 0, 0, time.UTC), // CST 2026-07-02 00:13
			want:        intPtr(1),
		},
		{
			name:        "CST 次日零点整前一刻仍是 0 天",
			listedSince: "2026-07-01",
			nowUTC:      time.Date(2026, 7, 1, 15, 59, 59, 0, time.UTC), // CST 2026-07-01 23:59:59
			want:        intPtr(0),
		},
		{
			name:        "整 7 天",
			listedSince: "2026-07-01",
			nowUTC:      time.Date(2026, 7, 8, 0, 13, 0, 0, time.UTC), // CST 2026-07-08 08:13，早已过 CST 07-08 零点
			want:        intPtr(7),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listedDaysSince(tt.listedSince, tt.nowUTC)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("listedDaysSince(%q, %v) = %v, want %v", tt.listedSince, tt.nowUTC, deref(got), deref(tt.want))
			}
			if got != nil && *got != *tt.want {
				t.Errorf("listedDaysSince(%q, %v) = %d, want %d", tt.listedSince, tt.nowUTC, *got, *tt.want)
			}
		})
	}
}

func intPtr(n int) *int {
	return &n
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
