<script setup lang="ts">
import { computed } from 'vue'
import { NIcon } from 'naive-ui'
import type { Entry } from '@/api/types'
import { entryIcon } from '@/utils/fileKind'

const props = withDefaults(defineProps<{ entry: Pick<Entry, 'kind' | 'name' | 'path'>; size?: number; mount?: boolean }>(), {
  size: 20,
  mount: false,
})
const meta = computed(() => entryIcon(props.entry, props.mount))
</script>

<template>
  <n-icon :size="size" :color="meta.color" :component="meta.icon" class="file-icon" />
</template>

<style scoped>
.file-icon {
  flex-shrink: 0;
}
</style>
