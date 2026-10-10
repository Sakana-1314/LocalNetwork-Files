<script setup lang="ts">
import { reactive, ref } from 'vue'
import { NButton, NForm, NFormItem, NInput, NSpace, useMessage, type FormInst, type FormRules } from 'naive-ui'
import { authApi } from '@/api/auth'

defineProps<{ showCancel?: boolean }>()
const emit = defineEmits<{ done: []; cancel: [] }>()

const message = useMessage()
const formRef = ref<FormInst | null>(null)
const loading = ref(false)
const form = reactive({ old: '', next: '', confirm: '' })

const rules: FormRules = {
  old: { required: true, message: '请输入当前密码', trigger: 'blur' },
  next: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 6, message: '新密码至少 6 位', trigger: 'blur' },
  ],
  confirm: {
    required: true,
    trigger: ['blur', 'input'],
    validator: (_r: unknown, v: string) => (v && v === form.next ? true : new Error('两次输入的密码不一致')),
  },
}

function reset(): void {
  Object.assign(form, { old: '', next: '', confirm: '' })
  formRef.value?.restoreValidation()
}

async function submit(): Promise<void> {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  loading.value = true
  try {
    await authApi.changePassword(form.old, form.next)
    message.success('密码已修改')
    reset()
    emit('done')
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}

defineExpose({ reset })
</script>

<template>
  <n-form ref="formRef" :model="form" :rules="rules" label-placement="top" @submit.prevent="submit">
    <n-form-item label="当前密码" path="old">
      <n-input v-model:value="form.old" type="password" show-password-on="click" autocomplete="current-password" placeholder="当前密码" />
    </n-form-item>
    <n-form-item label="新密码" path="next">
      <n-input v-model:value="form.next" type="password" show-password-on="click" autocomplete="new-password" placeholder="至少 6 位" />
    </n-form-item>
    <n-form-item label="确认新密码" path="confirm">
      <n-input
        v-model:value="form.confirm"
        type="password"
        show-password-on="click"
        autocomplete="new-password"
        placeholder="再次输入新密码"
      />
    </n-form-item>
    <n-space :justify="showCancel ? 'end' : 'start'">
      <n-button v-if="showCancel" @click="emit('cancel')">取消</n-button>
      <n-button type="primary" :loading="loading" attr-type="submit">修改密码</n-button>
    </n-space>
  </n-form>
</template>
