import { useRef, type ReactNode } from 'react';
import type { Annotation } from '../../types';
import { useAnnotationTooltip } from '../../hooks/useAnnotationTooltip';
import { AnnotationTooltip } from './AnnotationTooltip';

interface AnnotationChipProps {
  annotation: Annotation;
  className?: string;
  tooltipPlacement?: 'top' | 'bottom';
}

// ── 自定义 SVG 图标（与重构前完全一致）──────────────────

/** 公益站 — "益" 文字标签 */
function CategoryPublicIcon() {
  return (
    <span className="inline-flex items-center justify-center w-4 h-4 text-[10px] font-bold bg-info/20 text-info rounded cursor-default select-none">
      益
    </span>
  );
}

/** 🛡️ 公益链路（sponsor=public）— 盾牌 */
function SponsorPublicIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path
        d="M12 2L4 6v5c0 5.25 3.4 10.15 8 11.25C16.6 21.15 20 16.25 20 11V6L12 2z"
        className="fill-sponsor-public"
      />
    </svg>
  );
}

/** · 信号链路 — 光点（小圆）*/
function SponsorSignalIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="4" className="fill-sponsor-signal" />
    </svg>
  );
}

/** ◆ 脉冲链路 — 小菱形 */
function SponsorPulseIcon() {
  return (
    <svg className="w-[14px] h-[14px]" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <polygon points="12,4 19,12 12,20 5,12" className="fill-sponsor-pulse" />
    </svg>
  );
}

/** 🔺 信标链路 — 正三角形 */
function SponsorBeaconIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <polygon points="12,4 4,18 20,18" className="fill-sponsor-beacon" />
    </svg>
  );
}

/** ⬢ 骨干链路 — 实心六边形 */
function SponsorBackboneIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <polygon points="12,2 21,7 21,17 12,22 3,17 3,7" className="fill-sponsor-backbone" />
    </svg>
  );
}

/** 💠 核心链路 — 钻石+中心光点 */
function SponsorCoreIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <polygon points="12,2 22,12 12,22 2,12" className="fill-sponsor-core" />
      <circle cx="12" cy="12" r="3" fill="rgba(255,255,255,0.6)" />
    </svg>
  );
}

/** 频率指示器 — 时钟+循环箭头 */
function FrequencyIcon({ opacity }: { opacity: number }) {
  return (
    <svg
      className="w-4 h-4 text-accent"
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
      style={{ opacity }}
    >
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="2" />
      <line x1="12" y1="12" x2="12" y2="7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      <line x1="12" y1="12" x2="16" y2="12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      <path d="M19,5 L19,9 L15,9" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** ⚠️ 风险 — 警告三角形+感叹号 */
function RiskIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <polygon points="12,3 22,21 2,21" className="fill-warning/80" />
      <rect x="11" y="9" width="2" height="6" fill="white" rx="0.5" />
      <circle cx="12" cy="17.5" r="1" fill="white" />
    </svg>
  );
}

/** 质量移板 — 向下箭头落入板层（黄色，negative 家族，形状区别于风险三角）*/
function QualityDemoteIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false" data-icon="quality-demote">
      <path
        d="M12,3 L12,12 M8,8.5 L12,12.5 L16,8.5"
        fill="none"
        stroke="hsl(var(--warning))"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <rect x="4" y="16" width="16" height="4" rx="1" className="fill-warning/70" />
    </svg>
  );
}

/** 官方 API Key — 盾牌+勾号（蓝色，与旧 variant=info 一致）*/
function OfficialKeyIcon() {
  return (
    <svg className="w-4 h-4 opacity-60" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path
        d="M12,2 L4,5 L4,11 C4,16.5 7.4,21.7 12,23 C16.6,21.7 20,16.5 20,11 L20,5 L12,2 Z"
        className="fill-info"
      />
      <path d="M9,12 L11,14 L15,10" fill="none" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** 用户 API Key — 人形轮廓（蓝色，与旧 variant=info 一致）*/
function UserKeyIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="7" r="4" className="fill-info" />
      <path d="M12,13 C8,13 5,15.5 5,19 L5,20 L19,20 L19,19 C19,15.5 16,13 12,13 Z" className="fill-info" />
    </svg>
  );
}

/** 基准通道 — 靶心准星（蓝色，与旧 variant=info 一致）*/
function BaselineIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="10" className="fill-info" fillOpacity="0.3" />
      <circle cx="12" cy="12" r="6" className="fill-info" fillOpacity="0.5" />
      <circle cx="12" cy="12" r="2" className="fill-info" />
      <rect x="11.25" y="2" width="1.5" height="4" className="fill-info" />
      <rect x="11.25" y="18" width="1.5" height="4" className="fill-info" />
      <rect x="2" y="11.25" width="4" height="1.5" className="fill-info" />
      <rect x="18" y="11.25" width="4" height="1.5" className="fill-info" />
    </svg>
  );
}

