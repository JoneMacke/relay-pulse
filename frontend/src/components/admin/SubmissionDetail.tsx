import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import type { AdminSubmission, OnboardingTestResult } from '../../types/onboarding';
import { FormField, SelectField, ReadOnlyField } from './FormControls';

/** 可编辑字段列表 — 用于本地 draft 初始化和脏检测 */
const EDITABLE_FIELDS = [
  'provider_name', 'website_url', 'category', 'service_type',
  'template_name', 'sponsor_level', 'channel_type', 'channel_source',
  'target_provider', 'target_service', 'target_channel',
  'channel_name', 'listed_since', 'expires_at',
  'price_min', 'price_max', 'base_url', 'admin_note',
] as const;
type EditableKey = (typeof EDITABLE_FIELDS)[number];
type Draft = Record<EditableKey, string>;

function pickDraft(sub: AdminSubmission): Draft {
  const d = {} as Draft;
  for (const k of EDITABLE_FIELDS) d[k] = (sub[k] as string | number)?.toString() ?? '';
  return d;
}

function hasDraftChanged(draft: Draft, sub: AdminSubmission): boolean {
  return EDITABLE_FIELDS.some((k) => draft[k] !== ((sub[k] as string | number)?.toString() ?? ''));
}

interface SubmissionDetailProps {
  submission: AdminSubmission;
  apiKey: string;
  showApiKey: boolean;
  setShowApiKey: (show: boolean) => void;
  onSave: (fields: Partial<AdminSubmission>) => void;
  onTest: () => Promise<OnboardingTestResult | null>;
  onReject: (note: string) => void;
  onDelete: () => void;
  onPublish: (board: string) => void;
  suggestedChannel?: string;
  onBack: () => void;
}

