package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"monitor/internal/logger"
)

// freeTierIntervalFloor 是免费层通道的探测间隔下限（floor）。
// 全局 interval 若比此值更慢，取全局值（不让免费通道比全局更频繁）。
const freeTierIntervalFloor = 5 * time.Minute

// isPaidSponsorLevel 判断赞助等级是否属于付费档（beacon/backbone/core）。
// 注意：normalizeDurationsForMonitorAt 早于 normalizeMonitorsPreInheritance 中的
// SponsorLevel 旧值迁移（即 deprecated 别名统一化步骤），因此本函数必须在内部先调用
// deprecatedToNew()，否则 "advanced"/"enterprise" 会被误判为免费档。
func isPaidSponsorLevel(level SponsorLevel) bool {
	if migrated, ok := level.deprecatedToNew(); ok {
		level = migrated
	}
	switch level {
	case SponsorLevelBeacon, SponsorLevelBackbone, SponsorLevelCore:
		return true
	default:
		return false
	}
}

// tierAwareDefaultInterval 返回未显式配置 interval 时的层级感知默认值：
//   - 付费层（beacon/backbone/core）：使用全局 interval（维持高频）
//   - 免费层（none/public/signal/pulse）：使用 freeTierIntervalFloor 与全局值中的较大者
//     （保证免费通道不会比全局更频繁，同时不低于 5m 的探测节奏）
func tierAwareDefaultInterval(app *AppConfig, level SponsorLevel) time.Duration {
	if isPaidSponsorLevel(level) {
		return app.IntervalDuration
	}
	// 免费层：取 floor 和全局值中的较大者
	if app.IntervalDuration > freeTierIntervalFloor {
		return app.IntervalDuration
	}
	return freeTierIntervalFloor
}

// monitorPSCPrefix 构造形如 "monitor[i] (provider=..., service=..., channel=...)" 的错误前缀。
// 当 index < 0 时省略 "[i]"（用于 ResolveSingleMonitor 等单条调用场景）。
func monitorPSCPrefix(index int, cfg *ServiceConfig) string {
	if index >= 0 {
		return fmt.Sprintf("monitor[%d] (provider=%s, service=%s, channel=%s)",
			index, cfg.Provider, cfg.Service, cfg.Channel)
	}
	return fmt.Sprintf("monitor (provider=%s, service=%s, channel=%s)",
		cfg.Provider, cfg.Service, cfg.Channel)
}

// normalizeDurationsForMonitor 解析单条 ServiceConfig 的 Duration/Retry 派生字段。
// 等价于 normalizeMonitorsPreInheritance 中针对单个 monitor 的 Duration 下发逻辑，但不依赖外层 index。
//
// 调用方：config.ResolveSingleMonitor（admin/onboarding 临时探测路径）。
// loader 路径走 normalizeMonitorsPreInheritance 的 for 循环版本（带 index，错误前缀含 "monitor[i]"）。
func normalizeDurationsForMonitor(app *AppConfig, cfg *ServiceConfig) error {
	return normalizeDurationsForMonitorAt(app, cfg, -1)
}

