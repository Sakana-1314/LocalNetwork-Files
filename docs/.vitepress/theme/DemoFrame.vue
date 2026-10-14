<script setup lang="ts">
import { withBase } from 'vitepress'

/**
 * 内嵌 iframe。src 为站内路径（会自动加上站点 base），
 * 也可直接给完整外链（如 Apifox 在线文档）。
 */
const props = withDefaults(
  defineProps<{
    /** 站内路径（如 /demo/）或完整外链 */
    src: string
    title: string
    /** 高度，默认占满视口 */
    height?: string
    /** 是否把 src 当作外链（不加 base 前缀） */
    external?: boolean
  }>(),
  { height: 'calc(100vh - 190px)', external: false },
)

const url = props.external ? props.src : withBase(props.src)
</script>

<template>
  <div class="frame-box" :style="{ height: props.height }">
    <iframe
      class="frame"
      :src="url"
      :title="props.title"
      loading="lazy"
      referrerpolicy="no-referrer-when-downgrade"
      allow="fullscreen; clipboard-write"
    />
  </div>
  <p class="frame-actions">
    <a :href="url" target="_blank" rel="noopener">在新窗口打开 ↗</a>
  </p>
</template>

<style scoped>
.frame-box {
  position: relative;
  margin: 16px 0 0;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  overflow: hidden;
  background: var(--vp-c-bg);
}
.frame {
  display: block;
  width: 100%;
  height: 100%;
  border: 0;
}
.frame-actions {
  margin: 8px 0 0;
  font-size: 14px;
}
</style>
