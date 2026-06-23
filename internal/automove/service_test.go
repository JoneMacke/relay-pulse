package automove

import (
	"context"
	"errors"
	"testing"
	"time"

	"monitor/internal/config"
	"monitor/internal/storage"
)

// mockStorage 实现 storage.Storage 接口的测试替身
type mockStorage struct {
	history map[storage.MonitorKey][]*storage.ProbeRecord
	// batchErr 非 nil 时，GetHistoryBatch 返回该错误，用于测试查询失败 fallback。
	batchErr error
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		history: make(map[storage.MonitorKey][]*storage.ProbeRecord),
	}
}

func (m *mockStorage) Init() error                                   { return nil }
func (m *mockStorage) Close() error                                  { return nil }
func (m *mockStorage) Ping() error                                   { return nil }
func (m *mockStorage) WithContext(_ context.Context) storage.Storage { return m }
func (m *mockStorage) SaveRecord(_ *storage.ProbeRecord) error       { return nil }
func (m *mockStorage) GetLatest(_, _, _, _ string) (*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistory(_, _, _, _ string, _ time.Time) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryWithLimit(_, _, _, _ string, _ time.Time, _ int) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetLatestBatch(_ []storage.MonitorKey) (map[storage.MonitorKey]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryBatch(keys []storage.MonitorKey, _ time.Time) (map[storage.MonitorKey][]*storage.ProbeRecord, error) {
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	result := make(map[storage.MonitorKey][]*storage.ProbeRecord)
	for _, k := range keys {
		if records, ok := m.history[k]; ok {
			result[k] = records
		}
	}
	return result, nil
}
func (m *mockStorage) MigrateChannelData(_ []storage.ChannelMigrationMapping) error { return nil }
func (m *mockStorage) GetServiceState(_, _, _, _ string) (*storage.ServiceState, error) {
	return nil, nil
}
func (m *mockStorage) UpsertServiceState(_ *storage.ServiceState) error { return nil }
func (m *mockStorage) GetChannelState(_, _, _ string) (*storage.ChannelState, error) {
	return nil, nil
}
func (m *mockStorage) UpsertChannelState(_ *storage.ChannelState) error { return nil }
func (m *mockStorage) GetModelStatesForChannel(_, _, _ string) ([]*storage.ServiceState, error) {
	return nil, nil
}
func (m *mockStorage) SaveStatusEvent(_ *storage.StatusEvent) error { return nil }
func (m *mockStorage) GetStatusEvents(_ int64, _ int, _ *storage.EventFilters) ([]*storage.StatusEvent, error) {
	return nil, nil
}
func (m *mockStorage) GetLatestEventID() (int64, error) { return 0, nil }
func (m *mockStorage) PurgeOldRecords(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}

// makeRecords 生成指定状态的探测记录。
// 所有记录使用相同时间戳（当前时间），确保落在同一 bucket 内，
// 避免因 UTC 午夜附近运行导致的跨 bucket 脆弱测试。
func makeRecords(status int, count int) []*storage.ProbeRecord {
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, count)
	for i := range records {
		records[i] = &storage.ProbeRecord{Status: status, Timestamp: ts}
	}
	return records
}

func TestEvaluate_DualThreshold_DemoteHot(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}

	// 100% red → availability=0% < threshold_down=50%
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for bad/cc/vip")
	}
	if ov.Board != "secondary" {
		t.Errorf("expected board=secondary, got %s", ov.Board)
	}
}

func TestEvaluate_DualThreshold_PromoteSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "good", Service: "cc", Channel: "vip"}

	// 100% green → availability=100% >= threshold_up=55%
	store.history[key] = makeRecords(1, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "good", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for good/cc/vip")
	}
	if ov.Board != "hot" {
		t.Errorf("expected board=hot, got %s", ov.Board)
	}
}

