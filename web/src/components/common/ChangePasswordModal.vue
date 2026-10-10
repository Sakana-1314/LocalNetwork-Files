<script setup lang="ts">
import { NModal } from 'naive-ui'
import ChangePasswordForm from './ChangePasswordForm.vue'

const show = defineModel<boolean>('show', { default: false })
</script>

<template>
  <n-modal
    v-model:show="show"
    preset="card"
    title="修改密码"
    style="width: 420px; max-width: calc(100vw - 32px)"
    :mask-closable="false"
  >
    <change-password-form v-if="show" show-cancel @done="show = false" @cancel="show = false" />
  </n-modal>
</template>
