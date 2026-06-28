package api

import (
	"testing"
	"time"

	"monitor/internal/config"
	"monitor/internal/storage"
)

// TestBuildMonitorResultExposesChannelID 确认 buildMonitorResult 把监测行的运行时
// ChannelID 填进 MonitorResult.channel_id（跨产品 join 锚经 /api/status 暴露）。
func TestBuildMonitorResultExposesChannelID(t *testing.T) {
	h := &Handler{config: &config.AppConfig{}}
	task := config.ServiceConfig{
		Provider:  "acme",
		Service:   "cc",
		Channel:   "vip",
		Model:     "Opus",
		ChannelID: "ch_11111111-1111-4111-8111-111111111111",
	}
	result := h.buildMonitorResult(task, nil, []*storage.ProbeRecord{}, time.Now(), "24h", 0.7, nil, false)
	if result.ChannelID != task.ChannelID {
		t.Errorf("MonitorResult.ChannelID = %q, want %q", result.ChannelID, task.ChannelID)
	}
}