func TestEvaluate_DualThreshold_HysteresisBuffer(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "mid", Service: "cc", Channel: "vip"}

	// 52% availability: between threshold_down(50%) and threshold_up(55%)
	// 所有记录使用相同时间戳，确保落在同一 bucket，避免跨 bucket 脆弱测试
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, 100)
	for i := 0; i < 52; i++ {
		records[i] = &storage.ProbeRecord{Status: 1, Timestamp: ts}
	}
	for i := 52; i < 100; i++ {
		records[i] = &storage.ProbeRecord{Status: 0, Timestamp: ts}
	}
	store.history[key] = records

	// As secondary with 52% (< threshold_up 55%): should NOT promote
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "mid", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for secondary monitor at 52% (between thresholds)")
	}

	// As hot with 52% (> threshold_down 50%): should NOT demote
	cfg.Monitors[0].Board = "hot"
	svc2 := NewService(store, cfg)
	svc2.Evaluate(context.Background())

	_, ok = svc2.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for hot monitor at 52% (between thresholds)")
	}
}

func TestEvaluate_DualThreshold_PreviousOverridePreserved(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "mid", Service: "cc", Channel: "vip"}

	// 52% availability: between threshold_down(50%) and threshold_up(55%)
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, 100)
	for i := 0; i < 52; i++ {
		records[i] = &storage.ProbeRecord{Status: 1, Timestamp: ts}
	}
	for i := 52; i < 100; i++ {
		records[i] = &storage.ProbeRecord{Status: 0, Timestamp: ts}
	}
	store.history[key] = records

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "mid", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)

	// First: demote with 0% availability
	store.history[key] = makeRecords(0, 100)
	svc.Evaluate(context.Background())
	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatal("expected demote to secondary")
	}

	// Second: availability recovers to 52% (in buffer zone)
	// Override should be preserved — still secondary
	store.history[key] = records
	svc.Evaluate(context.Background())
	ov, ok = svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Errorf("expected override preserved as secondary in buffer zone, got ok=%v board=%s", ok, ov.Board)
	}
}

func TestEvaluate_MinProbes_Skip(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "new", Service: "cc", Channel: "vip"}

	// Only 5 records < min_probes=10: should skip
	store.history[key] = makeRecords(0, 5)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "new", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override when probes < min_probes")
	}
}

func TestEvaluate_ColdExcluded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "cold", Service: "cc", Channel: "vip"}

	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "cold", Service: "cc", Channel: "vip", Board: "cold"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for cold board monitors")
	}
}

func TestEvaluate_DisabledClears(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// Verify override exists
	_, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override after evaluate")
	}

	// Disable auto_move → UpdateConfig should clear overrides
	cfg2 := *cfg
	cfg2.Boards.AutoMove.Enabled = false
	svc.UpdateConfig(&cfg2)

	_, ok = svc.GetBoardOverride(key)
	if ok {
		t.Error("expected overrides cleared after disabling auto_move")
	}
}

