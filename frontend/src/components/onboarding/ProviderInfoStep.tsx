import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronRight } from 'lucide-react';
import type { OnboardingFormData, OnboardingMeta, ChannelSourceOption } from '../../types/onboarding';
import { inputClass, selectClass, labelClass, hintClass, primaryButtonClass } from './controls';
import { isProviderNameValid, isChannelNameValid, normalizeDisplayName, CHANNEL_NAME_MAX } from '../../utils/displayName';

interface ProviderInfoStepProps {
  formData: OnboardingFormData;
  updateField: <K extends keyof OnboardingFormData>(key: K, value: OnboardingFormData[K]) => void;
  meta: OnboardingMeta | null;
  onNext: () => void;
}

/** 通道分组代号：1-8 位小写字母或数字。 */
const GROUP_PATTERN = /^[a-z0-9]{1,8}$/;
/** Step 1: Provider information and channel configuration. */
export function ProviderInfoStep({ formData, updateField, meta, onNext }: ProviderInfoStepProps) {
  const { t } = useTranslation();

  // 当前服务的可选来源：先按 service 取词表，再按已选通道类型(O/R/M)过滤 category，
  // 保证「官方通道」不会列出逆向来源等不自洽组合（规则与后端 validateChannelTypeSource 同源）
  const sources: ChannelSourceOption[] = useMemo(() => {
    const all = meta?.channel_sources_by_service?.[formData.serviceType] ?? [];
    const allowed = meta?.channel_type_allowed_categories?.[formData.channelType];
    if (!allowed) return all;
    return all.filter((opt) => allowed.includes(opt.category));
  }, [meta, formData.serviceType, formData.channelType]);

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

  // 来源受 service + 通道类型双重约束；切换任一维度后，若已选来源不再出现在过滤结果中则清空。
  // 直接以 sources（已含双重过滤）为准，能保留仍合法的草稿来源、只清掉真正失配的。
  // meta 未加载时 sources 恒为空，须跳过，否则会误清掉草稿里本应合法的来源（meta 到达后无法恢复）。
  useEffect(() => {
    if (!meta || !formData.channelSource) return;
    if (!sources.some((opt) => opt.value === formData.channelSource)) {
      updateField('channelSource', '');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [meta, sources]);

  // 服务商名校验仅在失焦后暴露错误，避免输入中途逐键闪红（inline-validation：validate on blur）。
  const [providerNameTouched, setProviderNameTouched] = useState(false);
  const providerNameNormalized = normalizeDisplayName(formData.providerName);
  const providerNameValid = isProviderNameValid(formData.providerName);
  const providerNameError = providerNameTouched && providerNameNormalized.length > 0 && !providerNameValid;

  const groupValid = formData.channelGroup === '' || GROUP_PATTERN.test(formData.channelGroup);

  // 通道显示名称可选；校验剪除首尾空白后的值（粘贴带入的尾部换行不误判）
  const channelNameValid = isChannelNameValid(formData.channelName);

  // 通道标识预览：显示层大写（type-source），分组原样；存储层由后端统一小写
  const channelCode = useMemo(() => {
    if (!formData.channelType || !formData.channelSource) return '';
    const group = formData.channelGroup.trim() || (meta?.channel_group_rule?.default ?? 'main');
    return `${formData.channelType.toUpperCase()}-${formData.channelSource.toUpperCase()}-${group}`;
  }, [formData.channelType, formData.channelSource, formData.channelGroup, meta]);

  const canProceed = useMemo(() => {
    return (
      providerNameValid &&
      formData.websiteUrl.trim().length > 0 &&
      formData.serviceType.length > 0 &&
      formData.channelType.length > 0 &&
      formData.channelSource.length > 0 &&
      channelNameValid &&
      groupValid
    );
  }, [
    providerNameValid, formData.websiteUrl,
    formData.serviceType, formData.channelType, formData.channelSource, channelNameValid, groupValid,
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

      {/* Provider name */}
      <div>
        <label htmlFor="ob-provider-name" className={labelClass}>
          {t('onboarding.providerInfo.providerName')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <input
          id="ob-provider-name"
          type="text"
          required
          value={formData.providerName}
          onChange={(e) => updateField('providerName', e.target.value)}
          onBlur={() => setProviderNameTouched(true)}
          aria-invalid={providerNameError || undefined}
          aria-describedby="ob-provider-name-hint"
          placeholder={t('onboarding.providerInfo.providerNamePlaceholder')}
          className={inputClass(providerNameError)}
        />
        <p
          id="ob-provider-name-hint"
          className={`mt-1 text-xs ${providerNameError ? 'text-danger' : 'text-secondary'}`}
          role={providerNameError ? 'alert' : undefined}
        >
          {t('onboarding.providerInfo.providerNameHint', {
            defaultValue: '服务商展示名称，支持中文等任意语言（最长 100 字符）；若含非英文字符，用于网址的英文代号将由我们分配。',
          })}
        </p>
      </div>

      {/* Website URL */}
      <div>
        <label htmlFor="ob-website-url" className={labelClass}>
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
          className={inputClass()}
        />
      </div>

      {/* Service type - select */}
      <div>
        <label htmlFor="ob-service-type" className={labelClass}>
          {t('onboarding.providerInfo.serviceType')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <select
          id="ob-service-type"
          value={formData.serviceType}
          onChange={(e) => updateField('serviceType', e.target.value)}
          className={selectClass}
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
        <legend className={labelClass}>
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
        <label htmlFor="ob-channel-source" className={labelClass}>
          {t('onboarding.providerInfo.channelSource')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <select
          id="ob-channel-source"
          value={formData.channelSource}
          onChange={(e) => updateField('channelSource', e.target.value)}
          className={selectClass}
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
        <p className={hintClass}>
          {t('onboarding.providerInfo.channelSourceHint', { defaultValue: '没有合适的来源？请联系运营补充（QQ:18058344）' })}
        </p>
      </div>

      {/* Channel display name — free-form Unicode label, independent from the PSC identifier */}
      <div>
        <label htmlFor="ob-channel-name" className={labelClass}>
          {t('onboarding.providerInfo.channelName', { defaultValue: '通道显示名称' })}
          <span className="ml-1 text-xs font-normal text-secondary">
            {t('onboarding.providerInfo.optional', { defaultValue: '（可选）' })}
          </span>
        </label>
        <input
          id="ob-channel-name"
          type="text"
          value={formData.channelName}
          onChange={(e) => updateField('channelName', e.target.value)}
          placeholder={t('onboarding.providerInfo.channelNamePlaceholder', { defaultValue: '例如：Max 高速线路' })}
          aria-invalid={!channelNameValid || undefined}
          aria-describedby="ob-channel-name-hint"
          className={inputClass(!channelNameValid)}
        />
        <p
          id="ob-channel-name-hint"
          className={`mt-1 text-xs ${channelNameValid ? 'text-secondary' : 'text-danger'}`}
          role={channelNameValid ? undefined : 'alert'}
        >
          {channelNameValid
            ? t('onboarding.providerInfo.channelNameHint', {
                defaultValue: '显示在状态页通道列，可用中文；留空则显示通道标识；最多 {{max}} 个字符',
                max: CHANNEL_NAME_MAX,
              })
            : t('onboarding.providerInfo.channelNameInvalid', {
                defaultValue: '最多 {{max}} 个字符，且不能包含控制字符或零宽字符',
                max: CHANNEL_NAME_MAX,
              })}
        </p>
      </div>

      {/* Channel group code — relay's own grouping code, feeds the channel identifier */}
      <div>
        <label htmlFor="ob-channel-group" className={labelClass}>
          {t('onboarding.providerInfo.channelGroup', { defaultValue: '通道分组代号' })}
        </label>
        <input
          id="ob-channel-group"
          type="text"
          value={formData.channelGroup}
          onChange={(e) => updateField('channelGroup', e.target.value.toLowerCase().replace(/[^a-z0-9]/g, ''))}
          placeholder={meta.channel_group_rule?.default ?? 'main'}
          maxLength={groupMaxLength}
          className={inputClass()}
        />
        <p className={hintClass}>
          {t('onboarding.providerInfo.channelGroupHint', {
            defaultValue: '1-8 位小写字母或数字，仅用于生成通道标识（如 us、eu、v2），不作为展示名称；留空默认 main',
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
          className={primaryButtonClass}
        >
          {t('onboarding.next')}
          <ChevronRight className="w-4 h-4" />
        </button>
      </div>
    </form>
  );
}
