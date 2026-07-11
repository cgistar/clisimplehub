import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

const naiveUiVendorPackages = [
  '@css-render',
  'async-validator',
  'css-render',
  'date-fns',
  'date-fns-tz',
  'evtd',
  'highlight.js',
  'lodash',
  'lodash-es',
  'seemly',
  'treemate',
  'vdirs',
  'vooks',
  'vueuc'
]

function includesNodeModule(id, packageName) {
  return id.includes(`/node_modules/${packageName}/`)
}

function manualChunks(id) {
  if (!id.includes('/node_modules/')) return undefined
  if (includesNodeModule(id, 'naive-ui')) return 'naive-ui'
  if (naiveUiVendorPackages.some((name) => includesNodeModule(id, name))) return 'naive-ui-vendor'
  if (includesNodeModule(id, 'lucide-vue-next')) return 'icons'
  if (
    includesNodeModule(id, 'vue') ||
    includesNodeModule(id, '@vue') ||
    includesNodeModule(id, '@intlify') ||
    includesNodeModule(id, 'pinia') ||
    includesNodeModule(id, 'vue-demi') ||
    includesNodeModule(id, 'vue-i18n')
  ) {
    return 'vue-vendor'
  }
  return 'vendor'
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    port: 5173,
    strictPort: true,
    host: 'localhost',
    hmr: {
      protocol: 'ws',
      host: 'localhost',
      port: 5173,
      clientPort: 5173,
    },
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    sourcemap: false,
    minify: 'esbuild',
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        manualChunks
      }
    }
  }
})
