<script setup lang="ts">
import { NButton, NIcon, NTooltip, useThemeVars } from 'naive-ui'
import { CopyOutline } from '@vicons/ionicons5'
import { useCopy } from '@/composables/useCopy'

const props = defineProps<{ code: string; label?: string; inline?: boolean }>()
const copy = useCopy()
const vars = useThemeVars()
</script>

<template>
  <div class="copy-block" :class="{ inline }" :style="{ background: vars.actionColor, borderColor: vars.dividerColor }">
    <div v-if="label" class="label">{{ label }}</div>
    <pre class="code"><code>{{ props.code }}</code></pre>
    <n-tooltip>
      <template #trigger>
        <n-button class="copy-btn" size="small" quaternary circle @click="copy(props.code)">
          <template #icon><n-icon :component="CopyOutline" /></template>
        </n-button>
      </template>
      复制
    </n-tooltip>
  </div>
</template>

<style scoped>
.copy-block {
  position: relative;
  border: 1px solid;
  border-radius: 8px;
  margin: 8px 0;
}
.label {
  font-size: 12px;
  opacity: 0.6;
  padding: 8px 12px 0;
}
.code {
  margin: 0;
  padding: 10px 44px 10px 12px;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  overflow-x: auto;
}
.inline .code {
  padding: 6px 40px 6px 10px;
}
.copy-btn {
  position: absolute;
  top: 6px;
  right: 6px;
}
</style>
