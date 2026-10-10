import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { authHeaders, defaultStatusMessage, handleUnauthorized } from '@/api/http'
import { fsApi } from '@/api/fs'
import { joinPath, parentPath } from '@/utils/path'
import type { PickedFile } from '@/utils/dropFiles'

export type UploadStatus = 'pending' | 'uploading' | 'done' | 'error' | 'canceled' | 'conflict' | 'skipped'

export interface UploadItem {
  id: number
  file: File
  name: string
  /** 目标完整虚拟路径 */
  dest: string
  size: number
  loaded: number
  status: UploadStatus
  error?: string
  overwrite: boolean
  /** 速度（字节/秒） */
  speed: number
}

const CONCURRENCY = 3

export const useUploadsStore = defineStore('uploads', () => {
  const items = ref<UploadItem[]>([])
  const panelOpen = ref(false)
  const collapsed = ref(false)
  /** 每完成一个文件更新一次，供文件列表感知刷新 */
  const lastCompleted = shallowRef<{ dir: string; at: number } | null>(null)
  /** 冲突时“对后续全部冲突应用相同选择” */
  const conflictPolicy = ref<'ask' | 'overwrite' | 'skip'>('ask')

  const xhrs = new Map<number, XMLHttpRequest>()
  let seq = 0

  const active = computed(() => items.value.filter((i) => i.status === 'uploading' || i.status === 'pending'))
  const conflicts = computed(() => items.value.filter((i) => i.status === 'conflict'))
  const finished = computed(() => items.value.filter((i) => i.status === 'done').length)
  const failed = computed(() => items.value.filter((i) => i.status === 'error').length)
  const totalBytes = computed(() => active.value.reduce((s, i) => s + i.size, 0))
  const loadedBytes = computed(() => active.value.reduce((s, i) => s + i.loaded, 0))

  function find(id: number): UploadItem | undefined {
    return items.value.find((i) => i.id === id)
  }

  /** 将文件加入队列。dir 为目标目录虚拟路径。 */
  function enqueue(dir: string, files: PickedFile[]): void {
    if (!files.length) return
    if (!active.value.length && !conflicts.value.length) conflictPolicy.value = 'ask'
    for (const f of files) {
      items.value.push({
        id: ++seq,
        file: f.file,
        name: f.relPath,
        dest: joinPath(dir, f.relPath),
        size: f.file.size,
        loaded: 0,
        status: 'pending',
        overwrite: false,
        speed: 0,
      })
    }
    panelOpen.value = true
    collapsed.value = false
    pump()
  }

  function pump(): void {
    let running = items.value.filter((i) => i.status === 'uploading').length
    for (const item of items.value) {
      if (running >= CONCURRENCY) break
      if (item.status === 'pending') {
        start(item)
        running++
      }
    }
  }

  function start(item: UploadItem): void {
    const id = item.id
    const xhr = new XMLHttpRequest()
    xhrs.set(id, xhr)
    item.status = 'uploading'
    item.loaded = 0
    item.error = undefined
    let lastT = performance.now()
    let lastLoaded = 0

    xhr.open('PUT', fsApi.uploadPath(item.dest, item.overwrite))
    for (const [k, v] of Object.entries(authHeaders())) xhr.setRequestHeader(k, v)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')

    xhr.upload.onprogress = (ev) => {
      const it = find(id)
      if (!it) return
      it.loaded = ev.loaded
      const now = performance.now()
      if (now - lastT > 500) {
        it.speed = ((ev.loaded - lastLoaded) * 1000) / (now - lastT)
        lastT = now
        lastLoaded = ev.loaded
      }
    }
    xhr.onload = () => {
      xhrs.delete(id)
      const it = find(id)
      if (!it) return
      if (xhr.status >= 200 && xhr.status < 300) {
        it.status = 'done'
        it.loaded = it.size
        lastCompleted.value = { dir: parentPath(it.dest), at: Date.now() }
      } else if (xhr.status === 409) {
        if (conflictPolicy.value === 'overwrite') {
          it.overwrite = true
          it.status = 'pending'
        } else if (conflictPolicy.value === 'skip') {
          it.status = 'skipped'
        } else {
          it.status = 'conflict'
        }
      } else if (xhr.status === 401) {
        it.status = 'error'
        it.error = defaultStatusMessage(401)
        handleUnauthorized()
      } else {
        it.status = 'error'
        it.error = parseError(xhr)
      }
      pump()
    }
    xhr.onerror = () => {
      xhrs.delete(id)
      const it = find(id)
      if (it) {
        it.status = 'error'
        it.error = '网络错误'
      }
      pump()
    }
    xhr.onabort = () => {
      xhrs.delete(id)
      const it = find(id)
      if (it && it.status === 'uploading') it.status = 'canceled'
      pump()
    }
    xhr.send(item.file)
  }

  function parseError(xhr: XMLHttpRequest): string {
    try {
      const data = JSON.parse(xhr.responseText) as { error?: string }
      if (data.error) return data.error
    } catch {
      /* ignore */
    }
    return defaultStatusMessage(xhr.status)
  }

  function cancel(id: number): void {
    const it = find(id)
    if (!it) return
    if (it.status === 'uploading') {
      it.status = 'canceled'
      xhrs.get(id)?.abort()
    } else if (it.status === 'pending' || it.status === 'conflict') {
      it.status = 'canceled'
    }
    pump()
  }

  function retry(id: number): void {
    const it = find(id)
    if (!it) return
    if (it.status === 'error' || it.status === 'canceled' || it.status === 'skipped') {
      it.status = 'pending'
      it.loaded = 0
      pump()
    }
  }

  function retryFailed(): void {
    for (const it of items.value) if (it.status === 'error') it.status = 'pending'
    pump()
  }

  /** 解决冲突：overwrite=true 覆盖，否则跳过；applyAll 对其余冲突（含后续）应用相同选择 */
  function resolveConflict(id: number, overwrite: boolean, applyAll: boolean): void {
    const targets = applyAll ? conflicts.value.slice() : [find(id)].filter((x): x is UploadItem => !!x)
    if (applyAll) conflictPolicy.value = overwrite ? 'overwrite' : 'skip'
    for (const it of targets) {
      if (overwrite) {
        it.overwrite = true
        it.status = 'pending'
        it.loaded = 0
      } else {
        it.status = 'skipped'
      }
    }
    pump()
  }

  function cancelAll(): void {
    for (const it of items.value) {
      if (it.status === 'pending' || it.status === 'conflict') it.status = 'canceled'
    }
    for (const [id, xhr] of xhrs) {
      const it = find(id)
      if (it) it.status = 'canceled'
      xhr.abort()
    }
  }

  function clearFinished(): void {
    items.value = items.value.filter((i) => i.status === 'uploading' || i.status === 'pending' || i.status === 'conflict')
    if (!items.value.length) panelOpen.value = false
  }

  function close(): void {
    if (active.value.length || conflicts.value.length) {
      collapsed.value = true
      return
    }
    items.value = []
    panelOpen.value = false
  }

  return {
    items,
    panelOpen,
    collapsed,
    lastCompleted,
    active,
    conflicts,
    finished,
    failed,
    totalBytes,
    loadedBytes,
    enqueue,
    cancel,
    retry,
    retryFailed,
    resolveConflict,
    cancelAll,
    clearFinished,
    close,
  }
})
