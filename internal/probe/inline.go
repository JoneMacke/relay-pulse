package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"monitor/internal/config"
	"monitor/internal/identity"
	"monitor/internal/logger"
	"monitor/internal/monitor"
)

// DefaultMaxResponseBytes 响应体读取上限。
const DefaultMaxResponseBytes int64 = 10 << 20 // 10MB

// probeResult 内部探测结果。
type probeResult struct {
	Status          int
	SubStatus       string
	HTTPCode        int
	Latency         int // ms
	ResponseSnippet string
	Err             error
}

// internalProber 为底层安全探测器。
type internalProber struct {
	client       *http.Client
	maxBodyBytes int64
	uidMgr       *identity.UserIDManager
}

func newInternalProber(guard *SSRFGuard, maxBodyBytes int64, uidMgr *identity.UserIDManager) *internalProber {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxResponseBytes
	}
	return &internalProber{
		client:       newSafeHTTPClient(guard),
		maxBodyBytes: maxBodyBytes,
		uidMgr:       uidMgr,
	}
}

func (p *internalProber) probe(ctx context.Context, cfg *config.ServiceConfig) *probeResult {
	result := &probeResult{
		Status:    0,
		SubStatus: "none",
	}

	probeURL, probeBody, probeHeaders, probeSuccessContains, _, _ := monitor.InjectVariables(cfg, p.uidMgr)

	reqBody := bytes.NewBuffer([]byte(strings.TrimSpace(probeBody)))
	req, err := http.NewRequestWithContext(ctx, cfg.Method, probeURL, reqBody)
	if err != nil {
		result.SubStatus = "invalid_request"
		result.Err = fmt.Errorf("创建请求失败: %w", err)
		return result
	}
	req.Close = true

	for k, v := range probeHeaders {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	result.Latency = latency

	if err != nil {
		result.SubStatus = "network_error"
		result.Err = err
		return result
	}
	defer resp.Body.Close()

	result.HTTPCode = resp.StatusCode

	body, err := readBodyLimited(resp.Body, p.maxBodyBytes)
	if err != nil {
		result.SubStatus = "response_too_large"
		result.Err = err
		return result
	}

	status, sub := classifyHTTPStatus(resp.StatusCode, latency, cfg.SlowLatencyDuration)
	result.Status = status
	result.SubStatus = sub

	if len(body) > 0 {
		snippet := strings.TrimSpace(monitor.AggregateResponseText(body))
		const maxSnippetLen = 512
		if len(snippet) > maxSnippetLen {
			snippet = snippet[:maxSnippetLen] + "... (truncated)"
		}
		result.ResponseSnippet = snippet
	}

	if result.Status != 0 && strings.TrimSpace(probeSuccessContains) != "" {
		text := monitor.AggregateResponseText(body)
		if text == "" || !strings.Contains(text, probeSuccessContains) {
			result.Status = 0
			result.SubStatus = "content_mismatch"
			result.Err = fmt.Errorf("响应内容未包含预期关键字")
			return result
		}
	}

	return result
}

func readBodyLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) > limit {
		return data[:limit], fmt.Errorf("响应体超过上限 %d bytes", limit)
	}
	return data, nil
}

func classifyHTTPStatus(statusCode, latency int, slowLatency time.Duration) (int, string) {
	if statusCode >= 200 && statusCode < 300 {
		if slowLatency > 0 && latency > int(slowLatency/time.Millisecond) {
			return 2, "slow_latency"
		}
		return 1, "none"
	}

	if statusCode >= 300 && statusCode < 400 {
		return 0, "redirect_blocked"
	}

	if statusCode == 401 || statusCode == 403 {
		return 0, "auth_error"
	}

	if statusCode == 400 {
		return 0, "invalid_request"
	}

	if statusCode == 429 {
		// 与 scheduler/storage 口径对齐（monitor/probe.go 用 "rate_limit"），
		// 否则前端 i18n 与可用率统计会按两套字符串割裂。
		return 0, "rate_limit"
	}

	if statusCode >= 500 {
		return 0, "server_error"
	}

	if statusCode >= 400 {
		return 0, "client_error"
	}

	return 0, "unknown_error"
}

