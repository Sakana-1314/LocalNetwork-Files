import type { BucketView, Entry, MediaInfo, ServiceStatus, SyncResult, User } from './types'

/** Go 的 nil 切片会被序列化为 null，这里统一转换为空数组。 */
export function arr<T>(v: T[] | null | undefined): T[] {
  return Array.isArray(v) ? v : []
}

export function normUser(u: User): User {
  return { ...u, grants: arr(u.grants) }
}

export function normBucket(b: BucketView): BucketView {
  return { ...b, paths: arr(b.paths), dirs: arr(b.dirs) }
}

export function normSync(r: SyncResult): SyncResult {
  return { ...r, conflicts: arr(r.conflicts) }
}

export function normEntry<T extends Entry>(e: T): T {
  return { ...e, size: e.size ?? 0, kind: e.kind ?? (e.is_dir ? 'dir' : 'other') }
}

export function normMediaInfo(m: MediaInfo): MediaInfo {
  return { ...m, video: m.video ?? null, audio: arr(m.audio), subtitles: arr(m.subtitles) }
}

/**
 * 服务状态可能是被序列化成 JSON 的字符串。
 *
 * Apifox 的「Mock 字段」是纯文本输入框：即使在里面写 JSON 对象，返回值也是
 * 字符串，导入一份没有该配置的规范也不会清掉它。真实的 MList 后端始终返回
 * 对象，这里只是让前端对两种形态都健壮——否则 `services.webdav` 是字符串时
 * 读不到字段，卡片会显示「未启用」。
 */
export function normServiceStatus(v: ServiceStatus | string | null | undefined): ServiceStatus {
  let raw: unknown = v
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      raw = null
    }
  }
  if (raw === null || typeof raw !== 'object') return { enabled: false, running: false, port: 0 }
  const s = raw as Partial<ServiceStatus>
  return {
    ...s,
    enabled: s.enabled ?? false,
    running: s.running ?? false,
    port: s.port ?? 0,
  }
}
