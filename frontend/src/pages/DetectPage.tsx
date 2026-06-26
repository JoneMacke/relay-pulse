import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Helmet } from 'react-helmet-async';
import {
  Activity,
  ShieldQuestion,
  Fingerprint,
  ListChecks,
  HelpCircle,
  ExternalLink,
  ArrowRight,
} from 'lucide-react';

import { LANGUAGE_PATH_MAP, type SupportedLanguage } from '../i18n';
import { useRpdiagScores, lookupRpdiagScore } from '../hooks/useRpdiagScores';
import { useMonitorData } from '../hooks/useMonitorData';

/** 质量分色带：与 StatusTable.qualityScoreColor 同一套 hue 阶梯（红→翠绿）。
 *  StatusTable 内为私有函数，这里独立一份避免改动那个组件；阈值变更需两处同步。 */
function scoreColor(score: number): string {
  const stops: Array<[number, number, number, number]> = [
    [0, 0, 78, 50],
    [60, 40, 82, 50],
    [80, 75, 72, 48],
    [90, 105, 70, 46],
    [100, 140, 78, 44],
  ];
  const c = Math.max(0, Math.min(100, score));
  for (let i = 1; i < stops.length; i++) {
    if (c <= stops[i][0]) {
      const [s0, h0, sat0, l0] = stops[i - 1];
      const [s1, h1, sat1, l1] = stops[i];
      const t = s1 === s0 ? 0 : (c - s0) / (s1 - s0);
      return `hsl(${h0 + t * (h1 - h0)} ${sat0 + t * (sat1 - sat0)}% ${l0 + t * (l1 - l0)}%)`;
    }
  }
  return 'hsl(140 78% 44%)';
}

interface RankRow {
  id: string;
  providerName: string;
  providerSlug: string;
  channelLabel: string;
  serviceName: string;
  score: number;
  uptime: number;
  evidenceUrl?: string;
}

/** 实时质量排行：复用 useMonitorData（已按三元组 join rpdiag 质量分并带展示名/slug）
 *  + useRpdiagScores（取每行的 channel_url 作证据深链）。
 *
 *  关键的「正派」约束：纯按质量分降序，isInitialSort=false 关掉赞助置顶——质量排行不能
 *  让赞助商浮到真实分之上。展示名来自 relaypulse 自己的通道清单，不是 rpdiag map 的小写 key。 */
