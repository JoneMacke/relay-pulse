import { useState, useEffect, useRef, useCallback, useMemo, memo } from 'react';
import { createPortal } from 'react-dom';
import { List, type RowComponentProps } from 'react-window';
import { ArrowUpDown, ArrowUp, ArrowDown, Zap, Shield, Filter, Info } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { StatusDot } from './StatusDot';
import { HeatmapBlock } from './HeatmapBlock';
import { LayeredHeatmapBlock } from './LayeredHeatmapBlock';
import { ChannelTypeIcon, parseChannelType } from './ChannelTypeIcon';
import { ExternalLink } from './ExternalLink';
import { AnnotationCell } from './annotations';
import { FavoriteButton } from './FavoriteButton';
import { getTimeRanges } from '../constants';
import { availabilityToColor, latencyToColor, sponsorLevelToBorderClass, sponsorLevelToCardBorderColor, sponsorLevelToPinnedBgClass } from '../utils/color';
import { trackEvent } from '../utils/analytics';
import { aggregateHeatmap } from '../utils/heatmapAggregator';
import { createMediaQueryEffect } from '../utils/mediaQuery';
import { shortenModelName } from '../utils/modelName';
import { hasAnyAnnotation, hasAnyAnnotationInList } from '../utils/annotationUtils';
import { formatPriceRatioStructured } from '../utils/format';
import { HIDE_PRICE_COLUMN } from '../constants';
import { getServiceIconComponent } from './ServiceIcon';
import { lookupRpdiagScore } from '../hooks/useRpdiagScores';
import type { ProcessedMonitorData, SortConfig } from '../types';
import type { RpdiagModelScore, RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

type HistoryPoint = ProcessedMonitorData['history'][number];

// 虚拟滚动常量
const MOBILE_ROW_HEIGHT = 160;  // 移动端卡片高度（约 150px 内容 + 10px 间距）
const MOBILE_MAX_HEIGHT = 800;  // 移动端列表最大高度

// ServiceIcon 模块级缓存，避免重复调用 getServiceIconComponent
const serviceIconCache = new Map<string, ReturnType<typeof getServiceIconComponent>>();
const getCachedServiceIcon = (serviceType: string) => {
  if (!serviceIconCache.has(serviceType)) {
    serviceIconCache.set(serviceType, getServiceIconComponent(serviceType));
  }
  return serviceIconCache.get(serviceType);
};

// 通道单元格组件（带自定义 CSS tooltip，替代原生 title 属性）
interface ChannelCellProps {
  channel?: string;
  probeUrl?: string;
  templateName?: string;
  coldReason?: string;
  className?: string;
}

function ChannelCell({ channel, probeUrl, templateName, coldReason, className = '' }: ChannelCellProps) {
  const { t } = useTranslation();
  const channelType = parseChannelType(channel);
  const hasTooltip = !!(channelType || probeUrl || templateName || coldReason);
  const triggerRef = useRef<HTMLSpanElement>(null);
  const leaveTimer = useRef<number>(0);
  const [hover, setHover] = useState(false);
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);

  const updatePosition = useCallback(() => {
    if (!triggerRef.current) return;
    const rect = triggerRef.current.getBoundingClientRect();
    setPos({ x: rect.left, y: rect.bottom });
  }, []);

  const handleEnter = useCallback(() => {
    clearTimeout(leaveTimer.current);
    updatePosition();
    setHover(true);
  }, [updatePosition]);

  const handleLeave = useCallback(() => {
    clearTimeout(leaveTimer.current);
    leaveTimer.current = window.setTimeout(() => setHover(false), 100);
  }, []);

  // 卸载时清理定时器
  useEffect(() => () => { clearTimeout(leaveTimer.current); }, []);

  // tooltip 打开时跟随滚动/resize 更新位置
  useEffect(() => {
    if (!hover) return;
    updatePosition();
    window.addEventListener('resize', updatePosition);
    window.addEventListener('scroll', updatePosition, true);
    return () => {
      window.removeEventListener('resize', updatePosition);
      window.removeEventListener('scroll', updatePosition, true);
    };
  }, [hover, updatePosition]);

  const channelContent = (
    <>
      <ChannelTypeIcon channel={channel} />
      <span className="min-w-0 truncate">{channel || '-'}</span>
    </>
  );

  if (!hasTooltip) {
    return <span className={`inline-flex items-center gap-1 ${className}`}>{channelContent}</span>;
  }

  return (
    <span
      ref={triggerRef}
      className={`inline-flex items-center gap-1 cursor-help ${className}`}
      onMouseEnter={handleEnter}
      onMouseLeave={handleLeave}
    >
      {channelContent}
      {/* Portal 到 body — 逃出 backdrop-filter 造成的 containing block */}
      {hover && pos && createPortal(
        <span
          className="fixed px-2 py-1.5 bg-elevated border border-default text-xs rounded-lg shadow-lg z-50 select-text cursor-text md:min-w-[20rem] max-w-[90vw] md:max-w-2xl"
          style={{ left: pos.x, top: pos.y }}
          onMouseEnter={handleEnter}
          onMouseLeave={handleLeave}
        >
          <span className="flex flex-col gap-1">
            {channelType && (
              <span className="flex flex-col">
                <span className="text-muted text-[10px]">{t('table.channelTooltip.channelType')}</span>
                <span className="text-primary text-[11px]">
                  {t(`table.channelType.${channelType}`)} — {t(`table.channelType.${channelType}Desc`)}
                </span>
              </span>
            )}
            {probeUrl && (
              <span className="flex flex-col">
                <span className="text-muted text-[10px]">{t('table.channelTooltip.probeUrl')}</span>
                <span className="text-primary font-mono text-[11px] break-all">{probeUrl}</span>
              </span>
            )}
            {templateName && (
              <span className="flex flex-col">
                <span className="text-muted text-[10px]">{t('table.channelTooltip.template')}</span>
                <span className="text-primary font-mono text-[11px] break-all">{templateName}</span>
              </span>
            )}
            {coldReason && (
              <span className="flex flex-col">
                <span className="text-muted text-[10px]">{t('table.channelTooltip.coldReason', '冷板原因')}</span>
                <span className="text-warning text-[11px] break-all">{coldReason}</span>
              </span>
            )}
          </span>
        </span>,
        document.body,
      )}
    </span>
  );
}

