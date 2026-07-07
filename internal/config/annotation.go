package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// AnnotationFamily 注解语义分组，决定渲染区域和分隔符
type AnnotationFamily string

const (
	AnnotationFamilyPositive AnnotationFamily = "positive" // 正向（绿区）
	AnnotationFamilyNeutral  AnnotationFamily = "neutral"  // 中性（灰区）
	AnnotationFamilyNegative AnnotationFamily = "negative" // 负向（红区，用 | 分隔）
)

// defaultAnnotationPriority 未指定 priority 时的默认值
const defaultAnnotationPriority = 50

// Annotation 统一注解模型
// 后端直出 label/tooltip/icon，前端无需 i18n 映射
type Annotation struct {
	ID       string           `yaml:"id" json:"id"`                               // 唯一标识
	Family   AnnotationFamily `yaml:"family,omitempty" json:"family"`             // 语义分组
	Icon     string           `yaml:"icon,omitempty" json:"icon,omitempty"`       // 图标标识（前端 icon registry 映射）
	Label    string           `yaml:"label" json:"label"`                         // 显示文本（后端直出）
	Tooltip  string           `yaml:"tooltip,omitempty" json:"tooltip,omitempty"` // 提示文本
	Href     string           `yaml:"href,omitempty" json:"href,omitempty"`       // 可选链接
	Priority int              `yaml:"priority,omitempty" json:"priority"`         // 排序权重（越大越靠前）
	Origin   string           `yaml:"-" json:"origin"`                            // system | rule | config（不可配置，运行时填充）
	Metadata map[string]any   `yaml:"-" json:"metadata,omitempty"`                // 运行时元数据（前端渲染辅助）
}

// AnnotationMatch 规则匹配条件
// 空字段表示不限，所有非空字段必须同时匹配（AND 逻辑）
type AnnotationMatch struct {
	Provider     string       `yaml:"provider,omitempty" json:"provider,omitempty"`
	Service      string       `yaml:"service,omitempty" json:"service,omitempty"`
	Channel      string       `yaml:"channel,omitempty" json:"channel,omitempty"`
	Model        string       `yaml:"model,omitempty" json:"model,omitempty"`
	Category     string       `yaml:"category,omitempty" json:"category,omitempty"`
	SponsorLevel SponsorLevel `yaml:"sponsor_level,omitempty" json:"sponsor_level,omitempty"`
}

// AnnotationRule 注解规则
// 按配置顺序应用；同一条规则内先 remove 再 add
type AnnotationRule struct {
	Match  AnnotationMatch `yaml:"match" json:"match"`
	Add    []Annotation    `yaml:"add,omitempty" json:"add,omitempty"`
	Remove []string        `yaml:"remove,omitempty" json:"remove,omitempty"`
}

func (f AnnotationFamily) isValid() bool {
	switch f {
	case AnnotationFamilyPositive, AnnotationFamilyNeutral, AnnotationFamilyNegative:
		return true
	default:
		return false
	}
}

// matches 检查规则是否匹配指定监测项
func (m AnnotationMatch) matches(task ServiceConfig) bool {
	return matchField(m.Provider, task.Provider) &&
		matchField(m.Service, task.Service) &&
		matchField(m.Channel, task.Channel) &&
		matchField(m.Model, task.Model) &&
		matchField(m.Category, task.Category) &&
		matchField(string(m.SponsorLevel), string(task.SponsorLevel))
}

// matchField 空 expected 表示不限；非空时忽略大小写比较
func matchField(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	return strings.EqualFold(expected, strings.TrimSpace(actual))
}

// normalized 返回填充默认值后的副本
func (a Annotation) normalized(defaultOrigin string) Annotation {
	a.ID = strings.TrimSpace(a.ID)
	a.Icon = strings.TrimSpace(a.Icon)
	a.Label = strings.TrimSpace(a.Label)
	a.Tooltip = strings.TrimSpace(a.Tooltip)
	a.Href = strings.TrimSpace(a.Href)

	if a.Family == "" || !a.Family.isValid() {
		a.Family = AnnotationFamilyNeutral
	}
	if a.Priority == 0 {
		a.Priority = defaultAnnotationPriority
	}
	if a.Origin == "" {
		a.Origin = defaultOrigin
	}
	if a.Label == "" {
		a.Label = a.ID
	}
	return a
}

// resolveAnnotations 解析监测项的最终注解列表
// 优先级链：system annotations → annotation_rules (按配置顺序，先 remove 再 add)
// 同 ID 后写覆盖前写（last-writer-wins）
func resolveAnnotations(task ServiceConfig, rules []AnnotationRule, globalInterval time.Duration) []Annotation {
	byID := make(map[string]Annotation, 8)

	// 1. 系统派生注解
	for _, ann := range deriveSystemAnnotations(task, globalInterval) {
		byID[ann.ID] = ann
	}

	// 2. 按配置顺序应用规则
	for _, rule := range rules {
		if !rule.Match.matches(task) {
			continue
		}
		// 先 remove 再 add
		for _, id := range rule.Remove {
			id = strings.TrimSpace(id)
			if id != "" {
				delete(byID, id)
			}
		}
		for _, ann := range rule.Add {
			ann = ann.normalized("rule")
			ann.Origin = "rule"
			if ann.ID != "" {
				byID[ann.ID] = ann
			}
		}
	}

	if len(byID) == 0 {
		return nil
	}

	result := make([]Annotation, 0, len(byID))
	for _, ann := range byID {
		result = append(result, ann)
	}
	sortAnnotations(result)
	return result
}

