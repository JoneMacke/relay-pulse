package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// pscSegmentPattern 校验 PSC 段仅允许小写字母、数字、短横线，且不能以短横线开头或结尾。
var pscSegmentPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// PSCConflictError 表示 PSC 冲突错误，包含冲突信息和建议值。
type PSCConflictError struct {
	Provider         string
	Service          string
	Channel          string
	SuggestedChannel string
}

func (e *PSCConflictError) Error() string {
	return fmt.Sprintf("PSC %s/%s/%s 已存在于当前运行配置中，请调整 target_channel（建议: %s）",
		e.Provider, e.Service, e.Channel, e.SuggestedChannel)
}

// Service 提供自助收录的核心业务逻辑。
type Service struct {
	store               Store
	cipher              *KeyCipher
	proofIssuer         *ProofIssuer
	cfg                 *config.OnboardingConfig
	configDir           string               // config.yaml 所在目录（用于定位 templates/ 等）
	monitorStore        *config.MonitorStore // monitors.d/ CRUD
	configMonitorExists func(provider, service, channel string) bool
	mu                  sync.RWMutex
}

// NewService 创建 Service。configDir 是 config.yaml 所在目录。
func NewService(store Store, cfg *config.OnboardingConfig, configDir string) (*Service, error) {
	cipher, err := NewKeyCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 API Key 加密器失败: %w", err)
	}

	proofIssuer := NewProofIssuer(cfg.ProofSecret, cfg.ProofTTLDuration)

	return &Service{
		store:       store,
		cipher:      cipher,
		proofIssuer: proofIssuer,
		cfg:         cfg,
		configDir:   configDir,
	}, nil
}

// SetMonitorStore 设置 monitors.d/ 存储（publish 时写入 monitors.d/）
func (s *Service) SetMonitorStore(store *config.MonitorStore) {
	s.monitorStore = store
}

// SetConfigMonitorCheck 设置主配置 PSC 冲突检查回调。
func (s *Service) SetConfigMonitorCheck(fn func(string, string, string) bool) {
	s.configMonitorExists = fn
}

// SubmitRequest 用户提交申请的请求参数
type SubmitRequest struct {
	ProviderName  string `json:"provider_name" binding:"required,max=100"`
	WebsiteURL    string `json:"website_url" binding:"required,url,max=500"`
	Category      string `json:"category" binding:"required,oneof=commercial public"`
	ServiceType   string `json:"service_type" binding:"required,oneof=cc cx gm"`
	TemplateName  string `json:"template_name" binding:"required,max=100"`
	SponsorLevel  string `json:"sponsor_level" binding:"max=50"`
	ChannelType   string `json:"channel_type" binding:"required,oneof=O R M X"`
	ChannelSource string `json:"channel_source" binding:"required,max=50"`
	BaseURL       string `json:"base_url" binding:"required,url,max=500"`
	APIKey        string `json:"api_key" binding:"required,min=10,max=500"`
	TestProof     string `json:"test_proof" binding:"required"`
	TestJobID     string `json:"test_job_id" binding:"required"`
	TestType      string `json:"test_type" binding:"required,max=100"`    // 测试类型（用于 proof 校验）
	TestAPIURL    string `json:"test_api_url" binding:"required,max=500"` // 测试 API URL（用于 proof 校验）
	TestLatency   int    `json:"test_latency"`
	TestHTTPCode  int    `json:"test_http_code"`
	Locale        string `json:"locale" binding:"max=10"`
}

// SubmitResponse 提交申请的响应
type SubmitResponse struct {
	PublicID    string `json:"public_id"`
	ContactInfo string `json:"contact_info"` // 运营联系方式
}

