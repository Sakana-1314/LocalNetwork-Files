import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import 'dayjs/locale/zh-cn'

dayjs.extend(relativeTime)
dayjs.locale('zh-cn')

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatSize(bytes: number | null | undefined, digits = 1): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return '-'
  if (bytes < 1024) return `${bytes} B`
  let v = bytes
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : digits)} ${UNITS[i]}`
}

export function formatDate(d: string | null | undefined, fmt = 'YYYY-MM-DD HH:mm'): string {
  if (!d) return '-'
  const t = dayjs(d)
  return t.isValid() && t.year() > 1971 ? t.format(fmt) : '-'
}

/** 相对时间：一周内显示“3 小时前”，否则显示日期 */
export function formatRelative(d: string | null | undefined): string {
  if (!d) return '-'
  const t = dayjs(d)
  if (!t.isValid() || t.year() <= 1971) return '-'
  const diffDays = Math.abs(dayjs().diff(t, 'day'))
  if (diffDays < 7) return t.fromNow()
  if (t.year() === dayjs().year()) return t.format('MM-DD HH:mm')
  return t.format('YYYY-MM-DD')
}

export function formatDuration(seconds: number | null | undefined): string {
  if (!seconds || !Number.isFinite(seconds) || seconds < 0) return '00:00'
  const s = Math.floor(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${pad(m)}:${pad(sec)}`
}

export function formatBitrate(bps: number | null | undefined): string {
  if (!bps) return '-'
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(1)} Mbps`
  return `${Math.round(bps / 1000)} kbps`
}

export function formatNumber(n: number | null | undefined): string {
  if (n === null || n === undefined) return '-'
  return n.toLocaleString('zh-CN')
}
