import { buildUrl, http, type RequestOptions } from './http'
import { arr, normEntry } from './normalize'
import type { ListResponse, PublicShareInfo, Share, TextResponse } from './types'

/** 自己的分享（需要登录） */
export const sharesApi = {
  async list(): Promise<Share[]> {
    return arr(await http.get<Share[]>('/shares'))
  },
  /** expiresAt 为 null 表示永不过期 */
  create(path: string, expiresAt: string | null): Promise<Share> {
    return http.post<Share>('/shares', { path, expires_at: expiresAt })
  },
  setExpiry(id: number, expiresAt: string | null): Promise<Share> {
    return http.put<Share>(`/shares/${id}`, { expires_at: expiresAt })
  },
  remove(id: number): Promise<void> {
    return http.del<void>(`/shares/${id}`)
  },
}

const base = (token: string) => `/share/${encodeURIComponent(token)}`

/** 分享链接的公开访问（无需登录）；path 均相对于分享的文件夹 */
export const publicShareApi = {
  info(token: string, opts?: RequestOptions): Promise<PublicShareInfo> {
    return http.get<PublicShareInfo>(base(token), { ...opts, noAuthRedirect: true })
  },
  async list(token: string, path: string, opts?: RequestOptions): Promise<ListResponse> {
    const r = await http.get<ListResponse>(`${base(token)}/list`, { ...opts, query: { path }, noAuthRedirect: true })
    return { ...r, entries: arr(r.entries).map(normEntry) }
  },
  text(token: string, path: string, opts?: RequestOptions): Promise<TextResponse> {
    return http.get<TextResponse>(`${base(token)}/text`, { ...opts, query: { path }, noAuthRedirect: true })
  },
  /** 文件地址；末尾带文件名，浏览器下载时显示真实名称 */
  rawUrl(token: string, path: string, download = false): string {
    const name = path.split('/').filter(Boolean).pop() ?? 'file'
    return buildUrl(`${base(token)}/raw/${encodeURIComponent(name)}`, { path, download: download ? 1 : undefined })
  },
}