// Submit 处理用户提交申请。
func (s *Service) Submit(ctx context.Context, req *SubmitRequest, clientIP string) (*SubmitResponse, error) {
	// 停止受理 public/signal 自助赞助（2026-04-17 政策调整，详见 docs/user/sponsorship.md）
	if req.SponsorLevel == "public" || req.SponsorLevel == "signal" {
		return nil, fmt.Errorf("赞助等级 %q 已停止自助受理，请选择 pulse 或联系运营（QQ:18058344）", req.SponsorLevel)
	}

	// IP 限流
	ipHash := hashIP(clientIP)
	count, err := s.store.CountByIPToday(ctx, ipHash)
	if err != nil {
		return nil, fmt.Errorf("查询提交限额失败: %w", err)
	}
	if count >= s.cfg.MaxPerIPPerDay {
		return nil, fmt.Errorf("今日提交次数已达上限（%d/%d）", count, s.cfg.MaxPerIPPerDay)
	}

	// 验证 base_url HTTPS
	parsedBaseURL, err := url.Parse(req.BaseURL)
	if err != nil || parsedBaseURL.Scheme != "https" {
		return nil, fmt.Errorf("base_url 必须使用 HTTPS 协议")
	}

	// 验证 test_api_url 与 base_url 的 host 一致，防止"测试安全地址、提交不同目标"绕过 proof 绑定
	parsedTestURL, err := url.Parse(req.TestAPIURL)
	if err != nil || parsedTestURL.Hostname() == "" {
		return nil, fmt.Errorf("test_api_url 无效")
	}
	if !strings.EqualFold(parsedBaseURL.Hostname(), parsedTestURL.Hostname()) {
		return nil, fmt.Errorf("base_url 与 test_api_url 的 host 必须一致")
	}

	// 加密 API Key
	encrypted, err := s.cipher.Encrypt(req.APIKey)
	if err != nil {
		return nil, fmt.Errorf("加密 API Key 失败: %w", err)
	}
	fingerprint := s.cipher.Fingerprint(req.APIKey)
	last4 := Last4(req.APIKey)

	// 验证 test proof（绑定探测参数）
	err = s.proofIssuer.Verify(
		req.TestProof,
		req.TestJobID,
		req.TestType,
		req.TestAPIURL,
		fingerprint,
	)
	if err != nil {
		return nil, fmt.Errorf("测试证明无效: %w", err)
	}

	// 派生 channel code
	channelCode := deriveChannelCode(req.ChannelType, req.ChannelSource)

	now := time.Now().Unix()
	sub := &Submission{
		PublicID:          uuid.New().String(),
		Status:            StatusPending,
		ProviderName:      req.ProviderName,
		WebsiteURL:        req.WebsiteURL,
		Category:          req.Category,
		ServiceType:       req.ServiceType,
		TemplateName:      req.TemplateName,
		SponsorLevel:      req.SponsorLevel,
		ChannelType:       req.ChannelType,
		ChannelSource:     req.ChannelSource,
		ChannelCode:       channelCode,
		BaseURL:           req.BaseURL,
		APIKeyEncrypted:   encrypted,
		APIKeyFingerprint: fingerprint,
		APIKeyLast4:       last4,
		TestJobID:         req.TestJobID,
		TestPassedAt:      now,
		TestLatency:       req.TestLatency,
		TestHTTPCode:      req.TestHTTPCode,
		SubmitterIPHash:   ipHash,
		Locale:            req.Locale,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.store.Save(ctx, sub); err != nil {
		return nil, err
	}

	logger.Info("onboarding", "新申请已提交",
		"public_id", sub.PublicID,
		"provider", req.ProviderName,
		"service_type", req.ServiceType,
		"channel", channelCode)

	return &SubmitResponse{
		PublicID:    sub.PublicID,
		ContactInfo: s.cfg.ContactInfo,
	}, nil
}

// GetStatus 查询申请状态（用户端）
func (s *Service) GetStatus(ctx context.Context, publicID string) (*Submission, error) {
	return s.store.GetByPublicID(ctx, publicID)
}

// AdminList 管理员列表查询
func (s *Service) AdminList(ctx context.Context, status string, limit, offset int) ([]*Submission, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, status, limit, offset)
}

