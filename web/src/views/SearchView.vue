<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NEmpty, NIcon, NInput, NSkeleton, NTab, NTabs, useThemeVars } from 'naive-ui'
import { CloseCircle, FolderOpenOutline, SearchOutline } from '@vicons/ionicons5'
import type { Kind, SearchResponse, SearchResult } from '@/api/types'
import { fsApi } from '@/api/fs'
import { isAbortError } from '@/api/http'
import FileIcon from '@/components/common/FileIcon.vue'
import HighlightName from '@/components/common/HighlightName.vue'
import FolderPickerModal from '@/components/files/FolderPickerModal.vue'
import { useDebounceFn } from '@/composables/useDebounce'
import { filesRoute, normalizePath, parentPath } from '@/utils/path'
import { formatRelative, formatSize } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const vars = useThemeVars()

type KindTab = 'all' | Kind
const tabs: { key: KindTab; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'dir', label: '文件夹' },
  { key: 'image', label: '图片' },
  { key: 'video', label: '视频' },
  { key: 'audio', label: '音频' },
  { key: 'text', label: '文本' },
  { key: 'pdf', label: 'PDF' },
  { key: 'archive', label: '压缩包' },
  { key: 'other', label: '其他' },
]

const q = ref(typeof route.query.q === 'string' ? route.query.q : '')
const kind = ref<KindTab>(typeof route.query.kind === 'string' ? (route.query.kind as KindTab) : 'all')
const scope = ref(typeof route.query.path === 'string' ? normalizePath(route.query.path) : '/')

const pickerOpen = ref(false)
function onScopePicked(path: string): void {
  scope.value = normalizePath(path)
}

const result = ref<SearchResponse | null>(null)
const loading = ref(false)
const error = ref('')
let ctl: AbortController | null = null

async function run(): Promise<void> {
  const query = q.value.trim()
  ctl?.abort()
  if (!query) {
    result.value = null
    loading.value = false
    error.value = ''
    return
  }
  const c = new AbortController()
  ctl = c
  loading.value = true
  error.value = ''
  try {
    const r = await fsApi.search(
      { q: query, kind: kind.value === 'all' ? undefined : kind.value, path: scope.value !== '/' ? scope.value : undefined, limit: 200 },
      { signal: c.signal, silent: true },
    )
    if (ctl === c) result.value = r
  } catch (e) {
    if (isAbortError(e)) return
    if (ctl === c) error.value = e instanceof Error ? e.message : '搜索失败'
  } finally {
    if (ctl === c) loading.value = false
  }
}

const debounced = useDebounceFn(() => {
  syncQuery()
  void run()
}, 250)

function syncQuery(): void {
  const query: Record<string, string> = {}
  if (q.value.trim()) query.q = q.value.trim()
  if (kind.value !== 'all') query.kind = kind.value
  if (scope.value !== '/') query.path = scope.value
  void router.replace({ name: 'search', query })
}

watch(q, () => debounced())
watch([kind, scope], () => {
  debounced.cancel()
  syncQuery()
  void run()
})

// 顶部搜索框跳转过来时同步
watch(
  () => route.query.q,
  (v) => {
    const s = typeof v === 'string' ? v : ''
    if (s.trim() !== q.value.trim()) q.value = s
  },
)

const inputRef = ref<InstanceType<typeof NInput> | null>(null)
onMounted(() => {
  inputRef.value?.focus()
  if (q.value.trim()) void run()
})

function openResult(r: SearchResult): void {
  if (r.is_dir) void router.push(filesRoute(r.path))
  else void router.push(filesRoute(parentPath(r.path), { preview: r.name }))
}

function openParent(r: SearchResult, e: MouseEvent): void {
  e.stopPropagation()
  void router.push(filesRoute(parentPath(r.path), { preview: r.name }))
}

const summary = computed(() => {
  const r = result.value
  if (!r) return ''
  const shown = r.results.length
  const t = `${Math.round(r.took_ms * 10) / 10} ms`
  return r.total > shown ? `共 ${r.total} 条结果，显示前 ${shown} 条 · 耗时 ${t}` : `共 ${r.total} 条结果 · 耗时 ${t}`
})
</script>

