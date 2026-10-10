<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NConfigProvider, NIcon, NTooltip, darkTheme, useDialog } from 'naive-ui'
import {
  ChevronBackOutline,
  ChevronForwardOutline,
  CloseOutline,
  DownloadOutline,
  OpenOutline,
} from '@vicons/ionicons5'
import type { Entry } from '@/api/types'
import { fsApi } from '@/api/fs'
import { mediaApi } from '@/api/media'
import FileIcon from '@/components/common/FileIcon.vue'
import ImagePreview from './ImagePreview.vue'
import VideoPreview from './VideoPreview.vue'
import AudioPreview from './AudioPreview.vue'
import TextPreview from './TextPreview.vue'
import PdfPreview from './PdfPreview.vue'
import OtherPreview from './OtherPreview.vue'
import { triggerDownload } from '@/utils/download'
import { formatSize } from '@/utils/format'

const props = defineProps<{ items: Entry[]; writable: boolean }>()
const show = defineModel<boolean>('show', { default: false })
const index = defineModel<number>('index', { default: 0 })
const emit = defineEmits<{ saved: [entry: Entry]; closed: [] }>()
const dialog = useDialog()

const dirty = ref(false)
const current = computed<Entry | undefined>(() => props.items[index.value])
const hasPrev = computed(() => index.value > 0)
const hasNext = computed(() => index.value < props.items.length - 1)

watch(current, () => (dirty.value = false))

function guard(action: () => void): void {
  if (!dirty.value) {
    action()
    return
  }
  dialog.warning({
    title: '放弃修改？',
    content: '文本有未保存的修改，确定要放弃吗？',
    positiveText: '放弃',
    negativeText: '继续编辑',
    onPositiveClick: () => {
      dirty.value = false
      action()
    },
  })
}

function prev(): void {
  if (hasPrev.value) guard(() => index.value--)
}
function next(): void {
  if (hasNext.value) guard(() => index.value++)
}
function close(): void {
  guard(() => {
    show.value = false
    emit('closed')
  })
}

function isEditableTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

function onKey(e: KeyboardEvent): void {
  if (!show.value) return
  // 有对话框打开时交给对话框处理
  if (document.querySelector('.n-dialog')) return
  if (e.key === 'Escape') {
    if (isEditableTarget(e.target)) return
    e.preventDefault()
    close()
    return
  }
  if (isEditableTarget(e.target)) return
  const tag = (e.target as HTMLElement | null)?.tagName
  if (tag === 'VIDEO' || tag === 'AUDIO') return
  if (e.key === 'ArrowLeft') {
    e.preventDefault()
    prev()
  } else if (e.key === 'ArrowRight') {
    e.preventDefault()
    next()
  }
}

watch(
  show,
  (v) => {
    if (v) window.addEventListener('keydown', onKey)
    else window.removeEventListener('keydown', onKey)
  },
  { immediate: true },
)
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// 预加载相邻图片的预览图
watch(
  [current, show],
  () => {
    if (!show.value) return
    for (const d of [-1, 1]) {
      const e = props.items[index.value + d]
      if (e && e.kind === 'image' && e.thumb) {
        const img = new Image()
        img.src = mediaApi.thumbUrl(e.path, 1024)
      }
    }
  },
  { immediate: true },
)

function openRaw(): void {
  if (current.value) window.open(fsApi.rawUrl(current.value.path), '_blank', 'noopener')
}

// 触摸滑动切换（仅图片）
let touchX = 0
let touchY = 0
function onTouchStart(e: TouchEvent): void {
  const t = e.touches[0]
  if (!t || e.touches.length > 1) return
  touchX = t.clientX
  touchY = t.clientY
}
function onTouchEnd(e: TouchEvent): void {
  if (current.value?.kind !== 'image') return
  const t = e.changedTouches[0]
  if (!t) return
  const dx = t.clientX - touchX
  const dy = t.clientY - touchY
  if (Math.abs(dx) > 70 && Math.abs(dx) > Math.abs(dy) * 1.5) {
    if (dx > 0) prev()
    else next()
  }
}
</script>

