import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  server: {
    // 必须绑定 IPv4 回环地址：Wails 应用通过 127.0.0.1 代理请求 dev server，
    // vite 默认只监听 ::1（IPv6）会导致 502
    host: '127.0.0.1',
    port: Number(process.env.WAILS_VITE_PORT) || 34115,
    strictPort: true
  },
  build: {
    outDir: 'dist'
  }
})
