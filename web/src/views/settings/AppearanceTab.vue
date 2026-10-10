<script setup lang="ts">
import { NForm, NFormItem, NRadioButton, NRadioGroup } from 'naive-ui'
import { usePrefsStore } from '@/stores/prefs'

const prefs = usePrefsStore()
</script>

<template>
  <n-form label-placement="left" label-width="auto" class="form">
    <n-form-item label="主题">
      <n-radio-group v-model:value="prefs.theme">
        <n-radio-button value="auto">跟随系统</n-radio-button>
        <n-radio-button value="light">浅色</n-radio-button>
        <n-radio-button value="dark">深色</n-radio-button>
      </n-radio-group>
    </n-form-item>
    <n-form-item label="默认文件视图">
      <n-radio-group v-model:value="prefs.viewMode">
        <n-radio-button value="list">列表</n-radio-button>
        <n-radio-button value="grid">网格（缩略图）</n-radio-button>
      </n-radio-group>
    </n-form-item>
    <n-form-item label="侧边栏">
      <n-radio-group :value="prefs.siderCollapsed ? 'collapsed' : 'expanded'" @update:value="(v: string) => (prefs.siderCollapsed = v === 'collapsed')">
        <n-radio-button value="expanded">展开</n-radio-button>
        <n-radio-button value="collapsed">收起</n-radio-button>
      </n-radio-group>
    </n-form-item>
  </n-form>
  <p class="hint">外观设置保存在当前浏览器中。</p>
</template>

<style scoped>
.form {
  max-width: 520px;
  padding-top: 8px;
}
.hint {
  font-size: 12px;
  opacity: 0.55;
}
</style>