func TestUpdateConfig_PurgesStaleOverrides(t *testing.T) {
	store := newMockStorage()

	hotKey := storage.MonitorKey{Provider: "hot-provider", Service: "cc", Channel: "vip"}
	coldKey := storage.MonitorKey{Provider: "cold-provider", Service: "cc", Channel: "vip"}
	disabledKey := storage.MonitorKey{Provider: "disabled-provider", Service: "cc", Channel: "vip"}
	removedKey := storage.MonitorKey{Provider: "removed-provider", Service: "cc", Channel: "vip"}

	store.history[hotKey] = makeRecords(0, 20)
	store.history[coldKey] = makeRecords(0, 20)
	store.history[disabledKey] = makeRecords(0, 20)
	store.history[removedKey] = makeRecords(0, 20)

	// 初始配置：所有通道在 hot 板
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hot-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "cold-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "disabled-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "removed-provider", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// 验证：4 个通道都有 override（全部被降级到 secondary）
	for _, k := range []storage.MonitorKey{hotKey, coldKey, disabledKey, removedKey} {
		if _, ok := svc.GetBoardOverride(k); !ok {
			t.Fatalf("expected override for %s after initial evaluate", k.Provider)
		}
	}

	// 新配置：cold-provider 移入冷板，disabled-provider 被禁用，removed-provider 被移除
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hot-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "cold-provider", Service: "cc", Channel: "vip", Board: "cold"},
			{Provider: "disabled-provider", Service: "cc", Channel: "vip", Board: "hot", Disabled: true},
			// removed-provider 不再出现
		},
	}
	svc.UpdateConfig(cfg2)

	// hot-provider: 仍在 hot 板，override 应保留
	if _, ok := svc.GetBoardOverride(hotKey); !ok {
		t.Error("hot-provider override should be preserved")
	}

	// cold-provider: 已移入冷板，override 应被清除
	if _, ok := svc.GetBoardOverride(coldKey); ok {
		t.Error("cold-provider override should be purged after board changed to cold")
	}

	// disabled-provider: 已被禁用，override 应被清除
	if _, ok := svc.GetBoardOverride(disabledKey); ok {
		t.Error("disabled-provider override should be purged after being disabled")
	}

	// removed-provider: 已从配置移除，override 应被清除
	if _, ok := svc.GetBoardOverride(removedKey); ok {
		t.Error("removed-provider override should be purged after being removed from config")
	}
}

func TestUpdateConfig_PurgesHiddenOverrides(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "hidden", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hidden", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); !ok {
		t.Fatal("expected override after evaluate")
	}

	// 隐藏该通道
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hidden", Service: "cc", Channel: "vip", Board: "hot", Hidden: true},
		},
	}
	svc.UpdateConfig(cfg2)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Error("hidden monitor override should be purged")
	}
}

func TestUpdateConfig_NoOverrides_Noop(t *testing.T) {
	store := newMockStorage()
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "s", Channel: "c", Board: "cold"},
		},
	}

	svc := NewService(store, cfg)
	// 无 override 时 UpdateConfig 不应 panic
	svc.UpdateConfig(cfg)

	if overrides := svc.Overrides(); overrides != nil {
		t.Error("expected nil overrides when no prior overrides exist")
	}
}

func TestUpdateConfig_PurgesParentOverrides(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "p", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); !ok {
		t.Fatal("expected override after evaluate")
	}

	// 通道变为子通道（设置了 Parent），不再是根通道
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "cc", Channel: "vip", Board: "hot", Parent: "other/cc/root"},
		},
	}
	svc.UpdateConfig(cfg2)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Error("child monitor override should be purged after gaining parent")
	}
}

// TestEvaluate_ExpiredChannel_HealthyStaysOnBoardLevelDowngraded 验证解耦后的新语义：
// 到期不再强制移入备板——板块位置完全由可用率决定。
// 到期 + 可用率健康（100%，configBoard=hot）→ 板块保持 hot，仅 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredChannel_HealthyStaysOnBoardLevelDowngraded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}

	// 100% 可用率：应保持热板，不因到期强制移入备板
	store.history[key] = makeRecords(1, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:     "expired",
				Service:      "cc",
				Channel:      "vip",
				Board:        "hot",
				SponsorLevel: config.SponsorLevelBackbone,
				ExpiresAt:    yesterday,
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	// 可能有 override（仅含 SponsorLevel=pulse），但板块不得是 secondary 或 cold
	if ok {
		if ov.Board == "secondary" || ov.Board == "cold" {
			t.Errorf("到期健康通道不应被降至 %s（板块应由可用率决定，不由到期决定）", ov.Board)
		}
		if ov.SponsorLevel != config.SponsorLevelPulse {
			t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
		}
	} else {
		// 无 override 也可接受（level 已是 pulse 无需降级时），但本 case SponsorLevel=backbone，必须有降级
		t.Error("expected override with SponsorLevel=pulse for expired backbone channel")
	}
}

