package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestMonitorsDir 创建临时 monitors.d/ 结构用于测试。
// 返回 configDir（monitors.d 的父目录）和 cleanup 函数。
func setupTestMonitorsDir(t *testing.T) (string, func()) {
	t.Helper()
	configDir := t.TempDir()
	monitorsDir := filepath.Join(configDir, MonitorsDirName)
	if err := os.MkdirAll(monitorsDir, 0755); err != nil {
		t.Fatal(err)
	}
	return configDir, func() {} // t.TempDir auto-cleans
}

func writeTestMonitorFile(t *testing.T, dir, key string, content string) {
	t.Helper()
	path := filepath.Join(dir, MonitorsDirName, key+".yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func validMonitorYAML(provider, service, channel string, revision int64) string {
	return strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: " + intToStr(revision),
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: " + provider,
		"    service: " + service,
		"    channel: " + channel,
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
	}, "\n")
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	v := n
	if v < 0 {
		s = "-"
		v = -v
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return s + digits
}

func findMonitorByModel(t *testing.T, monitors []ServiceConfig, model string) ServiceConfig {
	t.Helper()
	for _, m := range monitors {
		if m.Model == model {
			return m
		}
	}
	t.Fatalf("monitor with model %q not found", model)
	return ServiceConfig{}
}

// --- SanitizeMonitorKey ---

func TestSanitizeMonitorKey_Valid(t *testing.T) {
	key, err := SanitizeMonitorKey("MyProvider--CC--VIP")
	if err != nil {
		t.Fatal(err)
	}
	if key != "myprovider--cc--vip" {
		t.Errorf("expected myprovider--cc--vip, got %s", key)
	}
}

func TestSanitizeMonitorKey_PathTraversal(t *testing.T) {
	cases := []string{
		"../evil--cc--vip",
		"ok--cc--../../etc/passwd",
		"a/b--cc--vip",
		"a\\b--cc--vip",
		"..--cc--vip",
	}
	for _, c := range cases {
		_, err := SanitizeMonitorKey(c)
		if err == nil {
			t.Errorf("expected error for key %q, got nil", c)
		}
	}
}

func TestSanitizeMonitorKey_InvalidFormat(t *testing.T) {
	cases := []string{
		"",
		"nohyphens",
		"only--two",
		"--empty--parts",
		"a----b",
	}
	for _, c := range cases {
		_, err := SanitizeMonitorKey(c)
		if err == nil {
			t.Errorf("expected error for key %q, got nil", c)
		}
	}
}

// --- MonitorStore.Create ---

func TestCreate_Success(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "testprov", Service: "cc", Channel: "vip", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}

	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}

	// 验证文件已创建
	if file.Key != "testprov--cc--vip" {
		t.Errorf("expected key testprov--cc--vip, got %s", file.Key)
	}
	if file.Metadata.Revision != 1 {
		t.Errorf("expected revision 1, got %d", file.Metadata.Revision)
	}
	if _, err := os.Stat(file.Path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestCreate_DuplicatePSC(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "testprov--cc--vip", validMonitorYAML("testprov", "cc", "vip", 1))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "testprov", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Create(file)
	if err == nil {
		t.Fatal("expected error for duplicate PSC")
	}
	if !strings.Contains(err.Error(), "已存在") {
		t.Errorf("expected '已存在' error, got: %v", err)
	}
}

func TestCreate_PathTraversal(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "../evil", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Create(file)
	if err == nil {
		t.Fatal("expected error for path traversal provider")
	}
}

// --- MonitorStore.Get ---

func TestGet_Exists(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 3))

	file, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if file.Key != "acme--cc--vip" {
		t.Errorf("expected key acme--cc--vip, got %s", file.Key)
	}
	if file.Metadata.Revision != 3 {
		t.Errorf("expected revision 3, got %d", file.Metadata.Revision)
	}
}

func TestGet_NotFound(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file, err := store.Get("nonexistent--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if file != nil {
		t.Errorf("expected nil for nonexistent key, got %+v", file)
	}
}

func TestGet_YmlExtension(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// 写 .yml 文件
	content := validMonitorYAML("acme", "cc", "vip", 5)
	path := filepath.Join(configDir, MonitorsDirName, "acme--cc--vip.yml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	file, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if file == nil {
		t.Fatal("expected non-nil file for .yml extension")
	}
	if !strings.HasSuffix(file.Path, ".yml") {
		t.Errorf("expected .yml path, got %s", file.Path)
	}
}

func TestGet_PathTraversal(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	_, err := store.Get("../evil--cc--vip")
	if err == nil {
		t.Fatal("expected error for path traversal key")
	}
}

