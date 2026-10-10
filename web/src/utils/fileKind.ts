import type { Component } from 'vue'
import {
  ArchiveOutline,
  CodeSlashOutline,
  DocumentOutline,
  DocumentTextOutline,
  Folder,
  ImageOutline,
  MusicalNotesOutline,
  ServerOutline,
  FilmOutline,
  DocumentAttachOutline,
} from '@vicons/ionicons5'
import type { Entry, Kind } from '@/api/types'
import { extName } from './path'

export interface KindMeta {
  label: string
  icon: Component
  color: string
}

export const KIND_META: Record<Kind, KindMeta> = {
  dir: { label: '文件夹', icon: Folder, color: '#f2b230' },
  image: { label: '图片', icon: ImageOutline, color: '#22a06b' },
  video: { label: '视频', icon: FilmOutline, color: '#e2564b' },
  audio: { label: '音频', icon: MusicalNotesOutline, color: '#9b5de5' },
  text: { label: '文本', icon: DocumentTextOutline, color: '#3b82f6' },
  pdf: { label: 'PDF', icon: DocumentAttachOutline, color: '#dc2626' },
  archive: { label: '压缩包', icon: ArchiveOutline, color: '#d97706' },
  other: { label: '其他', icon: DocumentOutline, color: '#8a94a6' },
}

const CODE_EXT = new Set([
  'js', 'ts', 'jsx', 'tsx', 'vue', 'go', 'py', 'rs', 'java', 'kt', 'c', 'h', 'cpp', 'hpp', 'cc', 'cs', 'rb', 'php',
  'sh', 'bash', 'zsh', 'ps1', 'bat', 'swift', 'm', 'sql', 'html', 'css', 'scss', 'less', 'json', 'yaml', 'yml',
  'toml', 'xml', 'ini', 'conf', 'lua', 'dart', 'r', 'pl', 'gradle', 'dockerfile', 'makefile',
])

export function entryIcon(entry: Pick<Entry, 'kind' | 'name' | 'path'>, isMount = false): KindMeta {
  if (isMount) return { ...KIND_META.dir, icon: ServerOutline, color: '#18a058' }
  if (entry.kind === 'text' && CODE_EXT.has(extName(entry.name))) {
    return { ...KIND_META.text, icon: CodeSlashOutline, color: '#0ea5e9' }
  }
  return KIND_META[entry.kind] ?? KIND_META.other
}

export const PREVIEWABLE_KINDS: ReadonlySet<Kind> = new Set<Kind>(['image', 'video', 'audio', 'text', 'pdf'])

export function isPreviewable(e: Pick<Entry, 'kind' | 'is_dir'>): boolean {
  return !e.is_dir && PREVIEWABLE_KINDS.has(e.kind)
}

/** 浏览器通常无法直接播放的音频格式 */
const UNSUPPORTED_AUDIO = new Set(['ape', 'wma', 'dsf', 'dff', 'aiff', 'aif', 'aifc', 'wv', 'tta', 'ac3', 'dts', 'ra', 'rm', 'amr', 'mka', 'tak', 'mpc'])

export function audioNeedsTranscode(name: string, mime?: string): boolean {
  if (UNSUPPORTED_AUDIO.has(extName(name))) return true
  if (mime && typeof document !== 'undefined') {
    const a = document.createElement('audio')
    if (a.canPlayType(mime) === '') return true
  }
  return false
}

/** 文本是否可在编辑器中编辑（扩展名粗略判断） */
export function isEditableText(e: Pick<Entry, 'kind'>): boolean {
  return e.kind === 'text'
}
