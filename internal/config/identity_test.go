package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMonitorFileRoundTripPreservesIDs 确认 channel_id（文件级）与 model_id（行级）
// 经 YAML unmarshal→marshal 往返不丢失，是稳定 id 落盘的底线保证。
func TestMonitorFileRoundTripPreservesIDs(t *testing.T) {
	src := `metadata:
  revision: 1
  channel_id: ch_11111111-1111-4111-8111-111111111111
monitors:
  - provider: acme
    service: cc
    channel: vip
    model: Opus
    model_id: md_22222222-2222-4222-8222-222222222222
`
	var f MonitorFile
	if err := yaml.Unmarshal([]byte(src), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Metadata.ChannelID != "ch_11111111-1111-4111-8111-111111111111" {
		t.Errorf("channel_id lost: %q", f.Metadata.ChannelID)
	}
	if f.Monitors[0].ModelID != "md_22222222-2222-4222-8222-222222222222" {
		t.Errorf("model_id lost: %q", f.Monitors[0].ModelID)
	}
	out, err := yaml.Marshal(&f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "channel_id: ch_1111") || !strings.Contains(string(out), "model_id: md_2222") {
		t.Errorf("ids not re-serialized:\n%s", out)
	}
}
