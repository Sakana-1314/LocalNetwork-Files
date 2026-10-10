<script setup lang="ts">
import { computed } from 'vue'
import {
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NLoadingBarProvider,
  NMessageProvider,
  NNotificationProvider,
  darkTheme,
  dateZhCN,
  useOsTheme,
  zhCN,
  type GlobalThemeOverrides,
} from 'naive-ui'
import { usePrefsStore } from '@/stores/prefs'
import FeedbackBridge from '@/components/common/FeedbackBridge.vue'

const prefs = usePrefsStore()
const osTheme = useOsTheme()

const isDark = computed(() => (prefs.theme === 'auto' ? osTheme.value === 'dark' : prefs.theme === 'dark'))
const theme = computed(() => (isDark.value ? darkTheme : null))

const overrides = computed<GlobalThemeOverrides>(() => ({
  common: {
    borderRadius: '6px',
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif',
    ...(isDark.value ? { bodyColor: '#141417' } : { bodyColor: '#f5f6f8' }),
  },
}))
</script>

<template>
  <n-config-provider :theme="theme" :theme-overrides="overrides" :locale="zhCN" :date-locale="dateZhCN">
    <n-global-style />
    <n-loading-bar-provider>
      <n-message-provider placement="top" :max="4">
        <n-notification-provider placement="bottom-right">
          <n-dialog-provider>
            <feedback-bridge :dark="isDark" />
            <router-view />
          </n-dialog-provider>
        </n-notification-provider>
      </n-message-provider>
    </n-loading-bar-provider>
  </n-config-provider>
</template>
