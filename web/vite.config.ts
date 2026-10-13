import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

// 默认产物是后端内嵌的前端（server/internal/webui/dist）；
// 演示站等场景可用 WEB_OUT_DIR 指定到别处（相对路径按 web/ 解析）。
const defaultOutDir = fileURLToPath(new URL('../server/internal/webui/dist', import.meta.url))
const customOutDir = process.env.WEB_OUT_DIR?.trim()
const outDir = customOutDir ? resolve(process.cwd(), customOutDir) : defaultOutDir

// 部署到子路径时用 WEB_BASE 覆盖（如 /MList/demo/）；默认为根路径。
const base = process.env.WEB_BASE?.trim() || '/'

// 可通过环境变量 MLIST_BACKEND 覆盖开发代理目标
const backend = process.env.MLIST_BACKEND || 'http://127.0.0.1:8080'

/**
 * emptyOutDir 会删掉仓库里跟踪的 .gitkeep，构建结束后重新创建。
 * 只有默认产物目录才需要：换成自定义目录时那里不由仓库跟踪。
 */
function keepGitkeep(): Plugin {
  return {
    name: 'lnf-keep-gitkeep',
    apply: 'build',
    closeBundle() {
      if (customOutDir) return
      mkdirSync(defaultOutDir, { recursive: true })
      writeFileSync(join(defaultOutDir, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  base,
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
