<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAvatar,
  NButton,
  NDrawer,
  NDrawerContent,
  NDropdown,
  NIcon,
  NInput,
  NLayout,
  NLayoutContent,
  NLayoutHeader,
  NLayoutSider,
  NTooltip,
  type DropdownOption,
} from 'naive-ui'
import {
  ContrastOutline,
  KeyOutline,
  LogOutOutline,
  MenuOutline,
  MoonOutline,
  SearchOutline,
  SunnyOutline,
} from '@vicons/ionicons5'
import AppLogo from '@/components/common/AppLogo.vue'
import AppMenu from '@/components/common/AppMenu.vue'
import ChangePasswordModal from '@/components/common/ChangePasswordModal.vue'
import UploadQueuePanel from '@/components/files/UploadQueuePanel.vue'
import { useAuthStore } from '@/stores/auth'
import { usePrefsStore } from '@/stores/prefs'
import { useIsMobile } from '@/composables/useIsMobile'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const prefs = usePrefsStore()
const isMobile = useIsMobile()

const drawerOpen = ref(false)
const pwdOpen = ref(false)
const searchText = ref('')

watch(
  () => route.fullPath,
  () => (drawerOpen.value = false),
)
watch(isMobile, (m) => {
  if (!m) drawerOpen.value = false
})
watch(
  () => route.query.q,
  (q) => {
    if (route.name === 'search') searchText.value = typeof q === 'string' ? q : ''
  },
  { immediate: true },
)

const title = computed(() => route.meta.title ?? '')

const themeIcon = computed(() => (prefs.theme === 'auto' ? ContrastOutline : prefs.theme === 'dark' ? MoonOutline : SunnyOutline))
const themeLabel = computed(() =>
  prefs.theme === 'auto' ? '主题：跟随系统' : prefs.theme === 'dark' ? '主题：深色' : '主题：浅色',
)

const renderIcon = (c: typeof KeyOutline) => () => h(NIcon, null, { default: () => h(c) })

const userOptions = computed<DropdownOption[]>(() => [
  {
    key: 'header',
    type: 'render',
    render: () =>
      h('div', { class: 'user-head' }, [
        h('div', { class: 'user-head-name' }, auth.user?.username ?? ''),
        h('div', { class: 'user-head-role' }, auth.isAdmin ? '管理员' : '普通用户'),
      ]),
  },
  { key: 'd1', type: 'divider' },
  { key: 'password', label: '修改密码', icon: renderIcon(KeyOutline) },
  { key: 'logout', label: '退出登录', icon: renderIcon(LogOutOutline) },
])

async function onUserSelect(key: string): Promise<void> {
  if (key === 'password') pwdOpen.value = true
  else if (key === 'logout') {
    await auth.logout()
    await router.replace({ name: 'login' })
  }
}

function doSearch(): void {
  const q = searchText.value.trim()
  void router.push({ name: 'search', query: q ? { q } : {} })
}

const avatarText = computed(() => (auth.user?.username ?? '?').slice(0, 1).toUpperCase())
</script>

<template>
  <n-layout has-sider position="absolute" class="main-layout">
    <n-layout-sider
      v-if="!isMobile"
      bordered
      collapse-mode="width"
      :collapsed-width="64"
      :width="220"
      :collapsed="prefs.siderCollapsed"
      show-trigger="bar"
      :native-scrollbar="false"
      @collapse="prefs.siderCollapsed = true"
      @expand="prefs.siderCollapsed = false"
    >
      <app-logo :collapsed="prefs.siderCollapsed" />
      <app-menu :collapsed="prefs.siderCollapsed" />
    </n-layout-sider>

    <n-drawer v-if="isMobile" v-model:show="drawerOpen" placement="left" :width="240">
      <n-drawer-content :native-scrollbar="false" body-content-style="padding: 0">
        <app-logo />
        <app-menu @navigate="drawerOpen = false" />
      </n-drawer-content>
    </n-drawer>

    <n-layout>
      <n-layout-header bordered class="header">
        <n-button v-if="isMobile" quaternary circle @click="drawerOpen = true">
          <template #icon><n-icon :component="MenuOutline" /></template>
        </n-button>
        <h1 class="header-title text-ellipsis">{{ title }}</h1>
        <div class="header-spacer" />
        <n-input
          v-if="!isMobile"
          v-model:value="searchText"
          class="header-search"
          placeholder="搜索文件… (Enter)"
          clearable
          round
          @keyup.enter="doSearch"
        >
          <template #prefix><n-icon :component="SearchOutline" /></template>
        </n-input>
        <n-button v-else quaternary circle @click="router.push({ name: 'search' })">
          <template #icon><n-icon :component="SearchOutline" /></template>
        </n-button>
        <n-tooltip>
          <template #trigger>
            <n-button quaternary circle @click="prefs.cycleTheme()">
              <template #icon><n-icon :component="themeIcon" /></template>
            </n-button>
          </template>
          {{ themeLabel }}（点击切换）
        </n-tooltip>
        <n-dropdown trigger="click" :options="userOptions" placement="bottom-end" @select="onUserSelect">
          <n-button quaternary class="user-btn">
            <n-avatar round :size="26" class="avatar">{{ avatarText }}</n-avatar>
            <span v-if="!isMobile" class="user-name">{{ auth.user?.username }}</span>
          </n-button>
        </n-dropdown>
      </n-layout-header>

      <n-layout-content position="absolute" class="content">
        <router-view v-slot="{ Component, route: r }">
          <div v-if="r.meta.fullHeight" class="full-page">
            <component :is="Component" />
          </div>
          <component :is="Component" v-else />
        </router-view>
      </n-layout-content>
    </n-layout>

    <change-password-modal v-model:show="pwdOpen" />
    <upload-queue-panel />
  </n-layout>
</template>

<style scoped>
.header {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
}
.header-title {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
  min-width: 0;
}
.header-spacer {
  flex: 1;
}
.header-search {
  width: 260px;
  margin-right: 4px;
}
.user-btn {
  padding: 0 6px;
}
.avatar {
  background: #18a058;
  color: #fff;
  font-weight: 600;
}
.user-name {
  margin-left: 8px;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.content {
  top: 56px;
}
.full-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
@media (max-width: 768px) {
  .header {
    padding: 0 8px;
    gap: 2px;
  }
}
</style>

<style>
.user-head {
  padding: 8px 14px 6px;
}
.user-head-name {
  font-weight: 600;
}
.user-head-role {
  font-size: 12px;
  opacity: 0.6;
}
</style>