// Result 为对外暴露的内联探测结果。
type Result struct {
	ProbeStatus     int    `json:"probe_status"`
	SubStatus       string `json:"sub_status"`
	HTTPCode        int    `json:"http_code"`
	Latency         int    `json:"latency"`
	ErrorMessage    string `json:"error_message,omitempty"`
	ResponseSnippet string `json:"response_snippet,omitempty"`
	ProbeID         string `json:"probe_id"`
}

// InlineProber 提供同步内联探测能力。
type InlineProber struct {
	prober *internalProber
	sem    chan struct{}
}

// NewInlineProber 创建内联探测器。
//
// uidMgr 用于注入 metadata.user_id 占位符；传 nil 会让严校验的 provider
// （如 TopRouterCN）判为"非 CLI 客户端"并返回 403。主程序应传入共享的
// UserIDManager 实例，与 scheduler 的请求构造保持一致。
func NewInlineProber(maxConcurrency int, uidMgr *identity.UserIDManager) *InlineProber {
	if maxConcurrency <= 0 {
		maxConcurrency = 5
	}
	return &InlineProber{
		prober: newInternalProber(NewSSRFGuard(), DefaultMaxResponseBytes, uidMgr),
		sem:    make(chan struct{}, maxConcurrency),
	}
}

// Probe 同步执行一次探测并返回结果。
func (p *InlineProber) Probe(ctx context.Context, serviceType, templateName, baseURL, apiKey string) *Result {
	result := &Result{
		ProbeID:     "probe-" + uuid.New().String(),
		ProbeStatus: 0,
		SubStatus:   "none",
	}
	// defer 单条日志，确保所有 early-return 分支都能被串联起来
	defer logInlineProbeResult(result, "service", strings.TrimSpace(serviceType),
		"template", strings.TrimSpace(templateName), "base_url", baseURL)

	if err := ctx.Err(); err != nil {
		result.SubStatus = "canceled"
		result.ErrorMessage = err.Error()
		return result
	}

	// 尝试获取信号量（满时立即拒绝）
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	default:
		result.SubStatus = "concurrency_limited"
		result.ErrorMessage = "探测并发已达上限，请稍后再试"
		return result
	}

	// 查找测试类型
	testType, ok := GetTestType(strings.TrimSpace(serviceType))
	if !ok {
		result.SubStatus = "unknown_test_type"
		result.ErrorMessage = fmt.Sprintf("不支持的服务类型: %s", serviceType)
		return result
	}

	// 解析模板变体
	variant, err := testType.ResolveVariant(templateName)
	if err != nil {
		result.SubStatus = "unknown_variant"
		result.ErrorMessage = err.Error()
		return result
	}

	// 构建探测配置
	cfg, err := testType.Builder.Build(baseURL, apiKey, variant)
	if err != nil {
		result.SubStatus = "build_failed"
		result.ErrorMessage = fmt.Sprintf("构建探测配置失败: %v", err)
		return result
	}

	// 使用模板超时（兜底 15s），外层 context 硬上限 30s
	timeout := cfg.TimeoutDuration
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行底层探测
	pr := p.prober.probe(probeCtx, cfg)
	if pr == nil {
		result.SubStatus = "internal_error"
		result.ErrorMessage = "探测器返回空结果"
		return result
	}

	result.ProbeStatus = pr.Status
	result.SubStatus = pr.SubStatus
	result.HTTPCode = pr.HTTPCode
	result.Latency = pr.Latency
	result.ResponseSnippet = pr.ResponseSnippet
	if pr.Err != nil {
		result.ErrorMessage = pr.Err.Error()
	}
	return result
}

