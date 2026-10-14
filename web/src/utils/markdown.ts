import MarkdownIt from 'markdown-it'
import { fsApi } from '@/api/fs'
import { extName, joinPath, parentPath } from './path'

/** 按扩展名判断是否为 Markdown 文件 */
export function isMarkdown(name: string): boolean {
  return ['md', 'markdown', 'mdown', 'mkd'].includes(extName(name).toLowerCase())
}

// 原始 HTML 不渲染（html: false），因此文档中的 <script>、事件属性等都会被转义；
// markdown-it 的 validateLink 也会拒绝 javascript: 等危险链接。
const md = new MarkdownIt({ html: false, linkify: true, breaks: false })

/** 绝对 URL、协议相对地址、页内锚点与 data: 图片保持原样 */
function isExternal(url: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(url) || url.startsWith('//') || url.startsWith('#')
}

/** 把相对路径解析为同一桶内文件的下载地址（带鉴权），使图片与附件链接可用 */
function resolveLocal(url: string, dir: string): string {
  const [pathPart = '', hash] = url.split('#', 2)
  let p: string
  try {
    p = decodeURI(pathPart.split('?')[0] ?? '')
  } catch {
    p = pathPart
  }
  const target = p.startsWith('/') ? joinPath(p) : joinPath(dir, p)
  return fsApi.rawUrl(target) + (hash ? '#' + hash : '')
}

const defaultImage = md.renderer.rules.image!
md.renderer.rules.image = (tokens, idx, options, env: { dir: string }, self) => {
  const t = tokens[idx]!
  const src = t.attrGet('src')
  if (src && !isExternal(src)) t.attrSet('src', resolveLocal(src, env.dir))
  t.attrSet('loading', 'lazy')
  return defaultImage(tokens, idx, options, env, self)
}

const defaultLinkOpen =
  md.renderer.rules.link_open ?? ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options))
md.renderer.rules.link_open = (tokens, idx, options, env: { dir: string }, self) => {
  const t = tokens[idx]!
  const href = t.attrGet('href')
  if (href && !href.startsWith('#')) {
    if (!isExternal(href)) t.attrSet('href', resolveLocal(href, env.dir))
    t.attrSet('target', '_blank')
    t.attrSet('rel', 'noopener noreferrer')
  }
  return defaultLinkOpen(tokens, idx, options, env, self)
}

/** 渲染 Markdown；filePath 为该文件的虚拟路径，用于解析相对链接与图片 */
export function renderMarkdown(source: string, filePath: string): string {
  return md.render(source, { dir: parentPath(filePath) })
}
