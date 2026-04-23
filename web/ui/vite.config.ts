import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'node:path'

const webProxyTarget = process.env.VITE_WEB_PROXY_TARGET || 'http://localhost:5600'

export default defineConfig({
  base: '/web/',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: resolve(__dirname, '../../internal/proxy/webui/dist'),
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/web/api': {
        target: webProxyTarget,
        changeOrigin: true,
      },
      '/web/sse': {
        target: webProxyTarget,
        changeOrigin: true,
      },
    },
  },
})
