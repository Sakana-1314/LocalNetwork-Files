import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const outDir = fileURLToPath(new URL('../server/internal/webui/dist', import.meta.url))
// 可通过环境变量 MLIST_BACKEND 覆盖开发代理目标
const backend = process.env.MLIST_BACKEND || 'http://127.0.0.1:8080'

/** emptyOutDir 会删掉仓库里跟踪的 .gitkeep，构建结束后重新创建。 */
function keepGitkeep(): Plugin {
  return {
    name: 'lnf-keep-gitkeep',
    apply: 'build',
    closeBundle() {
      mkdirSync(outDir, { recursive: true })
      writeFileSync(join(outDir, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [vue(), keepGitkeep()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    outDir,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1600,
  },
  server: {
    host: true,
    proxy: {
      // 使用正则，避免把前端路由 /api-docs 也转发到后端
      '^/api/': { target: backend, changeOrigin: false },
      '^/dav(/|$)': { target: backend, changeOrigin: false },
    },
  },
})
