import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/dashboard/static',
    emptyOutDir: true,
  },
  base: '/',
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