// AdminGetDetail 管理员获取详情（含解密后的 API Key）
func (s *Service) AdminGetDetail(ctx context.Context, publicID string) (*Submission, string, error) {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil || sub == nil {
		return sub, "", err
	}

	apiKey, err := s.cipher.Decrypt(sub.APIKeyEncrypted)
	if err != nil {
		return sub, "", fmt.Errorf("解密 API Key 失败: %w", err)
	}

	return sub, apiKey, nil
}

// AdminUpdate 管理员更新申请
func (s *Service) AdminUpdate(ctx context.Context, publicID string, updates map[string]any) (*Submission, error) {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, fmt.Errorf("申请不存在")
	}

	// 应用允许的更新字段
	if v, ok := updates["provider_name"].(string); ok && v != "" {
		sub.ProviderName = v
	}
	if v, ok := updates["website_url"].(string); ok && v != "" {
		sub.WebsiteURL = v
	}
	if v, ok := updates["category"].(string); ok && v != "" {
		sub.Category = v
	}
	if v, ok := updates["service_type"].(string); ok && v != "" {
		sub.ServiceType = v
	}
	if v, ok := updates["template_name"].(string); ok && v != "" {
		sub.TemplateName = v
	}
	if v, ok := updates["sponsor_level"].(string); ok && v != "" {
		sub.SponsorLevel = v
	}
	if v, ok := updates["channel_type"].(string); ok && v != "" {
		sub.ChannelType = v
	}
	if v, ok := updates["channel_source"].(string); ok && v != "" {
		sub.ChannelSource = v
	}
	if v, ok := updates["target_provider"].(string); ok {
		sub.TargetProvider = v
	}
	if v, ok := updates["target_service"].(string); ok {
		sub.TargetService = v
	}
	if v, ok := updates["target_channel"].(string); ok {
		sub.TargetChannel = v
	}
	if v, ok := updates["base_url"].(string); ok && v != "" {
		sub.BaseURL = v
	}
	if v, ok := updates["channel_name"].(string); ok {
		sub.ChannelName = v
	}
	if v, ok := updates["listed_since"].(string); ok {
		sub.ListedSince = v
	}
	if v, ok := updates["expires_at"].(string); ok {
		sub.ExpiresAt = v
	}
	if v, ok := updates["price_min"].(float64); ok {
		sub.PriceMin = v
	}
	if v, ok := updates["price_max"].(float64); ok {
		sub.PriceMax = v
	}
	if v, ok := updates["admin_note"].(string); ok {
		sub.AdminNote = v
	}
	if v, ok := updates["admin_config_json"].(string); ok {
		sub.AdminConfigJSON = v
	}

	// 重新派生 channel code
	sub.ChannelCode = deriveChannelCode(sub.ChannelType, sub.ChannelSource)
	sub.UpdatedAt = time.Now().Unix()

	if err := s.store.Update(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// AdminDelete 删除申请（硬删除，已上架的不允许删除）
func (s *Service) AdminDelete(ctx context.Context, publicID string) error {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status == StatusPublished {
		return fmt.Errorf("已上架的申请不能删除，请先在通道管理中下架")
	}
	return s.store.DeleteByPublicID(ctx, publicID)
}

// AdminReject 驳回申请
func (s *Service) AdminReject(ctx context.Context, publicID, note string) error {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status == StatusPublished {
		return fmt.Errorf("已上架的申请不能驳回")
	}

	now := time.Now().Unix()
	sub.Status = StatusRejected
	sub.AdminNote = note
	sub.ReviewedAt = &now
	sub.UpdatedAt = now
	return s.store.Update(ctx, sub)
}

// AdminPublish 上架：生成 ServiceConfig 并写入 monitors.d/。
// board 指定目标版块（hot/secondary/cold），优先级高于 AdminConfigJSON 中的同名字段。
// 使用原子文件写入（temp + fsync + rename）确保安全。
func (s *Service) AdminPublish(ctx context.Context, publicID, board string) error {
	if board == "" {
		board = "hot"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status != StatusPending && sub.Status != StatusApproved {
		return fmt.Errorf("只有待审核或已批准的申请可以上架，当前状态: %s", sub.Status)
	}

	// 解密 API Key
	apiKey, err := s.cipher.Decrypt(sub.APIKeyEncrypted)
	if err != nil {
		return fmt.Errorf("解密 API Key 失败: %w", err)
	}

	// 构建 ServiceConfig
	monitorCfg := s.buildServiceConfig(sub, apiKey)

	// 如果管理员有自定义配置，覆盖
	if sub.AdminConfigJSON != "" {
		var adminCfg config.ServiceConfig
		if err := json.Unmarshal([]byte(sub.AdminConfigJSON), &adminCfg); err != nil {
			return fmt.Errorf("解析管理员配置失败: %w", err)
		}
		monitorCfg = adminCfg
		// 确保 API key 不会被管理员配置覆盖为空
		if monitorCfg.APIKey == "" {
			monitorCfg.APIKey = apiKey
		}
	}
	// board 参数优先级高于 AdminConfigJSON，显式覆盖
	monitorCfg.Board = board

	// 发布前校验：验证生成的 monitor 配置是否合法
	if err := s.validateMonitorConfig(monitorCfg); err != nil {
		return fmt.Errorf("待发布 monitor 配置无效: %w", err)
	}

	// PSC 冲突预检：确认不与已有 monitors 冲突
	if s.configMonitorExists != nil &&
		s.configMonitorExists(monitorCfg.Provider, monitorCfg.Service, monitorCfg.Channel) {
		suggested := s.suggestUniqueChannel(monitorCfg.Provider, monitorCfg.Service, monitorCfg.Channel)
		return &PSCConflictError{
			Provider:         monitorCfg.Provider,
			Service:          monitorCfg.Service,
			Channel:          monitorCfg.Channel,
			SuggestedChannel: suggested,
		}
	}

	// 写入 monitors.d/
	if s.monitorStore == nil {
		return fmt.Errorf("MonitorStore 未初始化，无法写入 monitors.d/")
	}

	monitorFile := &config.MonitorFile{
		Metadata: config.MonitorFileMetadata{
			Source:    "onboarding",
			Revision:  1,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Monitors: []config.ServiceConfig{monitorCfg},
	}

	if err := s.monitorStore.Create(monitorFile); err != nil {
		return fmt.Errorf("写入 monitors.d/ 失败: %w", err)
	}

	// 更新 DB 状态
	now := time.Now().Unix()
	sub.Status = StatusPublished
	sub.ReviewedAt = &now
	sub.UpdatedAt = now
	if err := s.store.Update(ctx, sub); err != nil {
		// 文件已写入但 DB 更新失败 — 记录错误但不回滚文件
		// 下次热更新会正常加载，管理员可通过 admin 面板修正状态
		logger.Error("onboarding", "更新申请状态失败（文件已写入）",
			"public_id", publicID, "error", err)
		return fmt.Errorf("已写入配置文件但更新数据库状态失败: %w", err)
	}

	logger.Info("onboarding", "申请已上架",
		"public_id", publicID,
		"provider", sub.ProviderName,
		"channel", sub.ChannelCode)

	return nil
}

// IssueProof 签发测试证明（供内联探测调用）。
// 参数来自探测结果：jobID, testType, apiURL, apiKey。
func (s *Service) IssueProof(jobID, testType, apiURL, apiKey string) string {
	fingerprint := s.cipher.Fingerprint(apiKey)
	return s.proofIssuer.Issue(jobID, testType, apiURL, fingerprint)
}

// buildServiceConfig 从 Submission 构建 ServiceConfig
func (s *Service) buildServiceConfig(sub *Submission, apiKey string) config.ServiceConfig {
	// 派生默认 PSC 标识
	providerSlug := strings.ToLower(strings.ReplaceAll(sub.ProviderName, " ", "-"))
	serviceType := sub.ServiceType
	channelCode := sub.ChannelCode

	// 管理员可覆盖最终发布时的 PSC 标识
	if v := strings.TrimSpace(sub.TargetProvider); v != "" {
		providerSlug = v
	}
	if v := strings.TrimSpace(sub.TargetService); v != "" {
		serviceType = v
	}
	if v := strings.TrimSpace(sub.TargetChannel); v != "" {
		channelCode = v
	}

	cfg := config.ServiceConfig{
		Provider:     providerSlug,
		ProviderName: sub.ProviderName,
		ProviderURL:  sub.WebsiteURL,
		Service:      serviceType,
		Channel:      channelCode,
		ChannelName:  sub.ChannelName,
		Template:     sub.TemplateName,
		BaseURL:      sub.BaseURL,
		APIKey:       apiKey,
		Category:     sub.Category,
		ListedSince:  sub.ListedSince,
		ExpiresAt:    sub.ExpiresAt,
		SponsorLevel: config.SponsorLevel(sub.SponsorLevel),
	}
	if sub.PriceMin != 0 {
		v := sub.PriceMin
		cfg.PriceMin = &v
	}
	if sub.PriceMax != 0 {
		v := sub.PriceMax
		cfg.PriceMax = &v
	}
	return cfg
}

// hashIP 计算 IP 地址的 SHA256 哈希
func hashIP(ip string) string {
	h := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(h[:])
}

// validateMonitorConfig 在发布前校验即将写入 monitors.d/ 的 monitor 配置。
func (s *Service) validateMonitorConfig(m config.ServiceConfig) error {
	if err := validatePSCSegment("provider", m.Provider); err != nil {
		return err
	}
	if err := validatePSCSegment("service", m.Service); err != nil {
		return err
	}
	if err := validatePSCSegment("channel", m.Channel); err != nil {
		return err
	}
	if strings.TrimSpace(m.BaseURL) == "" {
		return fmt.Errorf("base_url 不能为空")
	}

	if m.ExpiresAt != "" {
		if _, err := time.Parse("2006-01-02", m.ExpiresAt); err != nil {
			return fmt.Errorf("expires_at 格式错误，应为 YYYY-MM-DD")
		}
	}

	templateName := strings.TrimSpace(m.Template)
	if templateName == "" {
		return fmt.Errorf("template 不能为空")
	}

	// 检查模板文件是否存在
	templatePath := filepath.Join(s.configDir, "templates", templateName+".json")
	if _, err := config.LoadProbeTemplate(templatePath); err != nil {
		return fmt.Errorf("template %q 不存在或无效: %w", templateName, err)
	}

	return nil
}

// validatePSCSegment 校验 PSC 段格式
func validatePSCSegment(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if !pscSegmentPattern.MatchString(value) {
		return fmt.Errorf("%s 格式无效（%q），仅允许小写字母、数字、短横线，且不能以短横线开头或结尾", field, value)
	}
	return nil
}

// suggestUniqueChannel 生成不冲突的 channel 名（追加 -2、-3...）
func (s *Service) suggestUniqueChannel(provider, service, channel string) string {
	if s.configMonitorExists == nil {
		return channel + "-2"
	}
	for i := 2; i <= 99; i++ {
		candidate := fmt.Sprintf("%s-%d", channel, i)
		if !s.configMonitorExists(provider, service, candidate) {
			return candidate
		}
	}
	return channel + "-new"
}

// deriveChannelCode 从通道类型和来源派生通道代码
func deriveChannelCode(channelType, channelSource string) string {
	source := strings.ToLower(strings.ReplaceAll(channelSource, " ", ""))
	return fmt.Sprintf("%s-%s", strings.ToLower(channelType), source)
}
