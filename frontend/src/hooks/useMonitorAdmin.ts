import { useState, useCallback, useEffect } from 'react';
import { apiGet, apiPost, apiPut, apiDelete, ApiError } from '../utils/apiClient';
import type {
  MonitorSummary,
  MonitorFile,
  AdminMonitorListResponse,
  AdminMonitorDetailResponse,
  AdminMonitorLogsResponse,
  ProbeHistoryEntry,
} from '../types/monitor';

export interface ProbeResult {
  probeId: string;
  probeStatus: number;
  subStatus: string;
  httpCode: number;
  latency: number;
  errorMessage: string;
  responseSnippet: string;
}

export function useMonitorAdmin(token: string) {
  const [monitors, setMonitors] = useState<MonitorSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Filters
  const [boardFilter, setBoardFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [searchQuery, setSearchQuery] = useState('');

  // Detail
  const [selectedMonitor, setSelectedMonitor] = useState<MonitorFile | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);

  const authHeaders = useCallback((): HeadersInit => ({
    Authorization: `Bearer ${token}`,
  }), [token]);

  // Fetch list
  const fetchList = useCallback(async () => {
    if (!token) return;
    setIsLoading(true);
    setError(null);

    try {
      const params = new URLSearchParams();
      if (boardFilter) params.set('board', boardFilter);
      if (statusFilter) params.set('status', statusFilter);
      if (searchQuery) params.set('q', searchQuery);

      const qs = params.toString();
      const resp = await apiGet<AdminMonitorListResponse>(
        `/api/admin/monitors${qs ? '?' + qs : ''}`,
        { headers: authHeaders() },
      );
      setMonitors(resp.monitors || []);
      setTotal(resp.total);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '加载失败');
    } finally {
      setIsLoading(false);
    }
  }, [token, boardFilter, statusFilter, searchQuery, authHeaders]);

  useEffect(() => {
    if (token) fetchList();
  }, [token, fetchList]);

  // Fetch templates
  const fetchTemplates = useCallback(async (): Promise<string[]> => {
    if (!token) return [];

    try {
      const resp = await apiGet<{ templates: string[] }>(
        '/api/admin/templates',
        { headers: authHeaders() },
      );
      return resp.templates || [];
    } catch {
      return [];
    }
  }, [token, authHeaders]);

  // Fetch detail
  const fetchDetail = useCallback(async (key: string) => {
    if (!token) return;
    setError(null);
    setProbeResult(null);
    setProbeError(null);

    try {
      const resp = await apiGet<AdminMonitorDetailResponse>(
        `/api/admin/monitors/${key}`,
        { headers: authHeaders() },
      );
      setSelectedMonitor(resp.monitor);
      setSelectedKey(key);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '加载详情失败');
    }
  }, [token, authHeaders]);

  // Create
  const createMonitor = useCallback(async (file: MonitorFile) => {
    if (!token) return;
    setError(null);

    try {
      await apiPost<AdminMonitorDetailResponse>(
        '/api/admin/monitors',
        file,
        { headers: authHeaders() },
      );
      fetchList();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : '创建失败';
      setError(msg);
      throw e;
    }
  }, [token, authHeaders, fetchList]);

  // Update
  const updateMonitor = useCallback(async (key: string, file: MonitorFile, revision: number) => {
    if (!token) return;
    setError(null);

    try {
      const resp = await apiPut<{ monitor: MonitorFile }>(
        `/api/admin/monitors/${key}`,
        { revision, monitor: file },
        { headers: authHeaders() },
      );
      setSelectedMonitor(resp.monitor);
      fetchList();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : '更新失败';
      setError(msg);
      throw e;
    }
  }, [token, authHeaders, fetchList]);

  // Delete
  const deleteMonitor = useCallback(async (key: string) => {
    if (!token) return;
    setError(null);

    try {
      await apiDelete(`/api/admin/monitors/${key}`, { headers: authHeaders() });
      setSelectedMonitor(null);
      setSelectedKey(null);
      fetchList();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '删除失败');
    }
  }, [token, authHeaders, fetchList]);

  // Toggle
  const toggleMonitor = useCallback(async (key: string, field: 'disabled' | 'hidden', value: boolean) => {
    if (!token) return;
    setError(null);

    try {
      const resp = await apiPost<{ monitor: MonitorFile }>(
        `/api/admin/monitors/${key}/toggle`,
        { field, value },
        { headers: authHeaders() },
      );
      setSelectedMonitor(resp.monitor);
      fetchList();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '切换失败');
    }
  }, [token, authHeaders, fetchList]);

  // Probe
  const [isProbing, setIsProbing] = useState(false);
  const [probeResult, setProbeResult] = useState<ProbeResult | null>(null);
  const [probeError, setProbeError] = useState<string | null>(null);

  const probeMonitor = useCallback(async (
    key: string,
    overrides?: { template?: string; base_url?: string; api_key?: string },
  ): Promise<ProbeResult | null> => {
    if (!token) return null;
    setIsProbing(true);
    setProbeResult(null);
    setProbeError(null);

    try {
      const resp = await apiPost<{
        probe_id: string;
        probe_status: number;
        sub_status: string;
        http_code: number;
        latency: number;
        error_message: string;
        response_snippet: string;
      }>(
        `/api/admin/monitors/${key}/probe`,
        overrides ?? {},
        { headers: authHeaders() },
      );
      const result: ProbeResult = {
        probeId: resp.probe_id,
        probeStatus: resp.probe_status,
        subStatus: resp.sub_status,
        httpCode: resp.http_code,
        latency: resp.latency,
        errorMessage: resp.error_message,
        responseSnippet: resp.response_snippet,
      };
      setProbeResult(result);
      return result;
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : '探测失败';
      setProbeError(msg);
      return null;
    } finally {
      setIsProbing(false);
    }
  }, [token, authHeaders]);

  // Logs：拉取某监测项的探测历史记录（按 timestamp 倒序）。
  // since: Go duration (默认 "1h") 或 RFC3339；limit: 默认 200，上限 1000；model: 可选过滤。
  const fetchMonitorLogs = useCallback(async (
    key: string,
    opts?: { since?: string; limit?: number; model?: string },
  ): Promise<ProbeHistoryEntry[]> => {
    if (!token) return [];

    const params = new URLSearchParams();
    if (opts?.since) params.set('since', opts.since);
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.model) params.set('model', opts.model);

    const qs = params.toString();
    const resp = await apiGet<AdminMonitorLogsResponse>(
      `/api/admin/monitors/${encodeURIComponent(key)}/logs${qs ? '?' + qs : ''}`,
      { headers: authHeaders() },
    );
    return resp.logs || [];
  }, [token, authHeaders]);

  return {
    monitors,
    total,
    isLoading,
    error,

    boardFilter,
    setBoardFilter,
    statusFilter,
    setStatusFilter,
    searchQuery,
    setSearchQuery,
    fetchList,

    selectedMonitor,
    selectedKey,
    setSelectedMonitor,
    setSelectedKey,
    fetchDetail,
    fetchTemplates,
    createMonitor,
    updateMonitor,
    deleteMonitor,
    toggleMonitor,
    probeMonitor,
    isProbing,
    probeResult,
    probeError,
    fetchMonitorLogs,
  };
}
