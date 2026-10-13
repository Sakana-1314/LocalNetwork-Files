<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NGrid,
  NGridItem,
  NIcon,
  NPopconfirm,
  NProgress,
  NSkeleton,
  NSpace,
  NStatistic,
  NTag,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { RefreshOutline } from '@vicons/ionicons5'
import type { CacheBucketStats, CacheSession, CacheStats, IndexStatus } from '@/api/types'
import { adminApi } from '@/api/admin'
import BucketsCard from '@/components/admin/BucketsCard.vue'
import { usePolling } from '@/composables/usePolling'
import { formatDate, formatNumber, formatRelative, formatSize } from '@/utils/format'

const message = useMessage()

// ---------- 缓存 ----------
const cache = ref<CacheStats | null>(null)
const cacheLoading = ref(false)
const cleaning = ref(false)

async function loadCache(): Promise<void> {
  cacheLoading.value = true
  try {
    cache.value = await adminApi.cache()
  } catch {
    /* 错误已提示 */
  } finally {
    cacheLoading.value = false
  }
}

async function cleanExpired(): Promise<void> {
  cleaning.value = true
  try {
    const r = await adminApi.cleanCache({ kind: 'all' })
    if (r.removed_files) message.success(`已清理 ${formatNumber(r.removed_files)} 个文件，释放 ${formatSize(r.removed_bytes)}`)
    else message.info('没有需要清理的过期缓存')
    await loadCache()
  } catch {
    /* 错误已提示 */
  } finally {
    cleaning.value = false
  }
}

function usage(b: CacheBucketStats | undefined): number {
  if (!b || !b.max_bytes) return 0
  return Math.min(100, Math.round((b.bytes / b.max_bytes) * 100))
}

const ttlText = (hours: number): string => (hours > 0 ? `保留 ${hours} 小时` : '永不过期')

const sessionColumns: DataTableColumns<CacheSession> = [
  { title: '文件', key: 'path', ellipsis: { tooltip: true } },
  { title: '画质', key: 'quality', width: 80 },
  { title: '用户', key: 'user', width: 100 },
  {
    title: '状态',
    key: 'running',
    width: 80,
    render: (r) => h(NTag, { size: 'small', bordered: false, type: r.running ? 'success' : 'default' }, () => (r.running ? '转码中' : '空闲')),
  },
  { title: '最近访问', key: 'last_access', width: 110, render: (r) => formatRelative(r.last_access) },
]

// ---------- 搜索索引 ----------
const index = ref<IndexStatus | null>(null)
const reindexing = ref(false)

async function loadIndex(silent = false): Promise<IndexStatus | null> {
  try {
    index.value = await adminApi.searchStatus(silent)
    return index.value
  } catch {
    return null
  }
}

const poll = usePolling(async () => {
  const s = await loadIndex(true)
  return !!s?.indexing
}, 2000)

async function reindex(): Promise<void> {
  reindexing.value = true
  try {
    await adminApi.reindex()
    message.success('已开始重建索引')
    await loadIndex()
    poll.start()
  } catch {
    /* 错误已提示 */
  } finally {
    reindexing.value = false
  }
}

onMounted(async () => {
  void loadCache()
  const s = await loadIndex()
  if (s?.indexing) poll.start()
})
</script>

