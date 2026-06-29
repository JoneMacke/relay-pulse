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
func (m *mockStorage) GetLatestByModelID(_ string) (*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryByModelID(_ string, _ time.Time) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryWithLimitByModelID(_ string, _ time.Time, _ int) ([]*storage.ProbeRecord, error) {
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
func (m *mockStorage) GetLatestBatchByModelID(_ []storage.ProbeHistoryKey) (map[storage.ProbeHistoryKey]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryBatchByModelID(_ []storage.ProbeHistoryKey, _ time.Time) (map[storage.ProbeHistoryKey][]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) MigrateChannelData(_ []storage.ChannelMigrationMapping) error { return nil }
func (m *mockStorage) BackfillProbeHistoryModelIDs(_ []storage.ModelIDMigrationMapping) error {
	return nil
}
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

func TestEvaluate_ExpiredChannel_DemotedAndDowngraded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}

	// 即使可用率 100%，到期后也应降级
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
	if !ok {
		t.Fatal("expected override for expired channel")
	}
	if ov.Board != "secondary" {
		t.Errorf("expected board=secondary, got %s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
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

func TestEvaluate_ExpiredAndAvailability_Coexist(t *testing.T) {
	store := newMockStorage()
	expiredKey := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}
	badKey := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}

	store.history[expiredKey] = makeRecords(1, 20) // good availability but expired
	store.history[badKey] = makeRecords(0, 20)     // bad availability

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

	// Expired channel: board=secondary, sponsor_level=pulse
	ov, ok := svc.GetBoardOverride(expiredKey)
	if !ok {
		t.Fatal("expected override for expired channel")
	}
	if ov.Board != "secondary" || ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expired: expected board=secondary+level=pulse, got board=%s level=%s", ov.Board, ov.SponsorLevel)
	}

	// Bad availability channel: board=secondary, sponsor_level unchanged (empty)
	ov2, ok := svc.GetBoardOverride(badKey)
	if !ok {
		t.Fatal("expected override for bad availability channel")
	}
	if ov2.Board != "secondary" {
		t.Errorf("bad availability: expected board=secondary, got %s", ov2.Board)
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

func TestEvaluate_AutoMoveExempt_ExpiryStillApplies(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}
	// 可用率 100%（因此可用率逻辑不会触发），但通道已到期
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
				AutoMoveExempt: true,
				ExpiresAt:      "2020-01-01", // 明确过期
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("已过期通道应产生 secondary override，即使 auto_move_exempt=true")
	}
	if ov.Board != "secondary" {
		t.Fatalf("已过期通道期望 board=secondary，实际得到 %s", ov.Board)
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

// TestEvaluate_ExpiredAndDead_MovesToCold 验证方案2核心语义：
// 已到期 且 7 天可用率低于 threshold_cold 且探测数充足 → 直接移入冷板（停探），而非停留在 secondary。
func TestEvaluate_ExpiredAndDead_MovesToCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "deadexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

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
		t.Fatalf("expected board=cold (可用率冷板优先于到期降级)，got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Fatal("expected ColdReason to be populated")
	}
}

// TestEvaluate_ExpiredAndDead_MinProbesFallsBackToSecondary 验证：
// 已到期但探测数不足 min_probes 时，无法判定冷板，回退为到期降级 secondary。
func TestEvaluate_ExpiredAndDead_MinProbesFallsBackToSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "newexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 5) // 可用率 0% 但探测不足

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "newexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected secondary override for expired channel with insufficient probes")
	}
	if ov.Board != "secondary" {
		t.Fatalf("expected board=secondary (探测不足回退到期降级)，got %s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredButHealthy_StaysSecondary 验证：
// 已到期但 7 天可用率仍达标（>= threshold_cold）→ 不冷板，仍按到期降级 secondary+pulse。
func TestEvaluate_ExpiredButHealthy_StaysSecondary(t *testing.T) {
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
	if !ok {
		t.Fatal("expected secondary override for healthy expired channel")
	}
	if ov.Board != "secondary" {
		t.Fatalf("expected board=secondary, got %s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_ExemptStaysSecondary 验证：
// 即使到期且可用率为 0%，auto_cold_exempt / auto_move_exempt 都应阻止冷板，仍走到期降级 secondary。
func TestEvaluate_ExpiredAndDead_ExemptStaysSecondary(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.ServiceConfig)
	}{
		{"auto_cold_exempt", func(m *config.ServiceConfig) { m.AutoColdExempt = true }},
		{"auto_move_exempt", func(m *config.ServiceConfig) { m.AutoMoveExempt = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockStorage()
			key := storage.MonitorKey{Provider: "exemptexpired", Service: "cc", Channel: "vip"}
			store.history[key] = makeRecords(0, 20) // 可用率 0%

			yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
			m := config.ServiceConfig{
				Provider: "exemptexpired", Service: "cc", Channel: "vip", Board: "hot",
				SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
			}
			tc.mutate(&m)
			cfg := expiredAutoMoveCfg(m)

			svc := NewService(store, cfg)
			svc.Evaluate(context.Background())

			ov, ok := svc.GetBoardOverride(key)
			if !ok {
				t.Fatal("expected secondary override for exempt expired channel")
			}
			if ov.Board != "secondary" {
				t.Fatalf("%s 应阻止冷板，期望 board=secondary，实际 %s", tc.name, ov.Board)
			}
		})
	}
}

// TestEvaluate_ExpiredAndDead_BoundaryNotCold 锁定严格 `<`：
// 可用率正好等于 threshold_cold 时不冷板，回退到期降级 secondary。
func TestEvaluate_ExpiredAndDead_BoundaryNotCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "boundaryexpired", Service: "cc", Channel: "vip"}
	// 全黄 + degraded_weight=0.10 → 可用率恰为 10.0%，等于 threshold_cold。
	store.history[key] = makeRecords(2, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "boundaryexpired", Service: "cc", Channel: "vip", Board: "hot",
		ExpiresAt: yesterday,
	})
	cfg.DegradedWeight = 0.10

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for boundary expired channel")
	}
	if ov.Board != "secondary" {
		t.Fatalf("可用率==threshold_cold 应不冷板，期望 board=secondary，实际 %s", ov.Board)
	}
}

// TestEvaluate_ExpiredAndDead_HistoryErrorFallsBackToSecondary 验证：
// 历史查询失败时无法判定可用率，到期项回退为 secondary，而非丢失 override。
func TestEvaluate_ExpiredAndDead_HistoryErrorFallsBackToSecondary(t *testing.T) {
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
		t.Fatal("查询失败时到期项应回退为 secondary override，而非丢失")
	}
	if ov.Board != "secondary" {
		t.Fatalf("expected board=secondary on history error, got %s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_ColdExemptBreaksStickyToSecondary 验证：
// 已是 sticky cold 的到期项，设置 auto_cold_exempt 后应打破 sticky，转为到期降级 secondary。
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
	if ov.Board != "secondary" {
		t.Fatalf("auto_cold_exempt 应打破 sticky 并按到期降级，期望 board=secondary，实际 %s", ov.Board)
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