// --- MonitorStore.Update ---

func TestUpdate_Success(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Template: "cc-opus-tiny", BaseURL: "https://new.com"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}
	if updated.Metadata.Revision != 2 {
		t.Errorf("expected revision 2, got %d", updated.Metadata.Revision)
	}
}

func TestUpdate_RevisionConflict(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 3))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Update("acme--cc--vip", updated, 1) // 期望 1，实际 3
	if err == nil {
		t.Fatal("expected revision conflict error")
	}
	if !strings.Contains(err.Error(), "revision") {
		t.Errorf("expected 'revision' in error, got: %v", err)
	}
}

func TestUpdate_PSCImmutability(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	// 尝试通过 update 把 channel 改成 free
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "free"},
		},
	}

	err := store.Update("acme--cc--vip", updated, 1)
	if err == nil {
		t.Fatal("expected PSC immutability error")
	}
	if !strings.Contains(err.Error(), "不可变更") {
		t.Errorf("expected '不可变更' in error, got: %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "nonexistent", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Update("nonexistent--cc--vip", updated, 1)
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("expected '不存在' in error, got: %v", err)
	}
}

// --- Update: admin hidden fields preservation ---

func TestUpdate_PreservesRootHiddenFields(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
		"    proxy: socks5://proxy.internal:1080",
		"    env_var_name: ACME_VIP_KEY",
		"    key_type: user",
		"    request_model: claude-3-5-sonnet",
		"    skip_url_validation: true",
		"    url_pattern: \"{{BASE_URL}}/v1/chat/completions\"",
		"    auto_cold_exempt: true",
	}, "\n"))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Template: "cc-opus-tiny", BaseURL: "https://new.example.com", Proxy: "socks5://proxy.internal:1080"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	root := got.Monitors[0]
	// Proxy is now JSON-visible, so it round-trips via the update payload
	if root.Proxy != "socks5://proxy.internal:1080" {
		t.Errorf("Proxy = %q, want round-tripped value", root.Proxy)
	}
	if root.EnvVarName != "ACME_VIP_KEY" {
		t.Errorf("EnvVarName = %q, want preserved", root.EnvVarName)
	}
	// KeyType is now JSON-visible and round-trips explicitly; not in update payload → empty
	if root.KeyType != "" {
		t.Errorf("KeyType = %q, want empty (JSON-visible, not preserved from disk)", root.KeyType)
	}
	if root.RequestModel != "claude-3-5-sonnet" {
		t.Errorf("RequestModel = %q, want preserved", root.RequestModel)
	}
	if !root.SkipURLValidation {
		t.Error("SkipURLValidation = false, want true")
	}
	if root.URLPattern != "{{BASE_URL}}/v1/chat/completions" {
		t.Errorf("URLPattern = %q, want preserved", root.URLPattern)
	}
	// AutoColdExempt is now JSON-visible and round-trips explicitly; not in update payload → false
	if root.AutoColdExempt {
		t.Error("AutoColdExempt = true, want false (JSON-visible, not preserved from disk)")
	}
	// JSON-visible fields should reflect the update, not the old value
	if root.Template != "cc-opus-tiny" {
		t.Errorf("Template = %q, want updated value", root.Template)
	}
}

func TestUpdate_PreservesChildHiddenFields(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    template: child-template",
		"    proxy: http://child-proxy:8080",
		"    env_var_name: CHILD_ENV",
		"    key_type: user",
		"    request_model: gpt-4o-2024-08-06",
		"    skip_url_validation: true",
		"    url_pattern: \"{{BASE_URL}}/v1/responses\"",
		"    auto_cold_exempt: true",
	}, "\n"))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Template: "cc-opus-tiny", BaseURL: "https://new.example.com"},
			{Parent: "acme/cc/vip", Model: "gpt-4o", Template: "child-updated", Proxy: "http://child-proxy:8080"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	child := findMonitorByModel(t, got.Monitors, "gpt-4o")
	// Proxy is now JSON-visible, so it round-trips via the update payload
	if child.Proxy != "http://child-proxy:8080" {
		t.Errorf("child.Proxy = %q, want round-tripped value", child.Proxy)
	}
	if child.EnvVarName != "CHILD_ENV" {
		t.Errorf("child.EnvVarName = %q, want preserved", child.EnvVarName)
	}
	if child.RequestModel != "gpt-4o-2024-08-06" {
		t.Errorf("child.RequestModel = %q, want preserved", child.RequestModel)
	}
	if !child.SkipURLValidation {
		t.Error("child.SkipURLValidation = false, want true")
	}
	// AutoColdExempt is now JSON-visible; not in update payload → false
	if child.AutoColdExempt {
		t.Error("child.AutoColdExempt = true, want false (JSON-visible, not preserved from disk)")
	}
}

