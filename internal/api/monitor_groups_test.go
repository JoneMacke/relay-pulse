package api

import (
	"testing"

	"monitor/internal/config"
)

// TestBuildMonitorGroupFromParentExposesChannelID 确认 group 把 parent 的运行时
// ChannelID 透出到 MonitorGroup.channel_id——多模型通道走 group 路径，质量列
// 跨产品 join 需要组级 channel_id（与 MonitorResult.channel_id 对齐，Plan A）。
func TestBuildMonitorGroupFromParentExposesChannelID(t *testing.T) {
	parent := config.ServiceConfig{
		Provider:  "acme",
		Service:   "cc",
		Channel:   "vip",
		Model:     "Opus",
		ChannelID: "ch_11111111-1111-4111-8111-111111111111",
	}
	g := buildMonitorGroupFromParent(parent, false, false)
	if g.ChannelID != parent.ChannelID {
		t.Errorf("MonitorGroup.ChannelID = %q, want %q", g.ChannelID, parent.ChannelID)
	}
}
