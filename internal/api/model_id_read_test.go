package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"monitor/internal/config"
	"monitor/internal/storage"
)

// newStoreBackedHandler 构建一个挂真 SQLite 存储的最小 Handler，供"按 model_id 读"
// 的端到端测试驱动状态展示读路径（serial/concurrent/batch + status/query）。
func newStoreBackedHandler(t *testing.T) (*Handler, storage.Storage) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	h := &Handler{
		storage: store,
		config:  &config.AppConfig{DegradedWeight: 0.7},
	}
	return h, store
}

// saveRecordAt 写一条绿色探测记录，带稳定 model_id 与（任意）展示名。
func saveRecordAt(t *testing.T, store storage.Storage, modelID, provider, service, channel, model string, ts int64) {
	t.Helper()
	rec := &storage.ProbeRecord{
		Provider:  provider,
		Service:   service,
		Channel:   channel,
		Model:     model,
		ModelID:   modelID,
		Status:    1,
		SubStatus: storage.SubStatusNone,
		HttpCode:  200,
		Latency:   123,
		Timestamp: ts,
	}
	if err := store.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
}

// TestStatusReadSurvivesRename 是本任务的核心证明：
// 历史记录以旧展示名 + 稳定 model_id 写入；监测行改名后展示名变（NewName），
// 但 model_id 不变。状态展示读路径必须凭 model_id 把历史接回来，而不是因展示名
// 不匹配而读空。覆盖 serial / concurrent / batch 三条读路径。
func TestStatusReadSurvivesRename(t *testing.T) {
	const (
		modelID = "md_r"
		oldName = "OldName"
		newName = "NewName"
	)

	now := time.Now()
	since := now.Add(-90 * time.Minute)
	endTime := now

	// 监测行：稳定 model_id 不变，展示名已是改名后的 NewName。
	monitors := []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: newName, ModelID: modelID},
	}

	assertFound := func(t *testing.T, results []MonitorResult) {
		t.Helper()
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		res := results[0]
		if res.Current == nil {
			t.Fatalf("Current is nil — history did not survive rename (model_id 读路径未生效)")
		}
		if res.Current.Status != 1 {
			t.Errorf("Current.Status = %d, want 1", res.Current.Status)
		}
		// 展示名应来自配置（NewName），而历史是凭 model_id 查到的。
		if res.Model != newName {
			t.Errorf("MonitorResult.Model = %q, want %q", res.Model, newName)
		}
		// timeline 至少有一个非缺失 bucket（证明 history 命中）。
		hasData := false
		for _, p := range res.Timeline {
			if p.Status != -1 {
				hasData = true
				break
			}
		}
		if !hasData {
			t.Errorf("timeline has no populated bucket — history not joined via model_id")
		}
	}

	t.Run("serial", func(t *testing.T) {
		h, store := newStoreBackedHandler(t)
		saveRecordAt(t, store, modelID, "acme", "cc", "vip", oldName, now.Unix())

		results, err := h.getStatusSerial(context.Background(), monitors, since, endTime, "90m", 0.7, nil, false)
		if err != nil {
			t.Fatalf("getStatusSerial: %v", err)
		}
		assertFound(t, results)
	})

	t.Run("concurrent", func(t *testing.T) {
		h, store := newStoreBackedHandler(t)
		saveRecordAt(t, store, modelID, "acme", "cc", "vip", oldName, now.Unix())

		results, err := h.getStatusConcurrent(context.Background(), monitors, since, endTime, "90m", 0.7, nil, 4, false)
		if err != nil {
			t.Fatalf("getStatusConcurrent: %v", err)
		}
		assertFound(t, results)
	})

	t.Run("batch", func(t *testing.T) {
		h, store := newStoreBackedHandler(t)
		saveRecordAt(t, store, modelID, "acme", "cc", "vip", oldName, now.Unix())

		results, err := h.getStatusBatch(context.Background(), monitors, since, endTime, "7d", 0.7, nil, false, false)
		if err != nil {
			t.Fatalf("getStatusBatch: %v", err)
		}
		assertFound(t, results)
	})
}

// TestExecuteStatusQueryReadsByModelID 证明轻量状态查询（/api/status/query 与
// POST /api/status/batch 的内核）也走 model_id 读路径：历史以旧展示名写入、
// 配置用新展示名，查询仍能返回 up。
func TestExecuteStatusQueryReadsByModelID(t *testing.T) {
	const (
		modelID = "md_q"
		oldName = "OldName"
		newName = "NewName"
	)
	h, store := newStoreBackedHandler(t)
	h.config.Monitors = []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: newName, ModelID: modelID},
	}
	saveRecordAt(t, store, modelID, "acme", "cc", "vip", oldName, time.Now().Unix())

	resp, err := h.executeStatusQuery(context.Background(), []StatusQuery{{Provider: "acme"}})
	if err != nil {
		t.Fatalf("executeStatusQuery: %v", err)
	}
	if len(resp.Results) != 1 || len(resp.Results[0].Services) != 1 {
		t.Fatalf("unexpected results shape: %+v", resp.Results)
	}
	channels := resp.Results[0].Services[0].Channels
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	if channels[0].Status != "up" {
		t.Errorf("channel status = %q, want %q (history not joined via model_id)", channels[0].Status, "up")
	}
}