func TestEvaluate_NotYetExpired_NoExpiryOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "active", Service: "cc", Channel: "vip"}

	// 100% green → should not be demoted by availability or expiry
	store.history[key] = makeRecords(1, 20)

	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:     "active",
				Service:      "cc",
				Channel:      "vip",
				Board:        "hot",
				SponsorLevel: config.SponsorLevelBackbone,
				ExpiresAt:    tomorrow,
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for not-yet-expired channel")
	}
}

func TestEvaluate_ExpiresToday_StillValid(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "today", Service: "cc", Channel: "vip"}

	store.history[key] = makeRecords(1, 20)

	today := time.Now().UTC().Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:     "today",
				Service:      "cc",
				Channel:      "vip",
				Board:        "hot",
				SponsorLevel: config.SponsorLevelCore,
				ExpiresAt:    today,
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for channel expiring today (still valid)")
	}
}

// TestEvaluate_ExpiredAndAvailability_Coexist 验证到期与可用率独立工作：
// 到期通道（可用率健康）→ 板块保持，仅 SponsorLevel 降级；
// 可用率差通道（未到期）→ 板块由可用率降级，SponsorLevel 不变。
func TestEvaluate_ExpiredAndAvailability_Coexist(t *testing.T) {
	store := newMockStorage()
	expiredKey := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}
	badKey := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}

	store.history[expiredKey] = makeRecords(1, 20) // 可用率 100%，但已到期
	store.history[badKey] = makeRecords(0, 20)     // 可用率 0%，未到期

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "expired", Service: "cc", Channel: "vip", Board: "hot", SponsorLevel: config.SponsorLevelBeacon, ExpiresAt: yesterday},
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// 到期通道：可用率健康，板块不降 secondary；但 SponsorLevel 降为 pulse
	ov, ok := svc.GetBoardOverride(expiredKey)
	if !ok {
		t.Fatal("expected override for expired channel (SponsorLevel=pulse)")
	}
	if ov.Board == "secondary" {
		t.Errorf("expired healthy channel should NOT be demoted to secondary by expiry alone, got board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired channel, got %s", ov.SponsorLevel)
	}

	// 可用率差通道（未到期）：板块由可用率降至 secondary，SponsorLevel 不变
	ov2, ok := svc.GetBoardOverride(badKey)
	if !ok {
		t.Fatal("expected override for bad availability channel")
	}
	if ov2.Board != "secondary" {
		t.Errorf("bad availability: expected board=secondary (availability-driven), got %s", ov2.Board)
	}
	if ov2.SponsorLevel != "" {
		t.Errorf("bad availability: expected empty sponsor_level (no downgrade), got %s", ov2.SponsorLevel)
	}
}

func TestEvaluate_ChildMonitorsExcluded(t *testing.T) {
	store := newMockStorage()
	childKey := storage.MonitorKey{Provider: "p", Service: "s", Channel: "c", Model: "child-model"}
	store.history[childKey] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "s", Channel: "c", Model: "child-model", Parent: "p/s/c", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(childKey)
	if ok {
		t.Error("expected no override for child monitors (have parent)")
	}
}

// === 自动冷板测试 ===

func TestEvaluate_AutoCold_DemotesToCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected cold override")
	}
	if ov.Board != "cold" {
		t.Fatalf("expected board=cold, got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Fatal("expected ColdReason to be populated")
	}
}

func TestEvaluate_AutoCold_Sticky(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "sticky", Service: "cc", Channel: "vip"}
	// 即使可用率恢复到 100%，sticky cold 也不应被清除
	store.history[key] = makeRecords(1, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "sticky", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	// 预注入 cold override
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "之前自动冷板"},
	})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected sticky cold override to be preserved")
	}
	if ov.Board != "cold" {
		t.Fatalf("expected board=cold, got %s", ov.Board)
	}
	if ov.ColdReason != "之前自动冷板" {
		t.Fatalf("expected original ColdReason, got %q", ov.ColdReason)
	}
}

