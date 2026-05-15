import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// Dev server proxies /api and /sub to the Go panel so the SPA can run
// hot-reloaded on :5173 while talking to the real backend on :8788.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8788', changeOrigin: true },
      '/sub': { target: 'http://localhost:8788', changeOrigin: true },
    },
  },
  build: {
    // Build directly into the Go embed location so `go build` picks up
    // the latest assets without a copy step.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (id.includes('echarts')) return 'echarts'
          if (id.includes('element-plus') || id.includes('@element-plus')) return 'element-plus'
          if (id.includes('vue-router') || id.includes('pinia') || id.includes('vue')) return 'vue-vendor'
          if (id.includes('qrcode')) return 'qrcode'
          return 'vendor'
        },
      },
    },
  },
})
