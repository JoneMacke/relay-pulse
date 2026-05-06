package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/logger"
	"monitor/internal/onboarding"
)

// checkAdminToken 验证管理员 Bearer token。
// 返回 true 表示验证通过，false 表示已返回错误响应。
func (h *Handler) checkAdminToken(c *gin.Context) bool {
	h.cfgMu.RLock()
	adminToken := h.config.Onboarding.AdminToken
	h.cfgMu.RUnlock()

	if adminToken == "" {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "管理后台暂不可用")
		return false
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "缺少 Authorization 请求头")
		return false
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Authorization 格式错误，应为 Bearer <token>")
		return false
	}

	token := strings.TrimPrefix(authHeader, bearerPrefix)
	if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
		apiError(c, http.StatusForbidden, ErrCodeForbidden, "管理员 token 无效")
		return false
	}

	return true
}

// AdminListSubmissions 管理员获取申请列表
// GET /api/admin/submissions
func (h *Handler) AdminListSubmissions(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	status := c.DefaultQuery("status", "all")
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	submissions, total, err := svc.AdminList(c.Request.Context(), status, limit, offset)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询申请列表失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"submissions": submissions,
		"total":       total,
		"limit":       limit,
		"offset":      offset,
	})
}

// AdminGetSubmission 管理员获取申请详情（含解密 API Key）
// GET /api/admin/submissions/:id
func (h *Handler) AdminGetSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	sub, apiKey, err := svc.AdminGetDetail(c.Request.Context(), publicID)
	if err != nil {
		logger.Error("admin", "获取申请详情失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取申请详情失败")
		return
	}
	if sub == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "申请不存在")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"submission": sub,
		"api_key":    apiKey,
	})
}

// AdminUpdateSubmission 管理员更新申请
// PUT /api/admin/submissions/:id
func (h *Handler) AdminUpdateSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	var updates map[string]any
	if err := c.ShouldBindJSON(&updates); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	sub, err := svc.AdminUpdate(c.Request.Context(), publicID, updates)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"submission": sub})
}

// AdminDeleteSubmission 管理员删除申请
// DELETE /api/admin/submissions/:id
func (h *Handler) AdminDeleteSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	if err := svc.AdminDelete(c.Request.Context(), publicID); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// AdminRejectSubmission 管理员驳回申请
// POST /api/admin/submissions/:id/reject
func (h *Handler) AdminRejectSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body) // note is optional

	if err := svc.AdminReject(c.Request.Context(), publicID, body.Note); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}

// AdminPublishSubmission 管理员上架申请
// POST /api/admin/submissions/:id/publish
func (h *Handler) AdminPublishSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")

	var req struct {
		Board string `json:"board"`
	}
	// 忽略解析错误（空 body 合法，使用默认值）
	_ = c.ShouldBindJSON(&req)
	board := strings.TrimSpace(req.Board)
	if board == "" {
		board = "hot"
	}
	switch board {
	case "hot", "secondary", "cold":
	default:
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "board 参数无效，可选值：hot/secondary/cold")
		return
	}

	if err := svc.AdminPublish(c.Request.Context(), publicID, board); err != nil {
		var conflict *onboarding.PSCConflictError
		if errors.As(err, &conflict) {
			logger.Warn("admin", "上架 PSC 冲突", "public_id", publicID,
				"psc", conflict.Provider+"/"+conflict.Service+"/"+conflict.Channel,
				"suggested", conflict.SuggestedChannel)
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{
					"code":    "PSC_CONFLICT",
					"message": conflict.Error(),
				},
				"suggested_channel": conflict.SuggestedChannel,
			})
			return
		}
		logger.Error("admin", "上架失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "published"})
}

// AdminTestSubmission 管理员对申请执行内联探测测试
// POST /api/admin/submissions/:id/test
func (h *Handler) AdminTestSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	if h.inlineProber == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "内联探测器未初始化")
		return
	}

	publicID := c.Param("id")
	sub, apiKey, err := svc.AdminGetDetail(c.Request.Context(), publicID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取申请详情失败")
		return
	}
	if sub == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "申请不存在")
		return
	}

	// 使用内联探测器同步执行
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result := h.inlineProber.Probe(ctx, sub.ServiceType, sub.TemplateName, sub.BaseURL, apiKey)

	c.JSON(http.StatusOK, gin.H{
		"probe_status":     result.ProbeStatus,
		"sub_status":       result.SubStatus,
		"http_code":        result.HTTPCode,
		"latency":          result.Latency,
		"error_message":    result.ErrorMessage,
		"response_snippet": result.ResponseSnippet,
		"probe_id":         result.ProbeID,
	})
}
