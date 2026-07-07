package automove

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/storage"
)

// MonitorOverride 运行时覆盖字段（不修改配置，仅在 API 层和 scheduler 层应用）。
type MonitorOverride struct {
	Board        string              // 板块覆盖（"hot"/"secondary"/"cold"）
	ColdReason   string              // 冷板原因（仅 Board=="cold" 时有值）
	SponsorLevel config.SponsorLevel // 赞助等级覆盖（空值表示不覆盖）
}

// Service 自动移板服务。
// 定期基于 7 天可用率评估 hot/secondary/cold 板块归属；赞助到期仅将等级降为 pulse（不影响板块）。维护运行时 override map。
// cold override 是 sticky 的：一旦生成，后续评估不再重新评估该项，仅可通过 auto_cold_exempt 手动解除。
type Service struct {
	storage storage.Storage

	cfgMu sync.RWMutex
	cfg   *config.AppConfig

	callbackMu       sync.RWMutex
	onOverrideChange func() // override 变更时通知 scheduler/events 刷新

	// 原子指针替换：evaluate 生成新 map → Store；Handler 读取 → Load。
	// nil 表示无 override（auto_move 未启用或无需移板）。
	overrides atomic.Pointer[map[storage.MonitorKey]MonitorOverride]

	// 异步持久化串行化，避免并发 goroutine 乱序覆盖较新的快照。
	persistMu  sync.Mutex
	persistSeq atomic.Uint64

	stopCh    chan struct{}
	triggerCh chan struct{} // 热更新后触发立即评估
	stopOnce  sync.Once
}

// NewService 创建自动移板服务（不启动 goroutine）。
func NewService(store storage.Storage, cfg *config.AppConfig) *Service {
	svc := &Service{
		storage:   store,
		stopCh:    make(chan struct{}),
		triggerCh: make(chan struct{}, 1),
	}
	svc.cfg = cfg
	return svc
}

// Start 在独立 goroutine 中启动定时评估循环。
// 启动时立即执行一次 Evaluate。
func (s *Service) Start(ctx context.Context) {
	go s.loop(ctx)
}

// Restore 从存储恢复持久化 override 快照。
// 仅更新内存态，不触发 onOverrideChange 回调；调用方应在首次 Evaluate 前调用。
func (s *Service) Restore() error {
	overrideStore, ok := s.storage.(storage.OverrideStorage)
	if !ok {
		return nil // 存储实现不支持 override 持久化，静默跳过
	}

	records, err := overrideStore.ListMonitorOverrides()
	if err != nil {
		return fmt.Errorf("加载自动移板 override 失败: %w", err)
	}

	if len(records) == 0 {
		return nil
	}

	overrides := recordsToOverrides(records)
	s.overrides.Store(&overrides)
	logger.Info("AutoMover", "已从存储恢复 override", "count", len(overrides))
	return nil
}

// Stop 优雅关闭评估循环，并同步刷写最后一次 override 到存储。
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.flushOverrides()
}

// flushOverrides 同步将当前内存态 override 写入存储。
// 用于优雅退出时确保最后一次评估结果不丢失。
func (s *Service) flushOverrides() {
	overrideStore, ok := s.storage.(storage.OverrideStorage)
	if !ok {
		return
	}

	overrides := s.currentOverrides()
	records := overridesToRecords(overrides)

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	if err := overrideStore.ReplaceMonitorOverrides(records); err != nil {
		logger.Warn("AutoMover", "关闭时刷写 override 失败", "error", err, "count", len(records))
	}
}

// UpdateConfig 热更新配置。若 auto_move 被禁用，立即清空 override。
// 若仍启用，清理新配置中已不再参与自动移板的监测项的旧 override
// （board 变为 cold、被 disabled/hidden、变为子通道，或设置了 auto_cold_exempt 的情况）。
func (s *Service) UpdateConfig(cfg *config.AppConfig) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()

	// 若禁用，立即清空 override
	if !cfg.Boards.Enabled || !cfg.Boards.AutoMove.Enabled {
		s.replaceOverrides(nil)
		return
	}

	// auto_move 仍启用：清理已不再参与自动移板的监测项的旧 override
	s.purgeStaleOverrides(cfg)

	// 通知 loop 立即重新评估（非阻塞，缓冲区为 1 防止重复信号）
	select {
	case s.triggerCh <- struct{}{}:
	default:
	}
}

