package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/storage"
)

// ===== 请求/响应结构体 =====

// StatusQueryRequest 批量状态查询请求（用于 POST /api/status/batch）
type StatusQueryRequest struct {
	Queries []StatusQuery `json:"queries"`
}

// StatusQuery 单个查询条件
type StatusQuery struct {
	Provider string `json:"provider"`
	Service  string `json:"service,omitempty"`
	Channel  string `json:"channel,omitempty"`
}

// StatusQueryResponse 状态查询响应
type StatusQueryResponse struct {
	AsOf    string              `json:"as_of"`
	Results []StatusQueryResult `json:"results"`
}

// StatusQueryResult 单个查询的返回结果
type StatusQueryResult struct {
	Query               StatusQuery             `json:"query"`
	Provider            string                  `json:"provider,omitempty"`              // 原始标识
	ProviderDisplayName string                  `json:"provider_display_name,omitempty"` // 显示名称（仅在与标识不同时返回）
	Services            []StatusQueryService    `json:"services,omitempty"`
	Error               *StatusQueryErrorObject `json:"error,omitempty"`
}

// StatusQueryErrorObject 查询错误对象
type StatusQueryErrorObject struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusQueryService 单个 service 的结果
type StatusQueryService struct {
	Name        string               `json:"name"`                   // 原始标识
	DisplayName string               `json:"display_name,omitempty"` // 显示名称（仅在与标识不同时返回）
	Channels    []StatusQueryChannel `json:"channels"`
}

// StatusQueryChannel 单个 channel 的结果
type StatusQueryChannel struct {
	Name        string `json:"name"`                   // 原始标识（可能为空字符串）
	DisplayName string `json:"display_name,omitempty"` // 显示名称（仅在与标识不同时返回）
	Status      string `json:"status"`                 // up/down/degraded
	LatencyMs   int    `json:"latency_ms,omitempty"`   // 毫秒
	UpdatedAt   string `json:"updated_at,omitempty"`   // RFC3339 格式
	// Board: channel 级别的活跃性折叠结果（仅用于订阅校验等场景）
	// 注意：这是二值结果（hot/cold），不等同于 /api/status 中逐监测项返回的 board (hot|secondary|cold)
	// - hot: 该 channel 仍有活跃监测项（包含配置为 hot/secondary 的项）
	// - cold: 该 channel 下（排除 disabled）全部为 cold
	Board string `json:"board,omitempty"`
}

// ===== 常量 =====

const (
	maxQueryGET  = 20 // GET 请求最多 20 组查询
	maxQueryPOST = 50 // POST 请求最多 50 组查询
)

// ===== Handler 方法 =====

