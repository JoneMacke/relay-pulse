import { useState, useCallback, useEffect, useMemo } from 'react';
import { apiGet, apiPost, apiPut, apiDelete, ApiError } from '../utils/apiClient';
import type { AdminChangeRequest, ChangeRequestStatus } from '../types/change';

type ChangeAction = 'approve' | 'reject' | 'apply' | 'delete';

export function useChangeAdmin(token: string) {
  const [changes, setChanges] = useState<AdminChangeRequest[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [featureDisabled, setFeatureDisabled] = useState(false);
  const [statusFilter, setStatusFilter] = useState<ChangeRequestStatus | 'all'>('all');
  const [selectedChange, setSelectedChange] = useState<AdminChangeRequest | null>(null);

  // per-id 操作态：admin 可能并发对不同 change 操作，单值标志会互相覆盖（A 的 finally 清掉 B），故用 map
  const [pendingActions, setPendingActions] = useState<Record<string, ChangeAction>>({});
  const markPending = useCallback((id: string, action: ChangeAction) => {
    setPendingActions(prev => ({ ...prev, [id]: action }));
  }, []);
  const clearPending = useCallback((id: string) => {
    setPendingActions(prev => {
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  const headers = useMemo(
    (): Record<string, string> => (token ? { Authorization: `Bearer ${token}` } : {}),
    [token],
  );

  const fetchList = useCallback(async () => {
    if (!token) return;
    setIsLoading(true);
    setError(null);
    setFeatureDisabled(false);
    try {
      const params = statusFilter !== 'all' ? `?status=${statusFilter}` : '';
      const resp = await apiGet<{ changes: AdminChangeRequest[]; total: number }>(`/api/admin/changes${params}`, { headers });
      setChanges(resp.changes || []);
    } catch (e) {
      if (e instanceof ApiError && e.code === 'FEATURE_DISABLED') {
        setFeatureDisabled(true);
      } else {
        setError(e instanceof ApiError ? e.message : 'Failed to load change requests');
      }
    } finally {
      setIsLoading(false);
    }
  }, [token, statusFilter, headers]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 挂载/筛选变更即取数：fetchList 在 await 前同步置 loading/清错误为有意
    fetchList();
  }, [fetchList]);

  const fetchDetail = useCallback(async (id: string) => {
    if (!token) return;
    setError(null);
    try {
      const resp = await apiGet<{ change: AdminChangeRequest; new_key?: string }>(`/api/admin/changes/${id}`, { headers });
      setSelectedChange(resp.change);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to load change request detail');
    }
  }, [token, headers]);

  const updateChange = useCallback(async (id: string, updates: Record<string, unknown>) => {
    if (!token) return;
    setError(null);
    try {
      await apiPut(`/api/admin/changes/${id}`, updates, { headers });
      await fetchList();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to update change request');
    }
  }, [token, headers, fetchList]);

  const approveChange = useCallback(async (id: string, note?: string) => {
    if (!token) return;
    setError(null);
    markPending(id, 'approve');
    try {
      await apiPost(`/api/admin/changes/${id}/approve`, { note }, { headers });
      await fetchList();
      setSelectedChange(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to approve');
    } finally {
      clearPending(id);
    }
  }, [token, headers, fetchList, markPending, clearPending]);

  const rejectChange = useCallback(async (id: string, note: string) => {
    if (!token) return;
    setError(null);
    markPending(id, 'reject');
    try {
      await apiPost(`/api/admin/changes/${id}/reject`, { note }, { headers });
      await fetchList();
      setSelectedChange(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to reject');
    } finally {
      clearPending(id);
    }
  }, [token, headers, fetchList, markPending, clearPending]);

  const applyChange = useCallback(async (id: string) => {
    if (!token) return;
    setError(null);
    markPending(id, 'apply');
    try {
      await apiPost(`/api/admin/changes/${id}/apply`, {}, { headers });
      await fetchList();
      setSelectedChange(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to apply');
    } finally {
      clearPending(id);
    }
  }, [token, headers, fetchList, markPending, clearPending]);

  const deleteChange = useCallback(async (id: string) => {
    if (!token) return;
    setError(null);
    markPending(id, 'delete');
    try {
      await apiDelete(`/api/admin/changes/${id}`, { headers });
      await fetchList();
      setSelectedChange(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to delete');
    } finally {
      clearPending(id);
    }
  }, [token, headers, fetchList, markPending, clearPending]);

  return {
    changes,
    isLoading,
    error,
    featureDisabled,
    statusFilter,
    setStatusFilter,
    selectedChange,
    setSelectedChange,
    pendingActions,
    fetchList,
    fetchDetail,
    updateChange,
    approveChange,
    rejectChange,
    applyChange,
    deleteChange,
  };
}
