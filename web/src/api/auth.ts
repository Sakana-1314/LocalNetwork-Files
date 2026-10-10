import { http } from './http'
import { normUser } from './normalize'
import { setToken } from './token'
import type { LoginResponse, PasswordChangeResponse, User } from './types'

export const authApi = {
  async login(username: string, password: string): Promise<LoginResponse> {
    const r = await http.post<LoginResponse>('/auth/login', { username, password }, { noAuthRedirect: true })
    return { ...r, user: normUser(r.user) }
  },
  logout(): Promise<void> {
    return http.post<void>('/auth/logout', undefined, { silent: true, noAuthRedirect: true })
  },
  async me(): Promise<User> {
    return normUser(await http.get<User>('/auth/me', { silent: true }))
  },
  /** 修改密码：旧会话全部失效，服务端返回新令牌，这里直接替换本地保存的令牌 */
  async changePassword(old_password: string, new_password: string): Promise<PasswordChangeResponse> {
    const r = await http.put<PasswordChangeResponse>('/auth/password', { old_password, new_password })
    if (r?.token) setToken(r.token)
    return r
  },
}