// purgeStaleOverrides 从当前 override map 中移除不再符合自动移板条件的 key。
// 保留条件与 evaluate() 一致：非 disabled、非 hidden、无 parent、board != cold。
// 当 auto_cold_exempt=true 时，会立即清除已有的 cold override；
// 当 configBoard=secondary 时，清除遗留的 Board=hot override（锚点上限，不向上越板）。
func (s *Service) purgeStaleOverrides(cfg *config.AppConfig) {
	ptr := s.overrides.Load()
	if ptr == nil {
		return
	}

	// 构建仍可参与自动移板的 key 集合及其 exempt 状态
	type eligibleInfo struct {
		configBoard    string
		autoColdExempt bool
		autoMoveExempt bool
	}
	eligible := make(map[storage.MonitorKey]eligibleInfo)
	for _, m := range cfg.Monitors {
		if m.Disabled || m.Hidden {
			continue
		}
		if strings.TrimSpace(m.Parent) != "" {
			continue
		}
		board := strings.ToLower(strings.TrimSpace(m.Board))
		if board == "" {
			board = "hot"
		}
		if board == "cold" {
			continue
		}
		key := storage.MonitorKey{
			Provider: m.Provider,
			Service:  m.Service,
			Channel:  m.Channel,
			Model:    m.Model,
		}
		eligible[key] = eligibleInfo{
			configBoard:    board,
			autoColdExempt: m.AutoColdExempt,
			autoMoveExempt: m.AutoMoveExempt,
		}
	}

	// 构建新 map，仅保留仍符合条件的 override（不原地修改，保证并发安全）
	filtered := make(map[storage.MonitorKey]MonitorOverride)
	for key, ov := range *ptr {
		info, ok := eligible[key]
		if !ok {
			continue // 不再参与自动移板（disabled/hidden/parent/manual-cold/已移除）
		}
		// auto_move_exempt 清除任意 availability-based override（人工完全接管移板决策）
		if info.autoMoveExempt {
			continue
		}
		// 锚点：configBoard=secondary 不允许遗留 Board=hot override（旧 promote 语义残留），
		// 立即丢弃使热更新无需等下一轮评估即满足"不向上越板"上限（与 evaluate 锚点逻辑一致）。
		if info.configBoard == "secondary" && isHotBoard(ov.Board) {
			continue
		}
		// auto_cold_exempt 清除 cold override（人工恢复信号）
		if info.autoColdExempt && isColdBoard(ov.Board) {
			continue
		}
		filtered[key] = ov
	}

	s.replaceOverrides(filtered)
}

// GetBoardOverride 查询指定监测项的 override。
// 返回 (MonitorOverride{}, false) 表示无 override，应使用配置原始值。
func (s *Service) GetBoardOverride(key storage.MonitorKey) (MonitorOverride, bool) {
	ptr := s.overrides.Load()
	if ptr == nil {
		return MonitorOverride{}, false
	}
	ov, ok := (*ptr)[key]
	return ov, ok
}

