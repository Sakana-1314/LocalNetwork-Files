import { clearToken, getToken } from './token'

export const API_BASE = '/api/v1'

export class ApiError extends Error {
  readonly status: number
  /** 解析后的错误响应体（JSON），用于读取 error 之外的字段 */
  readonly data?: unknown
  constructor(status: number, message: string, data?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.data = data
  }
}

export type QueryValue = string | number | boolean | null | undefined
export type Query = Record<string, QueryValue>

export interface RequestOptions {
  query?: Query
  /** JSON 请求体 */
  body?: unknown
  /** 原始请求体（不做 JSON 序列化） */
  rawBody?: BodyInit
  headers?: Record<string, string>
  signal?: AbortSignal
  /** 为 true 时出错不弹出全局提示 */
  silent?: boolean
  /** 为 true 时 401 不触发跳转登录（例如登录接口本身） */
  noAuthRedirect?: boolean
}

interface HttpHooks {
  onUnauthorized: () => void
  onError: (message: string) => void
}

const hooks: HttpHooks = {
  onUnauthorized: () => {},
  onError: () => {},
}

export function setHttpHooks(h: Partial<HttpHooks>): void {
  Object.assign(hooks, h)
}

/** 构造 URL：相对路径会加上 /api/v1 前缀，以 /api 开头的绝对路径保持不变。 */
export function buildUrl(path: string, query?: Query): string {
  let url = path.startsWith('/api/') ? path : API_BASE + (path.startsWith('/') ? path : '/' + path)
  if (query) {
    const qs = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) {
      if (v === undefined || v === null || v === '') continue
      qs.append(k, String(v))
    }
    const s = qs.toString()
    if (s) url += (url.includes('?') ? '&' : '?') + s
  }
  return url
}

/** 为 <img>/<video>/<a download> 等无法携带请求头的 URL 追加 access_token。 */
export function withToken(url: string): string {
  const token = getToken()
  if (!token) return url
  return url + (url.includes('?') ? '&' : '?') + 'access_token=' + encodeURIComponent(token)
}

export function authHeaders(): Record<string, string> {
  const token = getToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

/** 解析错误响应体 {error: string} */
export async function readErrorMessage(res: Response): Promise<string> {
  return (await readError(res)).message
}

async function readError(res: Response): Promise<{ message: string; data?: unknown }> {
  try {
    const text = await res.text()
    if (text) {
      try {
        const data = JSON.parse(text) as { error?: unknown }
        if (data && typeof data.error === 'string' && data.error) return { message: data.error, data }
      } catch {
        if (text.length < 300) return { message: text }
      }
    }
  } catch {
    /* ignore */
  }
  return { message: defaultStatusMessage(res.status) }
}

export function defaultStatusMessage(status: number): string {
  switch (status) {
    case 400:
      return '请求参数错误'
    case 401:
      return '登录已失效，请重新登录'
    case 403:
      return '没有权限执行此操作'
    case 404:
      return '资源不存在'
    case 409:
      return '目标已存在'
    case 413:
      return '文件过大'
    case 500:
      return '服务器内部错误'
    case 502:
    case 503:
    case 504:
      return '服务器暂时不可用'
    default:
      return `请求失败（${status}）`
  }
}

let unauthorizedNotified = false

/** 统一处理 401：清除令牌并通知应用跳转登录页。 */
export function handleUnauthorized(): void {
  clearToken()
  if (!unauthorizedNotified) {
    unauthorizedNotified = true
    hooks.onError('登录已失效，请重新登录')
    setTimeout(() => (unauthorizedNotified = false), 1500)
  }
  hooks.onUnauthorized()
}

export function reportError(message: string): void {
  hooks.onError(message)
}

export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders(), ...opts.headers }
  let body: BodyInit | undefined
  if (opts.rawBody !== undefined) {
    body = opts.rawBody
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }

  let res: Response
  try {
    res = await fetch(buildUrl(path, opts.query), {
      method,
      headers,
      body,
      signal: opts.signal,
      credentials: 'same-origin',
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    const err = new ApiError(0, '网络错误，无法连接服务器')
    if (!opts.silent) hooks.onError(err.message)
    throw err
  }

  if (res.status === 401 && !opts.noAuthRedirect) {
    handleUnauthorized()
    throw new ApiError(401, defaultStatusMessage(401))
  }

  if (!res.ok) {
    const { message, data } = await readError(res)
    if (!opts.silent) hooks.onError(message)
    throw new ApiError(res.status, message, data)
  }

  if (res.status === 204) return undefined as T
  const ct = res.headers.get('Content-Type') ?? ''
  if (ct.includes('application/json')) return (await res.json()) as T
  const text = await res.text()
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    return text as unknown as T
  }
}

export const http = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('POST', path, { ...opts, body }),
  put: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('PUT', path, { ...opts, body }),
  del: <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, opts),
}

/** 判断错误是否为请求被取消 */
export function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}
