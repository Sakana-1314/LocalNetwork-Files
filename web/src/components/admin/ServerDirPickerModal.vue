<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NEmpty, NIcon, NInput, NModal, NSpace, NSpin } from 'naive-ui'
import { ArrowUpOutline, Folder, HardwareChipOutline } from '@vicons/ionicons5'
import { adminApi } from '@/api/admin'

const props = defineProps<{ initialPath?: string }>()
const show = defineModel<boolean>('show', { default: false })
const emit = defineEmits<{ confirm: [path: string] }>()

const path = ref('')
const parent = ref('')
const dirs = ref<string[]>([])
const loading = ref(false)
const manual = ref('')

async function browse(p?: string): Promise<void> {
  loading.value = true
  try {
    const r = await adminApi.browse(p || undefined)
    path.value = r.path
    parent.value = r.parent
    dirs.value = r.dirs.slice().sort((a, b) => a.localeCompare(b, 'zh-CN', { numeric: true }))
    manual.value = r.path
  } catch {
    // 初始路径无效时回到根
    if (p) await browse()
  } finally {
    loading.value = false
  }
}

watch(show, (v) => {
  if (v) void browse(props.initialPath || undefined)
})

function label(full: string): string {
  const trimmed = full.replace(/[\\/]+$/, '')
  const i = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  const name = i >= 0 ? trimmed.slice(i + 1) : trimmed
  return name || full
}

const isRoot = computed(() => !path.value)

function confirm(): void {
  if (!path.value) return
  emit('confirm', path.value)
  show.value = false
}
</script>

<template>
  <n-modal v-model:show="show" preset="card" title="选择服务器目录" style="width: 560px; max-width: calc(100vw - 24px)">
    <div class="bar">
      <n-button :disabled="isRoot || loading" @click="browse(parent)">
        <template #icon><n-icon :component="ArrowUpOutline" /></template>
        上一级
      </n-button>
      <n-input v-model:value="manual" placeholder="输入绝对路径后回车" class="mono" @keyup.enter="browse(manual)" />
    </div>
    <div class="list">
      <n-spin :show="loading">
        <n-empty v-if="!loading && !dirs.length" description="没有子目录" style="padding: 40px 0" />
        <div v-for="d in dirs" :key="d" class="dir" :title="d" @click="browse(d)">
          <n-icon :component="isRoot ? HardwareChipOutline : Folder" :color="isRoot ? '#18a058' : '#f2b230'" size="18" />
          <span>{{ isRoot ? d : label(d) }}</span>
        </div>
      </n-spin>
    </div>
    <div class="current">当前：<code>{{ path || '（请选择磁盘或根目录）' }}</code></div>
    <template #footer>
      <n-space justify="end">
        <n-button @click="show = false">取消</n-button>
        <n-button type="primary" :disabled="!path" @click="confirm">选择此目录</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.bar {
  display: flex;
  gap: 8px;
  margin-bottom: 10px;
}
.bar .n-input {
  flex: 1;
  min-width: 0;
}
.list {
  height: 340px;
  max-height: 50vh;
  overflow-y: auto;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
  padding: 4px;
}
.dir {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 14px;
}
.dir:hover {
  background: rgba(128, 128, 128, 0.12);
}
.dir span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.current {
  margin-top: 10px;
  font-size: 13px;
  opacity: 0.75;
  word-break: break-all;
}
</style>
