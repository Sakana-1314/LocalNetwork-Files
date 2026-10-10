import type { Entry } from '@/api/types'

export type SortKey = 'name' | 'size' | 'mod_time'
export type SortOrder = 'ascend' | 'descend'
export interface SortState {
  key: SortKey
  order: SortOrder
}

const collator = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })

/** 排序：文件夹始终在前 */
export function sortEntries(list: Entry[], s: SortState): Entry[] {
  const dir = s.order === 'ascend' ? 1 : -1
  return list.slice().sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
    let r = 0
    if (s.key === 'size') r = (a.size ?? 0) - (b.size ?? 0)
    else if (s.key === 'mod_time') r = Date.parse(a.mod_time) - Date.parse(b.mod_time)
    if (r === 0 || Number.isNaN(r)) r = collator.compare(a.name, b.name)
    return r * dir
  })
}
