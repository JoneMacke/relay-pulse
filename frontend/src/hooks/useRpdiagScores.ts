import { useEffect, useState } from 'react';

import { apiGet } from '../utils/apiClient';
import type { RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

interface UseRpdiagScoresResult {
  scores: RpdiagScoresResponse;
  loaded: boolean;
}

/** 拉取 rpdiag 质量分索引（一次性，挂载时触发）。
 *
 *  - 后端缓存 TTL 10min，前端刷新更频繁也无收益
 *  - 失败时返回空对象 ⇒ 列表用 "-" 兜底，不阻塞页面
 *  - kill switch 由后端判断（MONITOR_RPDIAG_ENABLED）
 */
export function useRpdiagScores(): UseRpdiagScoresResult {
  const [scores, setScores] = useState<RpdiagScoresResponse>({});
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    apiGet<RpdiagScoresResponse>('/api/rpdiag-scores', { signal: controller.signal })
      .then((data) => {
        if (cancelled) return;
        setScores(data ?? {});
        setLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setScores({});
        setLoaded(true);
      });

    return () => {
      cancelled = true;
      controller.abort();
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
