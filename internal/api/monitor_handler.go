package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/probe"
	"monitor/internal/storage"
)

// adminProbeRequest 探测覆盖参数：非空字段会覆盖磁盘上保存的值，
// 用于"编辑未保存就先测一下"的场景。空字段回退到 store 里的当前值。
type adminProbeRequest struct {
	Template string `json:"template,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

// AdminListTemplates 列出 templates/ 中的可用模板
// GET /api/admin/templates
func (h *Handler) AdminListTemplates(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	templatesDir := filepath.Join(filepath.Dir(store.Dir()), "templates")
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{"templates": []string{}})
			return
		}
		logger.Error("admin", "读取模板目录失败", "dir", templatesDir, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "读取模板目录失败")
		return
	}

	templates := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		templates = append(templates, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(templates)

	c.JSON(http.StatusOK, gin.H{"templates": templates})
}

// AdminListMonitors 列出所有 monitors.d/ 中的监测项
// GET /api/admin/monitors
func (h *Handler) AdminListMonitors(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	summaries, err := store.List()
	if err != nil {
		logger.Error("admin", "列出监测项失败", "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "列出监测项失败")
		return
	}

	// 过滤
	board := strings.TrimSpace(c.Query("board"))
	status := strings.TrimSpace(c.Query("status"))
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))

	var filtered []config.MonitorSummary
	for _, s := range summaries {
		// 空 board 字段在前端语义上视为 hot（默认板），过滤时同样归一化，
		// 否则 ?board=hot 会漏掉历史上未填写 board 的通道。
		effectiveBoard := s.Board
		if effectiveBoard == "" {
			effectiveBoard = "hot"
		}
		if board != "" && effectiveBoard != board {
			continue
		}
		if status == "disabled" && !s.Disabled {
			continue
		}
		if status == "hidden" && !s.Hidden {
			continue
		}
		if status == "active" && (s.Disabled || s.Hidden) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(s.Provider + " " + s.Service + " " + s.Channel + " " + s.Template)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	c.JSON(http.StatusOK, gin.H{
		"monitors": filtered,
		"total":    len(filtered),
	})
}

// AdminGetMonitor 获取指定监测项详情
// GET /api/admin/monitors/:key
func (h *Handler) AdminGetMonitor(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	key := c.Param("key")
	file, err := store.Get(key)
	if err != nil {
		logger.Error("admin", "获取监测项失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取监测项失败")
		return
	}
	if file == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "监测项不存在")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"monitor": file,
	})
}

// AdminCreateMonitor 创建新监测项
// POST /api/admin/monitors
func (h *Handler) AdminCreateMonitor(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	var file config.MonitorFile
	if err := c.ShouldBindJSON(&file); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	if len(file.Monitors) == 0 {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "monitors 不能为空")
		return
	}

	// 验证基本字段
	for i, m := range file.Monitors {
		if strings.TrimSpace(m.Provider) == "" && strings.TrimSpace(m.Parent) == "" {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam,
				"monitors["+string(rune('0'+i))+"]: provider 不能为空（或通过 parent 继承）")
			return
		}
	}

	if file.Metadata.Source == "" {
		file.Metadata.Source = "admin"
	}

	// 跨源 PSC 冲突预检：确保新 PSC 不与 config.yaml 中已有的监测项冲突
	if err := h.checkPSCConflict(&file); err != nil {
		apiError(c, http.StatusConflict, ErrCodeInvalidParam, err.Error())
		return
	}

	if err := store.Create(&file); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "已存在") {
			apiError(c, http.StatusConflict, ErrCodeInvalidParam, errMsg)
			return
		}
		if strings.Contains(errMsg, "无效") || strings.Contains(errMsg, "不能") {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, errMsg)
			return
		}
		logger.Error("admin", "创建监测项失败", "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, errMsg)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"monitor": file,
	})
}

// AdminUpdateMonitor 更新监测项
// PUT /api/admin/monitors/:key
func (h *Handler) AdminUpdateMonitor(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	key := c.Param("key")

	var req struct {
		Revision int64              `json:"revision"`
		Monitor  config.MonitorFile `json:"monitor"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	if len(req.Monitor.Monitors) == 0 {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "monitors 不能为空")
		return
	}

	if err := store.Update(key, &req.Monitor, req.Revision); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "不存在") {
			apiError(c, http.StatusNotFound, ErrCodeNotFound, errMsg)
			return
		}
		if strings.Contains(errMsg, "revision") {
			apiError(c, http.StatusConflict, ErrCodeInvalidParam, errMsg)
			return
		}
		if strings.Contains(errMsg, "不可变更") || strings.Contains(errMsg, "无效") {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, errMsg)
			return
		}
		logger.Error("admin", "更新监测项失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, errMsg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"monitor": req.Monitor,
	})
}