<template>
  <div class="page search-page">
    <n-input
      ref="inputRef"
      v-model:value="q"
      size="large"
      round
      clearable
      placeholder="输入文件名关键字，支持模糊匹配（如 “dsc2024” 可匹配 DSC_2024.jpg）"
      @keyup.enter="debounced.cancel(), syncQuery(), run()"
    >
      <template #prefix><n-icon :component="SearchOutline" /></template>
    </n-input>

    <div class="filters">
      <n-tabs v-model:value="kind" type="segment" size="small" class="kind-tabs">
        <n-tab v-for="t in tabs" :key="t.key" :name="t.key">{{ t.label }}</n-tab>
      </n-tabs>
      <n-button class="scope-btn" :type="scope !== '/' ? 'primary' : 'default'" secondary @click="pickerOpen = true">
        <template #icon><n-icon :component="FolderOpenOutline" /></template>
        <span class="scope-text">{{ scope === '/' ? '全部目录' : scope }}</span>
      </n-button>
      <n-button v-if="scope !== '/'" quaternary size="small" @click="scope = '/'">清除范围</n-button>
    </div>

    <n-alert v-if="result?.indexing" type="info" :bordered="false" class="mb">
      索引构建中，搜索结果可能不完整。
    </n-alert>
    <n-alert v-if="error" type="error" :bordered="false" class="mb">{{ error }}</n-alert>

    <div v-if="summary" class="summary">{{ summary }}</div>

    <div v-if="loading && !result" class="list">
      <div v-for="i in 6" :key="i" class="sk"><n-skeleton circle width="32px" height="32px" /><n-skeleton text style="width: 60%" /></div>
    </div>
    <n-empty v-else-if="!q.trim()" description="输入关键字开始搜索" class="empty">
      <template #icon><n-icon :component="SearchOutline" /></template>
    </n-empty>
    <n-empty v-else-if="result && !result.results.length" description="没有找到匹配的文件" class="empty">
      <template #icon><n-icon :component="CloseCircle" /></template>
    </n-empty>
    <div v-else-if="result" class="list" :class="{ dim: loading }" :style="{ '--hover': vars.hoverColor, '--border': vars.dividerColor }">
      <div v-for="r in result.results" :key="r.path" class="item" @click="openResult(r)">
        <file-icon :entry="r" :size="30" />
        <div class="main">
          <div class="name"><highlight-name :name="r.name" :matches="r.matches" /></div>
          <a class="parent" :title="'打开所在文件夹 ' + parentPath(r.path)" @click="openParent(r, $event)">{{ parentPath(r.path) }}</a>
        </div>
        <div class="meta">
          <div>{{ r.is_dir ? '文件夹' : formatSize(r.size) }}</div>
          <div class="time">{{ formatRelative(r.mod_time) }}</div>
        </div>
      </div>
    </div>

    <folder-picker-modal
      v-model:show="pickerOpen"
      title="选择搜索范围"
      confirm-text="在此目录中搜索"
      :initial-path="scope"
      :require-writable="false"
      @confirm="onScopePicked"
    />
  </div>
</template>

<style scoped>
.search-page {
  max-width: 980px;
}
.filters {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 14px 0;
  flex-wrap: wrap;
}
.kind-tabs {
  width: auto;
  max-width: 100%;
  overflow-x: auto;
}
.kind-tabs :deep(.n-tabs-rail) {
  min-width: 560px;
}
.scope-btn {
  max-width: 320px;
}
.scope-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mb {
  margin-bottom: 12px;
}
.summary {
  font-size: 12px;
  opacity: 0.6;
  margin: 0 4px 8px;
}
.empty {
  padding: 80px 0;
}
.list {
  display: flex;
  flex-direction: column;
  transition: opacity 0.15s;
}
.list.dim {
  opacity: 0.6;
}
.sk {
  display: flex;
  gap: 12px;
  align-items: center;
  padding: 12px;
}
.item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 10px 12px;
  border-radius: 8px;
  cursor: pointer;
  border-bottom: 1px solid var(--border);
}
.item:hover {
  background: var(--hover);
}
.main {
  flex: 1;
  min-width: 0;
}
.name {
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.parent {
  display: inline-block;
  max-width: 100%;
  font-size: 12px;
  opacity: 0.55;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}
.parent:hover {
  opacity: 0.9;
  text-decoration: underline;
}
.meta {
  text-align: right;
  font-size: 12px;
  opacity: 0.7;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.time {
  opacity: 0.75;
}
</style>