<template>
  <div class="page">
    <h2 class="page-title">系统管理</h2>

    <buckets-card class="mb" />

    <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
      <n-grid-item>
        <n-card title="搜索索引" size="small">
          <template #header-extra>
            <n-button size="small" quaternary circle @click="loadIndex()">
              <template #icon><n-icon :component="RefreshOutline" /></template>
            </n-button>
          </template>
          <template v-if="index">
            <div class="stat-row">
              <n-statistic label="已索引条目" :value="formatNumber(index.entries)" />
              <n-statistic label="根目录" :value="index.roots" />
            </div>
            <div class="kv">
              <span>状态</span>
              <n-tag size="small" :bordered="false" :type="index.indexing ? 'info' : 'success'">
                {{ index.indexing ? '索引构建中…' : '就绪' }}
              </n-tag>
            </div>
            <div class="kv"><span>上次构建</span>{{ index.last_built ? formatDate(index.last_built) : '从未' }}</div>
            <div class="kv"><span>耗时</span>{{ index.duration_ms ? (index.duration_ms / 1000).toFixed(1) + ' 秒' : '-' }}</div>
            <n-progress v-if="index.indexing" type="line" :percentage="100" processing :show-indicator="false" class="mt" />
          </template>
          <n-skeleton v-else text :repeat="3" />
          <n-button block class="mt" :loading="reindexing || !!index?.indexing" :disabled="!!index?.indexing" @click="reindex">
            重建索引
          </n-button>
        </n-card>
      </n-grid-item>

      <n-grid-item>
        <n-card title="缓存" size="small">
          <template #header-extra>
            <n-button size="small" quaternary circle :loading="cacheLoading" @click="loadCache">
              <template #icon><n-icon :component="RefreshOutline" /></template>
            </n-button>
          </template>
          <template v-if="cache">
            <div class="bucket">
              <div class="bucket-head">
                <span>预览图</span>
                <span class="muted">{{ formatNumber(cache.thumb.files) }} 个文件</span>
              </div>
              <n-progress type="line" :percentage="usage(cache.thumb)" :show-indicator="false" :height="8" />
              <div class="bucket-foot">
                {{ formatSize(cache.thumb.bytes) }} / {{ cache.thumb.max_bytes ? formatSize(cache.thumb.max_bytes) : '不限' }}
                · {{ ttlText(cache.thumb.ttl_hours) }}
              </div>
            </div>
            <div class="bucket">
              <div class="bucket-head">
                <span>转码分片</span>
                <span class="muted">{{ formatNumber(cache.transcode.files) }} 个文件</span>
              </div>
              <n-progress type="line" :percentage="usage(cache.transcode)" :show-indicator="false" :height="8" status="info" />
              <div class="bucket-foot">
                {{ formatSize(cache.transcode.bytes) }} / {{ cache.transcode.max_bytes ? formatSize(cache.transcode.max_bytes) : '不限' }}
                · {{ ttlText(cache.transcode.ttl_hours) }}
              </div>
            </div>
            <div class="kv"><span>缓存目录</span><code class="dir">{{ cache.dir }}</code></div>
            <div class="kv"><span>上次清理</span>{{ cache.last_cleanup ? formatRelative(cache.last_cleanup) : '从未' }}</div>
          </template>
          <n-skeleton v-else text :repeat="4" />
          <n-space vertical class="mt" :size="8">
            <n-popconfirm @positive-click="cleanExpired">
              <template #trigger>
                <n-button block :loading="cleaning">清理过期</n-button>
              </template>
              只清理已过期的预览图与转码分片，正在使用的不会被删除。
            </n-popconfirm>
          </n-space>
        </n-card>
      </n-grid-item>

      <n-grid-item>
        <n-card title="转码会话" size="small" content-style="padding: 0">
          <n-data-table
            :columns="sessionColumns"
            :data="cache?.sessions ?? []"
            :row-key="(r: CacheSession) => r.id"
            size="small"
            :bordered="false"
            :scroll-x="640"
          >
            <template #empty><span class="muted">当前没有转码会话</span></template>
          </n-data-table>
        </n-card>
      </n-grid-item>
    </n-grid>
  </div>
</template>

<style scoped>
.page {
  max-width: none;
}
.mb {
  margin-bottom: 16px;
}
.stat-row {
  display: flex;
  gap: 32px;
  margin-bottom: 10px;
}
.kv {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  margin: 6px 0;
  min-width: 0;
}
.kv > span:first-child {
  opacity: 0.6;
  min-width: 64px;
  flex-shrink: 0;
}
.dir {
  font-size: 12px;
  word-break: break-all;
}
.mt {
  margin-top: 12px;
}
.bucket {
  margin-bottom: 14px;
}
.bucket-head {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  margin-bottom: 4px;
}
.bucket-foot {
  font-size: 12px;
  opacity: 0.65;
  margin-top: 4px;
}
.muted {
  opacity: 0.6;
}
</style>
