import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'

export default defineConfig(({ mode }) => ({
  plugins: [
    vue(),
    VitePWA({
      registerType: 'prompt', // 用户确认后更新
      injectRegister: 'auto',
      manifest: {
        name: 'Cinema115',
        short_name: 'Cinema115',
        description: '影视资源与媒体自动化平台',
        theme_color: '#0f172a',
        background_color: '#0f172a',
        display: 'standalone',
        icons: [
          {
            src: '/pwa-icon.svg',
            sizes: '192x192 512x512',
            type: 'image/svg+xml',
            purpose: 'any maskable'
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
