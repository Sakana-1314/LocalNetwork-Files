<script setup lang="ts">
import { computed, ref } from 'vue'
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
const failed = ref(false)
const base = computed(() => DOC_URL.replace(/\/+$/, ''))

function reload(): void {
  failed.value = false
  loading.value = true
  frameKey.value += 1
}
</script>

<template>
  <div class="api-page">
    <div class="bar">
      <div class="intro">
        <h2 class="page-title">API 文档</h2>
        <p class="page-desc">
          内嵌 Apifox 在线文档 <a :href="base" target="_blank" rel="noopener">{{ base }}</a
          >；规范源文件 <code>server/internal/api/openapi.yaml</code>，用 <code>server/scripts/sync-apifox.sh</code> 同步。
        </p>
      </div>
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
        @load="((loading = false), (failed = false))"
      />
      <div v-if="loading" class="overlay">正在加载 Apifox 文档…</div>
    </div>
    <p class="hint">
      若长时间空白，通常是浏览器阻止了第三方 Cookie 或网络不通；可直接在新窗口打开，或访问
      <a href="/api/openapi.json" target="_blank" rel="noopener">/api/openapi.json</a>。
      <n-button text type="primary" @click="failed = true">仍无法显示？</n-button>
    </p>
    <p v-if="failed" class="hint warn">内嵌视图可能被浏览器或网络策略拦截，请点击右上角「新窗口打开」。</p>
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
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.page-title {
  margin: 0 0 2px;
  font-size: 20px;
  font-weight: 600;
}
.page-desc {
  margin: 0;
  font-size: 12.5px;
  opacity: 0.7;
}
.page-desc code {
  padding: 1px 4px;
  border-radius: 4px;
  background: rgba(128, 128, 128, 0.16);
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
.hint {
  margin: 0 0 10px;
  font-size: 12px;
  opacity: 0.6;
  line-height: 1.7;
}
.hint.warn {
  color: #f0a020;
  opacity: 0.95;
}
</style>