func TestUpdate_NewChildDoesNotInheritHiddenFields(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    proxy: http://proxy-a:8080",
		"    env_var_name: CHILD_ENV",
	}, "\n"))

	// 用 gpt-5 替换 gpt-4o（新增 child 不应继承旧 child 的隐藏字段）
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "gpt-5", Template: "new-child"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	child := findMonitorByModel(t, got.Monitors, "gpt-5")
	if child.Proxy != "" {
		t.Errorf("new child.Proxy = %q, want empty", child.Proxy)
	}
	if child.EnvVarName != "" {
		t.Errorf("new child.EnvVarName = %q, want empty", child.EnvVarName)
	}
}

func TestUpdate_DeletedChildDisappears(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    proxy: http://proxy-a:8080",
	}, "\n"))

	// 提交时不包含子通道 → 子通道应该被删除
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 1 {
		t.Errorf("len(Monitors) = %d, want 1 (deleted child should be gone)", len(got.Monitors))
	}
}

func TestUpdate_ChildModelRenameIsRemoveAndAdd(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    proxy: http://proxy-a:8080",
		"    request_model: gpt-4o-2024-08-06",
	}, "\n"))

	// model 重命名 → 视为旧 child 删除 + 新 child 添加，不继承隐藏字段
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "gpt-4.1", Template: "renamed-child"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	child := findMonitorByModel(t, got.Monitors, "gpt-4.1")
	if child.Proxy != "" {
		t.Errorf("renamed child.Proxy = %q, want empty", child.Proxy)
	}
	if child.RequestModel != "" {
		t.Errorf("renamed child.RequestModel = %q, want empty", child.RequestModel)
	}
}

func TestUpdate_JSONVisibleFieldsNotMergedBack(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    board: hot",
		"    category: commercial",
		"    proxy: socks5://proxy:1080",
		"    env_var_name: ACME_VIP_KEY",
	}, "\n"))

	// board 显式设为空字符串 → 应覆盖旧值，不被回填
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Board: "", Category: "public", Proxy: ""},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	root := got.Monitors[0]
	// JSON-visible fields: should reflect the incoming update, not old values
	if root.Board != "" {
		t.Errorf("Board = %q, want empty (should not be merged back)", root.Board)
	}
	if root.Category != "public" {
		t.Errorf("Category = %q, want 'public' (should reflect update)", root.Category)
	}
	// Proxy is now JSON-visible: empty in update → empty in result
	if root.Proxy != "" {
		t.Errorf("Proxy = %q, want empty (JSON-visible, should not be merged back)", root.Proxy)
	}
	// json:"-" field: should still be preserved
	if root.EnvVarName != "ACME_VIP_KEY" {
		t.Errorf("EnvVarName = %q, want preserved", root.EnvVarName)
	}
}

func TestUpdate_ChildMatchingIgnoresOrder(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    env_var_name: GPT4O_KEY",
		"  - parent: acme/cc/vip",
		"    model: claude-3-7-sonnet",
		"    env_var_name: CLAUDE37_KEY",
	}, "\n"))

	// 子通道顺序反转 → 仍按 model 匹配
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "claude-3-7-sonnet"},
			{Parent: "acme/cc/vip", Model: "gpt-4o"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if c := findMonitorByModel(t, got.Monitors, "gpt-4o"); c.EnvVarName != "GPT4O_KEY" {
		t.Errorf("gpt-4o EnvVarName = %q, want preserved", c.EnvVarName)
	}
	if c := findMonitorByModel(t, got.Monitors, "claude-3-7-sonnet"); c.EnvVarName != "CLAUDE37_KEY" {
		t.Errorf("claude-3-7-sonnet EnvVarName = %q, want preserved", c.EnvVarName)
	}
}

func TestDelete_Success(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	if err := store.Delete("acme--cc--vip"); err != nil {
		t.Fatal(err)
	}

	// 原文件应该不存在了
	origPath := filepath.Join(configDir, MonitorsDirName, "acme--cc--vip.yaml")
	if _, err := os.Stat(origPath); !os.IsNotExist(err) {
		t.Error("expected original file to be removed")
	}

	// .archive/ 目录应该有文件
	archiveDir := filepath.Join(configDir, MonitorsDirName, ".archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 archived file, got %d", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "acme--cc--vip.") {
		t.Errorf("unexpected archive filename: %s", entries[0].Name())
	}
}