// AdminDeleteMonitor 归档删除监测项
// DELETE /api/admin/monitors/:key
func (h *Handler) AdminDeleteMonitor(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	key := c.Param("key")
	if err := store.Delete(key); err != nil {
		if strings.Contains(err.Error(), "不存在") {
			apiError(c, http.StatusNotFound, ErrCodeNotFound, err.Error())
			return
		}
		logger.Error("admin", "删除监测项失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "archived"})
}

// AdminToggleMonitor 切换监测项的 disabled/hidden 状态
// POST /api/admin/monitors/:key/toggle
func (h *Handler) AdminToggleMonitor(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	key := c.Param("key")

	var req struct {
		Field string `json:"field" binding:"required"` // "disabled" or "hidden"
		Value bool   `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}
	if req.Field != "disabled" && req.Field != "hidden" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "field 只能是 disabled 或 hidden")
		return
	}

	file, err := store.Get(key)
	if err != nil {
		logger.Error("admin", "获取监测项失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取监测项失败")
		return
	}
	if file == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "监测项不存在")
		return
	}

	for i := range file.Monitors {
		if strings.TrimSpace(file.Monitors[i].Parent) != "" {
			continue // 只修改父通道
		}
		switch req.Field {
		case "disabled":
			file.Monitors[i].Disabled = req.Value
		case "hidden":
			file.Monitors[i].Hidden = req.Value
		}
	}

	if err := store.Update(key, file, file.Metadata.Revision); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "revision") {
			apiError(c, http.StatusConflict, ErrCodeInvalidParam, errMsg)
			return
		}
		logger.Error("admin", "切换监测项状态失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, errMsg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"monitor": file,
	})
}

// AdminProbeMonitor 对监测项执行探测测试
// POST /api/admin/monitors/:key/probe
//
// 行为：按"当前运行时已解析"的 ServiceConfig 探测，与 scheduler 真实探测字段级一致。
// 与旧实现相比的改进：
//   - 不再只取 (service, template, base_url, api_key) 4 个标量，而是完整复用 headers/body/
//     success_contains/timeout/slow_latency/retry 等所有 monitor file 字段
//   - 通过 resolveRuntimeRoot 优先获取已经过模板填充和 Duration 派生的 runtime config，
//     未找到时 fallback 到 raw monitor file root 并即时 ResolveSingleMonitor
//   - 拒绝 template 覆盖：变更模板涉及 URLPattern/Headers/Body 等派生字段重新解析，
//     沙箱测试无法做到"半解析"，所以强制要求先保存
func (h *Handler) AdminProbeMonitor(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	if h.inlineProber == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "内联探测器未初始化")
		return
	}

	key := c.Param("key")
	file, err := store.Get(key)
	if err != nil {
		logger.Error("admin", "获取监测项失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取监测项失败")
		return
	}
	if file == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "监测项不存在")
		return
	}

	// 找到父通道
	var root *config.ServiceConfig
	for i := range file.Monitors {
		if strings.TrimSpace(file.Monitors[i].Parent) == "" {
			root = &file.Monitors[i]
			break
		}
	}
	if root == nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "找不到父通道")
		return
	}

	// 接收可选 override（用于"编辑未保存"的探测）。空 body 等价于按磁盘配置探测。
	var req adminProbeRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效: "+err.Error())
			return
		}
	}

	// 拒绝 template 覆盖：模板字段变更涉及 URLPattern/Headers/Body/SuccessContains 等
	// 派生字段的重新解析，沙箱测试无法做到"半解析"。前端应在用户编辑模板时禁用 Probe 按钮。
	if overrideTpl := strings.TrimSpace(req.Template); overrideTpl != "" && overrideTpl != strings.TrimSpace(root.Template) {
		apiError(c, http.StatusUnprocessableEntity, ErrCodeTemplateChangeRequiresSave,
			"修改模板需保存后再测试（base_url / api_key 可即时覆盖）")
		return
	}

	// 取已解析的 runtime config（与 scheduler 一致）；未命中则 fallback 到 raw + 即时解析。
	cfg, isResolved := h.resolveRuntimeRoot(root)

	if !isResolved {
		// fallback 路径：raw monitor file 未在运行时配置中（多见于刚 Create 还未热重载）。
		// 即时走 ResolveSingleMonitor 把模板和 Duration 派生上去；并在响应头标注 fallback。
		appCfg := h.snapshotAppConfig()
		if appCfg == nil {
			apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "运行时配置未就绪")
			return
		}
		if err := config.ResolveSingleMonitor(appCfg, &cfg, h.configDir()); err != nil {
			logger.Error("admin", "ResolveSingleMonitor 失败", "key", key, "error", err)
			apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "解析监测配置失败: "+err.Error())
			return
		}
		logger.Warn("admin", "AdminProbeMonitor 使用 raw fallback 解析",
			"key", key, "provider", root.Provider, "service", root.Service, "channel", root.Channel)
		c.Header("X-Probe-Config-Source", "raw-fallback")
	}

	// 应用 base_url / api_key 覆盖（用于"编辑未保存"场景）
	if v := strings.TrimSpace(req.BaseURL); v != "" {
		// 覆盖参数源自管理员输入，必须过 SSRF 守卫（与 OnboardingTest 一致）
		if err := probe.NewSSRFGuard().ValidateURL(v); err != nil {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "base_url 安全校验失败: "+err.Error())
			return
		}
		cfg.BaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(req.APIKey); v != "" {
		cfg.APIKey = v
	}

	if strings.TrimSpace(cfg.BaseURL) == "" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "base_url 未配置")
		return
	}

	// 使用内联探测器同步执行
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result := h.inlineProber.ProbeConfig(ctx, cfg)

	c.JSON(http.StatusOK, gin.H{
		"probe_id":         result.ProbeID,
		"probe_status":     result.ProbeStatus,
		"sub_status":       result.SubStatus,
		"http_code":        result.HTTPCode,
		"latency":          result.Latency,
		"error_message":    result.ErrorMessage,
		"response_snippet": result.ResponseSnippet,
	})
}

// resolveRuntimeRoot 根据 monitor file 的父通道 PSC 在运行时配置中查找已解析的 ServiceConfig。
//
// 返回值：
//   - 第一个返回值：找到时为运行时 resolved cfg 的值拷贝；未找到时为 raw root 的值拷贝
//   - 第二个返回值：是否命中运行时配置；调用方未命中时需要自行 ResolveSingleMonitor
//
// 注意：ServiceConfig 含 map（Headers）和 *int/*float64 等指针字段，本方法返回值拷贝，
// 浅拷贝足以支持"只覆盖 BaseURL/APIKey 等字符串字段"的探测覆盖场景；调用方如需修改
// Headers 等引用字段，必须自行深拷贝。
func (h *Handler) resolveRuntimeRoot(root *config.ServiceConfig) (config.ServiceConfig, bool) {
	if root == nil {
		return config.ServiceConfig{}, false
	}
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()
	if h.config == nil {
		return *root, false
	}
	for _, m := range h.config.Monitors {
		if strings.TrimSpace(m.Parent) != "" {
			continue
		}
		if m.Provider == root.Provider && m.Service == root.Service && m.Channel == root.Channel {
			return m, true
		}
	}
	return *root, false
}

// snapshotAppConfig 原子地读取当前运行时 AppConfig 指针，避免持锁太久。
// 返回的指针指向的 AppConfig 在调用方使用期间是稳定的（只读全局默认值）。
func (h *Handler) snapshotAppConfig() *config.AppConfig {
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()
	return h.config
}

// configDir 返回 monitors.d/ 与 templates/ 的共同父目录，供 ResolveSingleMonitor 加载 template JSON。
func (h *Handler) configDir() string {
	store := h.getMonitorStore()
	if store == nil {
		return ""
	}
	return filepath.Dir(store.Dir())
}

// adminMonitorLogItem 是 AdminGetMonitorLogs 返回的单条日志结构。
// 字段命名与前端 ProbeHistoryEntry 类型对齐。
type adminMonitorLogItem struct {
	ID          int64             `json:"id"`
	Provider    string            `json:"provider"`
	Service     string            `json:"service"`
	Channel     string            `json:"channel"`
	Model       string            `json:"model,omitempty"`
	Status      int               `json:"status"`
	SubStatus   storage.SubStatus `json:"sub_status"`
	HTTPCode    int               `json:"http_code"`
	Latency     int               `json:"latency"`
	Timestamp   int64             `json:"timestamp"`
	ErrorDetail string            `json:"error_detail,omitempty"`
}

// parseAdminLogsSince 解析 since query 参数。
// 支持 Go duration（如 "1h"、"30m"）或 RFC3339 时间戳；空值回退到 1h。
// 返回 (sinceTime, error)；error != nil 时 since 无效。
func parseAdminLogsSince(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "1h"
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("since 必须为正向时间跨度")
		}
		return now.Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("since 必须为 Go duration（如 1h）或 RFC3339 时间")
}

// AdminGetMonitorLogs 返回某监测项的最近探测历史记录（按时间倒序）。
//
// GET /api/admin/monitors/:key/logs?since=1h&limit=200&model=<可选>
//
// 实现要点：
//   - since 默认 1h，limit 默认 200（最大 1000）
//   - 不指定 model 时遍历 monitor file 中所有 PSCM 组合（父+所有子通道），合并后排序
//   - 指定 model 时只查该 model；避免父+多子通道场景下早期记录被 limit 截断
//   - error_detail 可能含上游返回的敏感信息，响应头加 Cache-Control: no-store
func (h *Handler) AdminGetMonitorLogs(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	store := h.getMonitorStore()
	if store == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "monitors.d 管理未启用")
		return
	}

	key := c.Param("key")
	file, err := store.Get(key)
	if err != nil {
		logger.Error("admin", "获取监测项失败", "key", key, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取监测项失败")
		return
	}
	if file == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "监测项不存在")
		return
	}

	const (
		defaultLimit = 200
		maxLimit     = 1000
	)

	limit := defaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "limit 必须为正整数")
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}

	since, err := parseAdminLogsSince(c.Query("since"), time.Now())
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	modelFilter := strings.TrimSpace(c.Query("model"))

	// 收集需要查询的 PSCM 组合。
	// monitor file 中父通道 model 可能为空（聚合状态），子通道 model 必填。
	type pscm struct {
		provider, service, channel, model string
	}
	var keys []pscm
	for _, m := range file.Monitors {
		// 父通道：以 file 父通道 PSC 作为基准（同 monitor_handler 既有惯例）
		provider := m.Provider
		service := m.Service
		channel := m.Channel
		// 子通道由 parent 继承 PSC，但 monitor file 直接存储字段；优先用 m 自身字段，
		// 空值（子通道未填）时回退到 file 父通道
		if strings.TrimSpace(provider) == "" || strings.TrimSpace(service) == "" || strings.TrimSpace(channel) == "" {
			for _, p := range file.Monitors {
				if strings.TrimSpace(p.Parent) == "" {
					provider = p.Provider
					service = p.Service
					channel = p.Channel
					break
				}
			}
		}
		model := m.Model
		if modelFilter != "" && model != modelFilter {
			continue
		}
		keys = append(keys, pscm{provider, service, channel, model})
	}

	if len(keys) == 0 {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"logs": []adminMonitorLogItem{}, "total": 0})
		return
	}

	// 用 context.WithTimeout 限制 DB 查询总时长（每条 PSCM 单独 limit，合并后裁剪）
	queryCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	db := h.storage.WithContext(queryCtx)
	logs := make([]adminMonitorLogItem, 0, limit)
	for _, k := range keys {
		records, err := db.GetHistoryWithLimit(k.provider, k.service, k.channel, k.model, since, limit)
		if err != nil {
			logger.Error("admin", "查询监测日志失败",
				"key", key, "provider", k.provider, "service", k.service,
				"channel", k.channel, "model", k.model, "error", err)
			apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询监测日志失败")
			return
		}
		for _, r := range records {
			logs = append(logs, adminMonitorLogItem{
				ID:          r.ID,
				Provider:    r.Provider,
				Service:     r.Service,
				Channel:     r.Channel,
				Model:       r.Model,
				Status:      r.Status,
				SubStatus:   r.SubStatus,
				HTTPCode:    r.HttpCode,
				Latency:     r.Latency,
				Timestamp:   r.Timestamp,
				ErrorDetail: r.ErrorDetail,
			})
		}
	}

	// 多 PSCM 合并后按 (timestamp DESC, id DESC) 整体排序，再裁剪到 limit。
	// 同一秒内 id 倒序保证分页结果稳定。
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].Timestamp == logs[j].Timestamp {
			return logs[i].ID > logs[j].ID
		}
		return logs[i].Timestamp > logs[j].Timestamp
	})
	if len(logs) > limit {
		logs = logs[:limit]
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"total": len(logs),
	})
}

// checkPSCConflict 预检新 MonitorFile 的 PSC 是否与当前已加载配置冲突。
// 检查范围：config.yaml 中已加载的监测项。
// monitors.d/ 内部冲突由 MonitorStore.Create 的文件系统检查覆盖。
func (h *Handler) checkPSCConflict(file *config.MonitorFile) error {
	pscKey, err := config.DeriveMonitorFileKey(*file)
	if err != nil {
		return err
	}

	// 将 monitors.d/ key（provider--service--channel）转为 PSC 格式（provider/service/channel）
	p, s, c, err := config.ParseMonitorFileKey(pscKey)
	if err != nil {
		return err
	}
	target := strings.ToLower(p) + "/" + strings.ToLower(s) + "/" + strings.ToLower(c)

	h.cfgMu.RLock()
	currentMonitors := h.config.Monitors
	h.cfgMu.RUnlock()

	existingKeys := config.CollectPSCKeys(currentMonitors)
	if _, exists := existingKeys[target]; exists {
		return &pscConflictError{psc: target}
	}
	return nil
}

type pscConflictError struct {
	psc string
}

func (e *pscConflictError) Error() string {
	return "PSC " + e.psc + " 已存在于当前配置中（config.yaml）"
}