// normalizeDurationsForMonitorAt 是 normalizeDurationsForMonitor 的带 index 版本，
// 错误信息会带上 "monitor[i]" 前缀（兼容现有 loader 测试断言的错误格式）。
//
// 副作用：将 cfg 的 *Duration / RetryCount / RetryJitterValue 等派生字段重置后重新计算。
// 这是为了兼容热更新场景（slice 元素复用，旧派生值可能残留）。
func normalizeDurationsForMonitorAt(app *AppConfig, cfg *ServiceConfig, index int) error {
	// 注意：以下 yaml:"-" 字段在热更新/复用 slice 元素的场景下，旧值可能残留。
	// 每次 Normalize 都从零值开始重新计算，确保派生逻辑稳定。
	cfg.SlowLatencyDuration = 0
	cfg.TimeoutDuration = 0
	cfg.IntervalDuration = 0
	cfg.RetryCount = 0
	cfg.RetryBaseDelayDuration = 0
	cfg.RetryMaxDelayDuration = 0
	cfg.RetryJitterValue = 0

	prefix := monitorPSCPrefix(index, cfg)

	// 解析 monitor 级 slow_latency（如有配置）
	if trimmed := strings.TrimSpace(cfg.SlowLatency); trimmed != "" {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("%s: 解析 slow_latency 失败: %w", prefix, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s: slow_latency 必须大于 0", prefix)
		}
		cfg.SlowLatencyDuration = d
	}

	// slow_latency 下发：monitor > global（模板值已在 resolveTemplates 填入 monitor）
	if cfg.SlowLatencyDuration == 0 {
		cfg.SlowLatencyDuration = app.SlowLatencyDuration
	}

	// 解析 monitor 级 timeout（如有配置）
	if trimmed := strings.TrimSpace(cfg.Timeout); trimmed != "" {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("%s: 解析 timeout 失败: %w", prefix, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s: timeout 必须大于 0", prefix)
		}
		cfg.TimeoutDuration = d
	}

	// timeout 下发：monitor > global（模板值已在 resolveTemplates 填入 monitor）
	if cfg.TimeoutDuration == 0 {
		cfg.TimeoutDuration = app.TimeoutDuration
	}

	// 警告：slow_latency >= timeout 时黄灯基本不会触发
	if cfg.SlowLatencyDuration >= cfg.TimeoutDuration {
		warnArgs := []any{
			"provider", cfg.Provider,
			"service", cfg.Service,
			"channel", cfg.Channel,
			"slow_latency", cfg.SlowLatencyDuration,
			"timeout", cfg.TimeoutDuration,
		}
		if index >= 0 {
			warnArgs = append([]any{"monitor_index", index}, warnArgs...)
		}
		logger.Warn("config", "slow_latency >= timeout，慢响应黄灯可能不会触发", warnArgs...)
	}

	// 解析单监测项的 interval，空值按赞助层级回退：
	//   - 显式配置了 interval 字符串 → 无条件使用（最高优先级）
	//   - 未配置 → 付费层用全局 interval，免费层用 freeTierIntervalFloor（最小 5m）
	// 注意：interval 错误信息历史上只带 monitor[i] 前缀，不带 PSC（保持向后兼容）
	if trimmed := strings.TrimSpace(cfg.Interval); trimmed != "" {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			if index >= 0 {
				return fmt.Errorf("monitor[%d]: 解析 interval 失败: %w", index, err)
			}
			return fmt.Errorf("monitor: 解析 interval 失败: %w", err)
		}
		if d <= 0 {
			if index >= 0 {
				return fmt.Errorf("monitor[%d]: interval 必须大于 0", index)
			}
			return fmt.Errorf("monitor: interval 必须大于 0")
		}
		cfg.IntervalDuration = d
	} else {
		cfg.IntervalDuration = tierAwareDefaultInterval(app, cfg.SponsorLevel)
	}

	// ===== 重试配置下发：monitor > global（模板值已在 resolveTemplates 填入 monitor） =====

	// retry 下发
	if cfg.Retry != nil {
		if *cfg.Retry < 0 {
			return fmt.Errorf("%s: retry 必须 >= 0", prefix)
		}
		cfg.RetryCount = *cfg.Retry
	} else {
		cfg.RetryCount = app.RetryCount
	}

	// retry_base_delay 下发
	if trimmed := strings.TrimSpace(cfg.RetryBaseDelay); trimmed != "" {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("%s: 解析 retry_base_delay 失败: %w", prefix, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s: retry_base_delay 必须 > 0", prefix)
		}
		cfg.RetryBaseDelayDuration = d
	} else {
		cfg.RetryBaseDelayDuration = app.RetryBaseDelayDuration
	}

	// retry_max_delay 下发
	if trimmed := strings.TrimSpace(cfg.RetryMaxDelay); trimmed != "" {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("%s: 解析 retry_max_delay 失败: %w", prefix, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s: retry_max_delay 必须 > 0", prefix)
		}
		cfg.RetryMaxDelayDuration = d
	} else {
		cfg.RetryMaxDelayDuration = app.RetryMaxDelayDuration
	}

	// retry_jitter 下发
	if cfg.RetryJitter != nil {
		v := *cfg.RetryJitter
		if v < 0 || v > 1 {
			return fmt.Errorf("%s: retry_jitter 必须在 0 到 1 之间", prefix)
		}
		cfg.RetryJitterValue = v
	} else {
		cfg.RetryJitterValue = app.RetryJitterValue
	}

	// 最终校验：max >= base
	if cfg.RetryMaxDelayDuration < cfg.RetryBaseDelayDuration {
		return fmt.Errorf("%s: retry_max_delay 必须 >= retry_base_delay", prefix)
	}

	return nil
}

