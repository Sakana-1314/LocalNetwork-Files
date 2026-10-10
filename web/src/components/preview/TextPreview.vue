<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NIcon, NInput, NSpace, NSpin, NSwitch, useMessage } from 'naive-ui'
import { CreateOutline, SaveOutline } from '@vicons/ionicons5'
import type { Entry } from '@/api/types'
import { fsApi } from '@/api/fs'

const props = defineProps<{ entry: Entry; writable: boolean }>()
const emit = defineEmits<{ saved: [entry: Entry]; dirty: [dirty: boolean] }>()
const message = useMessage()

const content = ref('')
const truncated = ref(false)
const loading = ref(true)
const failed = ref(false)
const editing = ref(false)
const draft = ref('')
const saving = ref(false)
const wrap = ref(true)

const dirty = computed(() => editing.value && draft.value !== content.value)
watch(dirty, (d) => emit('dirty', d))

const canEdit = computed(() => props.writable && props.entry.writable !== false && !truncated.value)
const lineCount = computed(() => (content.value ? content.value.split('\n').length : 0))

async function load(): Promise<void> {
  loading.value = true
  failed.value = false
  try {
    const r = await fsApi.text(props.entry.path)
    content.value = r.content ?? ''
    truncated.value = !!r.truncated
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}

function startEdit(): void {
  draft.value = content.value
  editing.value = true
}

function cancelEdit(): void {
  editing.value = false
  draft.value = ''
}

async function save(): Promise<void> {
  if (!editing.value || saving.value) return
  saving.value = true
  try {
    const blob = new Blob([draft.value], { type: 'text/plain;charset=utf-8' })
    const entry = await fsApi.writeFile(props.entry.path, blob, true)
    content.value = draft.value
    editing.value = false
    message.success('已保存')
    emit('saved', entry ?? props.entry)
  } catch {
    /* 错误已提示 */
  } finally {
    saving.value = false
  }
}

function onKeydown(e: KeyboardEvent): void {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    void save()
  }
  if (e.key === 'Tab') {
    // 在编辑器中插入制表符
    e.preventDefault()
    const ta = e.target as HTMLTextAreaElement
    const s = ta.selectionStart
    const end = ta.selectionEnd
    draft.value = draft.value.slice(0, s) + '\t' + draft.value.slice(end)
    requestAnimationFrame(() => ta.setSelectionRange(s + 1, s + 1))
  }
}

onMounted(load)
</script>

<template>
  <div class="text-preview">
    <div class="bar">
      <span class="info">{{ lineCount }} 行</span>
      <n-space align="center" :size="10">
        <span class="wrap-switch"><n-switch v-model:value="wrap" size="small" /> 自动换行</span>
        <template v-if="canEdit">
          <template v-if="editing">
            <n-button size="small" @click="cancelEdit">取消</n-button>
            <n-button size="small" type="primary" :loading="saving" :disabled="!dirty" @click="save">
              <template #icon><n-icon :component="SaveOutline" /></template>
              保存
            </n-button>
          </template>
          <n-button v-else size="small" secondary @click="startEdit">
            <template #icon><n-icon :component="CreateOutline" /></template>
            编辑
          </n-button>
        </template>
      </n-space>
    </div>
    <n-alert v-if="truncated" type="warning" :bordered="false" class="warn">
      文件过大，仅显示前 4 MiB 内容，无法在线编辑。
    </n-alert>
    <div class="body">
      <div v-if="loading" class="center"><n-spin /></div>
      <div v-else-if="failed" class="center">加载失败</div>
      <n-input
        v-else-if="editing"
        v-model:value="draft"
        type="textarea"
        class="editor mono"
        :class="{ nowrap: !wrap }"
        :resizable="false"
        placeholder=""
        @keydown="onKeydown"
      />
      <pre v-else class="code mono" :class="{ nowrap: !wrap }">{{ content }}</pre>
    </div>
  </div>
</template>

<style scoped>
.text-preview {
  width: min(1100px, 100%);
  height: 100%;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  padding: 0 16px 16px;
  min-height: 0;
}
.bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-bottom: 8px;
  gap: 8px;
}
.info {
  font-size: 12px;
  opacity: 0.6;
}
.wrap-switch {
  font-size: 13px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  opacity: 0.85;
}
.warn {
  margin-bottom: 8px;
}
.body {
  flex: 1;
  min-height: 0;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.09);
  overflow: hidden;
  display: flex;
}
.center {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}
.code {
  flex: 1;
  margin: 0;
  padding: 14px 16px;
  overflow: auto;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  tab-size: 4;
}
.code.nowrap {
  white-space: pre;
  word-break: normal;
}
.editor {
  flex: 1;
  height: 100%;
  font-size: 13px;
}
.editor :deep(textarea) {
  font-family: 'JetBrains Mono', 'SF Mono', Menlo, Consolas, monospace !important;
  line-height: 1.6 !important;
  tab-size: 4;
}
.editor.nowrap :deep(textarea) {
  white-space: pre;
  overflow-x: auto;
}
.editor :deep(.n-input-wrapper) {
  height: 100%;
}
</style>
