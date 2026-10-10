import { fsApi } from '@/api/fs'

/** 触发浏览器下载（附带 access_token） */
export function triggerDownload(path: string, name?: string): void {
  const a = document.createElement('a')
  a.href = fsApi.rawUrl(path, true)
  if (name) a.download = name
  a.rel = 'noopener'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  setTimeout(() => a.remove(), 0)
}

/** 依次下载多个文件（间隔一段时间，避免浏览器拦截） */
export async function downloadMany(items: { path: string; name: string }[]): Promise<void> {
  for (let i = 0; i < items.length; i++) {
    const it = items[i]!
    triggerDownload(it.path, it.name)
    if (i < items.length - 1) await new Promise((r) => setTimeout(r, 400))
  }
}