// Overrides 返回当前 override map 快照（只读）。
// 调用方应在单次请求内缓存返回值以保证一致性。
func (s *Service) Overrides() map[storage.MonitorKey]MonitorOverride {
	ptr := s.overrides.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

// SetOverrides 替换当前 override map（用于测试注入）。
// 注意：不触发 onOverrideChange 回调，避免测试中的意外副作用。
func (s *Service) SetOverrides(overrides map[storage.MonitorKey]MonitorOverride) {
	if len(overrides) == 0 {
		s.overrides.Store(nil)
	} else {
		s.overrides.Store(&overrides)
	}
}

// SetOnOverrideChange 设置 override 变更回调。
// 回调异步触发，用于通知 scheduler/events 等运行时依赖刷新状态。
func (s *Service) SetOnOverrideChange(fn func()) {
	s.callbackMu.Lock()
	s.onOverrideChange = fn
	s.callbackMu.Unlock()
}

// IsCold 返回指定监测项是否被 runtime override 判定为冷板。
// 支持 exact match 和同 PSC 父通道 cold override 向子模型的传播。
func (s *Service) IsCold(key storage.MonitorKey) bool {
	if s == nil {
		return false
	}
	overrides := s.currentOverrides()
	if len(overrides) == 0 {
		return false
	}
	// exact match
	if ov, ok := overrides[key]; ok && isColdBoard(ov.Board) {
		return true
	}
	// PSC 传播：同 provider/service/channel 的父通道有 cold override
	for k, ov := range overrides {
		if !isColdBoard(ov.Board) {
			continue
		}
		if k.Provider == key.Provider && k.Service == key.Service && k.Channel == key.Channel {
			return true
		}
	}
	return false
}

// ApplyOverrides 将 override map 应用到监测项列表（静态函数，不依赖 Service 实例）。
// exact match 作用于 root 监测项；PSC 回退仅作用于有 parent 的子模型。
// Board/ColdReason/SponsorLevel 字段会被覆盖。
func ApplyOverrides(monitors []config.ServiceConfig, overrides map[storage.MonitorKey]MonitorOverride) []config.ServiceConfig {
	if len(overrides) == 0 {
		return monitors
	}

	// 构建 PSC 级别的 override 索引
	pscOverrides := make(map[string]MonitorOverride, len(overrides))
	for key, ov := range overrides {
		pscKey := key.Provider + "|" + key.Service + "|" + key.Channel
		pscOverrides[pscKey] = ov
	}

	copied := make([]config.ServiceConfig, len(monitors))
	copy(copied, monitors)
	for i := range copied {
		key := storage.MonitorKey{
			Provider: copied[i].Provider,
			Service:  copied[i].Service,
			Channel:  copied[i].Channel,
			Model:    copied[i].Model,
		}

		// 精确匹配：root 监测项直接命中 override
		if ov, ok := overrides[key]; ok {
			applyOverrideToMonitor(&copied[i], ov)
			continue
		}

		// PSC 回退：子模型继承父通道的 override
		if strings.TrimSpace(copied[i].Parent) != "" {
			pscKey := copied[i].Provider + "|" + copied[i].Service + "|" + copied[i].Channel
			if ov, ok := pscOverrides[pscKey]; ok {
				applyOverrideToMonitor(&copied[i], ov)
			}
		}
	}
	return copied
}

func applyOverrideToMonitor(m *config.ServiceConfig, ov MonitorOverride) {
	if ov.Board != "" {
		m.Board = ov.Board
		if isColdBoard(ov.Board) {
			m.ColdReason = ov.ColdReason
		} else {
			m.ColdReason = ""
		}
	}
	if ov.SponsorLevel != "" {
		m.SponsorLevel = ov.SponsorLevel
	}
}

// Evaluate 执行一次完整的可用率评估和移板判断。
// 可导出，供测试和启动时首次调用。
func (s *Service) Evaluate(ctx context.Context) {
	snap := s.snapshot()
	if snap == nil {
		s.replaceOverrides(nil)
		return
	}

	if !snap.boardsEnabled || !snap.autoMove.Enabled {
		s.replaceOverrides(nil)
		return
	}

	overrides, stats := s.evaluate(ctx, snap)

	s.replaceOverrides(overrides)

	logger.Info("AutoMover", "评估完成",
		"checked", stats.checked,
		"cooled", stats.cooled,
		"demoted", stats.demoted,
		"promoted", stats.promoted,
		"expired", stats.expired,
		"skipped_min_probes", stats.skippedMinProbes)
}

// --- 内部实现 ---

type evalSnapshot struct {
	boardsEnabled     bool
	autoMove          config.BoardAutoMoveConfig
	degradedWeight    float64
	batchQueryMaxKeys int
	storageType       string
	monitors          []config.ServiceConfig
}

type evalStats struct {
	checked          int
	cooled           int
	demoted          int
	promoted         int
	expired          int
	skippedMinProbes int
}

// replaceOverrides 原子替换 override map，并在内容实际变化时触发回调和异步持久化。
func (s *Service) replaceOverrides(overrides map[storage.MonitorKey]MonitorOverride) {
	current := s.currentOverrides()
	if overridesEqual(current, overrides) {
		return
	}

	snapshot := cloneOverrides(overrides)
	if len(snapshot) == 0 {
		s.overrides.Store(nil)
	} else {
		s.overrides.Store(&snapshot)
	}
	s.persistOverridesAsync(snapshot)
	s.notifyOverrideChange()
}

func cloneOverrides(overrides map[storage.MonitorKey]MonitorOverride) map[storage.MonitorKey]MonitorOverride {
	if len(overrides) == 0 {
		return nil
	}
	cp := make(map[storage.MonitorKey]MonitorOverride, len(overrides))
	for k, v := range overrides {
		cp[k] = v
	}
	return cp
}

// persistOverridesAsync 异步持久化 override 快照到存储。
// 使用递增序号保证只有最新快照被写入，避免旧快照覆盖新快照。
func (s *Service) persistOverridesAsync(overrides map[storage.MonitorKey]MonitorOverride) {
	overrideStore, ok := s.storage.(storage.OverrideStorage)
	if !ok {
		return
	}

	records := overridesToRecords(overrides)
	seq := s.persistSeq.Add(1)

	go func() {
		s.persistMu.Lock()
		defer s.persistMu.Unlock()
		// 若已有更新的快照被排队，跳过本次写入
		if seq != s.persistSeq.Load() {
			return
		}
		if err := overrideStore.ReplaceMonitorOverrides(records); err != nil {
			logger.Warn("AutoMover", "持久化 override 失败", "error", err, "count", len(records))
		}
	}()
}

func overridesToRecords(overrides map[storage.MonitorKey]MonitorOverride) []storage.MonitorOverrideRecord {
	if len(overrides) == 0 {
		return nil
	}
	records := make([]storage.MonitorOverrideRecord, 0, len(overrides))
	for key, ov := range overrides {
		records = append(records, storage.MonitorOverrideRecord{
			Key:          key,
			Board:        ov.Board,
			ColdReason:   ov.ColdReason,
			SponsorLevel: string(ov.SponsorLevel),
		})
	}
	return records
}

func recordsToOverrides(records []storage.MonitorOverrideRecord) map[storage.MonitorKey]MonitorOverride {
	if len(records) == 0 {
		return nil
	}
	overrides := make(map[storage.MonitorKey]MonitorOverride, len(records))
	for _, r := range records {
		overrides[r.Key] = MonitorOverride{
			Board:        r.Board,
			ColdReason:   r.ColdReason,
			SponsorLevel: config.SponsorLevel(r.SponsorLevel),
		}
	}
	return overrides
}

func (s *Service) notifyOverrideChange() {
	s.callbackMu.RLock()
	cb := s.onOverrideChange
	s.callbackMu.RUnlock()
	if cb != nil {
		go cb()
	}
}

func overridesEqual(a, b map[storage.MonitorKey]MonitorOverride) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || bv != av {
			return false
		}
	}
	return true
}

