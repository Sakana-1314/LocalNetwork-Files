<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { NButton, NInput, NModal, NSpace, type InputInst } from 'naive-ui'

const props = withDefaults(
  defineProps<{
    title: string
    placeholder?: string
    defaultValue?: string
    confirmText?: string
    /** 返回错误信息表示校验失败 */
    validate?: (v: string) => string | null
    /** 选中文件名主体（不含扩展名） */
    selectStem?: boolean
    onConfirm: (v: string) => Promise<boolean | void> | boolean | void
  }>(),
  { placeholder: '', defaultValue: '', confirmText: '确定', validate: undefined, selectStem: false },
)
const show = defineModel<boolean>('show', { default: false })

const value = ref('')
const error = ref<string | null>(null)
const loading = ref(false)
const inputRef = ref<InputInst | null>(null)

watch(show, async (v) => {
  if (!v) return
  value.value = props.defaultValue
  error.value = null
  await nextTick()
  setTimeout(() => {
    inputRef.value?.focus()
    const el = inputRef.value?.inputElRef
    if (el) {
      const dot = props.selectStem ? value.value.lastIndexOf('.') : -1
      el.setSelectionRange(0, dot > 0 ? dot : value.value.length)
    }
  }, 50)
})

async function submit(): Promise<void> {
  const v = value.value.trim()
  error.value = props.validate ? props.validate(v) : v ? null : '不能为空'
  if (error.value) return
  loading.value = true
  try {
    const r = await props.onConfirm(v)
    if (r !== false) show.value = false
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <n-modal v-model:show="show" preset="card" :title="title" style="width: 420px; max-width: calc(100vw - 32px)">
    <n-input
      ref="inputRef"
      v-model:value="value"
      :placeholder="placeholder"
      :status="error ? 'error' : undefined"
      @keyup.enter="submit"
      @input="error = null"
    />
    <div v-if="error" class="err">{{ error }}</div>
    <template #footer>
      <n-space justify="end">
        <n-button @click="show = false">取消</n-button>
        <n-button type="primary" :loading="loading" @click="submit">{{ confirmText }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.err {
  color: #d03050;
  font-size: 13px;
  margin-top: 6px;
}
</style>
