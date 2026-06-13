import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Helmet } from 'react-helmet-async';
import { ClipboardList, RefreshCw, MessageCircle, Users, MessageSquare, Activity } from 'lucide-react';
import { LANGUAGE_PATH_MAP, type SupportedLanguage } from '../i18n';

function ContactCard({
  icon: Icon,
  title,
  description,
  onClick,
  external,
}: {
  icon: React.ElementType;
  title: string;
  description: string;
  onClick: () => void;
  external?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className="group flex flex-col items-start gap-4 p-6 rounded-2xl bg-surface border border-default hover:border-accent/40 transition-all duration-200 hover:shadow-lg hover:shadow-accent/5 text-left w-full"
    >
      <div className="flex items-center justify-center w-12 h-12 rounded-xl bg-accent/10 text-accent group-hover:bg-accent/20 transition">
        <Icon size={24} />
      </div>
      <div>
        <h3 className="text-lg font-semibold text-primary mb-1">{title}</h3>
        <p className="text-sm text-secondary leading-relaxed">{description}</p>
      </div>
      {external && (
        <span className="text-xs text-muted mt-auto">↗</span>
      )}
    </button>
  );
}

export default function ContactPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const langPrefix = LANGUAGE_PATH_MAP[i18n.language as SupportedLanguage];
  const homePath = langPrefix ? `/${langPrefix}` : '/';

  const buildPath = (path: string) =>
    langPrefix ? `/${langPrefix}${path}` : path;

  return (
    <>
      <Helmet>
        <title>{t('contact.meta.title')}</title>
        <meta name="description" content={t('contact.meta.description')} />
      </Helmet>

      <div className="min-h-screen bg-page flex flex-col">
        <header className="px-4 py-4 border-b border-default/50">
          <div className="max-w-4xl mx-auto flex items-center gap-3">
            <button
              onClick={() => navigate(homePath)}
              className="p-1.5 bg-accent/10 rounded-lg border border-accent/20 flex-shrink-0"
            >
              <Activity className="w-5 h-5 text-accent" />
            </button>
            <span className="text-lg font-bold text-gradient-hero">RelayPulse</span>
          </div>
        </header>

        <main className="flex-1 max-w-4xl mx-auto w-full px-4 py-12 sm:py-16">
          <div className="text-center mb-12">
            <h1 className="text-3xl sm:text-4xl font-bold text-primary mb-3">
              {t('contact.title')}
            </h1>
            <p className="text-secondary text-lg max-w-2xl mx-auto">
              {t('contact.description')}
            </p>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 sm:gap-6">
            <ContactCard
              icon={ClipboardList}
              title={t('contact.apply.title')}
              description={t('contact.apply.description')}
              onClick={() => navigate(buildPath('/contact/apply'))}
            />
            <ContactCard
              icon={RefreshCw}
              title={t('contact.change.title')}
              description={t('contact.change.description')}
              onClick={() => navigate(buildPath('/contact/change'))}
            />
            <ContactCard
              icon={MessageCircle}
              title={t('contact.feedback.title')}
              description={t('contact.feedback.description')}
              onClick={() => window.open('https://github.com/prehisle/relay-pulse/issues', '_blank', 'noopener,noreferrer')}
              external
            />
            <ContactCard
              icon={Users}
              title={t('contact.community.title')}
              description={t('contact.community.description')}
              onClick={() => window.open('https://qm.qq.com/q/oPN0J85hIs', '_blank', 'noopener,noreferrer')}
              external
            />
            <ContactCard
              icon={MessageSquare}
              title={t('contact.discussions.title')}
              description={t('contact.discussions.description')}
              onClick={() => window.open('https://github.com/prehisle/relay-pulse/discussions', '_blank', 'noopener,noreferrer')}
              external
            />
          </div>
        </main>
      </div>
    </>
  );
}
