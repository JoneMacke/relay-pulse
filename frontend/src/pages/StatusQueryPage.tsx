import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { Helmet } from 'react-helmet-async';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Activity, AlertCircle, CheckCircle2, Loader2, Search } from 'lucide-react';
import { LANGUAGE_PATH_MAP, type SupportedLanguage } from '../i18n';
import { apiGet, ApiError } from '../utils/apiClient';
import type { OnboardingStatusResponse, SubmissionStatus } from '../types/onboarding';
import type { ChangeRequestStatus, ChangeStatusResponse } from '../types/change';

// 收录申请与变更请求共用同一查询页：public_id 是 UUID，用户不一定记得是哪类申请，
// 因此先查 /api/onboarding/:id，命中 404 再回退查 /api/change/:id，由后端 404 语义消歧。
type StatusResult =
  | { kind: 'onboarding'; data: OnboardingStatusResponse }
  | { kind: 'change'; data: ChangeStatusResponse };

type StatusValue = SubmissionStatus | ChangeRequestStatus;

function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}

/** 状态徽章配色：复用后台 SubmissionList 的语义色。 */
function statusBadgeClass(status: StatusValue): string {
  switch (status) {
    case 'pending':
      return 'bg-warning/15 text-warning border-warning/30';
    case 'approved':
      return 'bg-accent/15 text-accent border-accent/30';
    case 'rejected':
      return 'bg-danger/15 text-danger border-danger/30';
    case 'published':
    case 'applied':
      return 'bg-success/15 text-success border-success/30';
    default:
      return 'bg-muted/15 text-muted border-muted/30';
  }
}

function formatTimestamp(ts: number): string {
  if (!ts) return '--';
  return new Date(ts * 1000).toLocaleString();
}

function InfoRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2 border-b border-muted/20 last:border-b-0">
      <span className="text-sm text-secondary flex-shrink-0">{label}</span>
      <span className="text-sm text-primary text-right break-all">{value}</span>
    </div>
  );
}