// GetStatusQuery GET /api/status/query
// 支持两种查询方式：
// - 单查：provider（必填）, service（可选）, channel（可选）
// - 多查：q=provider/service/channel 可重复，最多 20 组
func (h *Handler) GetStatusQuery(c *gin.Context) {
	rawQs := c.QueryArray("q")

	var queries []StatusQuery
	if len(rawQs) > 0 {
		// 多查模式
		if len(rawQs) > maxQueryGET {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, fmt.Sprintf("q 参数最多支持 %d 组查询", maxQueryGET))
			return
		}
		for _, raw := range rawQs {
			q, err := parsePackedQuery(raw)
			if err != nil {
				apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
				return
			}
			queries = append(queries, q)
		}
	} else {
		// 单查模式
		provider := strings.TrimSpace(c.Query("provider"))
		if provider == "" {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "provider 为必填参数（或使用 q=provider/service/channel）")
			return
		}
		queries = []StatusQuery{
			{
				Provider: provider,
				Service:  strings.TrimSpace(c.Query("service")),
				Channel:  strings.TrimSpace(c.Query("channel")),
			},
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	resp, err := h.executeStatusQuery(ctx, queries)
	if err != nil {
		logger.FromContext(c.Request.Context(), "api").Error("GetStatusQuery 失败", "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询失败，请稍后再试")
		return
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// PostStatusBatch POST /api/status/batch
// Body: {"queries":[{"provider":"X","service":"Y","channel":"Z"}, ...]}
// 最多支持 50 组查询
func (h *Handler) PostStatusBatch(c *gin.Context) {
	var req StatusQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求体必须是合法的 JSON")
		return
	}

	if len(req.Queries) == 0 {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "queries 不能为空")
		return
	}
	if len(req.Queries) > maxQueryPOST {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, fmt.Sprintf("queries 最多支持 %d 组查询", maxQueryPOST))
		return
	}

	// 规范化输入
	for i := range req.Queries {
		req.Queries[i].Provider = strings.TrimSpace(req.Queries[i].Provider)
		req.Queries[i].Service = strings.TrimSpace(req.Queries[i].Service)
		req.Queries[i].Channel = strings.TrimSpace(req.Queries[i].Channel)
		if req.Queries[i].Provider == "" {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "provider 为必填字段")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	resp, err := h.executeStatusQuery(ctx, req.Queries)
	if err != nil {
		logger.FromContext(c.Request.Context(), "api").Error("PostStatusBatch 失败", "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询失败，请稍后再试")
		return
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// ===== 内部方法 =====

// parsePackedQuery 解析紧凑格式的查询参数
// 格式：provider/service/channel（service 和 channel 可选）
func parsePackedQuery(raw string) (StatusQuery, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return StatusQuery{}, fmt.Errorf("q 不能为空")
	}

	parts := strings.Split(s, "/")
	if len(parts) > 3 {
		return StatusQuery{}, fmt.Errorf("q 格式错误: %s（应为 provider/service/channel）", raw)
	}

	q := StatusQuery{
		Provider: strings.TrimSpace(parts[0]),
	}
	if q.Provider == "" {
		return StatusQuery{}, fmt.Errorf("q 格式错误: %s（provider 不能为空）", raw)
	}
	if len(parts) >= 2 {
		q.Service = strings.TrimSpace(parts[1])
	}
	if len(parts) == 3 {
		q.Channel = strings.TrimSpace(parts[2])
	}
	return q, nil
}

// executeStatusQuery 执行状态查询核心逻辑
func (h *Handler) executeStatusQuery(ctx context.Context, queries []StatusQuery) (*StatusQueryResponse, error) {
	// 读取配置快照
	h.cfgMu.RLock()
	monitors := h.config.Monitors
	boardsEnabled := h.config.Boards.Enabled
	h.cfgMu.RUnlock()

	// 使用带 context 的 storage
	store := h.storage.WithContext(ctx)

	results := make([]StatusQueryResult, 0, len(queries))
	for _, q := range queries {
		// 展开查询目标（基于配置匹配）
		targets, queryErr := expandQueryTargets(monitors, boardsEnabled, q)
		if queryErr != nil {
			results = append(results, StatusQueryResult{
				Query: q,
				Error: queryErr,
			})
			continue
		}

		// 构建服务列表
		services := make([]StatusQueryService, 0, len(targets))
		for _, target := range targets {
			// 对 channels 按名称排序，保证输出稳定性
			sort.Slice(target.channels, func(i, j int) bool {
				return target.channels[i].name < target.channels[j].name
			})

			channelResults := make([]StatusQueryChannel, 0, len(target.channels))
			for _, ch := range target.channels {
				// 检查 context 是否已取消
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("查询超时或取消: %w", ctx.Err())
				default:
				}

				// 聚合该 channel 下的所有 model，取最差状态
				worstStatus := -1
				var worstRecord *storage.ProbeRecord
				for _, cm := range ch.models {
					// 检查 context 是否已取消
					select {
					case <-ctx.Done():
						return nil, fmt.Errorf("查询超时或取消: %w", ctx.Err())
					default:
					}

					// 按 model_id 稳定键查询，改展示名不断历史
					latest, err := store.GetLatestByModelID(cm.modelID)
					if err != nil {
						return nil, fmt.Errorf("查询失败(provider=%s service=%s channel=%s model=%s model_id=%s): %w",
							target.provider, target.service, ch.name, cm.model, cm.modelID, err)
					}

					status := -1
					if latest != nil {
						status = latest.Status
					}

					newWorst := pickWorstStatus(worstStatus, status)
					if newWorst != worstStatus {
						worstStatus = newWorst
						worstRecord = latest
						continue
					}

					// 同一严重程度时，优先选择更新时间更近的记录作为展示信息
					if latest != nil && worstRecord != nil && latest.Status == worstRecord.Status && latest.Timestamp > worstRecord.Timestamp {
						worstRecord = latest
					}
				}

				chResult := StatusQueryChannel{
					Name:        ch.name, // 返回原始标识
					DisplayName: ch.displayName,
					Status:      statusIntToString(worstStatus),
					Board:       ch.board,
				}
				if worstRecord != nil {
					chResult.LatencyMs = worstRecord.Latency
					chResult.UpdatedAt = time.Unix(worstRecord.Timestamp, 0).UTC().Format(time.RFC3339)
				}
				channelResults = append(channelResults, chResult)
			}

			services = append(services, StatusQueryService{
				Name:        target.service, // 返回原始标识
				DisplayName: target.serviceDisplayName,
				Channels:    channelResults,
			})
		}

		// 对 services 按名称排序，保证输出稳定性
		sort.Slice(services, func(i, j int) bool {
			return services[i].Name < services[j].Name
		})

		results = append(results, StatusQueryResult{
			Query:               q,
			Provider:            targets[0].provider, // 返回原始标识
			ProviderDisplayName: targets[0].providerDisplayName,
			Services:            services,
		})
	}

	return &StatusQueryResponse{
		AsOf:    time.Now().UTC().Format(time.RFC3339),
		Results: results,
	}, nil
}

