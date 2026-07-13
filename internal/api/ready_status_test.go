package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/reloadstatus"
	"monitor/internal/storage"
)

// newReadyRouter 搭一个只挂 /ready 的轻量路由，接真 SQLite 存储（Ping 必过），
// 便于单测 buildReadyHandler 对热更新跳过状态的暴露逻辑。
func newReadyRouter(t *testing.T, recorder *reloadstatus.Recorder) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "ready.db")
	store, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	router := gin.New()
	handler := buildReadyHandler(store, recorder)
	router.GET("/ready", handler)
	return router
}

func getReady(t *testing.T, router *gin.Engine) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非合法 JSON：%v，body=%q", err, w.Body.String())
	}
	return w.Code, body
}

// 从未发生过热更新跳过：/ready 保持原样 {"status":"ok"}，不含 config_reload（向后兼容）。
func TestReadyNoSkipKeepsOriginalBody(t *testing.T) {
	router := newReadyRouter(t, reloadstatus.New())

	code, body := getReady(t, router)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["status"] != "ok" {
		t.Fatalf("status 字段 = %v, want ok", body["status"])
	}
	if _, present := body["config_reload"]; present {
		t.Fatalf("未跳过时不应出现 config_reload，实际 body=%v", body)
	}
	if len(body) != 1 {
		t.Fatalf("健康 body 应严格只含 status 一个字段，实际 %v", body)
	}
}

// Ping 失败时 /ready 仍返回 503（抽出 buildReadyHandler 后语义不变），
// 且 Ping 失败短路在读 recorder 之前——即便有跳过记录也不该冒出 config_reload。
func TestReadyPingFailureReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "closed.db")
	store, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	_ = store.Close() // 关闭后 Ping 必失败

	recorder := reloadstatus.New()
	recorder.RecordSkip(errors.New("has skip"))

	router := gin.New()
	router.GET("/ready", buildReadyHandler(store, recorder))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503（存储不通）", w.Code)
	}
	if json.Valid(w.Body.Bytes()) {
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if _, present := body["config_reload"]; present {
			t.Fatalf("Ping 失败应短路，不该暴露 config_reload，实际 %v", body)
		}
	}
}

// recorder 为 nil（未接入）时也不 panic，仍返回原样健康 body。
func TestReadyNilRecorder(t *testing.T) {
	router := newReadyRouter(t, nil)

	code, body := getReady(t, router)

	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("nil recorder 应返回 200 ok，实际 %d %v", code, body)
	}
	if _, present := body["config_reload"]; present {
		t.Fatalf("nil recorder 不应出现 config_reload")
	}
}

// 发生过热更新跳过后：/ready 仍 200，但 body 附带 config_reload 三字段，
// 时间为带偏移的 RFC3339（UTC，末尾 Z）。
func TestReadySurfacesSkipStatus(t *testing.T) {
	recorder := reloadstatus.New()
	recorder.RecordSkip(errors.New("monitor[3] a/b/c/d: 缺 model_id"))

	router := newReadyRouter(t, recorder)
	code, body := getReady(t, router)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200（信息化，绝不翻 503）", code)
	}
	if body["status"] != "ok" {
		t.Fatalf("status 字段 = %v, want ok", body["status"])
	}

	raw, present := body["config_reload"]
	if !present {
		t.Fatalf("跳过后应出现 config_reload，实际 body=%v", body)
	}
	cr, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("config_reload 非对象：%T", raw)
	}
	if cr["last_error"] != "monitor[3] a/b/c/d: 缺 model_id" {
		t.Fatalf("last_error = %v", cr["last_error"])
	}
	// JSON number 解出为 float64
	if cnt, _ := cr["skipped_count"].(float64); cnt != 1 {
		t.Fatalf("skipped_count = %v, want 1", cr["skipped_count"])
	}
	tsStr, _ := cr["last_skipped_at"].(string)
	parsed, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		t.Fatalf("last_skipped_at 非 RFC3339：%q（%v）", tsStr, err)
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("last_skipped_at 应为 UTC，实际 %v", parsed.Location())
	}
}
