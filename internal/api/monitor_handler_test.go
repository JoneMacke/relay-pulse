package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
)

// newAdminMonitorTestHandler 起一个最小 admin monitors 路由 + 临时 monitors.d store。
func newAdminMonitorTestHandler(t *testing.T) *gin.Engine {
	t.Helper()
	configDir := t.TempDir()
	monitorsDir := filepath.Join(configDir, config.MonitorsDirName)
	if err := os.MkdirAll(monitorsDir, 0755); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		config:       &config.AppConfig{Onboarding: config.OnboardingConfig{AdminToken: "test-token"}},
		monitorStore: config.NewMonitorStore(monitorsDir),
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/admin/monitors", h.AdminCreateMonitor)
	r.GET("/api/admin/monitors/:key", h.AdminGetMonitor)
	return r
}

// TestAdminCreateMonitorGeneratesAndExposesIDs 端到端确认：admin 创建通道会生成
// channel_id/model_id（201 响应即带出），AdminGetMonitor 返回的整个 file 也含这两个 id，
// 且响应 wire 真的含 snake_case 的 channel_id 字段（rpdiag sampler 发现契约）。
func TestAdminCreateMonitorGeneratesAndExposesIDs(t *testing.T) {
	r := newAdminMonitorTestHandler(t)

	body := `{"monitors":[{"provider":"acme","service":"cc","channel":"vip","model":"Opus","template":"cc-haiku-tiny","base_url":"https://x.com"}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/monitors", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	var createResp struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	if !config.IsValidChannelID(createResp.Monitor.Metadata.ChannelID) {
		t.Errorf("create resp missing valid channel_id: %q", createResp.Monitor.Metadata.ChannelID)
	}
	if len(createResp.Monitor.Monitors) != 1 || !config.IsValidModelID(createResp.Monitor.Monitors[0].ModelID) {
		t.Errorf("create resp missing valid model_id: %+v", createResp.Monitor.Monitors)
	}

	// GET 回来，确认整个 file 含同一对 id
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/api/admin/monitors/acme--cc--vip", nil)
	req2.Header.Set("Authorization", "Bearer test-token")
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", w2.Code, w2.Body.String())
	}
	var getResp struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get resp: %v", err)
	}
	if getResp.Monitor.Metadata.ChannelID != createResp.Monitor.Metadata.ChannelID {
		t.Errorf("get channel_id mismatch: got %q want %q", getResp.Monitor.Metadata.ChannelID, createResp.Monitor.Metadata.ChannelID)
	}
	if len(getResp.Monitor.Monitors) != 1 || getResp.Monitor.Monitors[0].ModelID != createResp.Monitor.Monitors[0].ModelID {
		t.Errorf("get model_id mismatch: %+v", getResp.Monitor.Monitors)
	}
	if !strings.Contains(w2.Body.String(), `"channel_id"`) {
		t.Errorf("get response wire missing channel_id field: %s", w2.Body.String())
	}
}
