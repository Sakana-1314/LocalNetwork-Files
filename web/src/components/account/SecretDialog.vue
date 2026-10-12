<script setup lang="ts">
import { NButton, NModal } from 'naive-ui'
import CopyBlock from '@/components/common/CopyBlock.vue'

defineProps<{ title: string; items: { label: string; value: string }[] }>()
const show = defineModel<boolean>('show', { default: false })
</script>

<template>
  <n-modal
    v-model:show="show"
    preset="card"
    :title="title"
    style="width: 560px; max-width: calc(100vw - 24px)"
    :mask-closable="false"
  >
    <copy-block v-for="it in items" :key="it.label" :label="it.label" :code="it.value" />
    <template #footer>
      <div style="text-align: right">
        <n-button type="primary" @click="show = false">关闭</n-button>
      </div>
    </template>
  </n-modal>
</template>