func TestEvaluate_AutoCold_MinProbesProtection(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "new", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 5) // 不足 min_probes

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "new", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Fatal("expected no override: min_probes not met")
	}
}

func TestEvaluate_AutoColdExempt_SkipsColdDecision(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "exempt", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%，但已 exempt

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "exempt", Service: "cc", Channel: "vip", Board: "hot", AutoColdExempt: true},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override (should demote to secondary, not cold)")
	}
	if ov.Board == "cold" {
		t.Fatal("auto_cold_exempt should prevent cold board")
	}
	if ov.Board != "secondary" {
		t.Fatalf("expected board=secondary, got %s", ov.Board)
	}
}

func TestEvaluate_AutoMoveExempt_NoOverrideProduced(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "manual", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%，理论上会触发冷板

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "manual", Service: "cc", Channel: "vip", Board: "hot", AutoMoveExempt: true},
		},
	}

	svc := NewService(store, cfg)
	// 预先注入一个旧的 secondary override，验证 exempt 会将其清除
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "secondary"},
	})

	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("auto_move_exempt 通道不应产生任何 availability-based override")
	}
}

// TestEvaluate_AutoMoveExempt_ExpiryLevelDowngradeApplies 验证解耦后新语义：
// auto_move_exempt 跳过板块移动，但到期的 SponsorLevel 降级仍生效。
// 期望：override 含 SponsorLevel=pulse，Board 为空（不产生 secondary）。
func TestEvaluate_AutoMoveExempt_ExpiryLevelDowngradeApplies(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}
	// 可用率 100%（可用率逻辑不触发），但通道已到期且等级高于 pulse
	store.history[key] = makeRecords(1, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:       "expired",
				Service:        "cc",
				Channel:        "vip",
				Board:          "hot",
				SponsorLevel:   config.SponsorLevelBackbone,
				AutoMoveExempt: true,
				ExpiresAt:      "2020-01-01", // 明确过期
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("auto_move_exempt 到期通道应产生 SponsorLevel=pulse override")
	}
	// 板块不因到期移动（auto_move_exempt 阻止板块评估）
	if ov.Board == "secondary" {
		t.Errorf("auto_move_exempt 应阻止板块降级，不应产生 secondary，实际 board=%s", ov.Board)
	}
	// 但 SponsorLevel 仍应降级
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

func TestUpdateConfig_AutoMoveExemptPurgesExistingOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "manual", Service: "cc", Channel: "vip"}

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "manual", Service: "cc", Channel: "vip", Board: "hot", AutoMoveExempt: true},
		},
	}

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "secondary"},
	})

	svc.UpdateConfig(cfg)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("热更新后 auto_move_exempt 通道的旧 override 应被 purgeStaleOverrides 清除")
	}
}

// expiredAutoMoveCfg 构造一个启用冷板阈值的到期测试配置。
func expiredAutoMoveCfg(monitors ...config.ServiceConfig) *config.AppConfig {
	return &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors:          monitors,
	}
}

