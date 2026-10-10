import { buildUrl, http, request, withToken, type RequestOptions } from './http'
import { arr, normEntry } from './normalize'
import type {
  BatchResult,
  Entry,
  ListResponse,
  SearchParams,
  SearchResponse,
  TextResponse,
  TransferRequest,
} from './types'

function normBatch(r: BatchResult | undefined): BatchResult {
  return { succeeded: arr(r?.succeeded), failed: arr(r?.failed) }
}

export const fsApi = {
  async list(path: string, opts?: RequestOptions): Promise<ListResponse> {
    const r = await http.get<ListResponse>('/fs/list', { ...opts, query: { path } })
    return { path: r.path ?? path, writable: !!r.writable, entries: arr(r.entries).map(normEntry) }
  },
  async stat(path: string, opts?: RequestOptions): Promise<Entry> {
    return normEntry(await http.get<Entry>('/fs/stat', { ...opts, query: { path } }))
  },
  text(path: string, opts?: RequestOptions): Promise<TextResponse> {
    return http.get<TextResponse>('/fs/text', { ...opts, query: { path } })
  },
  /** 以原始请求体写入文件（小文件，如文本编辑保存）；大文件上传请使用 uploads store（带进度）。 */
  writeFile(path: string, body: BodyInit, overwrite = false): Promise<Entry> {
    return request<Entry>('PUT', '/fs/file', {
      query: { path, overwrite },
      rawBody: body,
      headers: { 'Content-Type': 'application/octet-stream' },
    })
  },
  mkdir(path: string): Promise<Entry> {
    return http.post<Entry>('/fs/mkdir', { path })
  },
  rename(path: string, name: string): Promise<Entry> {
    return http.post<Entry>('/fs/rename', { path, name })
  },
  async move(req: TransferRequest): Promise<BatchResult> {
    return normBatch(await http.post<BatchResult>('/fs/move', req))
  },
  async copy(req: TransferRequest): Promise<BatchResult> {
    return normBatch(await http.post<BatchResult>('/fs/copy', req))
  },
  async remove(paths: string[]): Promise<BatchResult> {
    return normBatch(await http.post<BatchResult>('/fs/delete', { paths }))
  },
  async search(params: SearchParams, opts?: RequestOptions): Promise<SearchResponse> {
    const r = await http.get<SearchResponse>('/fs/search', {
      ...opts,
      query: { q: params.q, path: params.path, kind: params.kind, limit: params.limit },
    })
    return {
      results: arr(r.results).map((x) => ({ ...normEntry(x), matches: arr(x.matches) })),
      total: r.total ?? 0,
      indexing: !!r.indexing,
      took_ms: r.took_ms ?? 0,
    }
  },

  /** 原始文件 URL（不含令牌），用于 fetch。 */
  rawPath(path: string, download = false): string {
    return buildUrl('/fs/raw', { path, download: download || undefined })
  },
  /** 原始文件 URL（含 access_token），用于 <img>/<video>/<iframe>/<a>。 */
  rawUrl(path: string, download = false): string {
    return withToken(fsApi.rawPath(path, download))
  },
  uploadPath(path: string, overwrite: boolean): string {
    return buildUrl('/fs/file', { path, overwrite })
  },
}
