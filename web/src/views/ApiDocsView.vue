<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { NButton, NIcon, NTooltip } from 'naive-ui'
import { OpenOutline, RefreshOutline } from '@vicons/ionicons5'

/**
 * 接口文档以 iframe 内嵌 Apifox 在线文档（https://local-files.apifox.cn）。
 * 规范源文件为 server/internal/api/openapi.yaml，用 server/scripts/sync-apifox.sh 同步。
 * 可通过 VITE_APIFOX_DOC_URL 覆盖地址。
 */
const DOC_URL = (import.meta.env.VITE_APIFOX_DOC_URL as string | undefined) ?? 'https://local-files.apifox.cn/'

const frameKey = ref(0)
const loading = ref(true)
const base = computed(() => DOC_URL.replace(/\/+$/, ''))

// 第三方 Cookie 被拦截时 iframe 的 load 事件也可能不触发；超时后收起遮罩，
// 用户仍可用右上角「新窗口打开」。
let hideTimer: ReturnType<typeof setTimeout> | undefined
function startTimer(): void {
  clearTimeout(hideTimer)
  hideTimer = setTimeout(() => (loading.value = false), 15000)
}

function reload(): void {
  loading.value = true
  frameKey.value += 1
  startTimer()
}

function onLoaded(): void {
  clearTimeout(hideTimer)
  loading.value = false
}

onMounted(startTimer)
onBeforeUnmount(() => clearTimeout(hideTimer))
</script>

<template>
  <div class="api-page">
    <div class="bar">
      <h2 class="page-title">API 文档</h2>
      <div class="actions">
        <n-tooltip>
          <template #trigger>
            <n-button quaternary circle @click="reload">
              <template #icon><n-icon :component="RefreshOutline" /></template>
            </n-button>
          </template>
          重新加载
        </n-tooltip>
        <n-button tag="a" :href="base" target="_blank" rel="noopener">
          <template #icon><n-icon :component="OpenOutline" /></template>
          新窗口打开
        </n-button>
      </div>
    </div>

    <div class="frame-box">
      <iframe
        :key="frameKey"
        class="frame"
        :src="base"
        title="Apifox API 文档"
        loading="lazy"
        referrerpolicy="no-referrer-when-downgrade"
        allow="fullscreen; clipboard-write"
        @load="onLoaded"
      />
      <div v-if="loading" class="overlay">正在加载 Apifox 文档…</div>
    </div>
  </div>
</template>

<style scoped>
.api-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  padding: 12px 16px 0;
}
.bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
}
.actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.frame-box {
  position: relative;
  flex: 1;
  min-height: 360px;
  margin: 12px 0 8px;
  border: 1px solid rgba(128, 128, 128, 0.24);
  border-radius: 10px;
  overflow: hidden;
  background: #fff;
}
.frame {
  display: block;
  width: 100%;
  height: 100%;
  border: 0;
}
.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  opacity: 0.65;
  background: rgba(128, 128, 128, 0.06);
  pointer-events: none;
}
</style>
