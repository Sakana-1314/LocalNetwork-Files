<script setup lang="ts">
import { computed } from 'vue'
import type { Entry } from '@/api/types'
import { fsApi } from '@/api/fs'

const props = defineProps<{ entry: Entry }>()
const src = computed(() => fsApi.rawUrl(props.entry.path))
</script>

<template>
  <div class="pdf-preview">
    <iframe :src="src" :title="entry.name" />
  </div>
</template>

<style scoped>
.pdf-preview {
  width: 100%;
  height: 100%;
  padding: 0 16px 16px;
}
iframe {
  width: 100%;
  height: 100%;
  border: 0;
  border-radius: 8px;
  background: #fff;
}
</style>
