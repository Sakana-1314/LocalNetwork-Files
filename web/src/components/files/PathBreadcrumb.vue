<script setup lang="ts">
import { computed } from 'vue'
import { NBreadcrumb, NBreadcrumbItem, NIcon } from 'naive-ui'
import { HomeOutline } from '@vicons/ionicons5'
import { splitPath } from '@/utils/path'

const props = defineProps<{ path: string }>()
const emit = defineEmits<{ navigate: [path: string] }>()

const crumbs = computed(() => {
  const segs = splitPath(props.path)
  return segs.map((name, i) => ({ name, path: '/' + segs.slice(0, i + 1).join('/') }))
})
</script>

<template>
  <n-breadcrumb class="crumbs">
    <n-breadcrumb-item :clickable="crumbs.length > 0" @click="emit('navigate', '/')">
      <span class="crumb"><n-icon :component="HomeOutline" class="home" />全部目录</span>
    </n-breadcrumb-item>
    <n-breadcrumb-item
      v-for="(c, i) in crumbs"
      :key="c.path"
      :clickable="i < crumbs.length - 1"
      @click="i < crumbs.length - 1 && emit('navigate', c.path)"
    >
      <span class="crumb" :title="c.name">{{ c.name }}</span>
    </n-breadcrumb-item>
  </n-breadcrumb>
</template>

<style scoped>
.crumbs {
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
  scrollbar-width: none;
}
.crumbs::-webkit-scrollbar {
  display: none;
}
.crumbs :deep(ul) {
  display: flex;
  flex-wrap: nowrap;
}
.crumb {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: bottom;
}
.home {
  font-size: 15px;
}
</style>
