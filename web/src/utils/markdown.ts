/** 极简 Markdown 渲染（仅用于 API 文档描述）：先转义 HTML，再处理粗体、行内代码、列表与段落。 */
function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

function inline(s: string): string {
  return escapeHtml(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
}

export function renderMarkdown(src: string | undefined): string {
  if (!src) return ''
  const lines = src.replace(/\r\n/g, '\n').split('\n')
  const html: string[] = []
  let list = false
  let para: string[] = []
  const flushPara = () => {
    if (para.length) html.push(`<p>${para.map(inline).join('<br>')}</p>`)
    para = []
  }
  for (const raw of lines) {
    const line = raw.trimEnd()
    const m = /^\s*[-*]\s+(.*)$/.exec(line)
    if (m) {
      flushPara()
      if (!list) {
        html.push('<ul>')
        list = true
      }
      html.push(`<li>${inline(m[1] ?? '')}</li>`)
      continue
    }
    if (list) {
      html.push('</ul>')
      list = false
    }
    if (!line.trim()) flushPara()
    else para.push(line)
  }
  flushPara()
  if (list) html.push('</ul>')
  return html.join('')
}
