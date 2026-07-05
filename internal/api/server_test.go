package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/rpdiag"
)

// newDetectRedirectRouter 搭一个只挂 redirectLegacyDetect 中间件的轻量路由，
// NoRoute 模拟生产里 SPA 兜底（非白名单路径注入 noindex），不依赖嵌入的前端产物。
func newDetectRedirectRouter(rpdiagEnabled bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := &Handler{}
	if rpdiagEnabled {
		handler.rpdiagClient = rpdiag.NewClient(nil, "", 0, true)
	}

	router := gin.New()
	router.Use(redirectLegacyDetect(handler))
	router.NoRoute(func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<meta name="robots" content="noindex, nofollow">`))
	})
	return router
}

// TestRedirectLegacyDetect 验证旧 /detect 专题页的 301 迁移矩阵：
// rpdiag 启用时四语言路径（含尾斜杠别名）GET/HEAD 均 301 到 rpdiag 站点且保留 query；
// rpdiag 关闭（私有部署）或非 GET/HEAD 时不跳转，落回 SPA 兜底。
func TestRedirectLegacyDetect(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		rpdiagEnabled bool
		wantStatus    int
		wantLocation  string
	}{
		{"GET /detect 301 到 diag", http.MethodGet, "/detect", true, http.StatusMovedPermanently, rpdiagSiteURL},
		{"query string 原样带走", http.MethodGet, "/detect?utm_source=search", true, http.StatusMovedPermanently, rpdiagSiteURL + "?utm_source=search"},
		{"HEAD 同样 301", http.MethodHead, "/detect", true, http.StatusMovedPermanently, rpdiagSiteURL},
		{"语言前缀路径 301", http.MethodGet, "/en/detect", true, http.StatusMovedPermanently, rpdiagSiteURL},
		{"尾斜杠旧别名 301", http.MethodGet, "/ja/detect/", true, http.StatusMovedPermanently, rpdiagSiteURL},
		{"rpdiag 关闭不跳转，落 SPA noindex", http.MethodGet, "/detect", false, http.StatusOK, ""},
		{"非 GET/HEAD 不跳转", http.MethodPost, "/detect", true, http.StatusOK, ""},
		{"无关路径不受影响", http.MethodGet, "/contact", true, http.StatusOK, ""},
		{"detect 子路径不匹配", http.MethodGet, "/detect/foo", true, http.StatusOK, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newDetectRedirectRouter(tt.rpdiagEnabled)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := w.Header().Get("Location"); got != tt.wantLocation {
				t.Fatalf("Location = %q, want %q", got, tt.wantLocation)
			}
			if tt.wantStatus == http.StatusOK && !strings.Contains(w.Body.String(), "noindex") {
				t.Fatalf("未跳转路径应落到 SPA 兜底，body = %q", w.Body.String())
			}
		})
	}
}
