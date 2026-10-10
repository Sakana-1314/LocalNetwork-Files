<script setup lang="ts">
import { NDescriptions, NDescriptionsItem, NDivider, NTag } from 'naive-ui'
import ChangePasswordForm from '@/components/common/ChangePasswordForm.vue'
import { useAuthStore } from '@/stores/auth'
import { formatDate } from '@/utils/format'

const auth = useAuthStore()
</script>

<template>
  <div class="account">
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
      <change-password-form />
    </div>
  </div>
</template>

<style scoped>
.account {
  padding-top: 8px;
}
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
