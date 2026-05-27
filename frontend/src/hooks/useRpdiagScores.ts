import { useEffect, useState } from 'react';

import { apiGet } from '../utils/apiClient';
import type { RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

interface UseRpdiagScoresResult {
  scores: RpdiagScoresResponse;
  loaded: boolean;
}

const RPDIAG_POLL_INTERVAL_MS = 10 * 60 * 1000; // 与后端缓存 TTL 对齐

/** 拉取 rpdiag 质量分索引，每 10 分钟自动刷新（与后端缓存 TTL 对齐）。
 *
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
        .catch(() => {
          if (cancelled) return;
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
 *  channel 入参可以是带 rpdiag 前缀的形式（如 "O-Max"），会自动剥前缀。 */
export function buildRpdiagKey(
  provider: string | undefined,
  service: string | undefined,
  channel: string | undefined,
): string {
  return [
    canonical(provider),
    canonical(service),
    stripChannelPrefix(canonical(channel)),
  ].join('|');
}

/** 按 (provider, service, channel) 查表，缺失返回 undefined。 */
export function lookupRpdiagScore(
  scores: RpdiagScoresResponse | undefined,
  provider: string | undefined,
  service: string | undefined,
  channel: string | undefined,
): RpdiagScore | undefined {
  if (!scores || !provider || !service || !channel) return undefined;
  return scores[buildRpdiagKey(provider, service, channel)];
}

function canonical(v: string | undefined): string {
  return (v ?? '').trim().toLowerCase();
}

function stripChannelPrefix(name: string): string {
  if (name.length > 2 && name[1] === '-' && 'ormu'.includes(name[0])) {
    return name.slice(2);
  }
  return name;
}