// ─── 模型列辅助函数 ───────────────────────────────────────────

function getModelDisplayList(modelEntries?: ProcessedMonitorData['modelEntries']): string[] {
  if (!modelEntries || modelEntries.length === 0) return [];
  return modelEntries
    .map((entry) => shortenModelName(entry.requestModel) || entry.model || '-')
    .filter(Boolean);
}

function getModelTooltip(modelEntries?: ProcessedMonitorData['modelEntries']): string | undefined {
  if (!modelEntries || modelEntries.length === 0) return undefined;
  return modelEntries
    .map((entry) => entry.requestModel || entry.model || '-')
    .join('\n');
}

interface StatusTableProps {
  data: ProcessedMonitorData[];
  sortConfig: SortConfig;
  isInitialSort?: boolean;   // 是否为初始排序状态（控制高亮显示）
  timeRange: string;
  slowLatencyMs: number;
  enableAnnotations?: boolean;      // 注解系统总开关，默认 true
  showCategoryTag?: boolean; // 是否显示分类标签（推荐/公益），默认 true
  showProvider?: boolean;    // 是否显示服务商名称，默认 true
  showSponsor?: boolean;     // 是否显示赞助者信息，默认 true
  isFavorite: (id: string) => boolean;  // 检查是否已收藏
  onToggleFavorite: (id: string) => void; // 切换收藏状态
  onSort: (key: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  onFilterProvider?: (providerId: string) => void; // 按服务商筛选
  /** rpdiag 质量分索引（按 "provider|service|channel" 键）。空对象表示功能未启用或上游不可达。 */
  rpdiagScores?: RpdiagScoresResponse;
}

// rpdiag 质量分单元格：所有 model 的 5 点 sparkline (30d 均 / 7d 均 / 最近 3
// 次单 sample 升序) 叠在同一个 SVG 画布上，**不分块**。每条 polyline 颜色由
// 该 model 自己最右侧 sample 的分数决定（100=绿/60=黄/0=红 平滑渐变），dot
// 标记每个真实数据点；80 / 100 两条点划参考线作 Y 轴刻度。
// 无可见数字，hover tooltip 出 per-model 明细。
// recent_scores 在 ranking-export.v5.2 起暴露；v5.1 wire 则 fallback 到 3 点
// (30d / 7d / latest) 显示，最新分始终落在最右侧槽位。
//
// 视觉读法：
//   3 条线靠近 → "所有 model 表现接近"（共识）
//   分散      → "各 model 差异大"（需点进详情看哪个掉了）
//   缺点      → 不补 0；缺一个点的 model 只画 dot/短线
function QualityScoreCell({ score, compact = false }: { score?: RpdiagScore; compact?: boolean }) {
  if (!score || !score.models || score.models.length === 0) {
    return <span className="text-muted text-xs">-</span>;
  }

  const ranked = [...score.models].sort(compareModelKeys);
  const title = ranked.map(formatModelTooltipRow).join('\n');

  const W = compact ? 36 : 44;
  const H = compact ? 14 : 36;
  // 5 槽位 sparkline：slot 0/1 是 30d / 7d 窗口均值，slot 2/3/4 是最近
  // 3 次单 sample 升序（旧→新）；缺值的槽位不画点，polyline 跨空槽直连
  // 反映样本稀少的自然间隔。
  const NUM_SLOTS = 5;
  const STEP = W / NUM_SLOTS;
  // 1.2px 内边距上下避免点贴边
  const PAD = 1.2;
  // 圆点半径/线粗随 H 等比放大，desktop H=36 时圆点更醒目
  const DOT_R = compact ? 1.4 : 2.4;
  const STROKE_W = compact ? 1.2 : 1.6;
  // Y 轴参考线：80 / 100 两档，让点的高度有"刻度感"。score=80 对应 norm 0.4、
  // score=100 对应 norm 1.0；与 polyline/dot 共用 qualityScoreYNorm 保证一致。
  const referenceLines = [80, 100].map((markerScore) => ({
    score: markerScore,
    y: H - PAD - qualityScoreYNorm(markerScore) * (H - 2 * PAD),
  }));

  // Y 轴分段非线性：高分段占 SVG 顶部 60% 像素，让 95 vs 100 等小差异有视觉空间。
  //   score 0-60   → SVG 底部 20%
  //   score 60-80  → 中间 20%
  //   score 80-100 → 顶部 60%（实际业务关心的"好通道"区域）
  // 跨 row 仍可比（同分数 → 同高度），但读 sparkline 时要意识到刻度不是匀速的。
  // 用 qualityScoreYNorm 计算。绝对分数由 dot 颜色 + tooltip 数字双重提供。
  const series = ranked
    .map((m) => {
      // 构造 5 槽位 values 数组：slot 0/1 永远是窗口均值；slot 2/3/4 是
      // recent_scores 右对齐升序填入（n=1 → slot 4，n=2 → slot 3/4，n=3 → slot 2/3/4）。
      // ranking-export.v5.1 wire 没有 recent_scores 时，用 latest 单点填 slot 4
      // 作为前向兼容 fallback——视觉上仍把"最新分"摆在最右。
      const slotValues: Array<number | null | undefined> = [
        m.trend?.avg_30d,
        m.trend?.avg_7d,
        null,
        null,
        null,
      ];
      const recentScores = Array.isArray(m.trend?.recent_scores)
        ? m.trend.recent_scores.slice(-3)
        : [];
      if (recentScores.length > 0) {
        const startSlot = NUM_SLOTS - recentScores.length;
        recentScores.forEach((v, i) => {
          slotValues[startSlot + i] = v;
        });
      } else if (typeof m.trend?.latest === 'number') {
        slotValues[NUM_SLOTS - 1] = m.trend.latest;
      }

      const present = slotValues
        .map((v, slot) => (typeof v === 'number' ? { value: v, slot } : null))
        .filter((p): p is { value: number; slot: number } => p !== null);
      if (present.length === 0) return null;
      const points = present.map(({ value, slot }) => {
        const clamped = Math.max(0, Math.min(100, value));
        const norm = qualityScoreYNorm(clamped);
        const x = STEP / 2 + slot * STEP;
        const y = H - PAD - norm * (H - 2 * PAD);
        return { x, y, value: clamped };
      });
      // 颜色取最右侧（最新）样本对应分；polyline 升序排，最后一个 present 总是 slot 最大者
      const recent = present[present.length - 1].value;
      return { points, color: qualityScoreColor(recent) };
    })
    .filter((s): s is { points: { x: number; y: number; value: number }[]; color: string } => s !== null);

  if (series.length === 0) {
    return <span className="text-muted text-xs">-</span>;
  }

  const content = (
    <span className={compact ? 'inline-flex items-center' : 'flex w-full items-center'} title={title || undefined}>
      <svg
        width={compact ? W : '100%'}
        height={H}
        viewBox={`0 0 ${W} ${H}`}
        aria-hidden="true"
        className="flex-shrink-0"
      >
        {referenceLines.map((line) => (
          <line
            key={line.score}
            x1="0"
            y1={line.y}
            x2={W}
            y2={line.y}
            stroke="hsl(0 0% 75% / 0.55)"
            strokeWidth="1"
            strokeDasharray="2 2"
          />
        ))}
        {series.map((s, i) => (
          <g key={i}>
            {s.points.length > 1 && (
              <polyline
                points={s.points.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ')}
                fill="none"
                stroke={s.color}
                strokeWidth={STROKE_W}
                strokeLinecap="round"
                strokeLinejoin="round"
                opacity="0.85"
              />
            )}
            {s.points.map((p, j) => (
              <circle key={j} cx={p.x} cy={p.y} r={DOT_R} fill={qualityScoreColor(p.value)} />
            ))}
          </g>
        ))}
      </svg>
    </span>
  );

  if (!score.channel_url) return content;

  // 裸 <a>：保留新窗 + noopener；不复用 ExternalLink 因为它强制带 ↗ 图标，
  // 在密集表格里这点宝贵宽度还是留给 sparkline。
  return (
    <a
      href={score.channel_url}
      target="_blank"
      rel="noopener noreferrer"
      className={
        compact
          ? 'inline-flex items-center hover:opacity-80 active:opacity-60'
          : 'flex w-full items-center hover:opacity-80 active:opacity-60'
      }
      onClick={() => trackEvent('click_external_link', { link_text: 'rpdiag quality score', link_url: score.channel_url, outbound: true })}
    >
      {content}
    </a>
  );
}

// 分数 → SVG 高度归一化值（0=底部，1=顶部）。Piecewise 把高分段 80-100
// 拉伸到 60% 像素带，让 95 vs 100 等小差异在视觉上看得见。
// 业务事实：rpdiag 80% 的通道分数集中在 80-100，原本线性映射 95-100 只占
// 顶 5% 像素，sparkline 全部贴顶看不出形状。
function qualityScoreYNorm(score: number): number {
  const c = Math.max(0, Math.min(100, score));
  if (c <= 60) return (c / 60) * 0.2;                  // [0,60]   → [0, 0.2]
  if (c <= 80) return 0.2 + ((c - 60) / 20) * 0.2;     // [60,80]  → [0.2, 0.4]
  return 0.4 + ((c - 80) / 20) * 0.6;                  // [80,100] → [0.4, 1.0]
}

// 分数 → HSL 颜色：5 个色站段内线性插值，高分段（80-100）分辨率最高，
// 让 90 / 95 / 100 也有清晰可辨的色差。
//   0   → 红     (hue 0)
//   60  → 橙黄   (hue 40)
//   80  → 黄绿   (hue 75)
//   90  → 草绿   (hue 105)
//   100 → 翠绿   (hue 140)
// 不复用 `availabilityToColor`：可用率与质量分语义不同，未来 rpdiag 可能调整阈值。
function qualityScoreColor(score: number): string {
  // [score, hue, saturation, lightness]
  const stops: Array<[number, number, number, number]> = [
    [0,   0,   78, 50],
    [60,  40,  82, 50],
    [80,  75,  72, 48],
    [90,  105, 70, 46],
    [100, 140, 78, 44],
  ];
  const c = Math.max(0, Math.min(100, score));
  for (let i = 1; i < stops.length; i++) {
    if (c <= stops[i][0]) {
      const [s0, h0, sat0, l0] = stops[i - 1];
      const [s1, h1, sat1, l1] = stops[i];
      const t = s1 === s0 ? 0 : (c - s0) / (s1 - s0);
      const h = h0 + t * (h1 - h0);
      const sat = sat0 + t * (sat1 - sat0);
      const l = l0 + t * (l1 - l0);
      return `hsl(${h.toFixed(0)} ${sat.toFixed(0)}% ${l.toFixed(0)}%)`;
    }
  }
  const last = stops[stops.length - 1];
  return `hsl(${last[1]} ${last[2]}% ${last[3]}%)`;
}

const _MODEL_FAMILY_ORDER: Record<string, number> = { haiku: 0, sonnet: 1, opus: 2 };

function compareModelKeys(a: RpdiagModelScore, b: RpdiagModelScore): number {
  const ra = _modelFamilyRank(a.model_key || a.model);
  const rb = _modelFamilyRank(b.model_key || b.model);
  if (ra !== rb) return ra - rb;
  return (a.model_key || a.model || '').localeCompare(b.model_key || b.model || '');
}

function _modelFamilyRank(name: string | undefined): number {
  if (!name) return 99;
  const lower = name.toLowerCase();
  for (const [family, rank] of Object.entries(_MODEL_FAMILY_ORDER)) {
    if (lower.includes(family)) return rank;
  }
  return 50;
}

function formatModelTooltipRow(m: RpdiagModelScore): string {
  const fmt = (v: number | null | undefined) => (typeof v === 'number' ? v.toFixed(1) : '—');
  const key = m.model_key || m.model || '?';
  const t = m.trend;
  return `${key}  30d=${fmt(t?.avg_30d)}  7d=${fmt(t?.avg_7d)}  latest=${fmt(t?.latest)}`;
}

// react-window v2 虚拟列表行组件（rowComponent 接口）
interface MobileRowProps {
  data: ProcessedMonitorData[];
  slowLatencyMs: number;
  enableAnnotations: boolean;
  showProvider: boolean;
  showSponsor: boolean;
  useLatencyGradient: boolean;
  isFavorite: (id: string) => boolean;
  onToggleFavorite: (id: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScores?: RpdiagScoresResponse;
}

function MobileRow({ index, style, data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores }: RowComponentProps<MobileRowProps>) {
  const item = data[index];
  return (
    <div style={style}>
      <div style={{ marginBottom: 8 }}>
        <MobileListItem
          item={item}
          slowLatencyMs={slowLatencyMs}
          enableAnnotations={enableAnnotations}
          showProvider={showProvider}
          showSponsor={showSponsor}
          useLatencyGradient={useLatencyGradient}
          isFavorite={isFavorite(item.id)}
          onToggleFavorite={() => onToggleFavorite(item.id)}
          onBlockHover={onBlockHover}
          onBlockLeave={onBlockLeave}
          rpdiagScore={lookupRpdiagScore(rpdiagScores, item.providerId, item.serviceType, item.channelName || item.channel)}
        />
      </div>
    </div>
  );
}

// 移动端卡片列表项组件
function MobileListItem({
  item,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  showSponsor = true,
  useLatencyGradient = false,
  isFavorite,
  onToggleFavorite,
  onBlockHover,
  onBlockLeave,
  rpdiagScore,
}: {
  item: ProcessedMonitorData;
  slowLatencyMs: number;
  enableAnnotations?: boolean;
  showProvider?: boolean;
  showSponsor?: boolean;
  useLatencyGradient?: boolean;
  isFavorite: boolean;
  onToggleFavorite: () => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScore?: RpdiagScore;
}) {
  const { i18n } = useTranslation();
  const ServiceIcon = getCachedServiceIcon(item.serviceType);

  // 聚合热力图数据
  const aggregatedHistory = useMemo(
    () => aggregateHeatmap(item.history, 30),
    [item.history]
  );

  // 检查是否有注解需要显示
  const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });

  // 卡片左边框颜色（仅基于赞助级别，置顶改用背景色）
  const borderColor = sponsorLevelToCardBorderColor(item.sponsorLevel);

  // 是否显示左边框（仅基于赞助级别）
  const hasLeftBorder = !!item.sponsorLevel;

  // 置顶项使用对应注解颜色的极淡背景色
  const pinnedBgClass = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
  const baseBgClass = pinnedBgClass || 'bg-surface/60';

  // 卡片最小高度 = 行高(160) - 行间距(8) = 152px
  // 确保所有卡片高度一致，避免虚拟列表中间距不均
  const cardMinHeight = 152;

  return (
    <div
      className={`${baseBgClass} border border-default rounded-r-xl ${hasLeftBorder ? 'rounded-l-sm border-l-2' : 'rounded-l-xl'} p-3 space-y-2`}
      style={{
        ...(borderColor ? { borderLeftColor: borderColor } : {}),
        minHeight: cardMinHeight,
      }}
    >
      {/* 注解行 - 仅在有注解时显示 */}
      {hasItemAnnotations && (
        <AnnotationCell annotations={item.annotations} />
      )}

      {/* 主要信息行 */}
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0 flex-1">
          {/* 服务图标 */}
          <div className="w-8 h-8 flex-shrink-0 rounded-lg bg-elevated flex items-center justify-center border border-default text-primary">
            {ServiceIcon ? (
              <ServiceIcon className="w-4 h-4" />
            ) : item.serviceType === 'cc' ? (
              <Zap className="text-service-cc" size={14} />
            ) : (
              <Shield className="text-service-cx" size={14} />
            )}
          </div>

          {/* 服务商名称 + 收藏按钮 */}
          <div className="min-w-0 flex-1">
            {showProvider && (
              <div className="flex items-center gap-1.5">
                <span className="font-semibold text-primary truncate text-sm leading-tight">
                  <ExternalLink href={item.providerUrl} compact requireConfirm>{item.providerName}</ExternalLink>
                </span>
                <FavoriteButton
                  isFavorite={isFavorite}
                  onToggle={onToggleFavorite}
                  size={12}
                  inline
                />
              </div>
            )}
            <div className="flex items-center gap-2 mt-0.5 text-xs text-secondary">
              {/* 赞助者（放在服务类型前） */}
              {showSponsor && item.sponsor && (
                <span className="text-[10px] text-muted truncate max-w-[80px]">
                  <ExternalLink href={item.sponsorUrl} compact>{item.sponsor}</ExternalLink>
                </span>
              )}
              <span
                className={`px-1.5 py-0.5 rounded text-[10px] font-mono border flex-shrink-0 ${
                  item.serviceType === 'cc'
                    ? 'border-service-cc text-service-cc bg-service-cc'
                    : item.serviceType === 'gm'
                    ? 'border-service-gm text-service-gm bg-service-gm'
                    : 'border-service-cx text-service-cx bg-service-cx'
                }`}
              >
                {item.serviceName.toUpperCase()}
              </span>
              {item.channel && (
                <ChannelCell
                  channel={item.channelName || item.channel}
                  probeUrl={item.probeUrl}
                  templateName={item.templateName}
                  coldReason={item.coldReason}
                  className="text-muted truncate"
                />
              )}
              {item.modelEntries && item.modelEntries.length > 0 && (() => {
                const models = getModelDisplayList(item.modelEntries);
                if (models.length === 0) return null;
                return (
                  <span
                    className="text-[10px] text-muted truncate max-w-[120px]"
                    title={getModelTooltip(item.modelEntries)}
                  >
                    {models.length === 1 ? models[0] : `${models[0]} +${models.length - 1}`}
                  </span>
                );
              })()}
              {/* 收录时间 */}
              {item.listedDays != null && (
                <span className="text-[10px] text-muted font-mono flex-shrink-0">
                  {item.listedDays}d
                </span>
              )}
            </div>
          </div>
        </div>

        {/* 状态、可用率、时间和延迟 */}
        <div className="flex flex-col items-end gap-1 flex-shrink-0">
          <div className="flex items-center p-1.5 rounded-full bg-elevated border border-default">
            <StatusDot status={item.currentStatus} size="sm" />
          </div>
          <span
            className="text-sm font-mono font-bold"
            style={{ color: availabilityToColor(item.uptime) }}
          >
            {item.uptime >= 0 ? `${item.uptime}%` : '--'}
          </span>
          {rpdiagScore && rpdiagScore.models && rpdiagScore.models.length > 0 && (
            <QualityScoreCell score={rpdiagScore} compact />
          )}
          {/* 时间和延迟（总是显示） */}
          <div className="flex items-center gap-2 text-[10px] text-muted font-mono">
            {item.lastCheckTimestamp && (
              <span>
                {new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, {
                  hour: '2-digit',
                  minute: '2-digit',
                })}
              </span>
            )}
            {item.lastCheckLatency !== undefined && (
              <span style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}>
                {item.lastCheckLatency}ms
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 热力图 */}
      <div className="flex items-center gap-[2px] h-5 w-full overflow-hidden rounded-sm">
        {aggregatedHistory.map((point, idx) => (
          <HeatmapBlock
            key={idx}
            point={point}
            width={`${100 / aggregatedHistory.length}%`}
            height="h-full"
            onHover={onBlockHover}
            onLeave={onBlockLeave}
            isMobile
            useLatencyGradient={useLatencyGradient}
          />
        ))}
      </div>
    </div>
  );
}