// ResolveAnnotations 是 resolveAnnotations 的导出包装。
// 供运行时覆盖场景（如 automove 到期降级 SponsorLevel 后）重算完整注解集——
// 直接复用 config 热加载时的同一套派生+规则逻辑，而非只替换单条注解，
// 这样 annotation_rules 里针对 sponsor_level 的匹配规则依然生效。
func ResolveAnnotations(task ServiceConfig, rules []AnnotationRule, globalInterval time.Duration) []Annotation {
	return resolveAnnotations(task, rules, globalInterval)
}

// deriveSystemAnnotations 从监测项事实属性自动派生系统级注解
// key_type 为空时按官方 API 处理；annotation_rules 可用相同 ID 覆盖
func deriveSystemAnnotations(task ServiceConfig, _ time.Duration) []Annotation {
	var result []Annotation

	// key_type 派生：默认 official，user 时替换标签
	switch strings.ToLower(strings.TrimSpace(task.KeyType)) {
	case "user":
		result = append(result, Annotation{
			ID:       "key_type",
			Family:   AnnotationFamilyNeutral,
			Icon:     "user",
			Label:    "用户 Key",
			Tooltip:  "使用用户自有的 API Key",
			Priority: 75,
			Origin:   "system",
		})
	default: // "official" 或空值
		result = append(result, Annotation{
			ID:       "key_type",
			Family:   AnnotationFamilyPositive,
			Icon:     "shield-check",
			Label:    "官方 API",
			Tooltip:  "使用服务商提供的官方 API Key",
			Priority: 5,
			Origin:   "system",
		})
	}

	// category=public → 公益站标注
	if strings.EqualFold(strings.TrimSpace(task.Category), "public") {
		result = append(result, Annotation{
			ID:       "public_service",
			Family:   AnnotationFamilyNeutral,
			Icon:     "heart",
			Label:    "公益站",
			Tooltip:  "公益服务，免费提供",
			Priority: 20,
			Origin:   "system",
		})
	}

	// sponsor_level → 赞助标注
	if ann, ok := sponsorAnnotation(task.SponsorLevel); ok {
		result = append(result, ann)
	}

	// 监测间隔：始终标注当前通道的监测间隔
	if task.IntervalDuration > 0 {
		result = append(result, Annotation{
			ID:       "monitor_frequency",
			Family:   AnnotationFamilyNeutral,
			Icon:     "activity",
			Label:    fmt.Sprintf("%s", task.IntervalDuration),
			Tooltip:  fmt.Sprintf("监测间隔 %s", task.IntervalDuration),
			Priority: 15,
			Origin:   "system",
			Metadata: map[string]any{"interval_ms": task.IntervalDuration.Milliseconds()},
		})
	}

	return result
}

// sponsorAnnotation 根据赞助等级返回对应的注解
func sponsorAnnotation(level SponsorLevel) (Annotation, bool) {
	type sponsorMeta struct {
		id, icon, label, tooltip string
		priority                 int
	}

	meta := map[SponsorLevel]sponsorMeta{
		SponsorLevelPublic:   {"sponsor_public", "shield-heart", "公益链路", "公益赞助链路", 10},
		SponsorLevelSignal:   {"sponsor_signal", "signal", "信号链路", "Signal 级赞助链路", 20},
		SponsorLevelPulse:    {"sponsor_pulse", "pulse", "脉冲链路", "Pulse 级赞助链路", 40},
		SponsorLevelBeacon:   {"sponsor_beacon", "beacon", "信标链路", "Beacon 级赞助链路", 60},
		SponsorLevelBackbone: {"sponsor_backbone", "backbone", "骨干链路", "Backbone 级赞助链路", 80},
		SponsorLevelCore:     {"sponsor_core", "core", "核心链路", "Core 级赞助链路", 100},
	}

	m, ok := meta[level]
	if !ok {
		return Annotation{}, false
	}
	return Annotation{
		ID:       m.id,
		Family:   AnnotationFamilyPositive,
		Icon:     m.icon,
		Label:    m.label,
		Tooltip:  m.tooltip,
		Priority: m.priority,
		Origin:   "system",
	}, true
}

// sortAnnotations 按 family 分组 → priority desc → id asc 排序
func sortAnnotations(items []Annotation) {
	familyOrder := map[AnnotationFamily]int{
		AnnotationFamilyPositive: 0,
		AnnotationFamilyNeutral:  1,
		AnnotationFamilyNegative: 2,
	}

	sort.SliceStable(items, func(i, j int) bool {
		if familyOrder[items[i].Family] != familyOrder[items[j].Family] {
			return familyOrder[items[i].Family] < familyOrder[items[j].Family]
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority > items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
}
