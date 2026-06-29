import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, AlertCircle, ChevronDown, ChevronUp, Check, X, Play, Trash2, Save, ArrowRight } from 'lucide-react';
import type { AdminChangeRequest, ChangeRequestStatus } from '../../types/change';
import { fieldShapeClass } from './fieldStyles';

// ── 编辑相关常量与工具 ──────────────────────────────────

const EDITABLE_PROPOSED_FIELDS = [
  'provider_name',
  'provider_url',
  'channel_name',
  'category',
  'sponsor_level',
  'listed_since',
  'expires_at',
  'price_min',
  'price_max',
] as const;

const SPONSOR_LEVEL_OPTIONS = ['', 'public', 'signal', 'pulse', 'beacon', 'backbone', 'core'] as const;

type EditableProposedField = (typeof EDITABLE_PROPOSED_FIELDS)[number];

interface EditDraft {
  proposed: Record<EditableProposedField, string>;
  admin_note: string;
}

function parseJsonRecord(json: string | undefined): Record<string, string> {
  if (!json) return {};
  try {
    const parsed = JSON.parse(json) as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed).map(([k, v]) => [k, v == null ? '' : String(v)]),
    );
  } catch {
    return {};
  }
}

function buildInitialDraft(cr: AdminChangeRequest): EditDraft {
  const proposed = parseJsonRecord(cr.proposed_changes);
  const snapshot = parseJsonRecord(cr.current_snapshot);
  // 提议未涉及的字段回退到提交时的快照原值，避免管理员看到全空表单
  return {
    proposed: {
      provider_name: proposed.provider_name ?? snapshot.provider_name ?? '',
      provider_url:  proposed.provider_url  ?? snapshot.provider_url  ?? '',
      channel_name:  proposed.channel_name  ?? snapshot.channel_name  ?? '',
      category:      proposed.category      ?? snapshot.category      ?? '',
      sponsor_level: proposed.sponsor_level ?? snapshot.sponsor_level ?? '',
      listed_since:  proposed.listed_since  ?? snapshot.listed_since  ?? '',
      expires_at:    proposed.expires_at    ?? snapshot.expires_at    ?? '',
      price_min:     proposed.price_min     ?? snapshot.price_min     ?? '',
      price_max:     proposed.price_max     ?? snapshot.price_max     ?? '',
    },
    admin_note: cr.admin_note ?? '',
  };
}

function getChangedUpdates(original: EditDraft, draft: EditDraft): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const field of EDITABLE_PROPOSED_FIELDS) {
    if (draft.proposed[field] !== original.proposed[field]) {
      result[field] = draft.proposed[field];
    }
  }
  if (draft.admin_note !== original.admin_note) {
    result.admin_note = draft.admin_note;
  }
  return result;
}

interface ChangeRequestListProps {
  changes: AdminChangeRequest[];
  isLoading: boolean;
  statusFilter: ChangeRequestStatus | 'all';
  setStatusFilter: (f: ChangeRequestStatus | 'all') => void;
  onSelect: (id: string) => void;
  onUpdate: (id: string, updates: Record<string, unknown>) => void;
  onApprove: (id: string) => void;
  onReject: (id: string, note: string) => void;
  onApply: (id: string) => void;
  onDelete: (id: string) => void;
  pendingActions?: Record<string, string>;
  error: string | null;
  featureDisabled?: boolean;
}

const STATUS_FILTERS: (ChangeRequestStatus | 'all')[] = ['all', 'pending', 'approved', 'rejected', 'applied'];

