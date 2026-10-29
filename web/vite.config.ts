import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  // 打包产物放 /static/，把 /assets/ 留给 Go 后端提供头像等静态资源
  build: {
    assetsDir: 'static',
  },
  server: {
    port: 5175,
    proxy: {
      '/api': 'http://127.0.0.1:18790',
      '/assets': 'http://127.0.0.1:18790',
      '/healthz': 'http://127.0.0.1:18790',
    },
  },
})
