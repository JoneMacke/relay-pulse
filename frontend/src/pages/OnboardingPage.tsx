import { Helmet } from 'react-helmet-async';
import { useTranslation } from 'react-i18next';
import { useOnboarding } from '../hooks/useOnboarding';
import { ProviderInfoStep } from '../components/onboarding/ProviderInfoStep';
import { ConnectionTestStep } from '../components/onboarding/ConnectionTestStep';
import { ConfirmStep } from '../components/onboarding/ConfirmStep';

export default function OnboardingPage() {
  const { t } = useTranslation();
  const {
    step, meta, metaError, formData, testResult, testProof,
    isTesting, isSubmitting, submitResult, error,
    updateField, goToStep, runTest, submit, reset,
  } = useOnboarding();

  return (
    <>
      <Helmet>
        <title>{t('onboarding.meta.title')} | RelayPulse</title>
        <meta name="description" content={t('onboarding.meta.description')} />
        <meta name="robots" content="noindex,nofollow" />
      </Helmet>

      <main className="min-h-screen bg-page py-8 px-4">
        <div className="max-w-2xl mx-auto space-y-6">
          {/* 页面标题 */}
          <header className="text-center space-y-3">
            <h1 className="text-3xl font-bold text-primary">{t('onboarding.title')}</h1>
            <p className="text-secondary">{t('onboarding.description')}</p>
          </header>

          {/* meta 加载失败提示 */}
          {metaError && (
            <div className="p-6 bg-surface border border-muted rounded-lg text-center space-y-3">
              <p className="text-danger font-medium">{metaError}</p>
              <p className="text-sm text-secondary">{t('onboarding.metaErrorHint')}</p>
            </div>
          )}

          {/* meta 加载中（无错误时） */}
          {!meta && !metaError && (
            <div className="p-6 bg-surface border border-muted rounded-lg text-center">
              <p className="text-secondary">{t('onboarding.loading')}</p>
            </div>
          )}

          {/* meta 加载成功后显示步骤 */}
          {meta && !metaError && (
            <>
              {/* 步骤指示器 */}
              <div className="flex items-center justify-center gap-2">
                {[1, 2, 3].map(s => (
                  <div key={s} className="flex items-center gap-2">
                    <div className={`w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold transition-colors ${
                      s === step
                        ? 'bg-accent text-white'
                        : s < step
                          ? 'bg-success/20 text-success'
                          : 'bg-muted text-muted'
                    }`}>
                      {s < step ? '✓' : s}
                    </div>
                    {s < 3 && (
                      <div className={`w-12 h-0.5 ${s < step ? 'bg-success/40' : 'bg-muted/30'}`} />
                    )}
                  </div>
                ))}
              </div>

              {/* 错误提示 */}
              {error && (
                <div className="p-4 bg-danger/10 border border-danger/20 rounded-lg">
                  <p className="text-danger font-medium">{error}</p>
                </div>
              )}

              {/* 步骤内容 */}
              {step === 1 && (
                <ProviderInfoStep
                  formData={formData}
                  updateField={updateField}
                  meta={meta}
                  onNext={() => goToStep(2)}
                />
              )}
              {step === 2 && (
                <ConnectionTestStep
                  formData={formData}
                  updateField={updateField}
                  meta={meta}
                  testResult={testResult}
                  testProof={testProof}
                  isTesting={isTesting}
                  onRunTest={runTest}
                  onBack={() => goToStep(1)}
                  onNext={() => goToStep(3)}
                />
              )}
              {step === 3 && (
                <ConfirmStep
                  formData={formData}
                  updateField={updateField}
                  submitResult={submitResult}
                  isSubmitting={isSubmitting}
                  onSubmit={submit}
                  onBack={() => goToStep(2)}
                  onReset={reset}
                />
              )}
            </>
          )}
        </div>
      </main>
    </>
  );
}
