import { useEffect, useState } from 'react';

import { apiGet } from '../utils/apiClient';
import type { RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

interface UseRpdiagScoresResult {
  scores: RpdiagScoresResponse;
  loaded: boolean;
}

const RPDIAG_POLL_INTERVAL_MS = 60_000; // 与状态列自动刷新频率一致

/** 拉取 rpdiag 质量分索引，每 60 秒自动刷新（与状态轮询同步）。
 *
 *  - 后端 10min cache 兜底，高频调用不会产生额外计算；质量分有更新时可在 60s 内呈现
 *  - 失败时保留上次成功快照，列表不闪"-"
 *  - kill switch 由后端判断（MONITOR_RPDIAG_ENABLED）
 */
export function useRpdiagScores(): UseRpdiagScoresResult {
  const [scores, setScores] = useState<RpdiagScoresResponse>({});
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let currentController: AbortController | null = null;

    function fetchScores() {
      currentController?.abort();
      const controller = new AbortController();
      currentController = controller;
      apiGet<RpdiagScoresResponse>('/api/rpdiag-scores', { signal: controller.signal })
        .then((data) => {
          if (cancelled) return;
          setScores(data ?? {});
          setLoaded(true);
        })
        .catch((error) => {
          if (cancelled) return;
          if (error instanceof Error && error.name === 'AbortError') return;
          // 保留上次成功快照；首次失败时标记 loaded 避免永久加载态
          setLoaded(true);
        });
    }

    fetchScores();
    const timer = setInterval(fetchScores, RPDIAG_POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      currentController?.abort();
      clearInterval(timer);
    };
  }, []);

  return { scores, loaded };
}

/** 构造与后端一致的 join key（lower-case "provider|service|channel"）。
 *  - channel 段用**原始通道名**（rpdiag channel_name），只做 trim + lower、不剥前缀——
 *    剥前缀会把仅靠前缀区分的通道折叠（如某商 o-cx 付费档 / u-cx 免费档都塌成 cx）。
 *  - provider 段用 rpdiag 的**展示名**（provider_name），不是 relaypulse 的 slug——
 *    后端 buildScoreRowView 即按 canonical(provider_name) 建 key，两侧须用同一标识。
 *  后端 buildScoreRowView 同样按原始 channel_name 建 key，两侧对齐。 */
export function buildRpdiagKey(
  provider: string | undefined,
  service: string | undefined,
  channel: string | undefined,
): string {
  return [canonical(provider), canonical(service), canonical(channel)].join('|');
}

/** 构造与后端 cidBucketKey 对称的稳定 channel_id join key。
 *  channelId 是 relay-pulse 不可变的 `ch_<uuidv4>`，只 trim、不 lower（与后端
 *  strings.TrimSpace 同口径）；空/纯空白返回 undefined（不拿 "cid:" 撞表）。 */
export function buildRpdiagCidKey(channelId: string | undefined): string | undefined {
  const trimmed = channelId?.trim();
  return trimmed ? `cid:${trimmed}` : undefined;
}

/** 查质量分：稳定 channel_id 优先、(provider, service, channel) 三元组兜底。
 *
 *  provider 接受单个字符串或候选数组，按顺序尝试、命中即返回。调用方传
 *  `[providerName, providerId]`（providerId = 归一化 slug）——**展示名优先、slug 兜底**：
 *  - 展示名（= rpdiag provider_name）与后端索引对齐，修好 slug≠展示名的服务商
 *    （如 WorldBase.ai 的 slug=worldbase、YunWu 的 slug=yunwui，否则查表落空）；
 *  - slug 兜底保证展示名缺失/为空白/与 rpdiag 不同步时，历史本可 join 的通道不回归。
 *  空白/空候选自动跳过（不会拿 `|svc|chan` 去撞表）。 */
export function lookupRpdiagScore(
  scores: RpdiagScoresResponse | undefined,
  provider: string | undefined | ReadonlyArray<string | undefined>,
  service: string | undefined,
  channel: string | undefined,
  channelId?: string,
): RpdiagScore | undefined {
  if (!scores) return undefined;
  // 稳定 id 优先：与后端 "cid:"+id 桶键对称，自洽不依赖展示名，吸收 channel_name 漂移。
  // cid key 自足，不需要 service/channel，故在三元组守卫之前查。
  const cidKey = buildRpdiagCidKey(channelId);
  if (cidKey) {
    const cidHit = scores[cidKey];
    if (cidHit) return cidHit;
  }
  // 兜底：rpdiag 尚未给该通道打 cid（过渡期）或 monitor 无 channel_id → 退三元组展示名。
  if (!service || !channel) return undefined;
  const candidates = Array.isArray(provider) ? provider : [provider];
  for (const candidate of candidates) {
    if (!canonical(candidate)) continue; // 跳过 undefined / 空 / 纯空白候选
    const hit = scores[buildRpdiagKey(candidate, service, channel)];
    if (hit) return hit;
  }
  return undefined;
}

function canonical(v: string | undefined): string {
  return (v ?? '').trim().toLowerCase();
}