func TestDelete_YmlExtension(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// 写 .yml 文件
	content := validMonitorYAML("acme", "cc", "vip", 1)
	path := filepath.Join(configDir, MonitorsDirName, "acme--cc--vip.yml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("acme--cc--vip"); err != nil {
		t.Fatal(err)
	}

	// .archive/ 中的文件应该保持 .yml 后缀
	archiveDir := filepath.Join(configDir, MonitorsDirName, ".archive")
	entries, _ := os.ReadDir(archiveDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 archived file, got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".yml") {
		t.Errorf("expected .yml extension in archive, got: %s", entries[0].Name())
	}
}

func TestDelete_NotFound(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	err := store.Delete("nonexistent--cc--vip")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("expected '不存在' in error, got: %v", err)
	}
}

func TestDelete_PathTraversal(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	err := store.Delete("../evil--cc--vip")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

// --- MonitorStore.List ---

func TestList_Empty(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestList_MultipleFiles(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "alpha--cc--vip", validMonitorYAML("alpha", "cc", "vip", 1))
	writeTestMonitorFile(t, configDir, "beta--cx--free", validMonitorYAML("beta", "cx", "free", 2))

	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Errorf("expected 2 summaries, got %d", len(summaries))
	}
}

// --- 完整 CRUD 流程 ---

func TestCRUD_FullLifecycle(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// Create
	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "lifecycle", Service: "cc", Channel: "test", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	got, err := store.Get("lifecycle--cc--test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil after Create")
	}
	if got.Metadata.Revision != 1 {
		t.Errorf("expected revision 1 after create, got %d", got.Metadata.Revision)
	}

	// Update
	got.Monitors[0].Template = "cc-opus-tiny"
	if err := store.Update("lifecycle--cc--test", got, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got2, _ := store.Get("lifecycle--cc--test")
	if got2.Metadata.Revision != 2 {
		t.Errorf("expected revision 2 after update, got %d", got2.Metadata.Revision)
	}
	if got2.Monitors[0].Template != "cc-opus-tiny" {
		t.Errorf("expected template cc-opus-tiny, got %s", got2.Monitors[0].Template)
	}

	// List
	summaries, _ := store.List()
	if len(summaries) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries))
	}

	// Delete
	if err := store.Delete("lifecycle--cc--test"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got3, _ := store.Get("lifecycle--cc--test")
	if got3 != nil {
		t.Error("expected nil after Delete")
	}
}

// --- 稳定 id：生成 / 暴露 / 不可变 ---

// TestCreateGeneratesIDs 确认 Create 给缺失的 channel_id/model_id 生成合法值并落盘。
func TestCreateGeneratesIDs(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}
	if !IsValidChannelID(file.Metadata.ChannelID) {
		t.Errorf("channel_id not generated: %q", file.Metadata.ChannelID)
	}
	if !IsValidModelID(file.Monitors[0].ModelID) {
		t.Errorf("model_id not generated: %q", file.Monitors[0].ModelID)
	}
	// 落盘后重新读应保留同一 id
	got, err := store.Get(file.Key)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Metadata.ChannelID != file.Metadata.ChannelID {
		t.Errorf("channel_id not persisted: got %q want %q", got.Metadata.ChannelID, file.Metadata.ChannelID)
	}
	if got.Monitors[0].ModelID != file.Monitors[0].ModelID {
		t.Errorf("model_id not persisted: got %q want %q", got.Monitors[0].ModelID, file.Monitors[0].ModelID)
	}
}

// TestListExposesChannelID 确认 List 摘要带出 channel_id（供 rpdiag sampler 发现）。
func TestListExposesChannelID(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ChannelID != file.Metadata.ChannelID {
		t.Errorf("List did not expose channel_id: %+v", list)
	}
}

// TestUpdateTreatsIDsAsImmutable 确认 Update 视 channel_id/model_id 为不可变：
// 客户端 PUT 篡改的 id 一律从磁盘 existing 还原。
func TestUpdateTreatsIDsAsImmutable(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}
	origChannelID := file.Metadata.ChannelID
	origModelID := file.Monitors[0].ModelID

	updated := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin", ChannelID: "ch_tampered"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com", ModelID: "md_tampered"},
		},
	}
	if err := store.Update(file.Key, updated, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Get(file.Key)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Metadata.ChannelID != origChannelID {
		t.Errorf("channel_id must be immutable: got %q want %q", got.Metadata.ChannelID, origChannelID)
	}
	if got.Monitors[0].ModelID != origModelID {
		t.Errorf("model_id must be immutable: got %q want %q", got.Monitors[0].ModelID, origModelID)
	}
}