func isColdBoard(board string) bool {
	return strings.EqualFold(strings.TrimSpace(board), "cold")
}

func isHotBoard(board string) bool {
	return strings.EqualFold(strings.TrimSpace(board), "hot")
}

func makeAutoColdReason(availability, threshold float64) string {
	return fmt.Sprintf("7天可用率 %.1f%% 低于自动冷板阈值 %.0f%%，已自动移入冷板",
		availability, threshold)
}

func (s *Service) snapshot() *evalSnapshot {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()

	cfg := s.cfg
	if cfg == nil {
		return nil
	}

	storageType := strings.ToLower(strings.TrimSpace(cfg.Storage.Type))
	if storageType == "" {
		storageType = "sqlite"
	}

	return &evalSnapshot{
		boardsEnabled:     cfg.Boards.Enabled,
		autoMove:          cfg.Boards.AutoMove,
		degradedWeight:    cfg.DegradedWeight,
		batchQueryMaxKeys: cfg.BatchQueryMaxKeys,
		storageType:       storageType,
		monitors:          cfg.Monitors,
	}
}

func (s *Service) loop(ctx context.Context) {
	// 首次立即执行
	s.Evaluate(ctx)

	for {
		interval := s.checkInterval()
		// ±10% jitter 避免所有实例同步
		jitter := time.Duration(float64(interval) * (rand.Float64()*0.2 - 0.1))
		timer := time.NewTimer(interval + jitter)

		select {
		case <-timer.C:
			s.Evaluate(ctx)
		case <-s.triggerCh:
			timer.Stop()
			s.Evaluate(ctx)
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.stopCh:
			timer.Stop()
			return
		}
	}
}

func (s *Service) checkInterval() time.Duration {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()

	if s.cfg == nil {
		return 30 * time.Minute
	}
	d := s.cfg.Boards.AutoMove.CheckIntervalDuration
	if d <= 0 {
		return 30 * time.Minute
	}
	return d
}

