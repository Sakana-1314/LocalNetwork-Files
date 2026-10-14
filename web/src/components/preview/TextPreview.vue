<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NIcon, NInput, NRadioButton, NRadioGroup, NSpace, NSpin, NSwitch, useMessage } from 'naive-ui'
import { CreateOutline, SaveOutline } from '@vicons/ionicons5'
import type { Entry } from '@/api/types'
import { fsApi } from '@/api/fs'
import { isMarkdown, renderMarkdown } from '@/utils/markdown'

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

// Markdown 文件默认显示渲染结果，可切换到源码
const markdown = computed(() => isMarkdown(props.entry.name))
const view = ref<'rendered' | 'source'>('rendered')
const rendered = computed(() => (markdown.value && view.value === 'rendered' ? renderMarkdown(content.value, props.entry.path) : ''))

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
        <n-radio-group v-if="markdown && !editing" v-model:value="view" size="small">
          <n-radio-button value="rendered">预览</n-radio-button>
          <n-radio-button value="source">源码</n-radio-button>
        </n-radio-group>
        <span v-if="!markdown || editing || view === 'source'" class="wrap-switch"><n-switch v-model:value="wrap" size="small" /> 自动换行</span>
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
      <!-- 渲染结果已禁用原始 HTML，见 utils/markdown.ts -->
      <!-- eslint-disable-next-line vue/no-v-html -->
      <article v-else-if="rendered" class="markdown-body" v-html="rendered" />
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
.markdown-body {
  flex: 1;
  overflow: auto;
  padding: 20px 28px;
  font-size: 15px;
  line-height: 1.75;
  word-wrap: break-word;
}
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) {
  margin: 1.4em 0 0.6em;
  line-height: 1.3;
  font-weight: 600;
}
.markdown-body :deep(h1) {
  font-size: 1.9em;
  padding-bottom: 0.3em;
  border-bottom: 1px solid rgba(255, 255, 255, 0.12);
}
.markdown-body :deep(h2) {
  font-size: 1.5em;
  padding-bottom: 0.3em;
  border-bottom: 1px solid rgba(255, 255, 255, 0.12);
}
.markdown-body :deep(h3) {
  font-size: 1.25em;
}
.markdown-body :deep(> :first-child) {
  margin-top: 0;
}
.markdown-body :deep(p),
.markdown-body :deep(ul),
.markdown-body :deep(ol),
.markdown-body :deep(blockquote),
.markdown-body :deep(table),
.markdown-body :deep(pre) {
  margin: 0 0 1em;
}
.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 2em;
}
.markdown-body :deep(a) {
  color: #70c0e8;
  text-decoration: none;
}
.markdown-body :deep(a:hover) {
  text-decoration: underline;
}
.markdown-body :deep(code) {
  font-family: 'JetBrains Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 0.88em;
  padding: 0.15em 0.4em;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.08);
}
.markdown-body :deep(pre) {
  padding: 12px 16px;
  overflow: auto;
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.3);
  line-height: 1.5;
}
.markdown-body :deep(pre code) {
  padding: 0;
  background: none;
  font-size: 13px;
}
.markdown-body :deep(blockquote) {
  padding: 0 1em;
  border-left: 4px solid rgba(255, 255, 255, 0.2);
  opacity: 0.8;
}
.markdown-body :deep(table) {
  border-collapse: collapse;
  display: block;
  overflow: auto;
  max-width: 100%;
}
.markdown-body :deep(th),
.markdown-body :deep(td) {
  padding: 6px 12px;
  border: 1px solid rgba(255, 255, 255, 0.15);
}
.markdown-body :deep(th) {
  background: rgba(255, 255, 255, 0.05);
}
.markdown-body :deep(img) {
  max-width: 100%;
  border-radius: 4px;
}
.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid rgba(255, 255, 255, 0.15);
  margin: 1.5em 0;
}
@media (max-width: 640px) {
  .markdown-body {
    padding: 14px 16px;
  }
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
