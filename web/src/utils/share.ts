import { router } from '@/router'

/** 分享的公开访问地址（兼容 hash 路由与子路径部署） */
export function shareLink(token: string): string {
  const href = router.resolve({ name: 'share-public', params: { token } }).href
  return new URL(href, window.location.href).href
}

export type ExpiryPreset = 'never' | '1d' | '7d' | '30d' | 'custom'

export const EXPIRY_OPTIONS: { label: string; value: ExpiryPreset }[] = [
  { label: '永久有效', value: 'never' },
  { label: '1 天', value: '1d' },
  { label: '7 天', value: '7d' },
  { label: '30 天', value: '30d' },
  { label: '自定义', value: 'custom' },
]

const DAY = 24 * 3600 * 1000

/** 把选择转换为过期时间（ISO 字符串）；永久为 null */
export function expiryFrom(preset: ExpiryPreset, custom: number | null): string | null {
  switch (preset) {
    case 'never':
      return null
    case '1d':
      return new Date(Date.now() + DAY).toISOString()
    case '7d':
      return new Date(Date.now() + 7 * DAY).toISOString()
    case '30d':
      return new Date(Date.now() + 30 * DAY).toISOString()
    case 'custom':
      return custom ? new Date(custom).toISOString() : null
  }
}