// currentOverrides 返回当前 override map 的快照（可能为 nil）
func (s *Service) currentOverrides() map[storage.MonitorKey]MonitorOverride {
	ptr := s.overrides.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

func (s *Service) evaluate(ctx context.Context, snap *evalSnapshot) (map[storage.MonitorKey]MonitorOverride, evalStats) {
	var stats evalStats
	overrides := make(map[storage.MonitorKey]MonitorOverride)
	nowUTC := time.Now().UTC()
	// 与 WebUI 的 7d day 对齐保持一致：endTime 为下一天 00:00 UTC
	// 注意：这个 UTC 对齐只服务于 7d 可用率窗口，与下方赞助到期判断（isSponsorExpired，按 CST 业务日解释）是两套独立时间基准，不要混用。
	endTime := alignToNextUTCDay(nowUTC)
	currentOverrides := s.currentOverrides()

	// sponsorDowngradeKeys 收集本次评估中需要降级 SponsorLevel 的 key（到期且当前等级 > pulse）。
	// 板块移板完成后统一应用：只写入 SponsorLevel，不覆盖 Board/ColdReason。
	sponsorDowngradeKeys := make(map[storage.MonitorKey]string) // key → expiresAt（仅用于日志）

	// applySponsorDowngrades 将到期降级合并到 overrides 中。
	// 必须在每个 return 路径前调用，以确保 sponsor 降级无论板块评估结果如何都生效。
	applySponsorDowngrades := func() {
		for key, expiresAt := range sponsorDowngradeKeys {
			ov := overrides[key] // 可能已含 board override，或为零值
			ov.SponsorLevel = config.SponsorLevelPulse
			overrides[key] = ov
			stats.expired++
			logger.Info("AutoMover", "赞助到期，自动降级赞助等级",
				"monitor", key.Provider+"/"+key.Service+"/"+key.Channel,
				"expires_at", expiresAt)
		}
	}

	// 收集 hot/secondary 的根监测项（排除 parent/disabled/hidden/cold）
	type candidate struct {
		key            storage.MonitorKey
		configBoard    string
		autoColdExempt bool
	}
	var candidates []candidate

	for _, m := range snap.monitors {
		if m.Disabled || m.Hidden {
			continue
		}
		// 仅处理根监测项（无 parent）
		if strings.TrimSpace(m.Parent) != "" {
			continue
		}
		board := strings.ToLower(strings.TrimSpace(m.Board))
		if board == "" {
			board = "hot"
		}
		if board == "cold" {
			continue
		}

		key := storage.MonitorKey{
			Provider: m.Provider,
			Service:  m.Service,
			Channel:  m.Channel,
			Model:    m.Model,
		}

		// 到期检查：到期日当天仍有效，次日起开始降级赞助等级。
		// 必须在 sticky-cold 块之前执行，确保 sticky-cold 通道也能被记录（否则 continue 会跳过）。
		// 本步骤只记录降级意图，不设置 Board——板块位置完全由后续可用率评估决定。
		expiresAt := strings.TrimSpace(m.ExpiresAt)
		if expiresAt != "" {
			if isSponsorExpired(expiresAt, nowUTC) {
				// 仅当赞助等级高于 pulse 时才需降级（避免将低等级"升级"到 pulse）
				if m.SponsorLevel.Weight() > config.SponsorLevelPulse.Weight() {
					sponsorDowngradeKeys[key] = expiresAt
				}
			}
		}

		// 已有 cold override 是 sticky 的：直接保留，不再重新评估。
		// 但 auto_cold_exempt 的项跳过 sticky 保留（人工恢复信号优先）。
		// SponsorLevel 降级已在上方记录，applySponsorDowngrades 会在 return 前合并。
		if ov, ok := currentOverrides[key]; ok && isColdBoard(ov.Board) {
			if !m.AutoColdExempt {
				overrides[key] = ov
				continue
			}
			// exempt: 不保留 cold，继续进入正常评估流程
		}

		// auto_move_exempt：跳过所有基于可用率的移板逻辑。
		// 到期的 SponsorLevel 降级已在上方记录，不受此约束。
		if m.AutoMoveExempt {
			continue
		}

		candidates = append(candidates, candidate{
			key:            key,
			configBoard:    board,
			autoColdExempt: m.AutoColdExempt,
		})
	}

	if len(candidates) == 0 {
		applySponsorDowngrades()
		if len(overrides) == 0 {
			return nil, stats
		}
		return overrides, stats
	}

	// 构建批量查询 keys
	keys := make([]storage.MonitorKey, len(candidates))
	for i, c := range candidates {
		keys[i] = c.key
	}

	// 分批查询历史记录（考虑 SQLite 参数上限）
	batchSize := snap.batchQueryMaxKeys
	if batchSize <= 0 {
		batchSize = 300
	}
	if snap.storageType == "sqlite" {
		const sqliteMaxParams = 999
		const keyParams = 4
		maxKeys := sqliteMaxParams / keyParams
		if batchSize > maxKeys {
			batchSize = maxKeys
		}
	}

	store := s.storage.WithContext(ctx)
	since := endTime.Add(-time.Duration(availabilityBucketCount) * availabilityBucketWindow)

	// 合并所有批次结果
	allHistory := make(map[storage.MonitorKey][]*storage.ProbeRecord)
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		historyMap, err := store.GetHistoryBatch(batch, since)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("AutoMover", "批量查询历史记录失败", "error", err)
			}
			// 查询失败时无法判断可用率，跳过板块评估；赞助等级降级仍正常应用。
			applySponsorDowngrades()
			return overrides, stats
		}
		for k, v := range historyMap {
			allHistory[k] = v
		}
	}

	// 冷板 + 锚点移板评估：configBoard（手动配置板位）决定自动移板上限，绝不向上越板；
	// effectiveBoard 仅在 configBoard=hot 时用于 hot↔secondary 迟滞判定。
	// 到期通道走与普通通道相同的路径：到期仅降赞助等级，板块位置完全由可用率决定。
	for _, c := range candidates {
		stats.checked++
		records := allHistory[c.key]
		availability, total := CalculateAvailability(records, endTime, snap.degradedWeight)

		// 有效板块 = 当前 override 优先，否则取配置值（用于日志 from 字段）。
		// 特例：auto_cold_exempt 打破了 sticky cold，此时旧 cold override 无效，
		// 应回到 configBoard 重新评估，而非沿用 cold 作为起点。
		effectiveBoard := c.configBoard
		if ov, ok := currentOverrides[c.key]; ok && ov.Board != "" {
			if !(c.autoColdExempt && isColdBoard(ov.Board)) {
				effectiveBoard = ov.Board
			}
		}

		if total < snap.autoMove.MinProbes {
			stats.skippedMinProbes++
			continue
		}

		// 冷板判断：可用率低于 threshold_cold 且未被 exempt
		if !c.autoColdExempt && availability < snap.autoMove.ThresholdCold {
			overrides[c.key] = MonitorOverride{
				Board:      "cold",
				ColdReason: makeAutoColdReason(availability, snap.autoMove.ThresholdCold),
			}
			stats.cooled++
			logger.Info("AutoMover", "自动移板: *→cold",
				"monitor", c.key.Provider+"/"+c.key.Service+"/"+c.key.Channel,
				"from", effectiveBoard,
				"availability", availability,
				"threshold_cold", snap.autoMove.ThresholdCold)
			continue
		}

		// board（configBoard）是自动移板的"锚点/天花板"：自动移板只能在配置板位及以下浮动，
		// 绝不向上越板。cold（向下闸，对 hot/secondary 均适用）已在上方处理。
		//   - configBoard=secondary：非 cold 状态一律保持配置 secondary，不写 override；
		//     任何遗留的 Board=hot override 会因本轮不写、随 replaceOverrides 整图替换被丢弃，自动落回 secondary。
		if c.configBoard == "secondary" {
			continue
		}

		// configBoard=="hot"：effectiveBoard 反映当前是否已被降级，决定用 threshold_down 还是 threshold_up（迟滞防抖）。
		switch effectiveBoard {
		case "hot":
			if availability < snap.autoMove.ThresholdDown {
				overrides[c.key] = MonitorOverride{Board: "secondary"}
				stats.demoted++
				logger.Info("AutoMover", "自动移板: hot→secondary",
					"monitor", c.key.Provider+"/"+c.key.Service+"/"+c.key.Channel,
					"availability", availability,
					"threshold_down", snap.autoMove.ThresholdDown)
			}
		case "secondary":
			if availability >= snap.autoMove.ThresholdUp {
				// 恢复：可用率回到 threshold_up → 清除降级 override（不写即可，整图替换后回到配置 hot）。
				stats.promoted++
				logger.Info("AutoMover", "自动移板: secondary→hot",
					"monitor", c.key.Provider+"/"+c.key.Service+"/"+c.key.Channel,
					"availability", availability,
					"threshold_up", snap.autoMove.ThresholdUp)
			} else {
				// 尚未恢复到 threshold_up → 保持降级 secondary。
				overrides[c.key] = MonitorOverride{Board: "secondary"}
			}
		}
	}

	applySponsorDowngrades()
	return overrides, stats
}

// alignToNextUTCDay 将时间向上对齐到下一天 00:00 UTC。
// 逻辑与 api/query.go alignTimestamp(t, "day") 保持一致。
func alignToNextUTCDay(t time.Time) time.Time {
	truncated := t.UTC().Truncate(24 * time.Hour)
	if truncated.Before(t.UTC()) {
		return truncated.Add(24 * time.Hour)
	}
	return truncated
}