/** Format a unix timestamp to a readable date string. */
function formatTimestamp(ts: number | null): string {
  if (!ts) return '--';
  return new Date(ts * 1000).toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

/** Mask an API key, showing only the last 4 characters. */
function maskApiKey(last4: string): string {
  return `${'*'.repeat(20)}${last4}`;
}

export const SubmissionDetail: React.FC<SubmissionDetailProps> = ({
  submission,
  apiKey,
  showApiKey,
  setShowApiKey,
  onSave,
  onTest,
  onReject,
  onDelete,
  onPublish,
  suggestedChannel,
  onBack,
}) => {
  const { t } = useTranslation();

  // 本地编辑 draft — submission 变化时重置
  const [draft, setDraft] = useState<Draft>(() => pickDraft(submission));
  useEffect(() => { setDraft(pickDraft(submission)); }, [submission]);

  const dirty = hasDraftChanged(draft, submission);
  const [isSaving, setIsSaving] = useState(false);

  const [rejectNote, setRejectNote] = useState('');
  const [showRejectInput, setShowRejectInput] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [testResult, setTestResult] = useState<OnboardingTestResult | null>(null);

  const [publishBoard, setPublishBoard] = useState('hot');
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const canPublish = submission.status === 'pending' || submission.status === 'approved';
  const canReject = submission.status === 'pending' || submission.status === 'approved';
  const canDelete = submission.status !== 'published';

  const updateField = (field: EditableKey, value: string) => {
    setDraft((prev) => ({ ...prev, [field]: value }));
  };

  const handleSave = async () => {
    if (!dirty) return;
    setIsSaving(true);
    try {
      // 只发送有变化的字段
      const changes: Partial<AdminSubmission> = {};
      for (const k of EDITABLE_FIELDS) {
        if (draft[k] !== ((submission[k] as string | number)?.toString() ?? '')) {
          if (k === 'price_min' || k === 'price_max') {
            (changes as Record<string, number>)[k] = parseFloat(draft[k]) || 0;
          } else {
            (changes as Record<string, string>)[k] = draft[k];
          }
        }
      }
      onSave(changes);
    } finally {
      setIsSaving(false);
    }
  };

  const handleRejectConfirm = () => {
    onReject(rejectNote);
    setShowRejectInput(false);
    setRejectNote('');
  };

  const handleTest = async () => {
    setIsTesting(true);
    setTestResult(null);
    try {
      const resp = await onTest();
      if (resp) setTestResult(resp);
    } finally {
      setIsTesting(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header with back button */}
      <div className="flex items-center gap-3">
        <button
          onClick={onBack}
          className="px-3 py-1.5 text-sm rounded-md border
                     border-default text-secondary hover:bg-elevated transition-colors"
        >
          {t('admin.detail.back')}
        </button>
        <h2 className="text-lg font-semibold text-primary">
          {t('admin.detail.title')}
        </h2>
        <span className="text-xs text-muted font-mono">{submission.public_id}</span>
      </div>

      {/* Main content card */}
      <div className="bg-surface border border-default rounded-lg p-6 space-y-6">
        {/* Metadata row */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <ReadOnlyField
            label={t('admin.detail.status')}
            value={t(`admin.status.${submission.status}`)}
          />
          <ReadOnlyField
            label={t('admin.detail.createdAt')}
            value={formatTimestamp(submission.created_at)}
          />
          <ReadOnlyField
            label={t('admin.detail.reviewedAt')}
            value={formatTimestamp(submission.reviewed_at)}
          />
          <ReadOnlyField
            label={t('admin.detail.channelCode')}
            value={submission.channel_code}
          />
        </div>

        <hr className="border-default" />

        {/* Editable fields */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <FormField
            label={t('admin.detail.providerName')}
            value={draft.provider_name}
            onChange={(v) => updateField('provider_name', v)}
          />
          <FormField
            label={t('admin.detail.websiteUrl')}
            value={draft.website_url}
            onChange={(v) => updateField('website_url', v)}
            type="url"
          />
          <SelectField
            label={t('admin.detail.category')}
            value={draft.category}
            onChange={(v) => updateField('category', v)}
            options={[
              { value: 'commercial', label: t('onboarding.providerInfo.categories.commercial') },
              { value: 'public', label: t('onboarding.providerInfo.categories.public') },
            ]}
          />
          <SelectField
            label={t('admin.detail.serviceType')}
            value={draft.service_type}
            onChange={(v) => updateField('service_type', v)}
            options={[
              { value: 'cc', label: 'CC (Claude Code)' },
              { value: 'cx', label: 'CX (Codex)' },
              { value: 'gm', label: 'GM (Gemini)' },
            ]}
          />
          <SelectField
            label={t('admin.detail.templateName')}
            value={draft.template_name}
            onChange={(v) => updateField('template_name', v)}
            options={[
              { value: 'cc-haiku-arith', label: 'cc-haiku-arith' },
              { value: 'cc-haiku-arith-lite', label: 'cc-haiku-arith-lite' },
              { value: 'cc-haiku-pro-2184', label: 'cc-haiku-pro-2184' },
              { value: 'cc-sonnet-arith', label: 'cc-sonnet-arith' },
              { value: 'cc-opus-arith', label: 'cc-opus-arith' },
              { value: 'cc-opus-ping', label: 'cc-opus-ping' },
              { value: 'cx-codex-arith', label: 'cx-codex-arith' },
              { value: 'cx-codex-mini-arith', label: 'cx-codex-mini-arith' },
              { value: 'cx-codex-max-arith', label: 'cx-codex-max-arith' },
              { value: 'cx-gpt-arith', label: 'cx-gpt-arith' },
              { value: 'gm-flash-arith', label: 'gm-flash-arith' },
              { value: 'gm-flash-preview-arith', label: 'gm-flash-preview-arith' },
              { value: 'gm-flash-thinking-arith', label: 'gm-flash-thinking-arith' },
              { value: 'gm-pro-arith', label: 'gm-pro-arith' },
            ]}
          />
          <SelectField
            label={t('admin.detail.sponsorLevel')}
            value={draft.sponsor_level}
            onChange={(v) => updateField('sponsor_level', v)}
            options={[
              { value: '', label: t('admin.monitors.sponsorLevels.none', { defaultValue: '(空)' }) },
              { value: 'public', label: 'Public' },
              { value: 'signal', label: 'Signal' },
              { value: 'pulse', label: 'Pulse' },
              { value: 'beacon', label: 'Beacon' },
              { value: 'backbone', label: 'Backbone' },
              { value: 'core', label: 'Core' },
            ]}
          />
          <SelectField
            label={t('admin.detail.channelType')}
            value={draft.channel_type}
            onChange={(v) => updateField('channel_type', v)}
            options={[
              { value: 'O', label: 'O - 官方直连' },
              { value: 'R', label: 'R - 逆向' },
              { value: 'M', label: 'M - 混合' },
              { value: 'X', label: 'X - 其他' },
            ]}
          />
          <FormField
            label={t('admin.detail.channelSource')}
            value={draft.channel_source}
            onChange={(v) => updateField('channel_source', v)}
            placeholder="API, Web, AWS, GCP..."
          />
          <FormField
            label={t('admin.detail.channelName')}
            value={draft.channel_name}
            onChange={(v) => updateField('channel_name', v)}
          />
          <FormField
            label={t('admin.detail.listedSince')}
            value={draft.listed_since}
            onChange={(v) => updateField('listed_since', v)}
            type="date"
          />
          <FormField
            label={t('admin.detail.expiresAt', { defaultValue: '到期日期' })}
            value={draft.expires_at}
            onChange={(v) => updateField('expires_at', v)}
            type="date"
          />
          <FormField
            label={t('admin.detail.targetProvider', { defaultValue: 'Provider 覆盖' })}
            value={draft.target_provider}
            onChange={(v) => updateField('target_provider', v)}
            placeholder={t('admin.detail.targetProviderHint', { defaultValue: '留空使用派生值' })}
          />
          <FormField
            label={t('admin.detail.targetService', { defaultValue: 'Service 覆盖' })}
            value={draft.target_service}
            onChange={(v) => updateField('target_service', v)}
            placeholder={t('admin.detail.targetServiceHint', { defaultValue: '留空使用派生值' })}
          />
          <div>
            <FormField
              label={t('admin.detail.targetChannel', { defaultValue: 'Channel 覆盖' })}
              value={draft.target_channel}
              onChange={(v) => updateField('target_channel', v)}
              placeholder={t('admin.detail.targetChannelHint', { defaultValue: '留空使用派生值' })}
            />
            {suggestedChannel && (
              <button
                type="button"
                className="mt-1 text-xs px-2 py-1 rounded bg-warning/15 border border-warning/30 text-warning hover:bg-warning/25 transition-colors"
                onClick={() => updateField('target_channel', suggestedChannel)}
              >
                {t('admin.detail.suggestedChannel', { defaultValue: '建议' })}: {suggestedChannel}
              </button>
            )}
          </div>
          <FormField
            label={t('admin.detail.priceMin')}
            value={draft.price_min}
            onChange={(v) => updateField('price_min', v)}
            type="number"
            placeholder="0"
          />
          <FormField
            label={t('admin.detail.priceMax')}
            value={draft.price_max}
            onChange={(v) => updateField('price_max', v)}
            type="number"
            placeholder="0"
          />
          <FormField
            label={t('admin.detail.baseUrl')}
            value={draft.base_url}
            onChange={(v) => updateField('base_url', v)}
            type="url"
          />
        </div>

        {/* API Key section */}
        <div>
          <label className="block text-xs font-medium text-muted mb-1">
            {t('admin.detail.apiKey')}
          </label>
          <div className="flex items-center gap-2">
            <div className="flex-1 px-3 py-2 bg-elevated border border-default rounded-md
                            text-sm font-mono text-secondary overflow-hidden text-ellipsis">
              {showApiKey
                ? apiKey || t('admin.detail.apiKeyNotLoaded')
                : maskApiKey(submission.api_key_last4)}
            </div>
            <button
              onClick={() => setShowApiKey(!showApiKey)}
              className="px-3 py-2 text-xs rounded-md border
                         bg-accent/10 border-accent/40 text-accent
                         hover:bg-accent/20 transition-colors whitespace-nowrap"
            >
              {showApiKey ? t('admin.detail.hideKey') : t('admin.detail.showKey')}
            </button>
          </div>
          <p className="mt-1 text-xs text-muted">
            {t('admin.detail.apiKeyFingerprint')}: {submission.api_key_fingerprint}
          </p>
        </div>

        {/* Test info */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <ReadOnlyField
            label={t('admin.detail.testLatency')}
            value={submission.test_latency_ms ? `${submission.test_latency_ms}ms` : '--'}
          />
          <ReadOnlyField
            label={t('admin.detail.testHttpCode')}
            value={submission.test_http_code ? String(submission.test_http_code) : '--'}
          />
          <ReadOnlyField
            label={t('admin.detail.testPassedAt')}
            value={formatTimestamp(submission.test_passed_at)}
          />
          <ReadOnlyField
            label={t('admin.detail.locale')}
            value={submission.locale}
          />
        </div>

        {/* Admin note */}
        <FormField
          label={t('admin.detail.adminNote')}
          value={draft.admin_note}
          onChange={(v) => updateField('admin_note', v)}
          placeholder={t('admin.detail.adminNotePlaceholder')}
          multiline
        />

      </div>

      {/* Action buttons */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Save — only visible when there are unsaved changes */}
        {dirty && (
          <button
            onClick={handleSave}
            disabled={isSaving}
            className="px-4 py-2 text-sm font-medium rounded-md border
                       bg-accent/10 border-accent/40 text-accent
                       hover:bg-accent/20 transition-colors
                       disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {isSaving ? t('admin.detail.saving') : t('admin.detail.save')}
          </button>
        )}

        <button
          onClick={handleTest}
          disabled={isTesting}
          className="px-4 py-2 text-sm font-medium rounded-md border
                     border-default text-secondary
                     hover:bg-elevated transition-colors
                     disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {isTesting ? t('admin.detail.testing') : t('admin.detail.test')}
        </button>

        {canPublish && (
          <>
            <SelectField
              label="发布版块"
              value={publishBoard}
              onChange={setPublishBoard}
              options={[
                { value: 'hot', label: '主板' },
                { value: 'secondary', label: '备板' },
                { value: 'cold', label: '冷板' },
              ]}
            />
            <button
              onClick={() => onPublish(publishBoard)}
              className="px-4 py-2 text-sm font-medium rounded-md border
                         bg-success/10 border-success/30 text-success
                         hover:bg-success/20 transition-colors"
            >
              {t('admin.detail.publish')}
            </button>
          </>
        )}

        {canReject && (
          !showRejectInput ? (
            <button
              onClick={() => setShowRejectInput(true)}
              className="px-4 py-2 text-sm font-medium rounded-md border
                         bg-danger/10 border-danger/30 text-danger
                         hover:bg-danger/20 transition-colors"
            >
              {t('admin.detail.reject')}
            </button>
          ) : (
            <div className="flex items-center gap-2 flex-1 min-w-[280px]">
              <input
                type="text"
                value={rejectNote}
                onChange={(e) => setRejectNote(e.target.value)}
                placeholder={t('admin.detail.rejectNotePlaceholder')}
                className="flex-1 px-3 py-2 bg-elevated border border-default rounded-md
                           text-primary placeholder:text-muted text-sm
                           focus:outline-none focus:border-danger focus:ring-1 focus:ring-accent
                           transition-colors"
                autoFocus
              />
              <button
                onClick={handleRejectConfirm}
                className="px-3 py-2 text-sm font-medium rounded-md border
                           bg-danger/10 border-danger/30 text-danger
                           hover:bg-danger/20 transition-colors"
              >
                {t('admin.detail.confirmReject')}
              </button>
              <button
                onClick={() => {
                  setShowRejectInput(false);
                  setRejectNote('');
                }}
                className="px-3 py-2 text-sm rounded-md border
                           border-default text-muted hover:text-secondary
                           hover:bg-elevated transition-colors"
              >
                {t('admin.detail.cancel')}
              </button>
            </div>
          )
        )}

        {canDelete && (
          !showDeleteConfirm ? (
            <button
              onClick={() => setShowDeleteConfirm(true)}
              className="px-4 py-2 text-sm font-medium rounded-md border
                         border-default text-muted
                         hover:bg-danger/10 hover:text-danger hover:border-danger/30
                         transition-colors"
            >
              {t('admin.detail.delete')}
            </button>
          ) : (
            <div className="flex items-center gap-2">
              <button
                onClick={() => { onDelete(); setShowDeleteConfirm(false); }}
                className="px-3 py-2 text-sm font-medium rounded-md border
                           bg-danger/10 border-danger/30 text-danger
                           hover:bg-danger/20 transition-colors"
              >
                {t('admin.detail.confirmDelete')}
              </button>
              <button
                onClick={() => setShowDeleteConfirm(false)}
                className="px-3 py-2 text-sm rounded-md border
                           border-default text-muted hover:text-secondary
                           hover:bg-elevated transition-colors"
              >
                {t('admin.detail.cancel')}
              </button>
            </div>
          )
        )}
      </div>

      {/* Test result */}
      {testResult && (
        <div className="bg-surface border border-default rounded-lg p-4 space-y-2">
          <h3 className="text-sm font-medium text-primary">{t('admin.detail.testResult')}</h3>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm">
            <div>
              <span className="text-muted">{t('admin.detail.testHttpCode')}: </span>
              <span className="text-primary font-mono">{testResult.http_code ?? '--'}</span>
            </div>
            <div>
              <span className="text-muted">{t('admin.detail.testLatency')}: </span>
              <span className="text-primary font-mono">{testResult.latency ? `${testResult.latency}ms` : '--'}</span>
            </div>
            <div>
              <span className="text-muted">{t('admin.detail.status')}: </span>
              <span className={`font-medium ${
                testResult.probe_status === 1 ? 'text-success' :
                testResult.probe_status === 2 ? 'text-warning' : 'text-danger'
              }`}>
                {testResult.probe_status === 1 ? t('status.available', { defaultValue: '可用' }) :
                 testResult.probe_status === 2 ? t('status.degraded', { defaultValue: '降级' }) :
                 t('status.unavailable', { defaultValue: '不可用' })}
              </span>
            </div>
            {testResult.sub_status && testResult.sub_status !== 'none' && (
              <div>
                <span className="text-muted">{t('admin.detail.subStatus')}: </span>
                <span className="text-secondary">{testResult.sub_status}</span>
              </div>
            )}
          </div>
          {testResult.error_message && (
            <p className="text-xs text-danger">{testResult.error_message}</p>
          )}
        </div>
      )}
    </div>
  );
};
