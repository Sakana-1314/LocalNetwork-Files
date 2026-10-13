import { http } from './http'
import { arr, normBucket, normSync, normUser } from './normalize'
import type {
  BrowseResponse,
  BucketInput,
  BucketSaved,
  BucketView,
  CacheStats,
  CleanRequest,
  CleanResult,
  IndexStatus,
  SyncResult,
  User,
  UserInput,
  UserPassword,
} from './types'

function normSaved(r: BucketSaved): BucketSaved {
  return { bucket: normBucket(r.bucket), sync: normSync(r.sync) }
}

export const adminApi = {
  async users(): Promise<User[]> {
    return arr(await http.get<User[]>('/admin/users')).map(normUser)
  },
  async createUser(input: UserInput): Promise<User> {
    return normUser(await http.post<User>('/admin/users', input))
  },
  async updateUser(id: number, input: UserInput): Promise<User> {
    return normUser(await http.put<User>(`/admin/users/${id}`, input))
  },
  deleteUser(id: number): Promise<void> {
    return http.del<void>(`/admin/users/${id}`)
  },
  /** 读取用户当前密码；known 为 false 表示该账户没有密文，只能重置 */
  async userPassword(id: number): Promise<UserPassword> {
    return http.get<UserPassword>(`/admin/users/${id}/password`, { silent: true })
  },
  async buckets(): Promise<BucketView[]> {
    return arr(await http.get<BucketView[]>('/admin/buckets')).map(normBucket)
  },
  /** 冲突时抛出 status=409 的 ApiError，data.conflicts 为冲突列表 */
  async createBucket(input: BucketInput): Promise<BucketSaved> {
    return normSaved(await http.post<BucketSaved>('/admin/buckets', input, { silent: true }))
  },
  async updateBucket(id: number, input: BucketInput): Promise<BucketSaved> {
    return normSaved(await http.put<BucketSaved>(`/admin/buckets/${id}`, input, { silent: true }))
  },
  deleteBucket(id: number): Promise<void> {
    return http.del<void>(`/admin/buckets/${id}`)
  },
  async syncBucket(id: number): Promise<SyncResult> {
    return normSync(await http.post<SyncResult>(`/admin/buckets/${id}/sync`))
  },
  async browse(path?: string): Promise<BrowseResponse> {
    const r = await http.get<BrowseResponse>('/admin/browse', { query: { path } })
    return { path: r.path ?? '', parent: r.parent ?? '', dirs: arr(r.dirs) }
  },
  async cache(): Promise<CacheStats> {
    const r = await http.get<CacheStats>('/admin/cache')
    return { ...r, sessions: arr(r.sessions) }
  },
  cleanCache(req: CleanRequest): Promise<CleanResult> {
    return http.post<CleanResult>('/admin/cache/clean', req)
  },
  searchStatus(silent = false): Promise<IndexStatus> {
    return http.get<IndexStatus>('/admin/search/status', { silent })
  },
  reindex(): Promise<void> {
    return http.post<void>('/admin/search/reindex')
  },
}
