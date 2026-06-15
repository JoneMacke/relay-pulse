/// <reference types="vitest" />
import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')

  return {
    server: {
      port: 5173,
      strictPort: true,
      // BACKEND_PORT 让本地起后端在非 8080 端口时也能复用 vite dev 代理
      // （`PORT=8082 ./monitor` + `BACKEND_PORT=8082 npm run dev`）。
      // 不设时回落到与生产嵌入构建一致的 8080。
      proxy: (() => {
        const target = `http://localhost:${env.BACKEND_PORT || '8080'}`
        return {
          '/api': target,
          '/health': target,
          '/ready': target,
          '/sitemap.xml': target,
          '/robots.txt': target,
        }
      })(),
    },
    plugins: [
      react(),
      {
        name: 'html-transform',
        transformIndexHtml(html) {
          return html.replace(
            '%VITE_GA_MEASUREMENT_ID%',
            env.VITE_GA_MEASUREMENT_ID || ''
          )
        },
      },
    ],

    // 构建优化配置
    build: {
      // CSS 代码分割
      cssCodeSplit: true,

      // vite 8（rolldown）不再内置 esbuild，转用 oxc 做转译/压缩；旧的
      // minify: 'esbuild' 会触发 vite:esbuild-transpile 插件去加载未安装的
      // esbuild 而构建失败。改用 vite 8 默认的 'oxc'（同样快、零额外依赖）。
      minify: 'oxc',

      // 调整 chunk 大小警告阈值
      chunkSizeWarningLimit: 500,

      // Rollup 构建选项
      rollupOptions: {
        output: {
          // 手动代码分块策略
          // vite 8（rolldown）移除了 manualChunks 的对象形式（构建期直接
          // "manualChunks is not a function" TypeError），仅保留函数形式。改按
          // node_modules 包路径分组，保持原有 5 个 vendor 分块语义不变。匹配锚定
          // `node_modules/<pkg>/` 的末尾斜杠，避免 react 误吞同前缀的 react-router /
          // react-helmet-async / lucide-react（react-window 未分组，交由自动分块）。
          manualChunks(id) {
            if (!id.includes('node_modules')) return
            // React 核心库（scheduler 是 react-dom 的内部依赖，一并归入）
            if (/node_modules\/(react|react-dom|scheduler)\//.test(id)) return 'react-vendor'
            // 路由库（react-router-dom → react-router → @remix-run/router）
            if (/node_modules\/(react-router|react-router-dom|@remix-run\/router)\//.test(id)) return 'router'
            // 国际化库
            if (/node_modules\/(i18next|react-i18next|i18next-browser-languagedetector)\//.test(id)) return 'i18n'
            // UI 图标库
            if (/node_modules\/lucide-react\//.test(id)) return 'icons'
            // Helmet（SEO）
            if (/node_modules\/react-helmet-async\//.test(id)) return 'helmet'
          },

          // 自定义 chunk 文件名
          chunkFileNames: 'assets/[name]-[hash].js',
          entryFileNames: 'assets/[name]-[hash].js',
          assetFileNames: 'assets/[name]-[hash].[ext]',
        },
      },
    },

    // Vite 的开发服务器默认支持 SPA 路由回退

    // Vitest 测试配置
    test: {
      globals: true,
      environment: 'node', // 纯函数测试不需要 DOM
    },
  }
})
