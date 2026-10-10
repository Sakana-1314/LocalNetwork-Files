<script setup lang="ts">
import { NButton, NIcon } from 'naive-ui'
import { DownloadOutline } from '@vicons/ionicons5'
import type { Entry } from '@/api/types'
import FileIcon from '@/components/common/FileIcon.vue'
import { KIND_META } from '@/utils/fileKind'
import { formatDate, formatSize } from '@/utils/format'
import { triggerDownload } from '@/utils/download'

defineProps<{ entry: Entry }>()
</script>

<template>
  <div class="other-preview">
    <div class="card">
      <file-icon :entry="entry" :size="72" />
      <div class="name">{{ entry.name }}</div>
      <div class="desc">该文件类型暂不支持在线预览</div>
      <dl>
        <dt>类型</dt>
        <dd>{{ KIND_META[entry.kind]?.label ?? '其他' }}<template v-if="entry.mime"> · {{ entry.mime }}</template></dd>
        <dt>大小</dt>
        <dd>{{ formatSize(entry.size) }}（{{ entry.size.toLocaleString() }} 字节）</dd>
        <dt>修改时间</dt>
        <dd>{{ formatDate(entry.mod_time, 'YYYY-MM-DD HH:mm:ss') }}</dd>
        <dt>路径</dt>
        <dd class="path">{{ entry.path }}</dd>
      </dl>
      <n-button type="primary" @click="triggerDownload(entry.path, entry.name)">
        <template #icon><n-icon :component="DownloadOutline" /></template>
        下载文件
      </n-button>
    </div>
  </div>
</template>

<style scoped>
.other-preview {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}
.card {
  width: min(440px, 100%);
  padding: 28px 24px;
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: 8px;
}
.name {
  font-size: 17px;
  font-weight: 600;
  word-break: break-all;
}
.desc {
  font-size: 13px;
  opacity: 0.6;
}
dl {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 6px 14px;
  margin: 12px 0 16px;
  text-align: left;
  font-size: 13px;
  width: 100%;
}
dt {
  opacity: 0.55;
}
dd {
  margin: 0;
  word-break: break-all;
}
</style>