// TestEvaluate_ExpiredAndDead_MovesToCold 验证：
// 已到期 且 7 天可用率低于 threshold_cold 且探测数充足 → 移入冷板（availability-driven），
// 同时 SponsorLevel 降为 pulse（解耦后两者独立叠加）。
func TestEvaluate_ExpiredAndDead_MovesToCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "deadexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0% < threshold_cold=10%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "deadexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for expired+dead channel")
	}
	if ov.Board != "cold" {
		t.Fatalf("expected board=cold (availability < threshold_cold)，got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Fatal("expected ColdReason to be populated")
	}
	// 到期降级 SponsorLevel 合并到同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired backbone channel, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_MinProbesSkipsBoard 验证解耦后新语义：
// 已到期但探测数不足 min_probes → 无法判断可用率，跳过板块移板；SponsorLevel 仍降为 pulse。
func TestEvaluate_ExpiredAndDead_MinProbesSkipsBoard(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "newexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 5) // 探测不足 min_probes=10

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "newexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	// 探测不足时跳过板块移板，但仍应有 SponsorLevel=pulse override（到期降级等级）
	if !ok {
		t.Fatal("expected override with SponsorLevel=pulse for expired channel")
	}
	// 探测不足不应产生 board override（不应因无法判断可用率而强制 secondary）
	if ov.Board == "secondary" || ov.Board == "cold" {
		t.Errorf("min_probes 不足时不应产生板块 override，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredButHealthy_BoardPreservedLevelDowngraded 验证解耦后新语义：
// 已到期但 7 天可用率健康（100%，高于所有阈值）→ 板块不降 secondary，仅 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredButHealthy_BoardPreservedLevelDowngraded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "healthyexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 可用率 100%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "healthyexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	// 必须有 override（含 SponsorLevel=pulse），但板块不得是 secondary 或 cold
	if !ok {
		t.Fatal("expected override with SponsorLevel=pulse for expired backbone channel")
	}
	if ov.Board == "secondary" || ov.Board == "cold" {
		t.Errorf("到期健康通道板块不应因到期降级，期望不是 secondary/cold，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_AutoColdExemptDemotesSecondary 验证解耦后语义：
// auto_cold_exempt + 到期 + 可用率 0% → 走可用率降级路径（hot→secondary），同时降级 SponsorLevel。
// auto_cold_exempt 阻止冷板，但不阻止 secondary。
func TestEvaluate_ExpiredAndDead_AutoColdExemptDemotesSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "exemptexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "exemptexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday, AutoColdExempt: true,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for auto_cold_exempt expired dead channel")
	}
	if ov.Board == "cold" {
		t.Fatal("auto_cold_exempt 应阻止冷板")
	}
	// 可用率 0% < threshold_down=50%，应触发 hot→secondary
	if ov.Board != "secondary" {
		t.Errorf("可用率降级应产生 secondary，实际 board=%s", ov.Board)
	}
	// 到期降级 SponsorLevel 合并进同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_AutoMoveExemptBoardUntouched 验证解耦后语义：
// auto_move_exempt + 到期 → 跳过板块评估，仅降级 SponsorLevel（Board 为空）。
func TestEvaluate_ExpiredAndDead_AutoMoveExemptBoardUntouched(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "moveexemptexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%（不影响，因为 exempt 跳过板块评估）

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "moveexemptexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday, AutoMoveExempt: true,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("auto_move_exempt 到期通道应产生 SponsorLevel=pulse override")
	}
	// auto_move_exempt 跳过板块评估，不产生 board
	if ov.Board != "" {
		t.Errorf("auto_move_exempt 不应产生板块 override，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_BoundaryNotCold 锁定严格 `<`：
// 可用率正好等于 threshold_cold 时不冷板；到期通道走普通 availability 路径，
// 10%==threshold_cold 且低于 threshold_down(50%) → hot→secondary（availability-driven）。
func TestEvaluate_ExpiredAndDead_BoundaryNotCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "boundaryexpired", Service: "cc", Channel: "vip"}
	// 全黄 + degraded_weight=0.10 → 可用率恰为 10.0%，等于 threshold_cold。
	store.history[key] = makeRecords(2, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "boundaryexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})
	cfg.DegradedWeight = 0.10

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for boundary expired channel")
	}
	// 可用率==threshold_cold 不触发冷板（严格 <）；10% < threshold_down(50%) → hot→secondary
	if ov.Board == "cold" {
		t.Fatalf("可用率==threshold_cold 不应冷板，实际 board=%s", ov.Board)
	}
	if ov.Board != "secondary" {
		t.Fatalf("可用率 10%% < threshold_down 50%%，期望 board=secondary（availability-driven），实际 %s", ov.Board)
	}
	// 到期降级 SponsorLevel 合并到同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired backbone channel, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_HistoryErrorAppliesLevelDowngrade 验证解耦后新语义：
// 历史查询失败时无法判定可用率 → 无板块 override；但到期的 SponsorLevel 降级仍应生效。
func TestEvaluate_ExpiredAndDead_HistoryErrorAppliesLevelDowngrade(t *testing.T) {
	store := newMockStorage()
	store.batchErr = errors.New("db unavailable")
	key := storage.MonitorKey{Provider: "errexpired", Service: "cc", Channel: "vip"}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "errexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("查询失败时到期项的 SponsorLevel 降级仍应产生 override")
	}
	// 查询失败无法判断可用率，不应产生板块 override
	if ov.Board == "secondary" || ov.Board == "cold" {
		t.Errorf("历史查询失败不应产生板块 override，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_ColdExemptBreaksStickyToSecondary 验证：
// 已是 sticky cold 的到期项，设置 auto_cold_exempt 后打破 sticky；
// 可用率 0% < threshold_down → availability-driven hot→secondary；同时 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredAndDead_ColdExemptBreaksStickyToSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "stickyexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "stickyexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday, AutoColdExempt: true,
	})

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "之前自动冷板"},
	})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override after exempt breaks sticky cold")
	}
	if ov.Board == "cold" {
		t.Fatal("auto_cold_exempt 应打破 sticky cold")
	}
	// auto_cold_exempt 打破 sticky 后，按 configBoard=hot 评估：0% < threshold_down → secondary
	if ov.Board != "secondary" {
		t.Fatalf("0%% 可用率应 hot→secondary，期望 board=secondary，实际 %s", ov.Board)
	}
	// 到期降级 SponsorLevel 合并到同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired backbone channel, got %s", ov.SponsorLevel)
	}
}

