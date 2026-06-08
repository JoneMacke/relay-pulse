import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronRight, ExternalLink } from 'lucide-react';
import type { OnboardingFormData, OnboardingMeta, ChannelSourceOption } from '../../types/onboarding';

interface ProviderInfoStepProps {
  formData: OnboardingFormData;
  updateField: <K extends keyof OnboardingFormData>(key: K, value: OnboardingFormData[K]) => void;
  meta: OnboardingMeta | null;
  onNext: () => void;
}

/** 服务商名称仅允许 ASCII 可打印字符（与后端 providerNamePattern 一致）。 */
const ASCII_PRINTABLE = /^[\x20-\x7E]+$/;
/** 通道分组：1-8 位小写字母或数字。 */
const GROUP_PATTERN = /^[a-z0-9]{1,8}$/;

/** Step 1: Provider information and channel configuration. */
export function ProviderInfoStep({ formData, updateField, meta, onNext }: ProviderInfoStepProps) {
  const { t } = useTranslation();

  // 当前服务的可选来源（受控词表，按 service 下发）
  const sources: ChannelSourceOption[] = useMemo(() => {
    return meta?.channel_sources_by_service?.[formData.serviceType] ?? [];
  }, [meta, formData.serviceType]);

  // 按 category 分组以便 optgroup 展示，保留词表原始顺序
  const sourceGroups = useMemo(() => {
    const groups: { category: string; options: ChannelSourceOption[] }[] = [];
    for (const opt of sources) {
      let g = groups.find((x) => x.category === opt.category);
      if (!g) {
        g = { category: opt.category, options: [] };
        groups.push(g);
      }
      g.options.push(opt);
    }
    return groups;
  }, [sources]);

  const groupMaxLength = meta?.channel_group_rule?.max_length ?? 8;

  // 切换 serviceType 时清空已选来源（不同服务的来源词表不通用）
  useEffect(() => {
    updateField('channelSource', '');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formData.serviceType]);

  const providerName = formData.providerName.trim();
  const providerNameValid = providerName.length > 0 && ASCII_PRINTABLE.test(providerName);
  const providerNameError = providerName.length > 0 && !providerNameValid;

  const groupValid = formData.channelGroup === '' || GROUP_PATTERN.test(formData.channelGroup);

  // 通道标识预览：显示层大写（type-source），分组原样；存储层由后端统一小写
  const channelCode = useMemo(() => {
    if (!formData.channelType || !formData.channelSource) return '';
    const group = formData.channelGroup.trim() || (meta?.channel_group_rule?.default ?? 'main');
    return `${formData.channelType.toUpperCase()}-${formData.channelSource.toUpperCase()}-${group}`;
  }, [formData.channelType, formData.channelSource, formData.channelGroup, meta]);

  const canProceed = useMemo(() => {
    return (
      formData.agreementAccepted &&
      providerNameValid &&
      formData.websiteUrl.trim().length > 0 &&
      formData.serviceType.length > 0 &&
      formData.channelType.length > 0 &&
      formData.channelSource.length > 0 &&
      groupValid
    );
  }, [
    formData.agreementAccepted, providerNameValid, formData.websiteUrl,
    formData.serviceType, formData.channelType, formData.channelSource, groupValid,
  ]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (canProceed) onNext();
  };

  if (!meta) {
    return (
      <div className="bg-surface border border-muted rounded-lg p-8 text-center">
        <p className="text-secondary">{t('onboarding.loading')}</p>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="bg-surface border border-muted rounded-lg p-6 space-y-6">
      <h2 className="text-xl font-semibold text-primary">
        {t('onboarding.providerInfo.title')}
      </h2>

      {/* Agreement checkbox */}
      <div className="p-4 bg-elevated rounded-lg space-y-3">
        <label className="flex items-start gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={formData.agreementAccepted}
            onChange={(e) => updateField('agreementAccepted', e.target.checked)}
            className="mt-1 w-4 h-4 rounded border-muted accent-accent"
          />
          <span className="text-sm text-secondary leading-relaxed">
            {t('onboarding.providerInfo.agreementText')}{' '}
            <a
              href="https://github.com/prehisle/relay-pulse/blob/main/docs/user/sponsorship.md"
              target="_blank"
              rel="noopener noreferrer"
              className="text-accent hover:text-accent-strong underline inline-flex items-center gap-0.5"
            >
              {t('onboarding.providerInfo.agreementLink')}
              <ExternalLink className="w-3 h-3" />
            </a>
          </span>
        </label>
      </div>

      {/* Provider name */}
      <div>
        <label htmlFor="ob-provider-name" className="block text-sm font-medium text-primary mb-2">
          {t('onboarding.providerInfo.providerName')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <input
          id="ob-provider-name"
          type="text"
          required
          value={formData.providerName}
          onChange={(e) => updateField('providerName', e.target.value)}
          placeholder={t('onboarding.providerInfo.providerNamePlaceholder')}
          className={`w-full px-4 py-2 bg-surface border rounded-lg text-primary placeholder-muted focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50 ${
            providerNameError ? 'border-danger' : 'border-muted'
          }`}
        />
        <p className={`mt-1 text-xs ${providerNameError ? 'text-danger' : 'text-secondary'}`}>
          {t('onboarding.providerInfo.providerNameHint', { defaultValue: '仅支持英文/数字/符号，不能包含中文' })}
        </p>
      </div>

      {/* Website URL */}
      <div>
        <label htmlFor="ob-website-url" className="block text-sm font-medium text-primary mb-2">
          {t('onboarding.providerInfo.websiteUrl')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <input
          id="ob-website-url"
          type="url"
          required
          value={formData.websiteUrl}
          onChange={(e) => updateField('websiteUrl', e.target.value)}
          placeholder="https://example.com"
          className="w-full px-4 py-2 bg-surface border border-muted rounded-lg text-primary placeholder-muted focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
        />
      </div>

      {/* Service type - select */}
      <div>
        <label htmlFor="ob-service-type" className="block text-sm font-medium text-primary mb-2">
          {t('onboarding.providerInfo.serviceType')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <select
          id="ob-service-type"
          value={formData.serviceType}
          onChange={(e) => updateField('serviceType', e.target.value)}
          className="w-full px-4 py-2 bg-surface border border-muted rounded-lg text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
        >
          {meta.service_types.map((st) => (
            <option key={st} value={st}>
              {t(`onboarding.providerInfo.serviceTypes.${st}`, { defaultValue: st.toUpperCase() })}
            </option>
          ))}
        </select>
      </div>

      {/* Channel type - card radio group */}
      <fieldset>
        <legend className="block text-sm font-medium text-primary mb-2">
          {t('onboarding.providerInfo.channelType')}
          <span className="text-danger ml-0.5">*</span>
        </legend>
        <div className="space-y-2">
          {meta.channel_types.map((ct) => (
            <label
              key={ct.value}
              className="flex items-start gap-3 cursor-pointer p-3 rounded-lg border border-muted hover:border-accent/40 transition-colors has-[:checked]:border-accent has-[:checked]:bg-accent/5"
            >
              <input
                type="radio"
                name="channelType"
                value={ct.value}
                checked={formData.channelType === ct.value}
                onChange={() => updateField('channelType', ct.value)}
                className="mt-0.5 w-4 h-4 accent-accent"
              />
              <div>
                <span className="text-sm font-medium text-primary">
                  {t(`onboarding.providerInfo.channelTypes.${ct.value}`, { defaultValue: ct.label })}
                </span>
                <p className="text-xs text-secondary mt-0.5">
                  {t(`onboarding.providerInfo.channelTypes.${ct.value}Desc`, { defaultValue: '' })}
                </p>
              </div>
            </label>
          ))}
        </div>
      </fieldset>

      {/* Channel source — flat per-service controlled vocabulary, grouped by category */}
      <div>
        <label htmlFor="ob-channel-source" className="block text-sm font-medium text-primary mb-2">
          {t('onboarding.providerInfo.channelSource')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <select
          id="ob-channel-source"
          value={formData.channelSource}
          onChange={(e) => updateField('channelSource', e.target.value)}
          className="w-full px-4 py-2 bg-surface border border-muted rounded-lg text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
        >
          <option value="" disabled>
            {t('onboarding.providerInfo.channelSourcePlaceholder', { defaultValue: '请选择接入来源' })}
          </option>
          {sourceGroups.map((g) => (
            <optgroup
              key={g.category}
              label={t(`onboarding.providerInfo.sourceCategories.${g.category}`, { defaultValue: g.category })}
            >
              {g.options.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}（{opt.value}）
                </option>
              ))}
            </optgroup>
          ))}
        </select>
        <p className="mt-1 text-xs text-secondary">
          {t('onboarding.providerInfo.channelSourceHint', { defaultValue: '没有合适的来源？请联系运营补充（QQ:18058344）' })}
        </p>
      </div>

      {/* Channel group — relay's own grouping tag */}
      <div>
        <label htmlFor="ob-channel-group" className="block text-sm font-medium text-primary mb-2">
          {t('onboarding.providerInfo.channelGroup', { defaultValue: '通道分组' })}
        </label>
        <input
          id="ob-channel-group"
          type="text"
          value={formData.channelGroup}
          onChange={(e) => updateField('channelGroup', e.target.value.toLowerCase().replace(/[^a-z0-9]/g, ''))}
          placeholder={meta.channel_group_rule?.default ?? 'main'}
          maxLength={groupMaxLength}
          className="w-full px-4 py-2 bg-surface border border-muted rounded-lg text-primary placeholder-muted focus:outline-none focus:ring-2 focus:ring-accent"
        />
        <p className="mt-1 text-xs text-secondary">
          {t('onboarding.providerInfo.channelGroupHint', {
            defaultValue: '用于区分同一来源的多条通道（如 us、eu、v2），留空默认 main',
          })}
        </p>
      </div>

      {/* Channel code preview */}
      {channelCode && (
        <div className="flex items-center gap-3 p-3 bg-elevated rounded-lg">
          <span className="text-sm text-secondary">{t('onboarding.providerInfo.channelCodePreview')}</span>
          <code className="px-3 py-1 bg-accent/10 border border-accent/30 rounded text-accent font-mono font-bold text-lg">
            {channelCode}
          </code>
        </div>
      )}

      {/* Next button */}
      <div className="flex justify-end pt-2">
        <button
          type="submit"
          disabled={!canProceed}
          className="flex items-center gap-2 px-6 py-3 bg-accent text-white rounded-lg font-medium hover:bg-accent-strong transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t('onboarding.next')}
          <ChevronRight className="w-4 h-4" />
        </button>
      </div>
    </form>
  );
}
