<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { NButton, NEmpty, NIcon, NModal, NSpace, NSpin, NTag, NTree, useThemeVars, type TreeOption } from 'naive-ui'
import { Folder, ServerOutline } from '@vicons/ionicons5'
import { fsApi } from '@/api/fs'
import { isAncestor } from '@/utils/path'

const props = withDefaults(
  defineProps<{
    title?: string
    confirmText?: string
    /** 初始展开/选中的目录 */
    initialPath?: string
    /** 不允许选择的路径（例如被移动的目录本身及其子目录） */
    exclude?: string[]
    /** 是否要求目标目录可写（复制/移动为 true，搜索范围等只读场景为 false） */
    requireWritable?: boolean
  }>(),
  { title: '选择目标文件夹', confirmText: '确定', initialPath: '/', exclude: () => [], requireWritable: true },
)
const show = defineModel<boolean>('show', { default: false })
const emit = defineEmits<{ confirm: [path: string] }>()
const vars = useThemeVars()

interface DirNode extends TreeOption {
  key: string
  label: string
  writable: boolean
  depth: number
  children?: DirNode[]
}

const nodes = ref<DirNode[]>([])
const loadingRoot = ref(false)
const selectedKey = ref<string | null>(null)
const expandedKeys = ref<string[]>([])
const writableMap = new Map<string, boolean>()

async function loadChildren(path: string, depth: number): Promise<DirNode[]> {
  const r = await fsApi.list(path)
  return r.entries
    .filter((e) => e.is_dir)
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-CN', { numeric: true }))
    .map((e) => {
      writableMap.set(e.path, e.writable)
      return { key: e.path, label: e.name, writable: e.writable, depth, isLeaf: false }
    })
}

watch(show, async (v) => {
  if (!v) return
  selectedKey.value = null
  expandedKeys.value = []
  nodes.value = []
  loadingRoot.value = true
  try {
    nodes.value = await loadChildren('/', 0)
    // 自动展开到初始目录
    const segs = props.initialPath.split('/').filter(Boolean)
    let list = nodes.value
    let cur = ''
    for (const s of segs) {
      cur += '/' + s
      const node = list.find((n) => n.key === cur)
      if (!node) break
      node.children = await loadChildren(cur, node.depth + 1)
      if (!node.children.length) node.isLeaf = true
      expandedKeys.value.push(cur)
      list = node.children
    }
    if (props.initialPath !== '/' && writableMap.has(props.initialPath)) selectedKey.value = props.initialPath
  } catch {
    /* 错误已提示 */
  } finally {
    loadingRoot.value = false
  }
})

async function onLoad(node: TreeOption): Promise<void> {
  const n = node as DirNode
  try {
    n.children = await loadChildren(n.key, n.depth + 1)
    if (!n.children.length) n.isLeaf = true
  } catch {
    n.children = []
    n.isLeaf = true
  }
}

function isExcluded(path: string): boolean {
  return props.exclude.some((x) => isAncestor(x, path))
}

const canConfirm = computed(() => {
  const k = selectedKey.value
  if (!k || isExcluded(k)) return false
  return props.requireWritable ? writableMap.get(k) === true : true
})

const hint = computed(() => {
  const k = selectedKey.value
  if (!k) return '请选择一个目标文件夹'
  if (isExcluded(k)) return '不能选择被操作的文件夹本身或其子文件夹'
  if (props.requireWritable && !writableMap.get(k)) return '该文件夹为只读，无法写入'
  return k
})

function renderPrefix({ option }: { option: TreeOption }) {
  const n = option as DirNode
  return h(NIcon, { color: n.depth === 0 ? vars.value.primaryColor : '#f2b230', size: 18 }, { default: () => h(n.depth === 0 ? ServerOutline : Folder) })
}

function renderSuffix({ option }: { option: TreeOption }) {
  const n = option as DirNode
  if (!props.requireWritable || n.writable) return null
  return h(NTag, { size: 'tiny', bordered: false }, { default: () => '只读' })
}

function confirm(): void {
  if (!canConfirm.value || !selectedKey.value) return
  emit('confirm', selectedKey.value)
  show.value = false
}
</script>

<template>
  <n-modal v-model:show="show" preset="card" :title="title" style="width: 520px; max-width: calc(100vw - 24px)">
    <div class="tree-box">
      <n-spin :show="loadingRoot">
        <n-empty v-if="!loadingRoot && !nodes.length" description="没有可用的目录" style="padding: 40px 0" />
        <n-tree
          v-else
          block-line
          :data="nodes"
          :on-load="onLoad"
          :selected-keys="selectedKey ? [selectedKey] : []"
          :expanded-keys="expandedKeys"
          :render-prefix="renderPrefix"
          :render-suffix="renderSuffix"
          expand-on-click
          @update:selected-keys="(k: Array<string | number>) => (selectedKey = (k[0] as string) ?? null)"
          @update:expanded-keys="(k: Array<string | number>) => (expandedKeys = k as string[])"
        />
      </n-spin>
    </div>
    <div class="hint" :class="{ ok: canConfirm }">{{ hint }}</div>
    <template #footer>
      <n-space justify="end">
        <n-button @click="show = false">取消</n-button>
        <n-button type="primary" :disabled="!canConfirm" @click="confirm">{{ confirmText }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.tree-box {
  height: 360px;
  max-height: 55vh;
  overflow: auto;
  border-radius: 6px;
}
.hint {
  margin-top: 10px;
  font-size: 13px;
  opacity: 0.65;
  word-break: break-all;
}
.hint.ok {
  opacity: 0.9;
}
</style>