// normalizeMonitorsPreInheritance 继承前的监测项规范化
// 包括：派生字段重置、时间配置下发、重试配置下发、元数据规范化、Provider 状态注入
// 注意：不包括 board 默认值填充和注解解析，这些需要在继承后处理
func (c *AppConfig) normalizeMonitorsPreInheritance(ctx *normalizeContext) error {
	for i := range c.Monitors {
		// Annotations 由 annotation 解析逻辑重新计算（在 post-inheritance 阶段）
		c.Monitors[i].Annotations = nil

		// Duration/Retry 派生逻辑抽出到 normalizeDurationsForMonitorAt，
		// 与 ResolveSingleMonitor 共享同一份代码路径。
		if err := normalizeDurationsForMonitorAt(c, &c.Monitors[i], i); err != nil {
			return err
		}

		// Board 和 ColdReason 仅做 trim，不填充默认值（留给 post-inheritance 处理）
		// 这样子通道可以正确继承父通道的 board 配置
		c.Monitors[i].Board = strings.ToLower(strings.TrimSpace(c.Monitors[i].Board))
		c.Monitors[i].ColdReason = strings.TrimSpace(c.Monitors[i].ColdReason)

		// 标准化 category 为小写（与 Validate 的 isValidCategory 保持一致）
		c.Monitors[i].Category = strings.ToLower(strings.TrimSpace(c.Monitors[i].Category))

		// 标准化 key_type 为小写（空值表示默认 official）
		c.Monitors[i].KeyType = strings.ToLower(strings.TrimSpace(c.Monitors[i].KeyType))

		// 旧赞助等级兼容迁移（持续 1 个版本周期）
		if migrated, ok := c.Monitors[i].SponsorLevel.deprecatedToNew(); ok {
			logger.Warn("config", "monitor 使用已废弃的赞助等级，已自动迁移",
				"monitor_index", i,
				"provider", c.Monitors[i].Provider,
				"service", c.Monitors[i].Service,
				"channel", c.Monitors[i].Channel,
				"old", c.Monitors[i].SponsorLevel, "new", migrated)
			c.Monitors[i].SponsorLevel = migrated
		}

		// 规范化 URLs：去除首尾空格和末尾的 /
		c.Monitors[i].BaseURL = strings.TrimRight(strings.TrimSpace(c.Monitors[i].BaseURL), "/")
		c.Monitors[i].ProviderURL = strings.TrimRight(strings.TrimSpace(c.Monitors[i].ProviderURL), "/")
		c.Monitors[i].SponsorURL = strings.TrimRight(strings.TrimSpace(c.Monitors[i].SponsorURL), "/")
		c.Monitors[i].Template = strings.TrimSpace(c.Monitors[i].Template)
		c.Monitors[i].URLPattern = strings.TrimSpace(c.Monitors[i].URLPattern)

		// provider_slug 仅做 trim，不填充默认值（留给 post-inheritance 处理）
		// 这样子通道可以正确继承父通道的 provider_slug 配置
		c.Monitors[i].ProviderSlug = strings.TrimSpace(c.Monitors[i].ProviderSlug)

		// 显示名称：仅做 trim 处理，不做回退
		// 空值表示"未配置"，由前端使用默认格式化逻辑
		c.Monitors[i].ProviderName = strings.TrimSpace(c.Monitors[i].ProviderName)
		c.Monitors[i].ServiceName = strings.TrimSpace(c.Monitors[i].ServiceName)
		c.Monitors[i].ChannelName = strings.TrimSpace(c.Monitors[i].ChannelName)

		// 计算最终禁用状态：providerDisabled || monitorDisabled
		// 原因优先级：monitor.DisabledReason > provider.Reason
		// 注意：查找时使用小写 provider，与 disabledProviderMap 构建逻辑一致
		normalizedProvider := strings.ToLower(strings.TrimSpace(c.Monitors[i].Provider))
		providerDisabledReason, providerDisabled := ctx.disabledProviderMap[normalizedProvider]
		if providerDisabled || c.Monitors[i].Disabled {
			c.Monitors[i].Disabled = true
			// 如果 monitor 自身没有设置原因，使用 provider 级别的原因
			monitorReason := strings.TrimSpace(c.Monitors[i].DisabledReason)
			if monitorReason == "" && providerDisabled {
				c.Monitors[i].DisabledReason = providerDisabledReason
			} else {
				c.Monitors[i].DisabledReason = monitorReason
			}
			// 停用即视为隐藏，防止展示，同时使用停用原因作为隐藏原因
			c.Monitors[i].Hidden = true
			if strings.TrimSpace(c.Monitors[i].HiddenReason) == "" {
				c.Monitors[i].HiddenReason = c.Monitors[i].DisabledReason
			}
		}

		// 计算最终隐藏状态：providerHidden || monitorHidden（仅对未禁用的项）
		// 原因优先级：monitor.HiddenReason > provider.Reason
		// 已禁用的监测项无需再覆盖隐藏原因
		providerReason, providerHidden := ctx.hiddenProviderMap[normalizedProvider]
		if !c.Monitors[i].Disabled && (providerHidden || c.Monitors[i].Hidden) {
			c.Monitors[i].Hidden = true
			// 如果 monitor 自身没有设置原因，使用 provider 级别的原因
			monitorReason := strings.TrimSpace(c.Monitors[i].HiddenReason)
			if monitorReason == "" && providerHidden {
				c.Monitors[i].HiddenReason = providerReason
			} else {
				c.Monitors[i].HiddenReason = monitorReason
			}
		}

		// Proxy 规范化（TrimSpace + scheme 小写化 + 去掉尾部 /）
		// 无条件先做 TrimSpace，确保空白字符串被清空
		c.Monitors[i].Proxy = strings.TrimSpace(c.Monitors[i].Proxy)
		if c.Monitors[i].Proxy != "" {
			parsed, err := url.Parse(c.Monitors[i].Proxy)
			if err == nil && parsed.Scheme != "" {
				// scheme 小写化
				parsed.Scheme = strings.ToLower(parsed.Scheme)
				// 去掉尾部 /（path 只保留空或非 / 的情况）
				if parsed.Path == "/" {
					parsed.Path = ""
				}
				c.Monitors[i].Proxy = parsed.String()
			}
			// 解析失败时保留 TrimSpace 后的值（防御性保留，正常调用链下不应触发）
		}
	}

	return nil
}

