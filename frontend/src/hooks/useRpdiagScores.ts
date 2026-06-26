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

/** 按 (provider, service, channel) 查表，缺失返回 undefined。
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
): RpdiagScore | undefined {
  if (!scores || !service || !channel) return undefined;
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
