/** 虚拟路径工具。虚拟路径总是以 / 开头，根为 "/"。 */

export function normalizePath(p: string | null | undefined): string {
  if (!p) return '/'
  const parts = p.split('/').filter((s) => s && s !== '.')
  const out: string[] = []
  for (const s of parts) {
    if (s === '..') out.pop()
    else out.push(s)
  }
  return '/' + out.join('/')
}

export function joinPath(...parts: string[]): string {
  return normalizePath(parts.join('/'))
}

export function splitPath(p: string): string[] {
  return normalizePath(p).split('/').filter(Boolean)
}

export function parentPath(p: string): string {
  const s = splitPath(p)
  s.pop()
  return '/' + s.join('/')
}

export function baseName(p: string): string {
  const s = splitPath(p)
  return s[s.length - 1] ?? ''
}

export function extName(name: string): string {
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(i + 1).toLowerCase() : ''
}

/** 路径所属的挂载名（第一级目录） */
export function mountOf(p: string): string {
  return splitPath(p)[0] ?? ''
}

export function isAncestor(ancestor: string, p: string): boolean {
  const a = normalizePath(ancestor)
  const b = normalizePath(p)
  if (a === '/') return true
  return b === a || b.startsWith(a + '/')
}

/** 文件名合法性校验（不含路径分隔符） */
export function validateName(name: string): string | null {
  const n = name.trim()
  if (!n) return '名称不能为空'
  if (n === '.' || n === '..') return '名称不合法'
  if (/[\\/]/.test(n)) return '名称不能包含 / 或 \\'
  if (/[\u0000-\u001f]/.test(n)) return '名称包含非法字符'
  return null
}

/** 文件浏览路由需要的路径段参数 */
export function filesRoute(path: string, query?: Record<string, string>) {
  return { name: 'files', params: { path: splitPath(path) }, query }
}
