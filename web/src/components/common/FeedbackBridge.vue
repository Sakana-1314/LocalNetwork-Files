<script setup lang="ts">
import { onBeforeUnmount, watchEffect } from 'vue'
import { useDialog, useLoadingBar, useMessage, useNotification, useThemeVars } from 'naive-ui'
import { feedback } from '@/composables/feedback'

const props = defineProps<{ dark: boolean }>()

feedback.message = useMessage()
feedback.dialog = useDialog()
feedback.notification = useNotification()
feedback.loadingBar = useLoadingBar()

const vars = useThemeVars()

// 让整个页面（含滚动条、表单控件、body 背景）跟随主题
watchEffect(() => {
  const root = document.documentElement
  root.style.colorScheme = props.dark ? 'dark' : 'light'
  root.dataset.theme = props.dark ? 'dark' : 'light'
  document.body.style.backgroundColor = vars.value.bodyColor
  document.body.style.color = vars.value.textColor2
  const meta = document.querySelector('meta[name="theme-color"]') ?? document.head.appendChild(document.createElement('meta'))
  meta.setAttribute('name', 'theme-color')
  meta.setAttribute('content', vars.value.bodyColor)
})

onBeforeUnmount(() => {
  feedback.message = null
  feedback.dialog = null
  feedback.notification = null
  feedback.loadingBar = null
})
</script>

<template>
  <span style="display: none" />
</template>
