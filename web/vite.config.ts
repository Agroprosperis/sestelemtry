import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://api:8080',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://api:8080',
        changeOrigin: true,
      },
      '/readyz': {
        target: 'http://api:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        // recharts and its d3-* dependencies weigh ~70 KB gzipped. The
        // function form of manualChunks is required by current Rollup
        // typings; an object form is rejected as a `ManualChunksFunction`
        // mismatch in this Vite/Rollup major. We isolate recharts and
        // every d3-* package it pulls in into one chunk so the dashboard
        // shell can paint before the chart bundle finishes parsing.
        manualChunks(id) {
          if (id.includes('node_modules/recharts/')) return 'recharts'
          if (/node_modules\/(d3|victory-vendor|internmap)\//.test(id)) return 'recharts'
          return undefined
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test-setup.ts',
  },
})
