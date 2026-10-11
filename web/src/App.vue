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
} from 'naive-ui'
import { usePrefsStore } from '@/stores/prefs'
import { themeOverrides } from '@/theme'
import FeedbackBridge from '@/components/common/FeedbackBridge.vue'

const prefs = usePrefsStore()
const osTheme = useOsTheme()

const isDark = computed(() => (prefs.theme === 'auto' ? osTheme.value === 'dark' : prefs.theme === 'dark'))
const theme = computed(() => (isDark.value ? darkTheme : null))
const overrides = computed(() => themeOverrides(isDark.value))
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
