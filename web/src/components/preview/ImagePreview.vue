<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NButtonGroup, NIcon, NSpin, NTooltip } from 'naive-ui'
import { AddOutline, ExpandOutline, RefreshOutline, RemoveOutline } from '@vicons/ionicons5'
import type { Entry } from '@/api/types'
import { fsApi } from '@/api/fs'
import { mediaApi } from '@/api/media'

const props = defineProps<{ entry: Entry }>()

const src = ref('')
const hiRes = ref(false)
const loading = ref(true)
const failed = ref(false)
const scale = ref(1)
const rotate = ref(0)
const tx = ref(0)
const ty = ref(0)
const box = ref<HTMLElement | null>(null)

let preloader: HTMLImageElement | null = null

function reset(): void {
  scale.value = 1
  rotate.value = 0
  tx.value = 0
  ty.value = 0
}

function load(): void {
  reset()
  failed.value = false
  loading.value = true
  hiRes.value = false
  const raw = fsApi.rawUrl(props.entry.path)
  // 先显示 1024 预览图，原图加载完成后替换
  src.value = props.entry.thumb ? mediaApi.thumbUrl(props.entry, 1024) : raw
  if (preloader) preloader.onload = preloader.onerror = null
  if (props.entry.thumb) {
    const img = new Image()
    preloader = img
    img.onload = () => {
      if (preloader !== img) return
      src.value = raw
      hiRes.value = true
      loading.value = false
    }
    img.onerror = () => {
      if (preloader === img) loading.value = false
    }
    img.src = raw
  }
}

watch(() => props.entry.path, load, { immediate: true })
onBeforeUnmount(() => {
  if (preloader) preloader.onload = preloader.onerror = null
  preloader = null
})

function onImgLoad(): void {
  if (!props.entry.thumb) loading.value = false
}

function onImgError(): void {
  // 预览图失败则直接加载原图
  const raw = fsApi.rawUrl(props.entry.path)
  if (src.value !== raw) src.value = raw
  else {
    failed.value = true
    loading.value = false
  }
}

function zoomAt(factor: number, cx?: number, cy?: number): void {
  const old = scale.value
  const next = Math.min(20, Math.max(0.1, old * factor))
  if (cx !== undefined && cy !== undefined && box.value) {
    const r = box.value.getBoundingClientRect()
    const px = cx - (r.left + r.width / 2)
    const py = cy - (r.top + r.height / 2)
    tx.value = px - (next / old) * (px - tx.value)
    ty.value = py - (next / old) * (py - ty.value)
  } else {
    tx.value *= next / old
    ty.value *= next / old
  }
  scale.value = next
  if (next <= 1.001 && next >= 0.999) {
    tx.value = 0
    ty.value = 0
  }
}

function onWheel(e: WheelEvent): void {
  e.preventDefault()
  const factor = Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0018))
  zoomAt(factor, e.clientX, e.clientY)
}

let dragging = false
let sx = 0
let sy = 0
let moved = false
function onPointerDown(e: PointerEvent): void {
  if (e.button !== 0) return
  dragging = true
  moved = false
  sx = e.clientX - tx.value
  sy = e.clientY - ty.value
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}
function onPointerMove(e: PointerEvent): void {
  if (!dragging) return
  const nx = e.clientX - sx
  const ny = e.clientY - sy
  if (Math.abs(nx - tx.value) + Math.abs(ny - ty.value) > 1) moved = true
  tx.value = nx
  ty.value = ny
}
function onPointerUp(): void {
  dragging = false
}
function onDblClick(e: MouseEvent): void {
  if (moved) return
  if (scale.value > 1.01) reset()
  else zoomAt(2.5, e.clientX, e.clientY)
}

const transform = computed(
  () => `translate(${tx.value}px, ${ty.value}px) scale(${scale.value}) rotate(${rotate.value}deg)`,
)
const percent = computed(() => Math.round(scale.value * 100))
</script>

<template>
  <div
    ref="box"
    class="image-preview"
    @wheel="onWheel"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
    @pointercancel="onPointerUp"
    @dblclick="onDblClick"
  >
    <img
      v-if="!failed"
      :src="src"
      :alt="entry.name"
      draggable="false"
      :style="{ transform }"
      @load="onImgLoad"
      @error="onImgError"
    />
    <div v-else class="failed">图片加载失败</div>
    <div v-if="loading && !failed" class="loading"><n-spin size="small" /> <span>{{ hiRes ? '' : '加载原图…' }}</span></div>
    <div class="tools" @pointerdown.stop @dblclick.stop>
      <n-button-group size="small">
        <n-tooltip>
          <template #trigger>
            <n-button secondary @click="zoomAt(1 / 1.4)"><template #icon><n-icon :component="RemoveOutline" /></template></n-button>
          </template>
          缩小
        </n-tooltip>
        <n-button secondary class="pct" @click="reset">{{ percent }}%</n-button>
        <n-tooltip>
          <template #trigger>
            <n-button secondary @click="zoomAt(1.4)"><template #icon><n-icon :component="AddOutline" /></template></n-button>
          </template>
          放大
        </n-tooltip>
        <n-tooltip>
          <template #trigger>
            <n-button secondary @click="rotate = (rotate + 90) % 360"><template #icon><n-icon :component="RefreshOutline" /></template></n-button>
          </template>
          旋转
        </n-tooltip>
        <n-tooltip>
          <template #trigger>
            <n-button secondary @click="reset"><template #icon><n-icon :component="ExpandOutline" /></template></n-button>
          </template>
          适应窗口
        </n-tooltip>
      </n-button-group>
    </div>
  </div>
</template>

<style scoped>
.image-preview {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  touch-action: none;
  cursor: grab;
}
.image-preview:active {
  cursor: grabbing;
}
img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  user-select: none;
  transform-origin: center center;
  transition: transform 0.06s linear;
  will-change: transform;
}
.loading {
  position: absolute;
  left: 16px;
  bottom: 16px;
  display: flex;
  align-items: center;
  gap: 6px;
  color: rgba(255, 255, 255, 0.75);
  font-size: 12px;
}
.failed {
  color: rgba(255, 255, 255, 0.7);
}
.tools {
  position: absolute;
  bottom: 16px;
  left: 50%;
  transform: translateX(-50%);
  cursor: default;
}
.pct {
  min-width: 64px;
  font-variant-numeric: tabular-nums;
}
</style>
