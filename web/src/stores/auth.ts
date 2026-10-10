import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { authApi } from '@/api/auth'
import { clearToken, setToken, tokenRef } from '@/api/token'
import type { User } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const token = computed(() => tokenRef.value)
  const isLoggedIn = computed(() => !!token.value && !!user.value)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const mountNames = computed(() => (user.value?.mounts ?? []).map((m) => m.name))

  let mePromise: Promise<User | null> | null = null

  async function login(username: string, password: string): Promise<User> {
    const r = await authApi.login(username, password)
    setToken(r.token)
    user.value = r.user
    return r.user
  }

  /** 拉取当前用户；失败（令牌失效）时清除令牌并返回 null */
  function fetchMe(): Promise<User | null> {
    if (!token.value) return Promise.resolve(null)
    if (!mePromise) {
      mePromise = authApi
        .me()
        .then((u) => (user.value = u))
        .catch(() => {
          clearToken()
          user.value = null
          return null
        })
        .finally(() => (mePromise = null))
    }
    return mePromise
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } catch {
      /* ignore */
    }
    reset()
  }

  function reset(): void {
    clearToken()
    user.value = null
  }

  return { user, token, isLoggedIn, isAdmin, mountNames, login, fetchMe, logout, reset }
})
