<script setup lang="ts">
import { computed, h, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NIcon, NMenu, type MenuOption } from 'naive-ui'
import {
  BookOutline,
  FolderOpenOutline,
  GitNetworkOutline,
  OptionsOutline,
  PeopleOutline,
  SearchOutline,
} from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'

defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{ navigate: [] }>()

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const icon = (c: Component) => () => h(NIcon, null, { default: () => h(c) })

const options = computed<MenuOption[]>(() => {
  const base: MenuOption[] = [
    { label: '文件', key: 'files', icon: icon(FolderOpenOutline) },
    { label: '搜索', key: 'search', icon: icon(SearchOutline) },
    { label: '挂载与服务', key: 'mount', icon: icon(GitNetworkOutline) },
    { label: 'API 文档', key: 'api-docs', icon: icon(BookOutline) },
  ]
  if (auth.isAdmin) {
    base.push(
      { label: '用户管理', key: 'admin-users', icon: icon(PeopleOutline) },
      { label: '系统管理', key: 'admin-system', icon: icon(OptionsOutline) },
    )
  }
  return base
})

const active = computed(() => (typeof route.name === 'string' ? route.name : ''))

function onSelect(key: string): void {
  emit('navigate')
  if (key === active.value && key !== 'files') return
  if (key === 'files') void router.push({ name: 'files', params: { path: [] } })
  else void router.push({ name: key })
}
</script>

<template>
  <n-menu
    :value="active"
    :options="options"
    :collapsed="collapsed"
    :collapsed-width="64"
    :collapsed-icon-size="22"
    :indent="20"
    @update:value="onSelect"
  />
</template>
