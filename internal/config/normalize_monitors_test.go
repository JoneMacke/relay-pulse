package config

import (
	"testing"
	"time"
)

// buildAppConfigForIntervalTest 构建用于 interval 单元测试的最小 AppConfig。
// 只需要设置时间相关 Duration 字段（不走完整 normalize，避免引入无关依赖）。
func buildAppConfigForIntervalTest(globalInterval time.Duration) *AppConfig {
	return &AppConfig{
		IntervalDuration:       globalInterval,
		SlowLatencyDuration:    5 * time.Second,
		TimeoutDuration:        10 * time.Second,
		RetryBaseDelayDuration: 200 * time.Millisecond,
		RetryMaxDelayDuration:  2 * time.Second,
		RetryJitterValue:       0.2,
	}
}

// TestDefaultIntervalForLevel 验证层级感知的 interval 回退逻辑：
//   - FREE 层（none/public/signal/pulse）全局 1m → 回退 5m
//   - PAID 层（beacon/backbone/core）全局 1m → 回退 1m（全局）
//   - FREE 层全局 10m（>5m）→ 回退 10m（"不快于全局"保护，free 不会比全局更频繁）
//   - 旧别名 basic(→pulse=FREE)/advanced(→backbone=PAID) 须在 helper 内自行迁移
func TestDefaultIntervalForLevel(t *testing.T) {
	globalOneMin := buildAppConfigForIntervalTest(time.Minute)
	globalTenMin := buildAppConfigForIntervalTest(10 * time.Minute)

	tests := []struct {
		name         string
		level        SponsorLevel
		app          *AppConfig
		wantInterval time.Duration
	}{
		// FREE 层（全局 1m）→ 5m
		{"none-global1m", SponsorLevelNone, globalOneMin, 5 * time.Minute},
		{"public-global1m", SponsorLevelPublic, globalOneMin, 5 * time.Minute},
		{"signal-global1m", SponsorLevelSignal, globalOneMin, 5 * time.Minute},
		{"pulse-global1m", SponsorLevelPulse, globalOneMin, 5 * time.Minute},
		// 旧别名 basic → pulse (FREE)
		{"basic(deprecated)-global1m", SponsorLevel("basic"), globalOneMin, 5 * time.Minute},
		// PAID 层（全局 1m）→ 1m（保持全局高频）
		{"beacon-global1m", SponsorLevelBeacon, globalOneMin, time.Minute},
		{"backbone-global1m", SponsorLevelBackbone, globalOneMin, time.Minute},
		{"core-global1m", SponsorLevelCore, globalOneMin, time.Minute},
		// 旧别名 advanced → backbone (PAID)
		{"advanced(deprecated)-global1m", SponsorLevel("advanced"), globalOneMin, time.Minute},
		// 旧别名 enterprise → core (PAID)
		{"enterprise(deprecated)-global1m", SponsorLevel("enterprise"), globalOneMin, time.Minute},
		// FREE 层但全局 interval 更慢（10m > 5m）→ 用全局值，不能让 free 比全局更频繁
		{"pulse-global10m", SponsorLevelPulse, globalTenMin, 10 * time.Minute},
		{"none-global10m", SponsorLevelNone, globalTenMin, 10 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ServiceConfig{SponsorLevel: tt.level}
			if err := normalizeDurationsForMonitor(tt.app, cfg); err != nil {
				t.Fatalf("normalizeDurationsForMonitor() err = %v", err)
			}
			if cfg.IntervalDuration != tt.wantInterval {
				t.Errorf("IntervalDuration = %v, want %v", cfg.IntervalDuration, tt.wantInterval)
			}
		})
	}
}

// TestExplicitIntervalAlwaysWins 验证显式设置 interval 时层级回退不生效：
// FREE 层通道若配置了 interval: "2m"，应使用 2m（不被降速到 5m）。
func TestExplicitIntervalAlwaysWins(t *testing.T) {
	app := buildAppConfigForIntervalTest(time.Minute)

	tests := []struct {
		name     string
		level    SponsorLevel
		interval string
		want     time.Duration
	}{
		{"free-explicit-2m", SponsorLevelPulse, "2m", 2 * time.Minute},
		{"paid-explicit-2m", SponsorLevelBeacon, "2m", 2 * time.Minute},
		{"none-explicit-30s", SponsorLevelNone, "30s", 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ServiceConfig{
				SponsorLevel: tt.level,
				Interval:     tt.interval,
			}
			if err := normalizeDurationsForMonitor(app, cfg); err != nil {
				t.Fatalf("normalizeDurationsForMonitor() err = %v", err)
			}
			if cfg.IntervalDuration != tt.want {
				t.Errorf("IntervalDuration = %v, want %v", cfg.IntervalDuration, tt.want)
			}
		})
	}
}

// TestPaidFreeMonitorNormalizeIntervalDuration 验证完整 normalize 链路中
// paid/free monitor 各自的 IntervalDuration 是否符合层级规则。
func TestPaidFreeMonitorNormalizeIntervalDuration(t *testing.T) {
	cfg := &AppConfig{
		// 全局 interval 1m
		Interval: "1m",
		Monitors: []ServiceConfig{
			{
				// PAID：beacon 无显式 interval → 应使用 1m（全局）
				Provider:     "demo",
				Service:      "cc",
				Channel:      "vip-paid",
				Model:        "paid",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				SponsorLevel: SponsorLevelBeacon,
			},
			{
				// FREE：无 SponsorLevel，无显式 interval → 应降速到 5m
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip-free",
				Model:      "free",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
				// SponsorLevel 留空 = SponsorLevelNone
			},
			{
				// FREE：pulse，但显式设置了 interval 2m → 应使用 2m
				Provider:     "demo",
				Service:      "cc",
				Channel:      "vip-explicit",
				Model:        "explicit",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				SponsorLevel: SponsorLevelPulse,
				Interval:     "2m",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() err = %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize() err = %v", err)
	}

	paid := &cfg.Monitors[0]
	free := &cfg.Monitors[1]
	explicit := &cfg.Monitors[2]

	if paid.IntervalDuration != time.Minute {
		t.Errorf("paid.IntervalDuration = %v, want 1m", paid.IntervalDuration)
	}
	if free.IntervalDuration != 5*time.Minute {
		t.Errorf("free.IntervalDuration = %v, want 5m", free.IntervalDuration)
	}
	if explicit.IntervalDuration != 2*time.Minute {
		t.Errorf("explicit.IntervalDuration = %v, want 2m", explicit.IntervalDuration)
	}
}