// 移动端排序菜单
function MobileSortMenu({
  sortConfig,
  isInitialSort,
  onSort,
}: {
  sortConfig: SortConfig;
  isInitialSort?: boolean;
  onSort: (key: string) => void;
}) {
  const { t } = useTranslation();

  const sortOptions = [
    { key: 'providerName', label: t('table.sorting.provider') },
    { key: 'uptime', label: t('table.sorting.uptime') },
    { key: 'lastCheck', label: t('table.sorting.lastCheck') },
    { key: 'serviceType', label: t('table.sorting.service') },
    ...(HIDE_PRICE_COLUMN ? [] : [{ key: 'priceRatio', label: t('table.sorting.priceRatio') }]),
    { key: 'listedDays', label: t('table.sorting.listedDays') },
  ];

  return (
    <div className="flex items-center gap-2 mb-2 overflow-x-auto pb-2">
      <span className="text-xs text-muted flex-shrink-0">{t('controls.sortBy')}</span>
      {sortOptions.map((option) => {
        // 初始状态下不高亮任何排序按钮
        const isActive = !isInitialSort && sortConfig.key === option.key;
        return (
          <button
            key={option.key}
            onClick={() => onSort(option.key)}
            className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors flex-shrink-0 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
              isActive
                ? 'bg-accent/20 text-accent border border-accent/30'
                : 'bg-elevated text-secondary border border-default hover:text-primary'
            }`}
          >
            {option.label}
            {isActive && (
              sortConfig.direction === 'asc' ? (
                <ArrowUp size={12} />
              ) : (
                <ArrowDown size={12} />
              )
            )}
          </button>
        );
      })}
    </div>
  );
}

function StatusTableComponent({
  data,
  sortConfig,
  isInitialSort = false,
  timeRange,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  showSponsor = true,
  isFavorite,
  onToggleFavorite,
  onSort,
  onBlockHover,
  onBlockLeave,
  onFilterProvider,
  rpdiagScores,
}: StatusTableProps) {
  const { t, i18n } = useTranslation();
  const [isMobile, setIsMobile] = useState(false);

  // 检测是否为平板/移动端（< 960px，兼容 Safari ≤13）
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  // 排序图标：初始状态下不显示高亮
  const SortIcon = ({ columnKey }: { columnKey: string }) => {
    // 初始状态下所有排序图标都不高亮
    if (isInitialSort || sortConfig.key !== columnKey) {
      return <ArrowUpDown size={14} className="opacity-30 ml-1" />;
    }
    return sortConfig.direction === 'asc' ? (
      <ArrowUp size={14} className="text-accent ml-1" />
    ) : (
      <ArrowDown size={14} className="text-accent ml-1" />
    );
  };

  const currentTimeRange = getTimeRanges(t).find((r) => r.id === timeRange);
  const useLatencyGradient = timeRange === '90m';

  // 移动端：虚拟滚动卡片列表视图
  if (isMobile) {
    // 计算虚拟列表高度（最大 MOBILE_MAX_HEIGHT，最小为所有项目高度）
    const mobileListHeight = Math.min(
      data.length * MOBILE_ROW_HEIGHT,
      MOBILE_MAX_HEIGHT
    );

    return (
      <div>
        <MobileSortMenu sortConfig={sortConfig} isInitialSort={isInitialSort} onSort={onSort} />
        <List
          style={{ height: mobileListHeight, width: '100%' }}
          rowCount={data.length}
          rowHeight={MOBILE_ROW_HEIGHT}
          overscanCount={3}
          rowComponent={MobileRow}
          rowProps={{ data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores }}
        />
      </div>
    );
  }

  // 检查是否有任何注解需要显示
  const hasAnnotations = hasAnyAnnotationInList(data, { enableAnnotations });

  // 桌面端：表格视图
  return (
    <div className="overflow-x-auto rounded-2xl border border-default/50 shadow-xl bg-surface/40 backdrop-blur-sm">
      <table className="w-full text-left border-collapse bg-transparent">
        <colgroup>
          {hasAnnotations && <col className="w-px" />}
          {showProvider && <col className="w-px" />}
          <col className="w-px" /> {/* service */}
          <col className="w-px" /> {/* channel */}
          <col className="w-px" /> {/* model */}
          {!HIDE_PRICE_COLUMN && <col className="w-px" />} {/* priceRatio */}
          <col className="w-px" /> {/* listedDays */}
          <col className="w-px" /> {/* uptime */}
          <col className="w-px" /> {/* lastCheck */}
          <col className="w-px" /> {/* quality */}
          <col className="w-full" /> {/* trend */}
        </colgroup>
        <thead>
          <tr className="border-b border-default/50 text-secondary text-xs uppercase tracking-wider">
            {/* 注解列 - 仅在有注解时显示 */}
            {hasAnnotations && (
              <th className="px-1 py-3 font-medium whitespace-nowrap">
                {t('table.headers.annotation')}
              </th>
            )}
            {/* 服务商列（合并赞助者） */}
            {showProvider && (
              <th
                className="px-3 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('providerName')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('providerName'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  {t('table.headers.provider')} <SortIcon columnKey="providerName" />
                </div>
              </th>
            )}
            <th
              className="px-2 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('serviceType')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('serviceType'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.service')} <SortIcon columnKey="serviceType" />
              </div>
            </th>
            <th
              className="px-2 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('channel')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('channel'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.channel')} <SortIcon columnKey="channel" />
              </div>
            </th>
            <th className="px-2 py-3 font-medium whitespace-nowrap">
              {t('table.headers.model')}
            </th>
            {!HIDE_PRICE_COLUMN && (
              <th
                className="px-2 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('priceRatio')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('priceRatio'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  <div className="flex flex-col leading-tight">
                    <span>{t('table.headers.priceRatioLine1')}</span>
                    <span className="text-[10px] opacity-50 font-normal">{t('table.headers.priceRatioLine2')}</span>
                  </div>
                  <span
                    className="relative group/price-tip ml-1 inline-flex items-center cursor-help"
                    onClick={(e) => e.stopPropagation()}
                    onKeyDown={(e) => e.stopPropagation()}
                  >
                    <Info size={12} className="text-secondary opacity-70" aria-hidden="true" />
                    <span className="absolute left-1/2 top-full z-50 mt-1 w-48 -translate-x-1/2 rounded-lg border border-default bg-elevated px-2 py-1.5 text-[11px] font-normal normal-case tracking-normal leading-snug whitespace-normal text-primary opacity-0 pointer-events-none shadow-lg transition-opacity delay-150 group-hover/price-tip:opacity-100 group-hover/price-tip:pointer-events-auto">
                      {t('table.headers.priceRatioTooltip')}
                    </span>
                  </span>
                  <SortIcon columnKey="priceRatio" />
                </div>
              </th>
            )}
            <th
              className="px-2 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('listedDays')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('listedDays'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.listedDaysLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.listedDaysLine2')}</span>
                </div>
                <SortIcon columnKey="listedDays" />
              </div>
            </th>
            <th
              className="px-2 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('uptime')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('uptime'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.uptime')} <SortIcon columnKey="uptime" />
              </div>
            </th>
            <th
              className="px-2 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('lastCheck')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('lastCheck'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.lastCheckLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.lastCheckLine2')}</span>
                </div>
                <SortIcon columnKey="lastCheck" />
              </div>
            </th>
            <th className="px-2 py-3 font-medium whitespace-nowrap">
              <div className="flex items-center gap-1">
                {t('table.headers.quality', '质量')}
                <span
                  className="relative group/quality-tip inline-flex items-center cursor-help"
                  onClick={(e) => e.stopPropagation()}
                  onKeyDown={(e) => e.stopPropagation()}
                >
                  <Info size={12} className="text-secondary opacity-70" aria-hidden="true" />
                  <span className="absolute right-0 top-full z-50 mt-1 w-56 rounded-lg border border-default bg-elevated px-2 py-1.5 text-[11px] font-normal normal-case tracking-normal leading-snug whitespace-normal text-primary opacity-0 pointer-events-none shadow-lg transition-opacity delay-150 group-hover/quality-tip:opacity-100 group-hover/quality-tip:pointer-events-auto">
                    {t(
                      'table.headers.qualityTooltip',
                      '由 rpdiag.relaypulse.top 独立采样的质量分（0-100）。通道里每个模型一条 5 点 sparkline 叠绘：30d 均 / 7d 均 / 最近 3 次单 sample；80 / 100 两条参考线作 Y 轴刻度。',
                    )}
                  </span>
                </span>
              </div>
            </th>
            <th className="pl-2 pr-4 py-3 font-medium min-w-[260px]">
              <div className="flex items-center gap-2">
                {t('table.headers.trend')}
                <span className="text-[10px] normal-case opacity-50 border border-default px-1 rounded">
                  {currentTimeRange?.label}
                </span>
              </div>
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-default/50 text-sm">
          {data.map((item, rowIndex) => {
            const ServiceIcon = getCachedServiceIcon(item.serviceType);
            const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });
            const pinnedBg = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
            return (
            <tr
              key={item.id}
              className={`group hover:bg-elevated/40 transition-[background-color,color] ${pinnedBg} ${sponsorLevelToBorderClass(item.sponsorLevel)}`}
            >
              {/* 注解列 */}
              {hasAnnotations && (
                <td className="px-1 py-1 whitespace-nowrap">
                  {hasItemAnnotations ? (
                    <AnnotationCell
                      annotations={item.annotations}
                      tooltipPlacement={rowIndex === 0 ? 'bottom' : 'top'}
                    />
                  ) : null}
                </td>
              )}
              {/* 服务商列（两行紧贴，整体垂直居中） */}
              {showProvider && (
                <td className="px-2 py-1.5">
                  <div className="flex items-center h-8 group/provider">
                    <div className="flex flex-col gap-0 flex-1 min-w-0 max-w-[13rem]">
                      <div className="flex items-center gap-1.5">
                        <span className="font-medium text-primary text-sm leading-tight truncate">
                          <ExternalLink href={item.providerUrl} inline requireConfirm>{item.providerName}</ExternalLink>
                        </span>
                        {/* 收藏按钮：始终显示，未收藏时弱化 */}
                        <div className="flex-shrink-0">
                          <FavoriteButton
                            isFavorite={isFavorite(item.id)}
                            onToggle={() => onToggleFavorite(item.id)}
                            size={12}
                            inline
                          />
                        </div>
                        {/* 过滤按钮：悬浮时显示 */}
                        {onFilterProvider && (
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              onFilterProvider(item.providerId);
                            }}
                            className="flex-shrink-0 p-0.5 rounded opacity-0 group-hover/provider:opacity-60 hover:!opacity-100 hover:text-accent transition-opacity cursor-pointer"
                            title={t('table.filterByProvider')}
                          >
                            <Filter size={10} />
                          </button>
                        )}
                      </div>
                      {showSponsor && item.sponsor && (
                        <span className="text-[9px] text-muted leading-none">
                          <ExternalLink href={item.sponsorUrl} inline>{item.sponsor}</ExternalLink>
                        </span>
                      )}
                    </div>
                  </div>
                </td>
              )}
              <td className="px-2 py-1">
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-mono border ${
                    item.serviceType === 'cc'
                      ? 'border-service-cc text-service-cc bg-service-cc'
                      : item.serviceType === 'gm'
                      ? 'border-service-gm text-service-gm bg-service-gm'
                      : 'border-service-cx text-service-cx bg-service-cx'
                  }`}
                >
                  {ServiceIcon ? (
                    <ServiceIcon className="w-3.5 h-3.5 mr-1 text-primary" />
                  ) : (
                    <>
                      {item.serviceType === 'cc' && <Zap size={10} className="mr-1 text-primary" />}
                      {item.serviceType === 'cx' && <Shield size={10} className="mr-1 text-primary" />}
                    </>
                  )}
                  {item.serviceName.toUpperCase()}
                </span>
              </td>
              <td className="px-2 py-1 text-secondary text-xs">
                <ChannelCell
                  channel={item.channelName || item.channel}
                  probeUrl={item.probeUrl}
                  templateName={item.templateName}
                  coldReason={item.coldReason}
                  className="max-w-[10rem]"
                />
              </td>
              <td className="px-2 py-1 text-secondary text-xs max-w-[14rem]">
                {(() => {
                  const models = getModelDisplayList(item.modelEntries);
                  if (models.length === 0) return <span className="text-muted">-</span>;
                  if (models.length === 1) {
                    return (
                      <span className="block truncate" title={getModelTooltip(item.modelEntries)}>
                        {models[0]}
                      </span>
                    );
                  }
                  return (
                    <div className="flex flex-col gap-0.5" title={getModelTooltip(item.modelEntries)}>
                      {models.map((m, i) => (
                        <span key={i} className="block truncate">{m}</span>
                      ))}
                    </div>
                  );
                })()}
              </td>
              {!HIDE_PRICE_COLUMN && (
                <td className="px-2 py-1 font-mono text-xs whitespace-nowrap">
                  {(() => {
                    const priceData = formatPriceRatioStructured(item.priceMin, item.priceMax);
                    if (!priceData) return <span className="text-muted">-</span>;
                    return (
                      <div className="flex flex-col leading-tight">
                        <span className="text-secondary">{priceData.base}</span>
                        {priceData.sub && (
                          <span className="text-[10px] text-muted">{priceData.sub}</span>
                        )}
                      </div>
                    );
                  })()}
                </td>
              )}
              <td className="px-2 py-1 font-mono text-xs text-secondary whitespace-nowrap">
                {item.listedDays != null ? `${item.listedDays}d` : '-'}
              </td>
              <td className="px-2 py-1 font-mono font-bold whitespace-nowrap">
                <span style={{ color: availabilityToColor(item.uptime) }}>
                  {item.uptime >= 0 ? `${item.uptime}%` : '--'}
                </span>
              </td>
              <td className="px-2 py-1">
                <div className="flex items-center gap-1.5">
                  <StatusDot status={item.currentStatus} size="sm" />
                  {item.lastCheckTimestamp ? (
                    <div className="text-xs text-secondary font-mono flex flex-col gap-0.5">
                      {item.lastCheckLatency !== undefined && (
                        <span
                          className="text-[10px] font-mono"
                          style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}
                        >
                          {item.lastCheckLatency}ms
                        </span>
                      )}
                      <span className="text-[10px] text-muted">{new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, { hour: '2-digit', minute: '2-digit' })}</span>
                    </div>
                  ) : (
                    <span className="text-muted text-xs">-</span>
                  )}
                </div>
              </td>
              <td className="px-2 py-1 whitespace-nowrap">
                <QualityScoreCell score={lookupRpdiagScore(rpdiagScores, item.providerId, item.serviceType, item.channelName || item.channel)} />
              </td>
              <td className="pl-2 pr-4 py-1.5 align-middle">
                <div className="flex items-center gap-[2px] h-5 w-full overflow-hidden rounded-sm">
                  {/* 热力图：多层 vs 单层 */}
                  {item.isMultiModel && item.layers ? (
                    // Phase B: 多层垂直堆叠热力图
                    item.history.map((_, idx) => (
                      <LayeredHeatmapBlock
                        key={idx}
                        layers={item.layers!}
                        timeIndex={idx}
                        width={`${100 / item.history.length}%`}
                        height="h-full"
                        onHover={onBlockHover}
                        onLeave={onBlockLeave}
                        isMobile={false}
                        slowLatencyMs={item.slowLatencyMs ?? slowLatencyMs}
                        useLatencyGradient={useLatencyGradient}
                      />
                    ))
                  ) : (
                    // Phase A: 单层传统热力图
                    item.history.map((point, idx) => (
                      <HeatmapBlock
                        key={idx}
                        point={point}
                        width={`${100 / item.history.length}%`}
                        height="h-full"
                        onHover={onBlockHover}
                        onLeave={onBlockLeave}
                        isMobile={false}
                        useLatencyGradient={useLatencyGradient}
                      />
                    ))
                  )}
                </div>
              </td>
            </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export const StatusTable = memo(StatusTableComponent);
