import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => ({
  plugins: [vue()],
  server: {
    // 开发时允许局域网设备访问，并固定端口避免占用后自动换端口。
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
