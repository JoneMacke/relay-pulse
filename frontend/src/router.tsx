import { lazy, Suspense } from 'react';
import { Routes, Route, Navigate, Outlet } from 'react-router-dom';
import { useSyncLanguage } from './hooks/useSyncLanguage';

// 路由级代码分割：懒加载页面组件
const App = lazy(() => import('./App'));
const ProviderPage = lazy(() => import('./pages/ProviderPage'));
const OnboardingPage = lazy(() => import('./pages/OnboardingPage'));
const ContactPage = lazy(() => import('./pages/ContactPage'));
const ChangeRequestPage = lazy(() => import('./pages/ChangeRequestPage'));
const AdminPage = lazy(() => import('./pages/AdminPage'));

/**
 * 语言布局组件
 *
 * 职责：
 * 1. 接收固定的语言前缀（如 'en'、'ru'、'ja'）
 * 2. 使用 useSyncLanguage Hook 同步语言状态
 * 3. 使用 Outlet 渲染匹配的子路由（App 或 ProviderPage）
 */
interface LanguageLayoutProps {
  /** 语言前缀（如 'en'、'ru'、'ja'），无前缀则为 undefined */
  lang?: string;
}

function LanguageLayout({ lang }: LanguageLayoutProps) {
  useSyncLanguage(lang);
  return <Outlet />;
}

/**
 * 路由级加载占位符
 * 使用与主题一致的背景色和三点跳动动画，避免视觉跳跃
 * 注意：使用 CSS 变量以支持主题切换
 */
function RouterFallback() {
  return (
    <div
      style={{
        minHeight: '100vh',
        backgroundColor: 'hsl(var(--bg-page, 222 47% 4%))',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '6px',
      }}
    >
      {[0, 0.15, 0.3].map((delay, i) => (
        <div
          key={i}
          style={{
            width: '10px',
            height: '10px',
            borderRadius: '50%',
            background: 'var(--gradient-button, linear-gradient(135deg, #06b6d4, #3b82f6))',
            animation: `bounce 0.6s ease-in-out ${delay}s infinite alternate`,
          }}
        />
      ))}
      <style>{`
        @keyframes bounce {
          from { transform: translateY(0); opacity: 0.4; }
          to { transform: translateY(-12px); opacity: 1; }
        }
      `}</style>
    </div>
  );
}

/**
 * 共享子路由定义
 *
 * 每个语言布局下的子路由相同，提取为函数避免重复。
 * 路由结构：
 * - /contact          → ContactPage（联系我们落地页）
 * - /contact/apply    → OnboardingPage（申请收录）
 * - /contact/change   → ChangeRequestPage（申请变更）
 * - /apply            → 重定向到 /contact/apply（向后兼容）
 */
function renderChildRoutes(langPrefix?: string) {
  const applyRedirect = langPrefix ? `/${langPrefix}/contact/apply` : '/contact/apply';
  return (
    <>
      <Route index element={<App />} />
      <Route path="p/:provider" element={<ProviderPage />} />
      <Route path="contact" element={<ContactPage />} />
      <Route path="contact/apply" element={<OnboardingPage />} />
      <Route path="contact/change" element={<ChangeRequestPage />} />
      <Route path="apply" element={<Navigate to={applyRedirect} replace />} />
      <Route path="admin" element={<AdminPage />} />
    </>
  );
}

/**
 * 应用路由配置
 *
 * 路由规则：
 * 1. 根路径 `/` 和子路由 → 默认语言（中文，由 i18n 检测器决定）
 * 2. 明确的语言前缀路径：
 *    - `/en/*` → 英文
 *    - `/ru/*` → 俄文
 *    - `/ja/*` → 日文
 * 3. 无效路径 → 重定向到根路径
 *
 * 嵌套路由结构：
 * - LanguageLayout 负责语言同步
 * - Outlet 渲染匹配的子路由
 *
 * 注意：
 * - 使用明确的路径前缀（/en、/ru、/ja）而非参数（:lang），避免与 /p/:provider 冲突
 * - `/api/*`、`/health` 等技术路径由后端处理，不会被前端路由拦截
 * - 所有内容页面自动获得 i18n 支持
 */
export default function AppRouter() {
  return (
    <Suspense fallback={<RouterFallback />}>
      <Routes>
        {/* 中文默认路径（无前缀） */}
        <Route element={<LanguageLayout />}>
          {renderChildRoutes()}
        </Route>

        {/* 英文路径 */}
        <Route path="en" element={<LanguageLayout lang="en" />}>
          {renderChildRoutes('en')}
        </Route>

        {/* 俄文路径 */}
        <Route path="ru" element={<LanguageLayout lang="ru" />}>
          {renderChildRoutes('ru')}
        </Route>

        {/* 日文路径 */}
        <Route path="ja" element={<LanguageLayout lang="ja" />}>
          {renderChildRoutes('ja')}
        </Route>

        {/* 捕获所有未匹配路径，重定向到根 */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Suspense>
  );
}
