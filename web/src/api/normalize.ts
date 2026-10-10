import type { Entry, MediaInfo, User } from './types'

/** Go 的 nil 切片会被序列化为 null，这里统一转换为空数组。 */
export function arr<T>(v: T[] | null | undefined): T[] {
  return Array.isArray(v) ? v : []
}

export function normUser(u: User): User {
  return { ...u, mounts: arr(u.mounts) }
}

export function normEntry<T extends Entry>(e: T): T {
  return { ...e, size: e.size ?? 0, kind: e.kind ?? (e.is_dir ? 'dir' : 'other') }
}

export function normMediaInfo(m: MediaInfo): MediaInfo {
  return { ...m, video: m.video ?? null, audio: arr(m.audio), subtitles: arr(m.subtitles) }
}