function QualityRanking() {
  const { t, i18n } = useTranslation();
  const langPrefix = LANGUAGE_PATH_MAP[i18n.language as SupportedLanguage];
  const providerHref = (slug: string) => (langPrefix ? `/${langPrefix}/p/${slug}` : `/p/${slug}`);
  const { scores, loaded } = useRpdiagScores();
  const { data, loading } = useMonitorData({
    timeRange: '90m',
    board: 'all',
    filterService: [],
    filterProvider: [],
    filterChannel: [],
    filterCategory: [],
    sortConfig: { key: 'qualityScore', direction: 'desc' },
    isInitialSort: false,
    rpdiagScores: scores,
    rpdiagScoresLoaded: loaded,
  });

  const rows = useMemo<RankRow[]>(() => {
    return data
      .filter((d) => typeof d.qualityScore === 'number')
      .map((d) => {
        const evidence = lookupRpdiagScore(
          scores,
          [d.providerName, d.providerId],
          d.serviceType,
          d.channelName || d.channel,
        );
        return {
          id: d.id,
          providerName: d.providerName,
          providerSlug: d.providerSlug,
          channelLabel: d.channelName || d.channel || '',
          serviceName: d.serviceName,
          score: d.qualityScore as number,
          uptime: d.uptime,
          evidenceUrl: evidence?.channel_url,
        };
      })
      .sort((a, b) => b.score - a.score);
  }, [data, scores]);

  if (loading && rows.length === 0) {
    return <p className="text-secondary text-sm py-8 text-center">{t('detect.ranking.loading')}</p>;
  }
  if (rows.length === 0) {
    return <p className="text-secondary text-sm py-8 text-center">{t('detect.ranking.empty')}</p>;
  }

  return (
    <div className="overflow-x-auto rounded-2xl border border-default bg-surface">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-muted border-b border-default/60">
            <th className="px-3 py-3 font-medium w-10">#</th>
            <th className="px-3 py-3 font-medium">{t('detect.ranking.col.provider')}</th>
            <th className="px-3 py-3 font-medium">{t('detect.ranking.col.channel')}</th>
            <th className="px-3 py-3 font-medium text-right">{t('detect.ranking.col.score')}</th>
            <th className="px-3 py-3 font-medium text-right whitespace-nowrap">{t('detect.ranking.col.uptime')}</th>
            <th className="px-3 py-3 font-medium text-right">{t('detect.ranking.col.evidence')}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.id} className="border-b border-default/30 hover:bg-elevated/40 transition-colors">
              <td className="px-3 py-2.5 text-muted font-mono">{i + 1}</td>
              <td className="px-3 py-2.5">
                {r.providerSlug ? (
                  <a
                    href={providerHref(r.providerSlug)}
                    className="text-primary font-medium hover:text-accent transition-colors"
                  >
                    {r.providerName}
                  </a>
                ) : (
                  <span className="text-primary font-medium">{r.providerName}</span>
                )}
                <span className="text-muted ml-2 text-xs">{r.serviceName}</span>
              </td>
              <td className="px-3 py-2.5 text-secondary">{r.channelLabel}</td>
              <td className="px-3 py-2.5 text-right">
                <span
                  className="inline-block min-w-[2.75rem] rounded-md px-2 py-0.5 font-mono font-bold text-[hsl(0_0%_100%)]"
                  style={{ backgroundColor: scoreColor(r.score) }}
                >
                  {r.score.toFixed(0)}
                </span>
              </td>
              <td className="px-3 py-2.5 text-right font-mono text-secondary">
                {r.uptime >= 0 ? `${r.uptime.toFixed(0)}%` : '—'}
              </td>
              <td className="px-3 py-2.5 text-right">
                {r.evidenceUrl ? (
                  <a
                    href={r.evidenceUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 text-accent hover:underline whitespace-nowrap"
                  >
                    {t('detect.ranking.col.evidenceLink')}
                    <ExternalLink size={12} />
                  </a>
                ) : (
                  <span className="text-muted">—</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Section({
  icon: Icon,
  title,
  children,
}: {
  icon: React.ElementType;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-14">
      <h2 className="flex items-center gap-2.5 text-2xl font-bold text-primary mb-5">
        <span className="flex items-center justify-center w-9 h-9 rounded-lg bg-accent/10 text-accent flex-shrink-0">
          <Icon size={20} />
        </span>
        {title}
      </h2>
      {children}
    </section>
  );
}

export default function DetectPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const langPrefix = LANGUAGE_PATH_MAP[i18n.language as SupportedLanguage];
  const homePath = langPrefix ? `/${langPrefix}` : '/';
  const buildPath = (path: string) => (langPrefix ? `/${langPrefix}${path}` : path);

  const problems = t('detect.what.items', { returnObjects: true }) as Array<{ t: string; d: string }>;
  const tactics = t('detect.tactics.items', { returnObjects: true }) as Array<{ name: string; how: string }>;
  const steps = t('detect.diy.steps', { returnObjects: true }) as string[];
  const faqs = t('detect.faq.items', { returnObjects: true }) as Array<{ q: string; a: string }>;

  return (
    <>
      <Helmet>
        <title>{t('detect.meta.title')}</title>
        <meta name="description" content={t('detect.meta.description')} />
      </Helmet>

      <div className="min-h-screen bg-page flex flex-col">
        <header className="px-4 py-4 border-b border-default/50">
          <div className="max-w-4xl mx-auto flex items-center gap-3">
            <button
              onClick={() => navigate(homePath)}
              className="p-1.5 bg-accent/10 rounded-lg border border-accent/20 flex-shrink-0"
              aria-label={t('detect.nav.home')}
            >
              <Activity className="w-5 h-5 text-accent" />
            </button>
            <span className="text-lg font-bold text-gradient-hero">RelayPulse</span>
          </div>
        </header>

        <main className="flex-1 max-w-4xl mx-auto w-full px-4 py-12 sm:py-16">
          {/* Hero */}
          <div className="mb-14">
            <h1 className="text-3xl sm:text-4xl font-bold text-primary mb-4 leading-tight">
              {t('detect.hero.h1')}
            </h1>
            <p className="text-secondary text-lg leading-relaxed max-w-3xl">{t('detect.hero.lead')}</p>
          </div>

          {/* ① 什么是中转站检测 */}
          <Section icon={ShieldQuestion} title={t('detect.what.title')}>
            <p className="text-secondary leading-relaxed mb-5">{t('detect.what.intro')}</p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {problems.map((p, i) => (
                <div key={i} className="rounded-xl border border-default bg-surface p-4">
                  <h3 className="text-primary font-semibold mb-1">{p.t}</h3>
                  <p className="text-secondary text-sm leading-relaxed">{p.d}</p>
                </div>
              ))}
            </div>
          </Section>

          {/* ② 两层信号 */}
          <Section icon={Fingerprint} title={t('detect.how.title')}>
            <p className="text-secondary leading-relaxed mb-5">{t('detect.how.intro')}</p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-4">
              <div className="rounded-xl border border-default bg-surface p-5">
                <h3 className="text-primary font-semibold mb-2 flex items-center gap-2">
                  <Activity size={16} className="text-accent" />
                  {t('detect.how.availability.t')}
                </h3>
                <p className="text-secondary text-sm leading-relaxed">{t('detect.how.availability.d')}</p>
              </div>
              <div className="rounded-xl border border-default bg-surface p-5">
                <h3 className="text-primary font-semibold mb-2 flex items-center gap-2">
                  <Fingerprint size={16} className="text-accent" />
                  {t('detect.how.quality.t')}
                </h3>
                <p className="text-secondary text-sm leading-relaxed">{t('detect.how.quality.d')}</p>
              </div>
            </div>
            <p className="text-sm text-muted leading-relaxed">{t('detect.how.note')}</p>
          </Section>

          {/* ③ 实时质量排行 */}
          <Section icon={ListChecks} title={t('detect.ranking.title')}>
            <p className="text-secondary leading-relaxed mb-2">{t('detect.ranking.intro')}</p>
            <p className="text-sm text-muted leading-relaxed mb-5">{t('detect.ranking.disclaimer')}</p>
            <QualityRanking />
          </Section>

          {/* ④ 常见掺水手法 */}
          <Section icon={ShieldQuestion} title={t('detect.tactics.title')}>
            <p className="text-secondary leading-relaxed mb-5">{t('detect.tactics.intro')}</p>
            <div className="space-y-3">
              {tactics.map((tac, i) => (
                <div key={i} className="rounded-xl border border-default bg-surface p-4">
                  <h3 className="text-primary font-semibold mb-1">{tac.name}</h3>
                  <p className="text-secondary text-sm leading-relaxed">{tac.how}</p>
                </div>
              ))}
            </div>
            <p className="text-sm text-muted leading-relaxed mt-4">{t('detect.tactics.evidenceNote')}</p>
          </Section>

          {/* ⑤ 怎么自己检测 */}
          <Section icon={ListChecks} title={t('detect.diy.title')}>
            <ol className="space-y-3 mb-6">
              {steps.map((s, i) => (
                <li key={i} className="flex items-start gap-3">
                  <span className="flex items-center justify-center w-6 h-6 rounded-full bg-accent/15 text-accent text-sm font-bold flex-shrink-0 mt-0.5">
                    {i + 1}
                  </span>
                  <span className="text-secondary leading-relaxed">{s}</span>
                </li>
              ))}
            </ol>
            <button
              onClick={() => navigate(buildPath('/contact/apply'))}
              className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl border border-accent/40 bg-accent/10 text-accent font-semibold hover:bg-accent/20 transition"
            >
              {t('detect.diy.cta')}
              <ArrowRight size={16} />
            </button>
            <p className="text-sm text-muted mt-2">{t('detect.diy.ctaDesc')}</p>
          </Section>

          {/* ⑥ FAQ */}
          <Section icon={HelpCircle} title={t('detect.faq.title')}>
            <div className="space-y-4">
              {faqs.map((f, i) => (
                <div key={i} className="rounded-xl border border-default bg-surface p-5">
                  <h3 className="text-primary font-semibold mb-2">{f.q}</h3>
                  <p className="text-secondary text-sm leading-relaxed">{f.a}</p>
                </div>
              ))}
            </div>
          </Section>
        </main>
      </div>
    </>
  );
}
