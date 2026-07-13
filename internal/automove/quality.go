package automove

import (
	"context"

	"monitor/internal/rpdiag"
)

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