func TestUpdateConfig_AutoColdExemptPurgesColdOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "recover", Service: "cc", Channel: "vip"}

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "recover", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "auto cold"},
	})

	// 热更新：设置 auto_cold_exempt
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    cfg.DegradedWeight,
		BatchQueryMaxKeys: cfg.BatchQueryMaxKeys,
		Monitors: []config.ServiceConfig{
			{Provider: "recover", Service: "cc", Channel: "vip", Board: "hot", AutoColdExempt: true},
		},
	}
	svc.UpdateConfig(cfg2)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("expected cold override to be purged by auto_cold_exempt")
	}
}

func TestOnOverrideChange_CalledOnColdTransition(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "cb", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "cb", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	called := make(chan struct{}, 1)
	svc := NewService(store, cfg)
	svc.SetOnOverrideChange(func() {
		select {
		case called <- struct{}{}:
		default:
		}
	})
	svc.Evaluate(context.Background())

	select {
	case <-called:
		// ok
	case <-time.After(time.Second):
		t.Fatal("expected onOverrideChange callback to fire")
	}
}

func TestIsCold_PSCPropagation(t *testing.T) {
	svc := NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c", Model: "root"}: {Board: "cold", ColdReason: "test"},
	})

	// 同 PSC 的子模型也应被判定为 cold
	if !svc.IsCold(storage.MonitorKey{Provider: "p", Service: "s", Channel: "c", Model: "child"}) {
		t.Fatal("expected IsCold to propagate to child model via PSC")
	}
	// 不同 PSC 不应被判定
	if svc.IsCold(storage.MonitorKey{Provider: "p", Service: "s", Channel: "other"}) {
		t.Fatal("expected IsCold to not propagate to different channel")
	}
}