function ResultCard({ result }: { result: StatusResult }) {
  const { t } = useTranslation();
  const { status } = result.data;

  return (
    <div className="bg-surface border border-default rounded-2xl p-6 space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={`inline-flex items-center px-2.5 py-1 rounded-full border text-xs font-medium ${
            result.kind === 'onboarding'
              ? 'bg-accent/10 text-accent border-accent/30'
              : 'bg-success/10 text-success border-success/30'
          }`}
        >
          {t(`statusQuery.type.${result.kind}`)}
        </span>
        <span
          className={`inline-flex items-center px-2.5 py-1 rounded-full border text-xs font-medium ${statusBadgeClass(status)}`}
        >
          {t(`statusQuery.status.${status}`, { defaultValue: status })}
        </span>
      </div>

      <div className="flex items-center gap-2">
        <CheckCircle2 className="w-5 h-5 text-success" />
        <h2 className="text-lg font-semibold text-primary">{t('statusQuery.resultTitle')}</h2>
      </div>

      <div className="bg-elevated rounded-lg p-4">
        <InfoRow
          label={t('statusQuery.fields.publicId')}
          value={<code className="font-mono text-xs select-all">{result.data.public_id}</code>}
        />
        {result.kind === 'onboarding' ? (
          <>
            <InfoRow label={t('statusQuery.fields.providerName')} value={result.data.provider_name} />
            <InfoRow label={t('statusQuery.fields.serviceType')} value={result.data.service_type.toUpperCase()} />
            <InfoRow label={t('statusQuery.fields.channelCode')} value={result.data.channel_code || '--'} />
          </>
        ) : (
          <>
            <InfoRow label={t('statusQuery.fields.targetKey')} value={result.data.target_key} />
            <InfoRow
              label={t('statusQuery.fields.applyMode')}
              value={t(`statusQuery.applyMode.${result.data.apply_mode}`, { defaultValue: result.data.apply_mode })}
            />
          </>
        )}
        <InfoRow label={t('statusQuery.fields.createdAt')} value={formatTimestamp(result.data.created_at)} />
        <InfoRow label={t('statusQuery.fields.updatedAt')} value={formatTimestamp(result.data.updated_at)} />
      </div>
    </div>
  );
}

export default function StatusQueryPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  const [inputId, setInputId] = useState(() => searchParams.get('id')?.trim() ?? '');
  const [result, setResult] = useState<StatusResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  // 自增序号：并发/快速重查时，只让最后一次请求的结果落地，避免旧响应覆盖新结果。
  const requestSeq = useRef(0);

  const langPrefix = LANGUAGE_PATH_MAP[i18n.language as SupportedLanguage];
  const homePath = langPrefix ? `/${langPrefix}` : '/';

  const runQuery = useCallback(
    async (rawId: string, syncUrl: boolean) => {
      const id = rawId.trim();
      const seq = ++requestSeq.current;

      setInputId(id);
      setResult(null);
      setError(null);
      setNotFound(false);

      if (!id) {
        setError(t('statusQuery.errors.emptyId'));
        return;
      }
      if (syncUrl) setSearchParams({ id }, { replace: true });

      setIsLoading(true);
      try {
        // 第一跳：收录申请。仅 404 视为"非该类型"继续回退；其他错误（网络/服务不可用）直接抛出展示。
        try {
          const data = await apiGet<OnboardingStatusResponse>(`/api/onboarding/${encodeURIComponent(id)}`);
          if (seq === requestSeq.current) setResult({ kind: 'onboarding', data });
          return;
        } catch (err) {
          if (!isNotFound(err)) throw err;
        }
        // 第二跳：变更请求。两类都 404 → 真正"未找到"。
        try {
          const data = await apiGet<ChangeStatusResponse>(`/api/change/${encodeURIComponent(id)}`);
          if (seq === requestSeq.current) setResult({ kind: 'change', data });
        } catch (err) {
          if (!isNotFound(err)) throw err;
          if (seq === requestSeq.current) setNotFound(true);
        }
      } catch (err) {
        if (seq === requestSeq.current) {
          setError(err instanceof ApiError ? err.message : t('statusQuery.errors.queryFailed'));
        }
      } finally {
        if (seq === requestSeq.current) setIsLoading(false);
      }
    },
    [setSearchParams, t],
  );

  // 深链：仅在挂载时若带 ?id= 自动查询一次（来自提交成功页的"查看进度"）。
  // 之后的查询都由表单提交驱动，避免 URL 同步与 effect 互相触发形成重复请求。
  const didAutoQuery = useRef(false);
  useEffect(() => {
    if (didAutoQuery.current) return;
    didAutoQuery.current = true;
    const initialId = searchParams.get('id')?.trim() ?? '';
    if (initialId) void runQuery(initialId, false);
  }, [searchParams, runQuery]);

  return (
    <>
      <Helmet>
        <title>{t('statusQuery.meta.title')}</title>
        <meta name="description" content={t('statusQuery.meta.description')} />
        <meta name="robots" content="noindex,nofollow" />
      </Helmet>

      <div className="min-h-screen bg-page flex flex-col">
        <header className="px-4 py-4 border-b border-default/50">
          <div className="max-w-4xl mx-auto flex items-center gap-3">
            <button
              type="button"
              onClick={() => navigate(homePath)}
              className="p-1.5 bg-accent/10 rounded-lg border border-accent/20 flex-shrink-0"
            >
              <Activity className="w-5 h-5 text-accent" />
            </button>
            <span className="text-lg font-bold text-gradient-hero">RelayPulse</span>
          </div>
        </header>

        <main className="flex-1 max-w-2xl mx-auto w-full px-4 py-12 sm:py-16">
          <div className="text-center mb-8">
            <h1 className="text-3xl sm:text-4xl font-bold text-primary mb-3">{t('statusQuery.title')}</h1>
            <p className="text-secondary max-w-xl mx-auto">{t('statusQuery.description')}</p>
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              void runQuery(inputId, true);
            }}
            className="bg-surface border border-default rounded-2xl p-5 sm:p-6 space-y-4 mb-6"
          >
            <label className="block text-sm font-medium text-secondary" htmlFor="status-public-id">
              {t('statusQuery.idLabel')}
            </label>
            <div className="flex flex-col sm:flex-row gap-3">
              <input
                id="status-public-id"
                type="text"
                value={inputId}
                onChange={(e) => setInputId(e.target.value)}
                placeholder={t('statusQuery.placeholder')}
                className="flex-1 px-3 py-2.5 rounded-xl bg-elevated border border-default text-primary
                           placeholder:text-muted focus:border-accent/50 focus:outline-none transition font-mono text-sm"
              />
              <button
                type="submit"
                disabled={isLoading}
                className="inline-flex items-center justify-center gap-2 px-5 py-2.5 rounded-xl bg-accent text-white
                           font-medium hover:bg-accent-strong transition disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {isLoading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Search className="w-4 h-4" />}
                {isLoading ? t('statusQuery.querying') : t('statusQuery.submit')}
              </button>
            </div>
          </form>

          {error && (
            <div className="flex items-start gap-3 p-4 rounded-xl bg-danger/10 border border-danger/20 text-danger mb-6">
              <AlertCircle className="w-5 h-5 mt-0.5 flex-shrink-0" />
              <p className="text-sm font-medium">{error}</p>
            </div>
          )}

          {notFound && (
            <div className="bg-surface border border-default rounded-2xl p-6 text-center space-y-2">
              <h2 className="text-lg font-semibold text-primary">{t('statusQuery.notFoundTitle')}</h2>
              <p className="text-sm text-secondary">{t('statusQuery.notFoundDescription')}</p>
            </div>
          )}

          {result && <ResultCard result={result} />}
        </main>
      </div>
    </>
  );
}
