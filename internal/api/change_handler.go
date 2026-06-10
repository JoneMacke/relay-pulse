package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"monitor/internal/change"
	"monitor/internal/logger"
)

// AuthChange 验证 API Key 并返回匹配通道列表
// POST /api/change/auth
func (h *Handler) AuthChange(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	// IP 限流：这是匿名 pre-auth 端点，按 API Key 指纹查候选通道，
	// 不限流则可被高频枚举。复用公共探测 limiter（main.go 无条件初始化）。
	if h.probeLimiter != nil && !h.probeLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "请求过于频繁，请稍后再试")
		return
	}

	var req change.AuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	resp, err := svc.Auth(req.APIKey)
	if err != nil {
		// 统一错误文案，防止枚举
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "API Key 验证失败")
		return
	}

	c.JSON(http.StatusOK, resp)
}

// SubmitChange 提交变更请求
// POST /api/change/submit
func (h *Handler) SubmitChange(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	var req change.SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("change", "提交参数校验失败", "error", err)
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效，请检查必填字段: "+err.Error())
		return
	}

	clientIP := c.ClientIP()
	resp, err := svc.Submit(c.Request.Context(), &req, clientIP)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// GetChangeStatus 查询变更请求状态
// GET /api/change/:id
func (h *Handler) GetChangeStatus(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	if publicID == "" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求 ID 不能为空")
		return
	}

	cr, err := svc.GetStatus(c.Request.Context(), publicID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询请求状态失败")
		return
	}
	if cr == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "变更请求不存在")
		return
	}

	// 用户端返回有限字段
	c.JSON(http.StatusOK, gin.H{
		"public_id":  cr.PublicID,
		"status":     cr.Status,
		"target_key": cr.TargetKey,
		"apply_mode": cr.ApplyMode,
		"created_at": cr.CreatedAt,
		"updated_at": cr.UpdatedAt,
	})
}

// === 管理端 ===

// AdminListChanges 管理员获取变更请求列表
// GET /api/admin/changes
func (h *Handler) AdminListChanges(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	status := c.DefaultQuery("status", "all")
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	changes, total, err := svc.AdminList(c.Request.Context(), status, limit, offset)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询变更请求列表失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"changes": changes,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// AdminGetChange 管理员获取变更请求详情
// GET /api/admin/changes/:id
func (h *Handler) AdminGetChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	cr, newKey, err := svc.AdminGetDetail(c.Request.Context(), publicID)
	if err != nil {
		logger.Error("admin", "获取变更请求详情失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取变更请求详情失败")
		return
	}
	if cr == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "变更请求不存在")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"change":  cr,
		"new_key": newKey,
	})
}

// AdminUpdateChange 管理员更新变更请求内容（proposed_changes 字段 + admin_note）
// PUT /api/admin/changes/:id
func (h *Handler) AdminUpdateChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	var updates map[string]any
	if err := c.ShouldBindJSON(&updates); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	cr, err := svc.AdminUpdate(c.Request.Context(), publicID, updates)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"change": cr})
}

// AdminApproveChange 管理员批准变更请求
// POST /api/admin/changes/:id/approve
func (h *Handler) AdminApproveChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := svc.AdminApprove(c.Request.Context(), publicID, body.Note); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "approved"})
}

// AdminRejectChange 管理员驳回变更请求
// POST /api/admin/changes/:id/reject
func (h *Handler) AdminRejectChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := svc.AdminReject(c.Request.Context(), publicID, body.Note); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}

// AdminApplyChange 管理员应用变更到 monitors.d/
// POST /api/admin/changes/:id/apply
func (h *Handler) AdminApplyChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	if err := svc.AdminApply(c.Request.Context(), publicID); err != nil {
		logger.Error("admin", "应用变更失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "applied"})
}

// AdminDeleteChange 管理员删除变更请求
// DELETE /api/admin/changes/:id
func (h *Handler) AdminDeleteChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	if err := svc.AdminDelete(c.Request.Context(), publicID); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
