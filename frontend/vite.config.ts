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

      // 使用 esbuild 压缩（更快，Vite 默认）
      minify: 'esbuild',

      // 调整 chunk 大小警告阈值
      chunkSizeWarningLimit: 500,

      // Rollup 构建选项
      rollupOptions: {
        output: {
          // 手动代码分块策略
          manualChunks: {
            // React 核心库
            'react-vendor': ['react', 'react-dom'],

            // 路由库
            'router': ['react-router-dom'],

            // 国际化库
            'i18n': ['i18next', 'react-i18next', 'i18next-browser-languagedetector'],

            // UI 图标库
            'icons': ['lucide-react'],

            // Helmet（SEO）
            'helmet': ['react-helmet-async'],
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