// logInlineProbeResult 在 InlineProber 的每次探测结束时打印一条结构化日志，
// 让运维可以 `grep probe_id=probe-xxx` 把一次 inline 探测的所有上下文串起来。
//
// 日志级别按主状态分级：绿 → Info；黄/红 → Warn（避免 Error 污染告警通道）。
//
// 字段说明：
//   - probe_id / status / sub_status / http_code / latency_ms：result 自身字段
//   - error：截断到 200 字节，避免日志被超长 payload 撑爆
//   - 不记录 ResponseSnippet：可能含上游返回的敏感数据（token / cookie / 内部 URL），
//     由 API 响应层按需返回给管理员前端
//   - extraFields：调用点已知的上下文（PSCM、template、base_url），按 slog 键值对追加
func logInlineProbeResult(r *Result, extraFields ...any) {
	if r == nil {
		return
	}
	fields := []any{
		"probe_id", r.ProbeID,
		"status", r.ProbeStatus,
		"sub_status", r.SubStatus,
		"http_code", r.HTTPCode,
		"latency_ms", r.Latency,
	}
	if r.ErrorMessage != "" {
		fields = append(fields, "error", truncateForLog(r.ErrorMessage, 200))
	}
	fields = append(fields, extraFields...)

	switch r.ProbeStatus {
	case 1:
		logger.Info("inline_probe", "探测完成", fields...)
	default:
		logger.Warn("inline_probe", "探测异常或不可用", fields...)
	}
}

// truncateForLog 安全截断字符串到 max 字节，避免日志被超长 payload 撑爆。
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// ProbeConfig 使用已解析完成的 ServiceConfig 执行一次内联探测。
//
// 适用场景：调用方持有一份**已经过模板填充 + Duration 派生**的 ServiceConfig
// （来自运行时 AppConfig.Monitors，或者经 config.ResolveSingleMonitor 处理过的
// 临时配置），希望以"沙箱测试"语义复用 InlineProber 的执行内核。
//
// 与 Probe 方法的区别：
//   - Probe(serviceType, templateName, baseURL, apiKey) 走 Builder.Build 从模板构造 cfg
//   - ProbeConfig(cfg) 跳过 Builder，直接使用调用方传入的 cfg
//
// 因此本方法**不会**对 cfg 做任何模板解析、父子继承、env 注入或 Duration 派生 ——
// 这些都是调用方的责任。返回值与 Probe 完全同构（携带 probe_id，可与日志/审计串联）。
//
// 仍保留的安全限制（继承自底层 internalProber + safe HTTP client）：
//   - SSRF 守卫：私网/回环/链路本地 IP 阻断
//   - 禁用代理：忽略 cfg.Proxy 与环境代理变量
//   - 禁用自动重定向：3xx 直接归类为 redirect_blocked
//   - 响应体读取上限：DefaultMaxResponseBytes (10 MB)
//   - 并发上限：与 Probe 共享同一 semaphore
func (p *InlineProber) ProbeConfig(ctx context.Context, cfg config.ServiceConfig) *Result {
	result := &Result{
		ProbeID:     "probe-" + uuid.New().String(),
		ProbeStatus: 0,
		SubStatus:   "none",
	}
	// defer 单条日志，把 probe_id 与 PSCM 上下文一起记下来；
	// 让运维 `grep probe_id=probe-xxx` 一行串联整次 inline 探测。
	defer logInlineProbeResult(result,
		"provider", cfg.Provider,
		"service", cfg.Service,
		"channel", cfg.Channel,
		"model", cfg.Model,
		"base_url", cfg.BaseURL,
		"template", cfg.Template)

	if err := ctx.Err(); err != nil {
		result.SubStatus = "canceled"
		result.ErrorMessage = err.Error()
		return result
	}

	// 尝试获取信号量（满时立即拒绝，避免与定时调度抢资源）
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	default:
		result.SubStatus = "concurrency_limited"
		result.ErrorMessage = "探测并发已达上限，请稍后再试"
		return result
	}

	// 探测期超时：优先使用 cfg.TimeoutDuration（已经过 ResolveSingleMonitor 派生），
	// 兜底 15s；外层硬上限由调用方传入的 ctx 控制（通常 handler 套 30s）。
	timeout := cfg.TimeoutDuration
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pr := p.prober.probe(probeCtx, &cfg)
	if pr == nil {
		result.SubStatus = "internal_error"
		result.ErrorMessage = "探测器返回空结果"
		return result
	}

	result.ProbeStatus = pr.Status
	result.SubStatus = pr.SubStatus
	result.HTTPCode = pr.HTTPCode
	result.Latency = pr.Latency
	result.ResponseSnippet = pr.ResponseSnippet
	if pr.Err != nil {
		result.ErrorMessage = pr.Err.Error()
	}
	return result
}
