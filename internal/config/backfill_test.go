package config

import (
	"path/filepath"
	"testing"
)

// TestBackfillDir 覆盖目录级回填编排：dry-run 只报告不落盘；实跑补 id 落盘；二次实跑幂等。
func TestBackfillDir(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	monitorsDir := filepath.Join(configDir, MonitorsDirName)
	writeTestMonitorFile(t, configDir, "acme--cc--vip", `metadata:
  revision: 1
monitors:
  - provider: acme
    service: cc
    channel: vip
    template: cc-haiku-tiny
    base_url: https://x.com
`)
	filePath := filepath.Join(monitorsDir, "acme--cc--vip.yaml")

	// dry-run：报告 1 文件 / 1 channel_id / 1 model_id 待补，但不落盘
	report, err := BackfillDir(monitorsDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesChanged != 1 || report.ChannelIDsAdded != 1 || report.ModelIDsAdded != 1 {
		t.Errorf("dry-run report unexpected: %+v", report)
	}
	if f, _ := loadMonitorFile(filePath); f.Metadata.ChannelID != "" {
		t.Error("dry-run must not write to disk")
	}

	// 实跑：补 id 并落盘
	if _, err := BackfillDir(monitorsDir, false); err != nil {
		t.Fatal(err)
	}
	f2, err := loadMonitorFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !IsValidChannelID(f2.Metadata.ChannelID) || !IsValidModelID(f2.Monitors[0].ModelID) {
		t.Errorf("real run must fill ids on disk: %+v", f2.Metadata)
	}

	// 幂等：二次实跑无改动
	report3, err := BackfillDir(monitorsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if report3.FilesChanged != 0 {
		t.Errorf("second run should be no-op: %+v", report3)
	}
}
