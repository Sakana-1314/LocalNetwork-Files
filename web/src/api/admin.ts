import { http } from './http'
import { arr, normUser } from './normalize'
import type {
  BrowseResponse,
  CacheStats,
  CleanRequest,
  CleanResult,
  IndexStatus,
  Settings,
  User,
  UserInput,
} from './types'

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
  async browse(path?: string): Promise<BrowseResponse> {
    const r = await http.get<BrowseResponse>('/admin/browse', { query: { path } })
    return { path: r.path ?? '', parent: r.parent ?? '', dirs: arr(r.dirs) }
  },
  settings(): Promise<Settings> {
    return http.get<Settings>('/admin/settings')
  },
  saveSettings(s: Settings): Promise<Settings> {
    return http.put<Settings>('/admin/settings', s)
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
