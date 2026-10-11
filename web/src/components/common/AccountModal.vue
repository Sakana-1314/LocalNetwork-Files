<script setup lang="ts">
import { NDescriptions, NDescriptionsItem, NDivider, NModal, NTag } from 'naive-ui'
import ChangePasswordForm from './ChangePasswordForm.vue'
import { useAuthStore } from '@/stores/auth'
import { formatDate } from '@/utils/format'

const show = defineModel<boolean>('show', { default: false })
const auth = useAuthStore()
</script>

<template>
  <n-modal
    v-model:show="show"
    preset="card"
    title="账号设置"
    style="width: 480px; max-width: calc(100vw - 32px)"
    :mask-closable="false"
  >
    <template v-if="show">
      <n-descriptions v-if="auth.user" :column="1" label-placement="left" size="small">
        <n-descriptions-item label="用户名">{{ auth.user.username }}</n-descriptions-item>
        <n-descriptions-item label="角色">
          <n-tag size="small" :type="auth.isAdmin ? 'warning' : 'default'" :bordered="false">
            {{ auth.isAdmin ? '管理员' : '普通用户' }}
          </n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="创建时间">{{ formatDate(auth.user.created_at) }}</n-descriptions-item>
        <n-descriptions-item label="授权目录">
          <div class="mounts">
            <n-tag v-for="m in auth.user.mounts" :key="m.id" size="small" :type="m.writable ? 'success' : 'default'" :bordered="false">
              {{ m.name }} · {{ m.writable ? '可写' : '只读' }}
            </n-tag>
            <span v-if="!auth.user.mounts.length" class="muted">无</span>
          </div>
        </n-descriptions-item>
      </n-descriptions>
      <n-divider title-placement="left">修改密码</n-divider>
      <div class="pwd">
        <change-password-form show-cancel @done="show = false" @cancel="show = false" />
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.mounts {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.pwd {
  max-width: 400px;
}
.muted {
  opacity: 0.6;
}
</style>
