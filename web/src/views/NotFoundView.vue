<script setup lang="ts">
import { NButton, NResult } from 'naive-ui'
</script>

<template>
  <div class="page">
    <n-result status="404" title="页面不存在" description="你访问的页面不存在或已被移除">
      <template #footer>
        <n-button type="primary" @click="$router.push('/files')">返回文件</n-button>
      </template>
    </n-result>
  </div>
</template>