// normalizeMonitorsPostInheritance 继承后的监测项规范化
// 包括：board 默认值填充、provider_slug 默认值填充、cold_reason 清理、注解解析
// 必须在 applyParentInheritance() 之后调用，确保继承的字段能正确处理
func (c *AppConfig) normalizeMonitorsPostInheritance(ctx *normalizeContext) error {
	for i := range c.Monitors {
		// Board 默认值填充：继承后仍为空则设为 "hot"
		if c.Monitors[i].Board == "" {
			c.Monitors[i].Board = "hot"
		}

		// cold_reason 仅在 board=cold 时有意义，其他情况清空并警告
		// 必须在继承后检查，因为继承可能带入 cold_reason
		if c.Monitors[i].ColdReason != "" && c.Monitors[i].Board != "cold" {
			logger.Warn("config", "cold_reason 仅在 board=cold 时有效，已忽略",
				"monitor_index", i,
				"provider", c.Monitors[i].Provider,
				"service", c.Monitors[i].Service)
			c.Monitors[i].ColdReason = ""
		}

		// provider_slug 默认值填充和验证：继承后仍为空则自动生成
		slug := c.Monitors[i].ProviderSlug
		if slug == "" {
			// 未配置时，自动生成: provider 转小写
			slug = strings.ToLower(strings.TrimSpace(c.Monitors[i].Provider))
		}

		// 无论自动生成还是手动配置/继承，都进行格式验证
		// 确保配置期即可发现 slug 格式问题，避免运行时 404
		if err := validateProviderSlug(slug); err != nil {
			return fmt.Errorf("monitor[%d]: provider_slug '%s' 无效 (来源: %s): %w",
				i, slug,
				map[bool]string{true: "自动生成", false: "手动配置或继承"}[c.Monitors[i].ProviderSlug == ""],
				err)
		}
		c.Monitors[i].ProviderSlug = slug

		// Annotation 解析：系统派生 + annotation_rules
		// 始终在规范化阶段解析；enable_annotations 仅控制 API 是否返回 annotations[]
		annotations := resolveAnnotations(c.Monitors[i], ctx.annotationRules, c.IntervalDuration)
		c.Monitors[i].Annotations = annotations
	}

	return nil
}
