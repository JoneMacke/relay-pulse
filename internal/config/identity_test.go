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

// TestIDFormat 覆盖 id 生成的前缀语义与格式校验：
// 生成值带正确前缀且自校验通过；前缀交叉校验必须失败（channel id 不被当 model id）；
// 非 uuid 主体被拒。
func TestIDFormat(t *testing.T) {
	cid := NewChannelID()
	if !strings.HasPrefix(cid, "ch_") || !IsValidChannelID(cid) {
		t.Errorf("bad channel id: %q", cid)
	}
	mid := NewModelID()
	if !strings.HasPrefix(mid, "md_") || !IsValidModelID(mid) {
		t.Errorf("bad model id: %q", mid)
	}
	if IsValidChannelID(mid) || IsValidModelID(cid) {
		t.Error("prefix cross-validation must fail (channel vs model)")
	}
	if IsValidChannelID("ch_not-a-uuid") {
		t.Error("non-uuid body must be invalid")
	}
}

// TestCollectModelIDsDetectsDuplicate 确认跨文件收集 model_id 时重复被检出并指名。
func TestCollectModelIDsDetectsDuplicate(t *testing.T) {
	files := []MonitorFile{
		{Monitors: []ServiceConfig{{ModelID: "md_dup"}, {ModelID: "md_uniq"}}},
		{Monitors: []ServiceConfig{{ModelID: "md_dup"}}},
	}
	_, err := CollectModelIDs(files)
	if err == nil {
		t.Fatal("expected duplicate model_id error")
	}
	if !strings.Contains(err.Error(), "md_dup") {
		t.Errorf("error should name the dup id: %v", err)
	}
}

// TestValidateRejectsDuplicateModelID 确认 validate() 拒绝跨监测行重复的 model_id。
// fixtures 带 base_url+method 以越过更早的字段校验，触达 id 校验。
func TestValidateRejectsDuplicateModelID(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "Opus", ModelID: "md_dup", BaseURL: "https://a.example", Method: "POST"},
		{Provider: "b", Service: "cc", Channel: "y", Model: "Sonnet", ModelID: "md_dup", BaseURL: "https://b.example", Method: "POST"},
	}}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "md_dup") {
		t.Fatalf("expected dup model_id rejection naming the id, got %v", err)
	}
}

// TestValidateRejectsMalformedModelID 确认 validate() 拒绝格式非法的 model_id。
func TestValidateRejectsMalformedModelID(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "Opus", ModelID: "garbage", BaseURL: "https://a.example", Method: "POST"},
	}}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "model_id") {
		t.Fatalf("expected malformed model_id rejection, got %v", err)
	}
}

// TestValidateAcceptsValidAndEmptyModelIDs 确认合法 model_id 通过，且空 model_id
// （回填前的现有行）不被误拒——空值合法是向后兼容的关键。
func TestValidateAcceptsValidAndEmptyModelIDs(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "Opus", ModelID: NewModelID(), BaseURL: "https://a.example", Method: "POST"},
		{Provider: "b", Service: "cc", Channel: "y", Model: "Sonnet", BaseURL: "https://b.example", Method: "POST"},
	}}
	if err := cfg.validate(); err != nil {
		t.Fatalf("valid+empty model_ids should pass, got %v", err)
	}
}

// TestLoadMonitorsDirInjectsChannelID 确认文件级 channel_id 在 load 时下沉到每个监测行
// 的运行时 ChannelID 字段（供 query.go 填进 /api/status）。
func TestLoadMonitorsDirInjectsChannelID(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	writeTestMonitorFile(t, configDir, "acme--cc--vip", `metadata:
  revision: 1
  channel_id: ch_11111111-1111-4111-8111-111111111111
monitors:
  - provider: acme
    service: cc
    channel: vip
    template: cc-haiku-tiny
    base_url: https://x.com
`)
	monitors, _, err := loadMonitorsDir(configDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(monitors))
	}
	if monitors[0].ChannelID != "ch_11111111-1111-4111-8111-111111111111" {
		t.Errorf("channel_id not injected into ServiceConfig: %q", monitors[0].ChannelID)
	}
}

// TestLoadMonitorsDirRejectsMalformedChannelID 确认手工 YAML 填了非法 channel_id 时
// 加载失败（fail-closed，不静默）。
func TestLoadMonitorsDirRejectsMalformedChannelID(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	writeTestMonitorFile(t, configDir, "acme--cc--vip", `metadata:
  revision: 1
  channel_id: not-a-valid-id
monitors:
  - provider: acme
    service: cc
    channel: vip
    template: cc-haiku-tiny
    base_url: https://x.com
`)
	if _, _, err := loadMonitorsDir(configDir); err == nil || !strings.Contains(err.Error(), "channel_id") {
		t.Fatalf("expected malformed channel_id rejection, got %v", err)
	}
}

// TestBackfillFileIDs 确认回填幂等：补缺失的 channel_id/model_id、绝不覆盖既有、二次为 no-op。
func TestBackfillFileIDs(t *testing.T) {
	f := &MonitorFile{
		Metadata: MonitorFileMetadata{ChannelID: ""},
		Monitors: []ServiceConfig{
			{Provider: "a", Service: "cc", Channel: "x", Model: "Opus", ModelID: "md_keep"},
			{Provider: "a", Service: "cc", Channel: "x", Model: "Sonnet"},
		},
	}
	if !BackfillFileIDs(f) {
		t.Fatal("expected changed=true (channel_id + one model_id missing)")
	}
	if !IsValidChannelID(f.Metadata.ChannelID) {
		t.Error("channel_id not filled")
	}
	if f.Monitors[0].ModelID != "md_keep" {
		t.Error("existing model_id overwritten")
	}
	if !IsValidModelID(f.Monitors[1].ModelID) {
		t.Error("missing model_id not filled")
	}
	if BackfillFileIDs(f) {
		t.Error("second pass should be no-op (idempotent)")
	}
}