// channelModel 是 channel 下单个监测层的 model 标识。
// modelID 是状态读取的稳定查询键（改展示名不断历史）；model 仅用于日志/错误信息。
type channelModel struct {
	model   string // 展示名（原始配置值，仅用于日志/错误信息）
	modelID string // 稳定 model_id（查询键）
}

// channelInfo 通道信息（内部使用）
type channelInfo struct {
	name        string         // 原始配置值（用于 API 返回）
	displayName string         // 显示名称（仅在与 name 不同时非空）
	board       string         // hot/cold（channel 级别的 board 状态）
	models      []channelModel // 该 channel 下的所有监测层（model_id + 展示名）
}

// serviceTarget 服务目标（内部使用）
type serviceTarget struct {
	provider            string        // 原始配置值
	providerDisplayName string        // Provider 显示名称（仅在与 provider 不同时非空）
	service             string        // 原始配置值
	serviceDisplayName  string        // Service 显示名称（仅在与 service 不同时非空）
	channels            []channelInfo // 通道列表
}

// expandQueryTargets 根据配置展开查询目标
// 支持 service/channel 为空时查询所有匹配项
// boardsEnabled: 是否启用热板/冷板功能
//
// Board 计算规则（channel 级别的活跃性折叠）：
// - 仅当 boards 启用且该 channel 下（排除 disabled）全为 cold 时，返回 "cold"
// - 其他情况（包括 hot/secondary 混合、仅 secondary 等）都返回 "hot"
// 注意：这是二值活跃性结果，不是完整的 board 枚举（不返回 secondary）
func expandQueryTargets(monitors []config.ServiceConfig, boardsEnabled bool, q StatusQuery) ([]serviceTarget, *StatusQueryErrorObject) {
	queryProvider := strings.ToLower(strings.TrimSpace(q.Provider))
	queryService := strings.ToLower(strings.TrimSpace(q.Service))
	queryChannel := strings.ToLower(strings.TrimSpace(q.Channel))

	// 第一步：筛选匹配的 provider
	var providerMatches []config.ServiceConfig
	var originalProvider string
	var providerDisplayName string
	for _, m := range monitors {
		if strings.ToLower(strings.TrimSpace(m.Provider)) == queryProvider {
			providerMatches = append(providerMatches, m)
			if originalProvider == "" {
				originalProvider = m.Provider
			}
			if providerDisplayName == "" && m.ProviderName != "" && m.ProviderName != m.Provider {
				providerDisplayName = m.ProviderName
			}
		}
	}
	if len(providerMatches) == 0 {
		return nil, &StatusQueryErrorObject{Code: "NOT_FOUND", Message: "provider 不存在"}
	}

	// 第二步：按 service 分组
	serviceMap := make(map[string][]config.ServiceConfig) // key: lowercase service
	serviceOriginal := make(map[string]string)            // key: lowercase service -> original name
	serviceDisplayNames := make(map[string]string)        // key: lowercase service -> display name

	for _, m := range providerMatches {
		svcLower := strings.ToLower(strings.TrimSpace(m.Service))
		serviceMap[svcLower] = append(serviceMap[svcLower], m)
		if _, ok := serviceOriginal[svcLower]; !ok {
			serviceOriginal[svcLower] = m.Service
		}
		if _, ok := serviceDisplayNames[svcLower]; !ok {
			if m.ServiceName != "" && m.ServiceName != m.Service {
				serviceDisplayNames[svcLower] = m.ServiceName
			}
		}
	}

	// 第三步：确定要查询的 service 列表
	var targetServices []string
	if queryService != "" {
		if _, ok := serviceMap[queryService]; !ok {
			return nil, &StatusQueryErrorObject{Code: "NOT_FOUND", Message: "service 不存在"}
		}
		targetServices = []string{queryService}
	} else {
		for svc := range serviceMap {
			targetServices = append(targetServices, svc)
		}
	}

	// 第四步：为每个 service 展开 channel（并聚合每个 channel 的 models）
	var targets []serviceTarget
	for _, svcLower := range targetServices {
		svcConfigs := serviceMap[svcLower]

		// 收集该 service 下的 channel（并聚合每个 channel 的 models）
		channelMap := make(map[string]*channelInfo)                   // key: lowercase channel
		modelSetByChannel := make(map[string]map[string]channelModel) // chLower -> model_id -> 监测层
		modelIDsByChannel := make(map[string][]string)                // chLower -> 去重后的 model_id 列表

		// board 统计：记录每个 channel 下是否有 hot/secondary/cold model
		type boardStat struct {
			hasHot  bool // hot 或 secondary 都视为活跃
			hasCold bool
		}
		boardStatByChannel := make(map[string]*boardStat) // chLower -> board stat

		for _, m := range svcConfigs {
			chLower := strings.ToLower(strings.TrimSpace(m.Channel))
			if _, ok := channelMap[chLower]; !ok {
				ch := &channelInfo{
					name:  m.Channel,
					board: "hot", // 默认 hot
				}
				if m.ChannelName != "" && m.ChannelName != m.Channel {
					ch.displayName = m.ChannelName
				}
				channelMap[chLower] = ch
			}

			// 统计 board（仅当 boardsEnabled 时有意义）
			// 注意：跳过 Disabled 的监测项，避免 disabled 的 hot model 污染 cold 判定
			if !m.Disabled {
				if _, ok := boardStatByChannel[chLower]; !ok {
					boardStatByChannel[chLower] = &boardStat{}
				}
				boardLower := strings.ToLower(strings.TrimSpace(m.Board))
				if boardLower == "cold" {
					boardStatByChannel[chLower].hasCold = true
				} else {
					// 空值、"hot" 或 "secondary" 都视为活跃（hasHot）
					boardStatByChannel[chLower].hasHot = true
				}
			}

			// 以 model_id 为去重键（状态查询的稳定键）。同一 channel 下不同展示名但
			// 同 model_id 的监测层会被折叠为一次查询（与状态展示读路径口径一致）。
			modelID := strings.TrimSpace(m.ModelID)
			if _, ok := modelSetByChannel[chLower]; !ok {
				modelSetByChannel[chLower] = make(map[string]channelModel)
			}
			if _, ok := modelSetByChannel[chLower][modelID]; !ok {
				modelSetByChannel[chLower][modelID] = channelModel{
					model:   strings.TrimSpace(m.Model),
					modelID: modelID,
				}
				modelIDsByChannel[chLower] = append(modelIDsByChannel[chLower], modelID)
			}
		}

		// 为每个 channel 填充 models 列表和 board 状态
		for chLower, ch := range channelMap {
			// 按 model_id 排序，保证输出确定性（不依赖 map 迭代顺序）
			ids := modelIDsByChannel[chLower]
			sort.Strings(ids)
			for _, id := range ids {
				ch.models = append(ch.models, modelSetByChannel[chLower][id])
			}

			// 计算 channel 的 board：仅当 boards 启用且该 channel 全为 cold 时，视为 cold
			if boardsEnabled {
				if st, ok := boardStatByChannel[chLower]; ok {
					if st.hasCold && !st.hasHot {
						ch.board = "cold"
					} else {
						ch.board = "hot"
					}
				}
			}
		}

		// 确定要查询的 channel 列表
		var channels []channelInfo
		if queryChannel != "" {
			ch, ok := channelMap[queryChannel]
			if !ok {
				return nil, &StatusQueryErrorObject{Code: "NOT_FOUND", Message: "channel 不存在"}
			}
			channels = []channelInfo{*ch}
		} else {
			for _, ch := range channelMap {
				channels = append(channels, *ch)
			}
		}

		targets = append(targets, serviceTarget{
			provider:            originalProvider,
			providerDisplayName: providerDisplayName,
			service:             serviceOriginal[svcLower],
			serviceDisplayName:  serviceDisplayNames[svcLower],
			channels:            channels,
		})
	}

	return targets, nil
}

// statusIntToString 将状态码转换为字符串
// 无数据/缺失（-1）时返回 "down"（符合 API 规范：只允许 up/down/degraded）
func statusIntToString(status int) string {
	switch status {
	case 1:
		return "up"
	case 2:
		return "degraded"
	default:
		return "down"
	}
}
