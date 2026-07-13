package automove

import (
	"context"
	"strings"

	"monitor/internal/rpdiag"
)

// qualityRecoveryDebounce 是自动升板（清除质量闩锁）所需的连续、代次互异的
// 新鲜 Recovered 快照数量。两次而非一次，避免单次抽样恰好命中恢复窗口就把
// 通道从备板拉回热板——需要跨两个不同评估代次都持续 Recovered 才放行。
const qualityRecoveryDebounce = 2

// qualityDecision 是单个候选在本轮评估中推进后的质量闩锁状态（纯值，无副作用）。
type qualityDecision struct {
	latched        bool
	recoveryCount  int
	triggerModels  string
	lastGeneration uint64
	reason         string // "quality_hardfail" 或 ""
}

// computeQualityLatch 从候选的既有持久化 override 推进一步质量闩锁。
//
//	!fresh（feed 失败 / 源为 nil）→ 冻结：闩锁/计数/触发模型/代次/reason 全部原样不动。
//	fresh 时按信号推进：
//	  HardFail  → 立即闩锁、计数清零、记录触发模型、reason=quality_hardfail；
//	  Recovered → 仅当已闩锁且代次相较上次推进（gen != prev.lastGeneration）时计数 +1，
//	              达到 qualityRecoveryDebounce 即升板（清空闩锁/计数/触发/reason）；
//	  Unknown   → 保持（不动计数、不清闩锁），但推进 lastGeneration，
//	              使同代次内的 Recovered 不会被重复计入。
//
// lastGeneration 无论何种 fresh 分支都推进到当前代次：它只在闩锁存续期用于
// "同代次去重"，一旦升板（未闩锁）即失去意义。
func computeQualityLatch(prev MonitorOverride, fresh bool, gen uint64, sig rpdiag.ChannelQualitySignal) qualityDecision {
	d := qualityDecision{
		latched:        prev.QualityLatched,
		recoveryCount:  prev.QualityRecoveryCount,
		triggerModels:  prev.QualityTriggerModels,
		lastGeneration: prev.QualityLastGeneration,
		// reason 逐字节沿用既有 override，绝不"发明"一个 reason：
		// config-secondary 通道会被持久化为 QualityLatched=true 且 BoardReason=""
		// （质量未造成实际移板），冻结时必须保持 ""，不能凭 QualityLatched 反推 "quality_hardfail"。
		reason: prev.BoardReason,
	}
	if !fresh {
		return d // 冻结：全部保持不变
	}
	switch sig.State {
	case rpdiag.QualityHardFail:
		d.latched = true
		d.recoveryCount = 0
		d.triggerModels = strings.Join(sig.TriggerModels, ",")
		d.lastGeneration = gen
		d.reason = "quality_hardfail"
	case rpdiag.QualityRecovered:
		if d.latched && gen != prev.QualityLastGeneration {
			d.recoveryCount++
		}
		d.lastGeneration = gen
		if d.latched && d.recoveryCount >= qualityRecoveryDebounce {
			// 升板：清空闩锁/计数/触发/reason，仅保留当前代次。
			d = qualityDecision{lastGeneration: gen}
		}
	case rpdiag.QualityUnknown:
		d.lastGeneration = gen // 保持，仅推进代次
	}
	return d
}

// computeAvailabilityLatched 用既有可用率闩锁作为记忆，套用 hot<->secondary 迟滞。
// 关键：记忆来自 prev.AvailabilityLatched（可用率自己的闩锁），绝不是被质量压下去
// 的 board——否则质量短暂降板会污染可用率判定（dual-latch 分离修复的核心）。
func computeAvailabilityLatched(prev bool, availability, thresholdDown, thresholdUp float64) bool {
	if prev {
		return availability < thresholdUp // 已闩锁：直到可用率回升越过 up 阈值才解闩
	}
	return availability < thresholdDown // 未闩锁：跌破 down 阈值才闩锁
}

// frozenQualityOverride 构造一个"冻结可用率、应用本轮质量决策"的 override：
// 用于 MinProbes / 历史查询失败等无法（重）算可用率的退出路径。
//
// 关键（Fix 2）：冻结可用率 = 把冻结的可用率闩锁套在**配置锚点** configBoard 上重算板位，
// 与正常路径完全一致——绝不沿用 prev.Board。prev.Board 会把「质量压下去的 secondary」
// 与「auto_cold_exempt 通道遗留的 stale cold」混进来，导致：① 冻结路径无质量信号时凭 prev
// 的质量 secondary 假性降板；② exempt 通道该解 cold 却被 prev.Board=cold 卡住。
// 冻结的可用率永远不会产出 cold（cold 需要一次实时可用率读数），故绝不设 ColdReason。
func frozenQualityOverride(configBoard string, prev MonitorOverride, q qualityDecision) MonitorOverride {
	board := configBoard // 锚点（hot 或 secondary）——cold 绝不会是 configBoard
	if prev.AvailabilityLatched {
		board = "secondary" // 冻结的可用率闩锁
	}
	if q.latched {
		board = "secondary" // 质量上限压到 secondary
	}
	reason := q.reason
	if configBoard == "secondary" {
		reason = "" // config-secondary：质量未造成实际移板，不作虚假声明
	}
	return MonitorOverride{
		Board:                 board,
		ColdReason:            "",
		BoardReason:           reason,
		QualityLatched:        q.latched,
		QualityRecoveryCount:  q.recoveryCount,
		QualityTriggerModels:  q.triggerModels,
		QualityLastGeneration: q.lastGeneration,
		AvailabilityLatched:   prev.AvailabilityLatched,
	}
}

// isInertHotOverride 报告该 override 是否等价于"无 override"（配置热板、无任何
// 闩锁/reason/sponsor 需要保留）。冻结退出路径（MinProbes / DB 失败）用它决定是否
// 跳过写入，以保持"干净热板通道不产生多余 override"的既有不变量。
// 刻意忽略 QualityLastGeneration：它只在闩锁存续期有意义，未闩锁时不值得为它写 override。
func isInertHotOverride(ov MonitorOverride) bool {
	return isHotBoard(ov.Board) &&
		!ov.QualityLatched &&
		ov.BoardReason == "" &&
		ov.QualityRecoveryCount == 0 &&
		ov.QualityTriggerModels == "" &&
		!ov.AvailabilityLatched &&
		ov.ColdReason == "" &&
		ov.SponsorLevel == ""
}

// qualitySource 是 automove 对 rpdiag 的最小依赖：只需一份质量快照。
// *rpdiag.Client 已满足该接口（Task 3 起提供 QualitySignals）。
// 注入 nil（从未注入）时 evaluate 视质量为"无新鲜信号"——冻结，绝不清除既有闩锁；
// 真正的注入发生在后续 task（Task 7 的 main.go 装配）。
type qualitySource interface {
	QualitySignals(ctx context.Context) (rpdiag.QualitySnapshot, error)
}

// SetQualitySource 在 rpdiag 客户端构造完成后注入它（后续 task 装配）。
// 仅在启动期、评估循环开始前调用一次，之后 qualitySource 只读，故此处无锁赋值即正确。
func (s *Service) SetQualitySource(src qualitySource) {
	s.qualitySource = src
}
