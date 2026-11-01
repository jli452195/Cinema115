import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'

export default defineConfig(({ mode }) => ({
  resolve: {
    dedupe: ['@codemirror/state', '@codemirror/view', '@codemirror/language', '@lezer/highlight'],
  },
  optimizeDeps: {
    // Keep the lazy YAML editor's extensions in the same prebundle from startup.
    include: ['@codemirror/state', '@codemirror/view', '@codemirror/commands', '@codemirror/language', '@codemirror/lang-yaml', '@codemirror/theme-one-dark', '@lezer/highlight'],
  },
  plugins: [
    vue(),
    {
      name: 'cinema115-build-info',
      transformIndexHtml: () => Object.entries({
        version: process.env.APP_VERSION || 'dev',
        revision: process.env.APP_REVISION || '',
        created: process.env.APP_CREATED || '',
      }).map(([key, content]) => ({
        tag: 'meta',
        attrs: { name: `cinema115-${key}`, content },
        injectTo: 'head' as const,
      })),
    },
    VitePWA({
      registerType: 'prompt', // 用户确认后更新
      injectRegister: 'auto',
      manifest: {
        name: 'Cinema115',
        short_name: 'Cinema115',
        description: '影视资源与媒体自动化平台',
        // Static installation/splash fallback; document theme-color follows the active theme.
        theme_color: '#f8fafc',
        background_color: '#f8fafc',
        display: 'standalone',
        icons: [
          {
            src: '/brand/cinema115-mark.svg',
            sizes: 'any',
            type: 'image/svg+xml',
            purpose: 'any',
          },
          {
            src: '/pwa-icon-192.png',
            sizes: '192x192',
            type: 'image/png',
            purpose: 'any',
          },
          {
            src: '/pwa-icon-512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'any maskable',
          }
        ]
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,ico,png,svg,woff,woff2,ttf}'],
        navigateFallbackDenylist: [/^\/api/],
        runtimeCaching: [
          {
            urlPattern: /^\/api\//,
            handler: 'NetworkOnly',
          }
        ]
      },
      devOptions: {
        enabled: true,
        type: 'module',
      },
    })
  ],
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: `http://127.0.0.1:${loadEnv(mode, '..', 'HTTP_').HTTP_PORT || '3002'}`,
        changeOrigin: true,
      },
    },
  },
}))