func TestApplyOverrides_ColdReasonPropagation(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {Board: "cold", ColdReason: "auto cold test"},
	}

	monitors := []config.ServiceConfig{
		{Provider: "p", Service: "s", Channel: "c", Board: "hot"},
		{Provider: "p", Service: "s", Channel: "c", Model: "gpt-4o", Parent: "p/s/c", Board: "hot"},
	}

	result := ApplyOverrides(monitors, overrides)

	if result[0].Board != "cold" || result[0].ColdReason != "auto cold test" {
		t.Fatalf("root: board=%s cold_reason=%q", result[0].Board, result[0].ColdReason)
	}
	if result[1].Board != "cold" || result[1].ColdReason != "auto cold test" {
		t.Fatalf("child: board=%s cold_reason=%q", result[1].Board, result[1].ColdReason)
	}
}

func TestApplyOverrides_ClearsColdReasonOnNonCold(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {Board: "secondary"},
	}

	monitors := []config.ServiceConfig{
		{Provider: "p", Service: "s", Channel: "c", Board: "cold", ColdReason: "旧原因"},
	}

	result := ApplyOverrides(monitors, overrides)

	if result[0].Board != "secondary" {
		t.Fatalf("board=%s, want secondary", result[0].Board)
	}
	if result[0].ColdReason != "" {
		t.Fatalf("cold_reason=%q, want empty", result[0].ColdReason)
	}
}

// === 到期解耦新增测试 ===

// TestEvaluate_ExpiredAndCold_ColdWithLevelDowngrade 验证解耦后的叠加语义：
// 到期 + 可用率低于 threshold_cold（探测充足）→ availability-driven 冷板 + SponsorLevel=pulse 叠加。
func TestEvaluate_ExpiredAndCold_ColdWithLevelDowngrade(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "coldexpired", Service: "cc", Channel: "vip"}
	// 全红 20 条 → 可用率 0% < threshold_cold=10%，探测充足
	store.history[key] = makeRecords(0, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "coldexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBeacon, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for expired+low-availability channel")
	}
	// availability < threshold_cold → 冷板（availability-driven）
	if ov.Board != "cold" {
		t.Errorf("expected board=cold (availability-driven), got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Error("expected ColdReason to be populated")
	}
	// 到期降级 SponsorLevel 合并到同一 override（board 和 level 独立，互不覆盖）
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired beacon channel, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAlreadyPulse_NoSponsorDowngrade 验证：
// 到期但 SponsorLevel 已是 pulse → 不产生 sponsor 降级（不"升级"），且可用率健康不产生板块 override。
func TestEvaluate_ExpiredAlreadyPulse_NoSponsorDowngrade(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "pulsexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 可用率 100%，不触发板块降级

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "pulsexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelPulse, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// 可用率 100% 且已是 pulse 等级 → 无任何 override
	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("已是 pulse 等级的到期通道（可用率健康）不应产生任何 override")
	}
}

// TestEvaluate_ExpiredStickyNotExempt_ColdWithLevelDowngrade 验证排序依赖：
// expiry check 必须在 sticky-cold 块之前。
// 非 exempt 的 sticky cold 到期通道：sticky cold 保留，且 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredStickyNotExempt_ColdWithLevelDowngrade(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "stickycoldexpired", Service: "cc", Channel: "vip"}
	// 即使可用率健康，sticky cold 也不被清除
	store.history[key] = makeRecords(1, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "stickycoldexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelCore, ExpiresAt: yesterday,
		// AutoColdExempt=false（默认）：sticky cold 会保留
	})

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "之前自动冷板"},
	})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override (sticky cold should be preserved)")
	}
	// sticky cold 保留
	if ov.Board != "cold" {
		t.Errorf("expected board=cold (sticky), got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Error("expected ColdReason to be preserved")
	}
	// 到期降级 SponsorLevel 必须叠加（expiry check 在 sticky-cold 前记录，applySponsorDowngrades 在 return 前合并）
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expired sticky-cold channel must get SponsorLevel=pulse, got %s", ov.SponsorLevel)
	}
}