export function ChangeRequestList({
  changes,
  isLoading,
  statusFilter,
  setStatusFilter,
  onUpdate,
  onApprove,
  onReject,
  onApply,
  onDelete,
  pendingActions,
  error,
  featureDisabled,
}: ChangeRequestListProps) {
  const { t } = useTranslation();
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [rejectNote, setRejectNote] = useState('');
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [editDrafts, setEditDrafts] = useState<Record<string, EditDraft>>({});

  // 字段键 → 本地化标签；未收录的键回退原始键名，避免漏译时出现空白
  const fieldLabel = (key: string) => t(`admin.changes.fields.${key}`, { defaultValue: key });

  const statusLabel = (status: string) => {
    const map: Record<string, string> = {
      pending: t('admin.changes.statusPending'),
      approved: t('admin.changes.statusApproved'),
      rejected: t('admin.changes.statusRejected'),
      applied: t('admin.changes.statusApplied'),
    };
    return map[status] || status;
  };

  const statusColor = (status: string) => {
    switch (status) {
      case 'pending': return 'text-warning';
      case 'approved': return 'text-accent';
      case 'rejected': return 'text-danger';
      case 'applied': return 'text-success';
      default: return 'text-muted';
    }
  };

  if (featureDisabled) {
    return (
      <div className="p-4 bg-muted/10 border border-default rounded-lg text-muted text-sm">
        {t('admin.changes.featureDisabled')}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Status filter */}
      <div className="flex gap-1 flex-wrap">
        {STATUS_FILTERS.map(f => (
          <button
            key={f}
            onClick={() => setStatusFilter(f)}
            className={`px-3 py-1.5 text-xs rounded-lg transition ${
              statusFilter === f
                ? 'bg-accent/20 text-accent font-medium'
                : 'bg-elevated text-muted hover:text-secondary'
            }`}
          >
            {f === 'all' ? t('admin.filter.all') : statusLabel(f)}
          </button>
        ))}
      </div>

      {error && (
        <div className="flex items-center gap-2 p-3 rounded-lg bg-danger/10 text-danger text-sm">
          <AlertCircle size={16} />
          <span>{error}</span>
        </div>
      )}

      {isLoading ? (
        <div className="flex items-center justify-center py-8 text-muted">
          <Loader2 size={20} className="animate-spin mr-2" />
          {t('admin.table.loading')}
        </div>
      ) : changes.length === 0 ? (
        <div className="text-center py-8 text-muted">{t('admin.changes.empty')}</div>
      ) : (
        <div className="space-y-2">
          {changes.map(cr => {
            const isExpanded = expandedId === cr.public_id;
            const proposedChanges = parseJsonRecord(cr.proposed_changes);
            const currentSnapshot = parseJsonRecord(cr.current_snapshot);
            const originalDraft = buildInitialDraft(cr);
            const draft = editDrafts[cr.public_id] ?? originalDraft;
            const changedUpdates = getChangedUpdates(originalDraft, draft);
            const hasEdits = Object.keys(changedUpdates).length > 0;
            const canEdit = cr.status !== 'applied';

            return (
              <div key={cr.public_id} className="rounded-xl border border-default bg-surface overflow-hidden">
                {/* Row header */}
                <button
                  onClick={() => {
                    if (!isExpanded) {
                      setEditDrafts(prev =>
                        prev[cr.public_id] ? prev : { ...prev, [cr.public_id]: buildInitialDraft(cr) }
                      );
                    }
                    setExpandedId(isExpanded ? null : cr.public_id);
                  }}
                  className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-elevated/50 transition"
                >
                  <code className="text-xs text-muted font-mono">{cr.public_id.slice(0, 8)}</code>
                  <span className="text-sm text-primary font-medium flex-1 truncate">{cr.target_key}</span>
                  <span className={`text-xs px-2 py-0.5 rounded-md bg-muted/20 ${statusColor(cr.status)}`}>
                    {statusLabel(cr.status)}
                  </span>
                  <span className="text-xs text-muted">
                    {cr.apply_mode === 'auto' ? t('admin.changes.modeAuto') : t('admin.changes.modeManual')}
                  </span>
                  <span className="text-xs text-muted">
                    {new Date(cr.created_at * 1000).toLocaleDateString()}
                  </span>
                  {isExpanded ? <ChevronUp size={14} className="text-muted" /> : <ChevronDown size={14} className="text-muted" />}
                </button>

                {/* Expanded detail */}
                {isExpanded && (
                  <div className="px-4 pb-4 border-t border-default/50 space-y-3">
                    {/* Proposed changes */}
                    <div className="mt-3">
                      <div className="text-xs font-medium text-muted mb-1">{t('admin.changes.proposedChanges')}</div>
                      <div className="space-y-1">
                        {Object.entries(proposedChanges).map(([k, v]) => (
                          <div key={k} className="flex gap-2 text-sm">
                            <span className="text-muted min-w-[100px]">{fieldLabel(k)}:</span>
                            <span className="text-primary font-medium">{v}</span>
                          </div>
                        ))}
                      </div>
                    </div>

                    {/* Admin edit section */}
                    <div className="mt-2 rounded-lg border border-default/60 bg-elevated/20 p-3 space-y-2">
                      <div className="text-xs font-medium text-muted">
                        {t('admin.changes.adminEdit', { defaultValue: '管理员修改' })}
                        {cr.status === 'applied' && (
                          <span className="ml-2 text-muted opacity-60">({t('admin.changes.readOnly', { defaultValue: '只读' })})</span>
                        )}
                      </div>

                      {/* admin_note */}
                      <div>
                        <div className="text-xs text-muted mb-1">{t('admin.changes.adminNote')}</div>
                        <textarea
                          value={draft.admin_note}
                          readOnly={!canEdit}
                          onChange={e => {
                            const value = e.target.value;
                            setEditDrafts(prev => ({
                              ...prev,
                              [cr.public_id]: { ...draft, admin_note: value },
                            }));
                          }}
                          rows={2}
                          className={`${fieldShapeClass({ dense: true, xs: true })} w-full resize-none read-only:opacity-60 read-only:cursor-not-allowed`}
                        />
                      </div>

                      {/* Editable proposed fields */}
                      <div className="space-y-1.5">
                        {/* 列头：字段 / 当前 → 改为，避免管理员分不清哪列是现值哪列是改后 */}
                        <div className="grid grid-cols-[110px_1fr_auto_1fr] gap-2 items-center text-[11px] font-medium uppercase tracking-wide text-muted/70">
                          <span>{t('admin.changes.colField')}</span>
                          <span>{t('admin.changes.colCurrent')}</span>
                          <span aria-hidden="true" />
                          <span>{t('admin.changes.colProposed')}</span>
                        </div>
                        {EDITABLE_PROPOSED_FIELDS.map(field => (
                          <div key={field} className="grid grid-cols-[110px_1fr_auto_1fr] gap-2 items-center">
                            <span className="text-xs text-muted truncate" title={field}>{fieldLabel(field)}</span>
                            <span className="text-xs text-secondary truncate" title={currentSnapshot[field] || ''}>{currentSnapshot[field] || '—'}</span>
                            <ArrowRight className="w-3 h-3 flex-shrink-0 text-muted/50" aria-hidden="true" />
                            {field === 'sponsor_level' ? (
                              <select
                                value={draft.proposed.sponsor_level}
                                disabled={!canEdit}
                                onChange={e => {
                                  const value = e.target.value;
                                  setEditDrafts(prev => ({
                                    ...prev,
                                    [cr.public_id]: { ...draft, proposed: { ...draft.proposed, sponsor_level: value } },
                                  }));
                                }}
                                className={`${fieldShapeClass({ dense: true, xs: true })} disabled:opacity-60 disabled:cursor-not-allowed`}
                              >
                                {SPONSOR_LEVEL_OPTIONS.map(opt => (
                                  <option key={opt || 'empty'} value={opt}>{opt || '(空)'}</option>
                                ))}
                              </select>
                            ) : (
                              <input
                                type="text"
                                value={draft.proposed[field]}
                                readOnly={!canEdit}
                                onChange={e => {
                                  const value = e.target.value;
                                  setEditDrafts(prev => ({
                                    ...prev,
                                    [cr.public_id]: { ...draft, proposed: { ...draft.proposed, [field]: value } },
                                  }));
                                }}
                                className={`${fieldShapeClass({ dense: true, xs: true })} read-only:opacity-60 read-only:cursor-not-allowed`}
                              />
                            )}
                          </div>
                        ))}
                      </div>

                      {hasEdits && canEdit && (
                        <div className="flex justify-end pt-1">
                          <button
                            onClick={() => onUpdate(cr.public_id, changedUpdates)}
                            className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-accent/10 text-accent hover:bg-accent/20 transition"
                          >
                            <Save size={11} />{t('common.save', { defaultValue: '保存' })}
                          </button>
                        </div>
                      )}
                    </div>

                    {/* New API Key indicator */}
                    {cr.new_key_last4 && (
                      <div className="text-sm">
                        <span className="text-muted">{t('admin.changes.newApiKey')}:</span>{' '}
                        <span className="text-primary">...{cr.new_key_last4}</span>
                      </div>
                    )}

                    {/* Test info */}
                    {cr.requires_test && (
                      <div className="space-y-1 text-xs text-muted">
                        {cr.test_type && (
                          <div>{t('admin.changes.testType', { defaultValue: '服务类型' })}: {cr.test_type}</div>
                        )}
                        {cr.test_variant && (
                          <div>{t('admin.changes.testVariant', { defaultValue: '请求模板' })}: {cr.test_variant}</div>
                        )}
                        {cr.test_passed_at && (
                          <div>
                            {t('admin.changes.testInfo')}: {cr.test_latency_ms}ms / HTTP {cr.test_http_code}
                          </div>
                        )}
                      </div>
                    )}

                    {/* Actions */}
                    <div className="space-y-2 pt-2">
                      {hasEdits && canEdit && (
                        <div className="flex items-center gap-1 text-xs text-warning" role="alert" aria-live="assertive">
                          <AlertCircle className="w-3 h-3 flex-shrink-0" />
                          有未保存修改，请先保存再审批
                        </div>
                      )}
                      <div className="flex gap-2 flex-wrap">
                      {cr.status === 'pending' && (
                        <>
                          <button
                            onClick={() => onApprove(cr.public_id)}
                            disabled={(hasEdits && canEdit) || !!pendingActions?.[cr.public_id]}
                            title={hasEdits && canEdit ? '请先保存修改' : undefined}
                            className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-accent/10 text-accent hover:bg-accent/20 transition disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            {pendingActions?.[cr.public_id] === 'approve'
                              ? <Loader2 size={12} className="animate-spin" />
                              : <Check size={12} />}
                            {t('admin.changes.approve')}
                          </button>
                          <div className="flex items-center gap-1">
                            <input
                              type="text"
                              value={rejectNote}
                              onChange={e => setRejectNote(e.target.value)}
                              placeholder={t('admin.changes.rejectNotePlaceholder')}
                              className="px-2 py-1 text-xs rounded-lg bg-elevated border border-default text-primary w-48"
                            />
                            <button
                              onClick={() => { onReject(cr.public_id, rejectNote); setRejectNote(''); }}
                              disabled={!!pendingActions?.[cr.public_id]}
                              className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-danger/10 text-danger hover:bg-danger/20 transition disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                              {pendingActions?.[cr.public_id] === 'reject'
                                ? <Loader2 size={12} className="animate-spin" />
                                : <X size={12} />}
                              {t('admin.changes.reject')}
                            </button>
                          </div>
                        </>
                      )}
                      {cr.status === 'approved' && cr.apply_mode === 'auto' && (
                        <button
                          onClick={() => onApply(cr.public_id)}
                          disabled={(hasEdits && canEdit) || !!pendingActions?.[cr.public_id]}
                          title={hasEdits && canEdit ? '请先保存修改' : undefined}
                          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-success/10 text-success hover:bg-success/20 transition disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                          {pendingActions?.[cr.public_id] === 'apply'
                            ? <Loader2 size={12} className="animate-spin" />
                            : <Play size={12} />}
                          {t('admin.changes.apply')}
                        </button>
                      )}
                      {confirmDeleteId === cr.public_id ? (
                        <div className="flex items-center gap-1">
                          <span className="text-xs text-danger">{t('admin.changes.confirmDelete')}</span>
                          <button
                            onClick={() => { onDelete(cr.public_id); setConfirmDeleteId(null); }}
                            className="px-2 py-1 text-xs rounded bg-danger text-white"
                          >
                            {t('admin.changes.delete')}
                          </button>
                          <button
                            onClick={() => setConfirmDeleteId(null)}
                            className="px-2 py-1 text-xs rounded border border-default text-muted"
                          >
                            {t('common.cancel')}
                          </button>
                        </div>
                      ) : (
                        <button
                          onClick={() => setConfirmDeleteId(cr.public_id)}
                          disabled={!!pendingActions?.[cr.public_id]}
                          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg text-muted hover:text-danger transition ml-auto disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                          {pendingActions?.[cr.public_id] === 'delete'
                            ? <Loader2 size={12} className="animate-spin" />
                            : <Trash2 size={12} />}
                          {t('admin.changes.delete')}
                        </button>
                      )}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