/** 功能特性 — 闪电（绿色，与旧 variant=success 一致）*/
function FeatureIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M13,2 L4,14 L11,14 L11,22 L20,10 L13,10 L13,2 Z" className="fill-success" />
    </svg>
  );
}

/** 通用信息 — 圆形带 i（蓝色）*/
function GenericInfoIcon() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="10" className="fill-info" />
      <circle cx="12" cy="8" r="1" fill="white" />
      <rect x="11" y="11" width="2" height="6" rx="1" fill="white" />
    </svg>
  );
}

// ── 频率 opacity 计算（与旧 FrequencyIndicator 一致）──

function getFrequencyOpacity(intervalMs: number): number {
  const min = 30000;  // 30s
  const max = 300000; // 5min
  const clamped = Math.min(max, Math.max(min, intervalMs));
  const ratio = (clamped - min) / (max - min);
  return 1 - ratio * 0.7; // 高频 1.0 → 低频 0.3
}

// ── 图标路由：后端 annotation → 旧 SVG 组件 ──
// 每个图标有自己的固定颜色，与重构前完全一致

function renderIcon(annotation: Annotation): ReactNode {
  const { icon, id } = annotation;

  // 系统派生注解：根据 id 精确匹配
  switch (id) {
    case 'public_service':
      return <CategoryPublicIcon />;
    case 'sponsor_public':
      return <SponsorPublicIcon />;
    case 'sponsor_signal':
      return <SponsorSignalIcon />;
    case 'sponsor_pulse':
      return <SponsorPulseIcon />;
    case 'sponsor_beacon':
      return <SponsorBeaconIcon />;
    case 'sponsor_backbone':
      return <SponsorBackboneIcon />;
    case 'sponsor_core':
      return <SponsorCoreIcon />;
    case 'monitor_frequency': {
      let ms = 60000; // 默认 1 分钟
      const metaMs = annotation.metadata?.interval_ms;
      if (typeof metaMs === 'number' && metaMs > 0) {
        ms = metaMs;
      }
      return <FrequencyIcon opacity={getFrequencyOpacity(ms)} />;
    }
    case 'quality_hardfail':
      return <QualityDemoteIcon />;
  }

  // 规则注解：根据 icon 字段匹配
  switch (icon) {
    // 赞助等级（各自专属形状+颜色）
    case 'shield-heart':
    case 'heart':
      return <SponsorPublicIcon />;
    case 'signal':
      return <SponsorSignalIcon />;
    case 'pulse':
      return <SponsorPulseIcon />;
    case 'beacon':
      return <SponsorBeaconIcon />;
    case 'backbone':
      return <SponsorBackboneIcon />;
    case 'core':
      return <SponsorCoreIcon />;
    // 风险（黄色三角）
    case 'warning':
    case 'alert-triangle':
      return <RiskIcon />;
    // API Key 来源（蓝色）
    case 'shield-check':
      return <OfficialKeyIcon />;
    case 'user':
      return <UserKeyIcon />;
    // 基准通道（蓝色准星）
    case 'crosshair':
      return <BaselineIcon />;
    // 功能特性（绿色闪电）
    case 'feature':
    case 'zap':
      return <FeatureIcon />;
    // 频率（青色时钟）
    case 'activity':
    case 'clock':
    case 'clock-3':
      return <FrequencyIcon opacity={0.7} />;
    // 信息（蓝色圆i）
    case 'info':
      return <GenericInfoIcon />;
  }

  // fallback
  return <GenericInfoIcon />;
}

/**
 * 单个注解图标组件
 * - 仅显示图标（与重构前一致的自定义 SVG）
 * - hover 700ms 后显示 tooltip（label + tooltip 文本）
 * - 支持链接跳转（href 字段）
 */
export function AnnotationChip({
  annotation,
  className = '',
  tooltipPlacement = 'top',
}: AnnotationChipProps) {
  const triggerRef = useRef<HTMLSpanElement>(null);
  const { isOpen, position, handleMouseEnter, handleMouseLeave } = useAnnotationTooltip(
    triggerRef,
    tooltipPlacement
  );

  const tooltipText = annotation.tooltip?.trim();
  const ariaLabel = tooltipText ? `${annotation.label}: ${tooltipText}` : annotation.label;

  const chip = (
    <span
      ref={triggerRef}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
      className={`inline-flex items-center justify-center hover:opacity-70 transition-opacity select-none ${
        annotation.href ? 'cursor-pointer' : 'cursor-default'
      } ${className}`}
      role="img"
      aria-label={ariaLabel}
    >
      {renderIcon(annotation)}
    </span>
  );

  return (
    <>
      {annotation.href ? (
        <a
          href={annotation.href}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex"
          onClick={(e) => e.stopPropagation()}
        >
          {chip}
        </a>
      ) : (
        chip
      )}

      <AnnotationTooltip isOpen={isOpen} position={position}>
        <span className="font-medium">{annotation.label}</span>
        {tooltipText && (
          <span className="text-secondary ml-1">- {tooltipText}</span>
        )}
      </AnnotationTooltip>
    </>
  );
}
