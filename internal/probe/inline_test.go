package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"monitor/internal/config"
	"monitor/internal/identity"
)

// classifyHTTPStatus 的 sub_status 字符串必须与 scheduler/storage 口径
// （monitor/probe.go）一致，否则前端 i18n 与可用率统计会按两套字符串割裂。
// 429 历史上 inline 误用 "rate_limited"（带 -ed），此处锁定为 "rate_limit"。
func TestClassifyHTTPStatus_SubStatusMatchesScheduler(t *testing.T) {
	const slow = 5 * time.Second
	cases := []struct {
		name       string
		statusCode int
		latency    int
		wantStatus int
		wantSub    string
	}{
		{"rate_limit", 429, 100, 0, "rate_limit"},
		{"auth_error", 401, 100, 0, "auth_error"},
		{"invalid_request", 400, 100, 0, "invalid_request"},
		{"server_error", 503, 100, 0, "server_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotSub := classifyHTTPStatus(tc.statusCode, tc.latency, slow)
			if gotStatus != tc.wantStatus || gotSub != tc.wantSub {
				t.Errorf("classifyHTTPStatus(%d) = (%d, %q), want (%d, %q)",
					tc.statusCode, gotStatus, gotSub, tc.wantStatus, tc.wantSub)
			}
		})
	}
}

// TestInternalProber_InjectsUserID 验证 InlineProber 走真实 UserIDManager 时
// 生成的请求 body 里 metadata.user_id 非空且格式正确。回归 TopRouterCN 403 bug。
func TestInternalProber_InjectsUserID(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"answer":"RP_ANSWER=7"}`))
	}))
	defer srv.Close()

	cfg := &config.ServiceConfig{
		Provider:        "probe",
		Service:         "cc",
		Channel:         "",
		BaseURL:         srv.URL,
		URLPattern:      srv.URL,
		APIKey:          "test-key",
		Method:          "POST",
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            `{"metadata":{"user_id":"{{USER_ID}}"}}`,
		TimeoutDuration: 5 * time.Second,
	}

	// 构造 internalProber 时跳过 SSRF（httptest 用 127.0.0.1，SSRF 会拒）
	p := &internalProber{
		client:       srv.Client(),
		maxBodyBytes: DefaultMaxResponseBytes,
		uidMgr:       identity.NewUserIDManager(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := p.probe(ctx, cfg)
	if result.Err != nil {
		t.Fatalf("probe failed: %v", result.Err)
	}
	if result.HTTPCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", result.HTTPCode)
	}

	var parsed struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(capturedBody, &parsed); err != nil {
		t.Fatalf("captured body not JSON: %v\nbody=%s", err, capturedBody)
	}

	uidPattern := regexp.MustCompile(`^user_[0-9a-f]{64}_account__session_[0-9a-f-]+$`)
	if !uidPattern.MatchString(parsed.Metadata.UserID) {
		t.Fatalf("metadata.user_id does not match expected format: %q", parsed.Metadata.UserID)
	}
}

// TestInternalProber_NilUidMgrLeavesUserIDEmpty 文档化旧 bug 行为：不传 uidMgr
// 时 user_id 为空字符串（这是我们修好的 403 路径）。
func TestInternalProber_NilUidMgrLeavesUserIDEmpty(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.ServiceConfig{
		Provider:        "probe",
		Service:         "cc",
		BaseURL:         srv.URL,
		URLPattern:      srv.URL,
		APIKey:          "test-key",
		Method:          "POST",
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            `{"metadata":{"user_id":"{{USER_ID}}"}}`,
		TimeoutDuration: 5 * time.Second,
	}

	p := &internalProber{
		client:       srv.Client(),
		maxBodyBytes: DefaultMaxResponseBytes,
		uidMgr:       nil,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = p.probe(ctx, cfg)

	if !strings.Contains(string(capturedBody), `"user_id":""`) {
		t.Fatalf("expected empty user_id when uidMgr is nil; body=%s", capturedBody)
	}
}
