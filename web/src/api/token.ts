import { ref } from 'vue'

const KEY = 'lnf_token'

function read(): string {
  try {
    return localStorage.getItem(KEY) ?? ''
  } catch {
    return ''
  }
}

/** 当前会话令牌（响应式，便于 store / 组件感知 401 后的清除）。 */
export const tokenRef = ref<string>(read())

export function getToken(): string {
  return tokenRef.value
}

export function setToken(token: string): void {
  tokenRef.value = token
  try {
    localStorage.setItem(KEY, token)
  } catch {
    /* 隐私模式等场景下忽略 */
  }
}

export function clearToken(): void {
  tokenRef.value = ''
  try {
    localStorage.removeItem(KEY)
  } catch {
    /* ignore */
  }
}
