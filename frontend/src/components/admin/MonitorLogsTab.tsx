import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { MonitorFile, ProbeHistoryEntry } from '../../types/monitor';

interface MonitorLogsTabProps {
  monitorKey: string;
  monitorFile: MonitorFile;
  fetchLogs: (
    key: string,
    opts?: { since?: string; limit?: number; model?: string },
  ) => Promise<ProbeHistoryEntry[]>;
}

const SINCE_OPTIONS = [
  { value: '10m', labelKey: 'admin.monitors.logs.since10m' },
  { value: '1h', labelKey: 'admin.monitors.logs.since1h' },
  { value: '6h', labelKey: 'admin.monitors.logs.since6h' },
  { value: '24h', labelKey: 'admin.monitors.logs.since24h' },
  { value: '168h', labelKey: 'admin.monitors.logs.since7d' },
] as const;

/**
 * MonitorLogsTab 展示某监测项最近的探测历史记录。
 * 数据来源：GET /api/admin/monitors/:key/logs
 *
 * 设计取舍：
 *   - 默认拉取 1h 范围内所有 model 的最近 200 条；用户可切换时间范围、按 model 过滤
 *   - error_detail 默认折叠（含上游可能的敏感信息），点击行展开查看
 *   - 表格按 timestamp DESC 显示（最新在顶部）
 */
export function MonitorLogsTab({ monitorKey, monitorFile, fetchLogs }: MonitorLogsTabProps) {
  const { t } = useTranslation();
  const [since, setSince] = useState<string>('1h');
  const [modelFilter, setModelFilter] = useState<string>('');
  const [logs, setLogs] = useState<ProbeHistoryEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedId, setExpandedId] = useState<number | null>(null);

  // 从 monitor file 推断可选 model 列表：父通道 model + 所有子通道 model
  const availableModels = useMemo(() => {
    const set = new Set<string>();
    for (const m of monitorFile.monitors) {
      if (m.model) set.add(m.model);
    }
    return Array.from(set).sort();
  }, [monitorFile]);

  const load = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const items = await fetchLogs(monitorKey, {
        since,
        limit: 200,
        model: modelFilter || undefined,
      });
      setLogs(items);
    } catch (e) {
      setError(e instanceof Error ? e.message : t('admin.monitors.logs.loadFailed'));
    } finally {
      setIsLoading(false);
    }
  }, [fetchLogs, monitorKey, since, modelFilter, t]);

  useEffect(() => { load(); }, [load]);

  return (
    <div className="space-y-4">
      {/* 过滤栏 */}
      <div className="flex flex-wrap gap-3 items-center">
        <label className="text-xs text-muted">
          {t('admin.monitors.logs.sinceLabel')}
          <select
            value={since}
            onChange={(e) => setSince(e.target.value)}
            className="ml-2 px-2 py-1 rounded bg-elevated border border-default text-primary text-xs"
          >
            {SINCE_OPTIONS.map(opt => (
              <option key={opt.value} value={opt.value}>{t(opt.labelKey)}</option>
            ))}
          </select>
        </label>

        {availableModels.length > 0 && (
          <label className="text-xs text-muted">
            {t('admin.monitors.logs.modelLabel')}
            <select
              value={modelFilter}
              onChange={(e) => setModelFilter(e.target.value)}
              className="ml-2 px-2 py-1 rounded bg-elevated border border-default text-primary text-xs"
            >
              <option value="">{t('admin.monitors.logs.allModels')}</option>
              {availableModels.map(m => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
          </label>
        )}

        <button
          onClick={load}
          disabled={isLoading}
          className="px-3 py-1 rounded-lg border border-default text-secondary hover:text-primary text-xs transition disabled:opacity-50"
        >
          {isLoading ? t('admin.monitors.logs.loading') : t('admin.monitors.logs.refresh')}
        </button>

        <span className="text-xs text-muted">
          {t('admin.monitors.logs.totalCount', { count: logs.length })}
        </span>
      </div>

      {/* 错误提示 */}
      {error && (
        <div className="p-3 bg-danger/10 border border-danger/20 rounded-lg text-danger text-sm">
          {error}
        </div>
      )}

      {/* 表格 */}
      {!error && logs.length === 0 && !isLoading ? (
        <div className="text-center py-8 text-muted text-sm">
          {t('admin.monitors.logs.empty')}
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-default text-left text-muted">
                <th className="py-2 px-2 w-40">{t('admin.monitors.logs.colTime')}</th>
                <th className="py-2 px-2">{t('admin.monitors.logs.colModel')}</th>
                <th className="py-2 px-2 w-16 text-center">{t('admin.monitors.logs.colStatus')}</th>
                <th className="py-2 px-2 w-32">{t('admin.monitors.logs.colSubStatus')}</th>
                <th className="py-2 px-2 w-16 text-center">{t('admin.monitors.logs.colHttp')}</th>
                <th className="py-2 px-2 w-20 text-right">{t('admin.monitors.logs.colLatency')}</th>
                <th className="py-2 px-2">{t('admin.monitors.logs.colError')}</th>
              </tr>
            </thead>
            <tbody>
              {logs.map(entry => {
                const hasDetail = !!entry.error_detail;
                const isExpanded = expandedId === entry.id;
                return (
                  <tr
                    key={entry.id}
                    onClick={() => hasDetail && setExpandedId(isExpanded ? null : entry.id)}
                    className={`border-b border-default/30 ${hasDetail ? 'cursor-pointer hover:bg-elevated/50' : ''} transition`}
                  >
                    <td className="py-1.5 px-2 text-secondary font-mono">
                      {formatTimestamp(entry.timestamp)}
                    </td>
                    <td className="py-1.5 px-2 text-muted">{entry.model || '-'}</td>
                    <td className="py-1.5 px-2 text-center">
                      <StatusDot status={entry.status} />
                    </td>
                    <td className="py-1.5 px-2 text-muted">{entry.sub_status || '-'}</td>
                    <td className="py-1.5 px-2 text-center text-muted">{entry.http_code || '-'}</td>
                    <td className="py-1.5 px-2 text-right text-secondary">
                      {entry.latency > 0 ? `${entry.latency}ms` : '-'}
                    </td>
                    <td className="py-1.5 px-2 text-muted">
                      {hasDetail ? (
                        isExpanded ? (
                          <pre className="whitespace-pre-wrap break-all text-xs max-h-40 overflow-y-auto bg-surface p-2 rounded">
                            {entry.error_detail}
                          </pre>
                        ) : (
                          <span className="truncate inline-block max-w-[300px] align-middle" title={entry.error_detail}>
                            {entry.error_detail}
                          </span>
                        )
                      ) : (
                        '-'
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function formatTimestamp(unixSec: number): string {
  const d = new Date(unixSec * 1000);
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mi = String(d.getMinutes()).padStart(2, '0');
  const ss = String(d.getSeconds()).padStart(2, '0');
  return `${yyyy}-${mm}-${dd} ${hh}:${mi}:${ss}`;
}

function StatusDot({ status }: { status: number }) {
  const color =
    status === 1 ? 'bg-success' :
    status === 2 ? 'bg-warning' :
    status === 0 ? 'bg-danger' :
    'bg-muted';
  return <span className={`inline-block w-2 h-2 rounded-full ${color}`} />;
}
