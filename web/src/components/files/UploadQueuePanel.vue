<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NCheckbox, NIcon, NModal, NProgress, NSpace, NTooltip, useThemeVars } from 'naive-ui'
import {
  AlertCircleOutline,
  CheckmarkCircle,
  ChevronDownOutline,
  ChevronUpOutline,
  CloseOutline,
  RefreshOutline,
  CloudUploadOutline,
} from '@vicons/ionicons5'
import { useUploadsStore, type UploadItem } from '@/stores/uploads'
import { formatSize } from '@/utils/format'

const store = useUploadsStore()
const vars = useThemeVars()

const applyAll = ref(false)
const currentConflict = computed<UploadItem | undefined>(() => store.conflicts[0])

const totalPercent = computed(() => {
  if (!store.totalBytes) return 100
  return Math.min(100, Math.round((store.loadedBytes / store.totalBytes) * 100))
})

const summary = computed(() => {
  const a = store.active.length
  if (a) return `正在上传 ${a} 个文件 · ${totalPercent.value}%`
  if (store.conflicts.length) return `${store.conflicts.length} 个文件等待处理冲突`
  const parts = [`已完成 ${store.finished} 个`]
  if (store.failed) parts.push(`失败 ${store.failed} 个`)
  return parts.join('，')
})

function percent(i: UploadItem): number {
  return i.size ? Math.min(100, Math.round((i.loaded / i.size) * 100)) : i.status === 'done' ? 100 : 0
}

const statusText: Record<UploadItem['status'], string> = {
  pending: '等待中',
  uploading: '',
  done: '完成',
  error: '失败',
  canceled: '已取消',
  conflict: '已存在',
  skipped: '已跳过',
}

function resolve(overwrite: boolean): void {
  const c = currentConflict.value
  if (!c) return
  store.resolveConflict(c.id, overwrite, applyAll.value)
  if (applyAll.value) applyAll.value = false
}

// 只渲染最近的 300 条，避免一次性上传大量文件时卡顿
const shown = computed(() => {
  const list = store.items
  return list.length > 300 ? list.slice(list.length - 300) : list
})
</script>

<template>
  <transition name="slide-up">
    <div
      v-if="store.panelOpen"
      class="upload-panel"
      :style="{ background: vars.cardColor, borderColor: vars.dividerColor, color: vars.textColor2 }"
    >
      <div class="head" @click="store.collapsed = !store.collapsed">
        <n-icon :component="CloudUploadOutline" size="18" :color="vars.primaryColor" />
        <span class="title">{{ summary }}</span>
        <n-button quaternary circle size="small" @click.stop="store.collapsed = !store.collapsed">
          <template #icon><n-icon :component="store.collapsed ? ChevronUpOutline : ChevronDownOutline" /></template>
        </n-button>
        <n-button quaternary circle size="small" @click.stop="store.close()">
          <template #icon><n-icon :component="CloseOutline" /></template>
        </n-button>
      </div>
      <n-progress
        v-if="store.active.length"
        type="line"
        :percentage="totalPercent"
        :show-indicator="false"
        :height="2"
        :border-radius="0"
      />
      <div v-show="!store.collapsed" class="body">
        <div v-for="item in shown" :key="item.id" class="item">
          <div class="row">
            <span class="name" :title="item.dest">{{ item.name }}</span>
            <span class="state">
              <template v-if="item.status === 'uploading'">
                {{ percent(item) }}%<template v-if="item.speed > 0"> · {{ formatSize(item.speed) }}/s</template>
              </template>
              <n-icon v-else-if="item.status === 'done'" :component="CheckmarkCircle" :color="vars.successColor" size="16" />
              <n-tooltip v-else-if="item.status === 'error'">
                <template #trigger>
                  <span class="err"><n-icon :component="AlertCircleOutline" size="14" /> 失败</span>
                </template>
                {{ item.error }}
              </n-tooltip>
              <template v-else>{{ statusText[item.status] }}</template>
            </span>
            <n-button
              v-if="item.status === 'error' || item.status === 'canceled' || item.status === 'skipped'"
              quaternary
              circle
              size="tiny"
              title="重试"
              @click="store.retry(item.id)"
            >
              <template #icon><n-icon :component="RefreshOutline" /></template>
            </n-button>
            <n-button
              v-if="item.status === 'uploading' || item.status === 'pending' || item.status === 'conflict'"
              quaternary
              circle
              size="tiny"
              title="取消"
              @click="store.cancel(item.id)"
            >
              <template #icon><n-icon :component="CloseOutline" /></template>
            </n-button>
          </div>
          <n-progress
            v-if="item.status === 'uploading'"
            type="line"
            :percentage="percent(item)"
            :show-indicator="false"
            :height="3"
          />
          <div class="size">{{ formatSize(item.size) }}</div>
        </div>
      </div>
      <div v-show="!store.collapsed" class="foot">
        <n-button v-if="store.failed" size="tiny" quaternary type="warning" @click="store.retryFailed()">全部重试</n-button>
        <n-button v-if="store.active.length" size="tiny" quaternary type="error" @click="store.cancelAll()">全部取消</n-button>
        <n-button size="tiny" quaternary @click="store.clearFinished()">清除已结束</n-button>
      </div>
    </div>
  </transition>

  <n-modal
    :show="!!currentConflict"
    preset="dialog"
    type="warning"
    title="文件已存在"
    :closable="false"
    :mask-closable="false"
  >
    <div v-if="currentConflict" class="conflict">
      <p>
        目标位置已存在同名文件：<br /><b class="path">{{ currentConflict.dest }}</b>
      </p>
      <n-checkbox v-if="store.conflicts.length > 1" v-model:checked="applyAll">
        对其余 {{ store.conflicts.length - 1 }} 个冲突（及本次后续冲突）执行相同操作
      </n-checkbox>
    </div>
    <template #action>
      <n-space>
        <n-button @click="resolve(false)">跳过</n-button>
        <n-button type="warning" @click="resolve(true)">覆盖</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.upload-panel {
  position: fixed;
  right: 16px;
  bottom: 16px;
  width: 380px;
  max-width: calc(100vw - 24px);
  border: 1px solid;
  border-radius: 10px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.18);
  z-index: 1500;
  overflow: hidden;
}
@media (max-width: 768px) {
  .upload-panel {
    right: 12px;
    bottom: 12px;
  }
}
.head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 8px 8px 14px;
  cursor: pointer;
}
.title {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
}
.body {
  max-height: 300px;
  overflow-y: auto;
  padding: 2px 0;
}
.item {
  padding: 6px 14px;
}
.row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}
.name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.state {
  font-size: 12px;
  opacity: 0.75;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-variant-numeric: tabular-nums;
}
.err {
  color: #d03050;
  display: inline-flex;
  align-items: center;
  gap: 2px;
}
.size {
  font-size: 11px;
  opacity: 0.5;
  margin-top: 1px;
}
.foot {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
  padding: 4px 8px 8px;
}
.conflict .path {
  word-break: break-all;
}
.slide-up-enter-active,
.slide-up-leave-active {
  transition:
    transform 0.2s,
    opacity 0.2s;
}
.slide-up-enter-from,
.slide-up-leave-to {
  transform: translateY(20px);
  opacity: 0;
}
</style>