<template>
  <teleport to="body">
    <n-config-provider :theme="darkTheme" abstract>
      <transition name="fade">
        <div v-if="show && current" class="preview-overlay" role="dialog" aria-modal="true" @touchstart.passive="onTouchStart" @touchend="onTouchEnd">
          <header class="bar">
            <file-icon :entry="current" :size="20" />
            <div class="title">
              <div class="name" :title="current.name">{{ current.name }}</div>
              <div class="sub">
                {{ formatSize(current.size) }}<template v-if="items.length > 1"> · {{ index + 1 }} / {{ items.length }}</template>
              </div>
            </div>
            <n-tooltip>
              <template #trigger>
                <n-button quaternary circle @click="openRaw">
                  <template #icon><n-icon :component="OpenOutline" /></template>
                </n-button>
              </template>
              在新标签页打开
            </n-tooltip>
            <n-tooltip>
              <template #trigger>
                <n-button quaternary circle @click="triggerDownload(current.path, current.name)">
                  <template #icon><n-icon :component="DownloadOutline" /></template>
                </n-button>
              </template>
              下载
            </n-tooltip>
            <n-tooltip>
              <template #trigger>
                <n-button quaternary circle @click="close">
                  <template #icon><n-icon :component="CloseOutline" size="22" /></template>
                </n-button>
              </template>
              关闭 (Esc)
            </n-tooltip>
          </header>

          <main class="stage">
            <image-preview v-if="current.kind === 'image'" :key="'i' + current.path" :entry="current" />
            <video-preview v-else-if="current.kind === 'video'" :key="'v' + current.path" :entry="current" />
            <audio-preview v-else-if="current.kind === 'audio'" :key="'a' + current.path" :entry="current" />
            <text-preview
              v-else-if="current.kind === 'text'"
              :key="'t' + current.path"
              :entry="current"
              :writable="writable"
              @dirty="(d: boolean) => (dirty = d)"
              @saved="(e: Entry) => emit('saved', e)"
            />
            <pdf-preview v-else-if="current.kind === 'pdf'" :key="'p' + current.path" :entry="current" />
            <other-preview v-else :key="'o' + current.path" :entry="current" />

            <button v-if="hasPrev" class="nav prev" title="上一个 (←)" @click="prev">
              <n-icon :component="ChevronBackOutline" size="28" />
            </button>
            <button v-if="hasNext" class="nav next" title="下一个 (→)" @click="next">
              <n-icon :component="ChevronForwardOutline" size="28" />
            </button>
          </main>
        </div>
      </transition>
    </n-config-provider>
  </teleport>
</template>

<style scoped>
.preview-overlay {
  position: fixed;
  inset: 0;
  z-index: 1800;
  background: #0c0c0e;
  color: rgba(255, 255, 255, 0.88);
  display: flex;
  flex-direction: column;
  color-scheme: dark;
}
.bar {
  height: 56px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px 0 18px;
}
.title {
  flex: 1;
  min-width: 0;
  margin-left: 6px;
}
.name {
  font-size: 15px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sub {
  font-size: 12px;
  opacity: 0.55;
}
.stage {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: stretch;
  justify-content: center;
}
.nav {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  width: 44px;
  height: 72px;
  border: 0;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.6;
  transition:
    opacity 0.15s,
    background 0.15s;
  z-index: 2;
}
.nav:hover {
  opacity: 1;
  background: rgba(255, 255, 255, 0.14);
}
.prev {
  left: 12px;
}
.next {
  right: 12px;
}
@media (max-width: 768px) {
  .nav {
    width: 34px;
    height: 56px;
  }
  .prev {
    left: 4px;
  }
  .next {
    right: 4px;
  }
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
