import { useState, useCallback, useEffect } from 'react';
import { apiGet, apiPost, apiPut, apiDelete, ApiError } from '../utils/apiClient';
import type {
  AdminSubmission,
  AdminListResponse,
  AdminDetailResponse,
  OnboardingTestResult,
  SubmissionStatus,
} from '../types/onboarding';

const TOKEN_KEY = 'relay-pulse-admin-token';

export function useAdmin() {
  const [token, setTokenState] = useState<string>(() => {
    try { return localStorage.getItem(TOKEN_KEY) || ''; } catch { return ''; }
  });
  const [isAuthenticated, setIsAuthenticated] = useState(!!token);

  // List state
  const [submissions, setSubmissions] = useState<AdminSubmission[]>([]);
  const [total, setTotal] = useState(0);
  const [statusFilter, setStatusFilter] = useState<SubmissionStatus | 'all'>('all');
  const [page, setPage] = useState(1);
  const [isLoading, setIsLoading] = useState(false);

  // Detail state
  const [selectedSubmission, setSelectedSubmission] = useState<AdminSubmission | null>(null);
  const [selectedApiKey, setSelectedApiKey] = useState<string>('');
  const [showApiKey, setShowApiKey] = useState(false);

  const [error, setError] = useState<string | null>(null);
  const [suggestedChannel, setSuggestedChannel] = useState<string>('');

  const authHeaders = useCallback((): HeadersInit => ({
    Authorization: `Bearer ${token}`,
  }), [token]);

  const setToken = useCallback((t: string) => {
    setTokenState(t);
    try { localStorage.setItem(TOKEN_KEY, t); } catch { /* ignore */ }
    setIsAuthenticated(!!t);
  }, []);

  const logout = useCallback(() => {
    setTokenState('');
    try { localStorage.removeItem(TOKEN_KEY); } catch { /* ignore */ }
    setIsAuthenticated(false);
  }, []);

  // Fetch list
  const fetchList = useCallback(async () => {
    if (!token) return;
    setIsLoading(true);
    setError(null);

    try {
      const limit = 20;
      const resp = await apiGet<AdminListResponse>(
        `/api/admin/submissions?status=${statusFilter}&limit=${limit}&offset=${(page - 1) * limit}`,
        { headers: authHeaders() },
      );
      setSubmissions(resp.submissions || []);
      setTotal(resp.total);
    } catch (e) {
      if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
        setIsAuthenticated(false);
        setError('认证失败，请重新输入 token');
      } else {
        setError(e instanceof ApiError ? e.message : '加载失败');
      }
    } finally {
      setIsLoading(false);
    }
  }, [token, statusFilter, page, authHeaders]);

  // Auto-fetch on filter/page change
  useEffect(() => {
    if (isAuthenticated) fetchList();
  }, [isAuthenticated, fetchList]);

  // Fetch detail
  const fetchDetail = useCallback(async (publicId: string) => {
    if (!token) return;
    setError(null);

    try {
      const resp = await apiGet<AdminDetailResponse>(
        `/api/admin/submissions/${publicId}`,
        { headers: authHeaders() },
      );
      setSelectedSubmission(resp.submission);
      setSelectedApiKey(resp.api_key);
      setShowApiKey(false);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '加载详情失败');
    }
  }, [token, authHeaders]);

  // Update submission
  const updateSubmission = useCallback(async (publicId: string, updates: Record<string, unknown>) => {
    if (!token) return;
    setError(null);

    try {
      const resp = await apiPut<{ submission: AdminSubmission }>(
        `/api/admin/submissions/${publicId}`,
        updates,
        { headers: authHeaders() },
      );
      setSelectedSubmission(resp.submission);
      fetchList(); // refresh list
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '更新失败');
    }
  }, [token, authHeaders, fetchList]);

  // Reject
  const rejectSubmission = useCallback(async (publicId: string, note: string) => {
    if (!token) return;
    setError(null);

    try {
      await apiPost(`/api/admin/submissions/${publicId}/reject`, { note }, { headers: authHeaders() });
      fetchList();
      setSelectedSubmission(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '驳回失败');
    }
  }, [token, authHeaders, fetchList]);

  // Test — inline probe, returns result synchronously
  const testSubmission = useCallback(async (publicId: string): Promise<OnboardingTestResult | null> => {
    if (!token) return null;
    setError(null);

    try {
      const resp = await apiPost<OnboardingTestResult>(
        `/api/admin/submissions/${publicId}/test`,
        {},
        { headers: authHeaders() },
      );
      return resp;
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '测试失败');
      return null;
    }
  }, [token, authHeaders]);

  // Delete
  const deleteSubmission = useCallback(async (publicId: string) => {
    if (!token) return;
    setError(null);

    try {
      await apiDelete(`/api/admin/submissions/${publicId}`, { headers: authHeaders() });
      fetchList();
      setSelectedSubmission(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '删除失败');
    }
  }, [token, authHeaders, fetchList]);

  // Publish
  const publishSubmission = useCallback(async (publicId: string, board = 'hot') => {
    if (!token) return;
    setError(null);
    setSuggestedChannel('');

    try {
      await apiPost(`/api/admin/submissions/${publicId}/publish`, { board }, { headers: authHeaders() });
      fetchList();
      setSelectedSubmission(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && e.data) {
        const suggested = e.data.suggested_channel;
        if (typeof suggested === 'string') {
          setSuggestedChannel(suggested);
        }
      }
      setError(e instanceof ApiError ? e.message : '上架失败');
    }
  }, [token, authHeaders, fetchList]);

  return {
    // Auth
    token,
    isAuthenticated,
    setToken,
    logout,

    // List
    submissions,
    total,
    statusFilter,
    setStatusFilter,
    page,
    setPage,
    isLoading,
    fetchList,

    // Detail
    selectedSubmission,
    selectedApiKey,
    showApiKey,
    setShowApiKey,
    fetchDetail,
    updateSubmission,
    testSubmission,
    rejectSubmission,
    deleteSubmission,
    publishSubmission,
    setSelectedSubmission,

    error,
    suggestedChannel,
  };
}
