package onboarding

import (
	"context"
	"testing"
	"time"

	"monitor/internal/config"
)

// newTestService 基于内存 SQLite store 构造一个最小可用的 Service（仅覆盖 AdminUpdate 所需依赖）。
func newTestService(t *testing.T) (*Service, *SQLStore) {
	t.Helper()
	store := newTestStore(t)
	cfg := &config.OnboardingConfig{
		EncryptionKey:    testKey(),
		ProofSecret:      "test-proof-secret",
		ProofTTLDuration: 5 * time.Minute,
	}
	svc, err := NewService(store, cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store
}

// TestAdminUpdate_RederiveGuard 锁定 AdminUpdate 的四元组重派生护栏行为：
// 仅当 service/type/source/group 任一变化时才重新校验并重派生 channel_code，
// 否则保留库内原值（保护 legacy 两段记录不被无关编辑改写成三段）。
func TestAdminUpdate_RederiveGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("无关字段编辑不触发重派生", func(t *testing.T) {
		svc, store := newTestService(t)
		// legacy 两段记录：source=api、group 为空、channel_code 存为两段。
		saveSubmission(t, store, "legacy-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "legacy-1", map[string]any{
			"admin_note": "仅改备注",
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelCode != "o-api" {
			t.Errorf("无关字段编辑应保留两段 channel_code o-api，实际 %q", got.ChannelCode)
		}
		if got.ChannelGroup != "" {
			t.Errorf("无关字段编辑不应补全 channel_group，实际 %q", got.ChannelGroup)
		}
		if got.AdminNote != "仅改备注" {
			t.Errorf("admin_note 未写入，实际 %q", got.AdminNote)
		}
	})

	t.Run("改 channel_group 触发重派生为三段", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "regroup-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "regroup-1", map[string]any{
			"channel_group": "US", // 大写应被归一化为小写
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelCode != "o-api-us" {
			t.Errorf("改 group 应重派生为 o-api-us，实际 %q", got.ChannelCode)
		}
		if got.ChannelGroup != "us" {
			t.Errorf("channel_group 应归一化为 us，实际 %q", got.ChannelGroup)
		}
	})

	t.Run("改 channel_source 触发重派生且空 group 回退 main", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "resrc-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "resrc-1", map[string]any{
			"channel_source": "MAX", // cc 词表含 max；大写应归一化
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelCode != "o-max-main" {
			t.Errorf("改 source 应重派生为 o-max-main（空 group 回退 main），实际 %q", got.ChannelCode)
		}
		if got.ChannelSource != "max" || got.ChannelGroup != "main" {
			t.Errorf("source/group 归一化错误：source=%q group=%q", got.ChannelSource, got.ChannelGroup)
		}
	})

	t.Run("非法 channel_source 被词表校验拒绝", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "badsrc-1", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "badsrc-1", map[string]any{
			"channel_source": "zzz", // 任何 service 词表都不含
		}); err == nil {
			t.Errorf("非法 channel_source zzz 应被拒绝")
		}
	})

	t.Run("非法枚举 channel_type / service_type 被拒绝", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "badenum-1", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "badenum-1", map[string]any{
			"channel_type": "X",
		}); err == nil {
			t.Errorf("非法 channel_type X 应被拒绝")
		}
		if _, err := svc.AdminUpdate(ctx, "badenum-1", map[string]any{
			"service_type": "zz",
		}); err == nil {
			t.Errorf("非法 service_type zz 应被拒绝")
		}
	})

	t.Run("改 service_type 后 source 须在新 service 词表内", func(t *testing.T) {
		svc, store := newTestService(t)
		// source=api：cc 与 cx 词表均含 api，所以仅切 service_type 到 cx 仍合法且重派生。
		saveSubmission(t, store, "svc-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "svc-1", map[string]any{
			"service_type": "cx",
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ServiceType != "cx" || got.ChannelCode != "o-api-main" {
			t.Errorf("切 service_type 到 cx 应重派生为 o-api-main，实际 service=%q code=%q", got.ServiceType, got.ChannelCode)
		}
	})

	t.Run("仅改 channel_type 与现有 source 类别不符被拒", func(t *testing.T) {
		svc, store := newTestService(t)
		// 基础记录 type=O、source=api(official)。改 type=R（仅允许 reverse）应被自洽校验拒绝。
		saveSubmission(t, store, "tcm-1", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "tcm-1", map[string]any{
			"channel_type": "R",
		}); err == nil {
			t.Errorf("O→R 但 source=api 仍为官方类，应被类型↔来源自洽校验拒绝")
		}
	})

	t.Run("同时改 channel_type+source 到自洽组合通过并重派生", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "tcm-2", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "tcm-2", map[string]any{
			"channel_type":   "R",
			"channel_source": "kiro", // cc 词表含 kiro(reverse)，与 R 自洽
		})
		if err != nil {
			t.Fatalf("R+kiro 应通过: %v", err)
		}
		if got.ChannelCode != "r-kiro-main" {
			t.Errorf("R+kiro 应重派生为 r-kiro-main（空 group 回退 main），实际 %q", got.ChannelCode)
		}
	})

	t.Run("channel_name 过校验：中文放行、不可见字符拒绝", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "chname-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "chname-1", map[string]any{
			"channel_name": "  华东线路 ",
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelName != "华东线路" {
			t.Errorf("channel_name 应剪除首尾空白后写入，实际 %q", got.ChannelName)
		}
		if _, err := svc.AdminUpdate(ctx, "chname-1", map[string]any{
			"channel_name": "a\u202eb", // bidi 方向控制符
		}); err == nil {
			t.Errorf("含 bidi 控制符的 channel_name 应被拒绝")
		}
	})
}
