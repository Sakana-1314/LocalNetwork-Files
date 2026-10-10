<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDivider,
  NForm,
  NFormItemGi,
  NGrid,
  NGridItem,
  NIcon,
  NInput,
  NInputNumber,
  NPopconfirm,
  NProgress,
  NSelect,
  NSkeleton,
  NSpace,
  NStatistic,
  NSwitch,
  NTag,
  useDialog,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { RefreshOutline, SaveOutline } from '@vicons/ionicons5'
import type { CacheBucketStats, CacheSession, CacheStats, CleanRequest, IndexStatus, Settings } from '@/api/types'
import { adminApi } from '@/api/admin'
import { usePolling } from '@/composables/usePolling'
import { formatDate, formatNumber, formatRelative, formatSize } from '@/utils/format'

const message = useMessage()
const dialog = useDialog()

// ---------- 系统设置 ----------
const settings = ref<Settings | null>(null)
const original = ref('')
const saving = ref(false)
const settingsLoading = ref(true)

function defaults(s: Partial<Settings> | null | undefined): Settings {
  return {
    cache: {
      thumb_ttl_hours: 0,
      thumb_max_mb: 0,
      transcode_ttl_hours: 0,
      transcode_max_mb: 0,
      cleanup_interval_minutes: 0,
      ...(s?.cache ?? {}),
    },
    transcode: { encoder: 'libx264', preset: 'veryfast', max_sessions: 2, segment_seconds: 4, ...(s?.transcode ?? {}) },
    search: { reindex_interval_minutes: 0, watch: false, ...(s?.search ?? {}) },
    services: {
      webdav_enabled: false,
      s3_enabled: false,
      s3_port: 9000,
      s3_region: 'us-east-1',
      smb_enabled: false,
      smb_port: 445,
      smb_workgroup: 'WORKGROUP',
      ...(s?.services ?? {}),
    },
  }
}

async function loadSettings(): Promise<void> {
  settingsLoading.value = true
  try {
    const s = defaults(await adminApi.settings())
    settings.value = s
    original.value = JSON.stringify(s)
  } catch {
    /* 错误已提示 */
  } finally {
    settingsLoading.value = false
  }
}

const dirty = computed(() => !!settings.value && JSON.stringify(settings.value) !== original.value)

async function saveSettings(): Promise<void> {
  if (!settings.value) return
  saving.value = true
  try {
    const s = defaults(await adminApi.saveSettings(settings.value))
    settings.value = s
    original.value = JSON.stringify(s)
    message.success('设置已保存并生效')
    void loadCache()
  } catch {
    /* 错误已提示 */
  } finally {
    saving.value = false
  }
}

function resetSettings(): void {
  if (original.value) settings.value = JSON.parse(original.value) as Settings
}

const encoderOptions = [
  { label: 'libx264（软件编码，兼容性最好）', value: 'libx264' },
  { label: 'h264_videotoolbox（macOS 硬件加速）', value: 'h264_videotoolbox' },
  { label: 'h264_nvenc（NVIDIA）', value: 'h264_nvenc' },
  { label: 'h264_qsv（Intel Quick Sync）', value: 'h264_qsv' },
  { label: 'h264_vaapi（Linux VA-API）', value: 'h264_vaapi' },
]
const presetOptions = ['ultrafast', 'superfast', 'veryfast', 'faster', 'fast', 'medium'].map((v) => ({ label: v, value: v }))

// ---------- 缓存 ----------
const cache = ref<CacheStats | null>(null)
const cacheLoading = ref(false)
const cleaning = ref<string | null>(null)

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

async function clean(key: string, req: CleanRequest): Promise<void> {
  cleaning.value = key
  try {
    const r = await adminApi.cleanCache(req)
    message.success(`已清理 ${formatNumber(r.removed_files)} 个文件，释放 ${formatSize(r.removed_bytes)}`)
    await loadCache()
  } catch {
    /* 错误已提示 */
  } finally {
    cleaning.value = null
  }
}

function confirmClean(key: string, title: string, req: CleanRequest): void {
  dialog.warning({
    title,
    content: req.kind === 'transcode' ? '将删除所有转码分片，正在播放的视频可能需要重新转码。' : '将删除所有已生成的预览图，之后会按需重新生成。',
    positiveText: '清空',
    negativeText: '取消',
    positiveButtonProps: { type: 'error' },
    onPositiveClick: () => {
      void clean(key, req)
    },
  })
}

function usage(b: CacheBucketStats | undefined): number {
  if (!b || !b.max_bytes) return 0
  return Math.min(100, Math.round((b.bytes / b.max_bytes) * 100))
}

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
  void loadSettings()
  void loadCache()
  const s = await loadIndex()
  if (s?.indexing) poll.start()
})
</script>

<template>
  <div class="page">
    <h2 class="page-title">系统设置</h2>
    <p class="page-desc">修改后立即生效，相关服务会按需自动重启。</p>

    <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
      <!-- 左侧：设置表单 -->
      <n-grid-item span="1 l:2">
        <n-card title="配置" size="small">
          <template #header-extra>
            <n-space :size="8">
              <n-button size="small" :disabled="!dirty" @click="resetSettings">撤销修改</n-button>
              <n-button size="small" type="primary" :disabled="!dirty" :loading="saving" @click="saveSettings">
                <template #icon><n-icon :component="SaveOutline" /></template>
                保存
              </n-button>
            </n-space>
          </template>
          <n-skeleton v-if="settingsLoading && !settings" text :repeat="10" />
          <n-form v-else-if="settings" label-placement="top" size="small" :show-feedback="false">
            <n-divider title-placement="left">服务</n-divider>
            <n-grid cols="1 s:2 m:3" responsive="screen" :x-gap="16" :y-gap="14">
              <n-form-item-gi label="WebDAV">
                <n-switch v-model:value="settings.services.webdav_enabled" />
              </n-form-item-gi>
              <n-form-item-gi label="SMB">
                <n-switch v-model:value="settings.services.smb_enabled" />
              </n-form-item-gi>
              <n-form-item-gi label="S3">
                <n-switch v-model:value="settings.services.s3_enabled" />
              </n-form-item-gi>
              <n-form-item-gi label="SMB 端口">
                <n-input-number v-model:value="settings.services.smb_port" :min="1" :max="65535" :show-button="false" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="SMB 工作组">
                <n-input v-model:value="settings.services.smb_workgroup" placeholder="WORKGROUP" />
              </n-form-item-gi>
              <n-grid-item />
              <n-form-item-gi label="S3 端口">
                <n-input-number v-model:value="settings.services.s3_port" :min="1" :max="65535" :show-button="false" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="S3 区域">
                <n-input v-model:value="settings.services.s3_region" placeholder="us-east-1" />
              </n-form-item-gi>
            </n-grid>

            <n-divider title-placement="left">视频转码</n-divider>
            <n-grid cols="1 s:2" responsive="screen" :x-gap="16" :y-gap="14">
              <n-form-item-gi label="视频编码器">
                <n-select v-model:value="settings.transcode.encoder" :options="encoderOptions" />
              </n-form-item-gi>
              <n-form-item-gi label="编码预设（越快画质越低）">
                <n-select v-model:value="settings.transcode.preset" :options="presetOptions" />
              </n-form-item-gi>
              <n-form-item-gi label="最大并发转码数">
                <n-input-number v-model:value="settings.transcode.max_sessions" :min="1" :max="32" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="分片时长（秒）">
                <n-input-number v-model:value="settings.transcode.segment_seconds" :min="1" :max="30" style="width: 100%" />
              </n-form-item-gi>
            </n-grid>

            <n-divider title-placement="left">缓存</n-divider>
            <n-grid cols="1 s:2" responsive="screen" :x-gap="16" :y-gap="14">
              <n-form-item-gi label="预览图保留时长（小时）">
                <n-input-number v-model:value="settings.cache.thumb_ttl_hours" :min="0" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="预览图缓存上限（MB）">
                <n-input-number v-model:value="settings.cache.thumb_max_mb" :min="0" :step="256" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="转码缓存保留时长（小时）">
                <n-input-number v-model:value="settings.cache.transcode_ttl_hours" :min="0" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="转码缓存上限（MB）">
                <n-input-number v-model:value="settings.cache.transcode_max_mb" :min="0" :step="1024" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="自动清理间隔（分钟）">
                <n-input-number v-model:value="settings.cache.cleanup_interval_minutes" :min="1" style="width: 100%" />
              </n-form-item-gi>
            </n-grid>

            <n-divider title-placement="left">搜索</n-divider>
            <n-grid cols="1 s:2" responsive="screen" :x-gap="16" :y-gap="14">
              <n-form-item-gi label="定时全量重建间隔（分钟，0 关闭）">
                <n-input-number v-model:value="settings.search.reindex_interval_minutes" :min="0" style="width: 100%" />
              </n-form-item-gi>
              <n-form-item-gi label="监听文件变化（增量更新）">
                <n-switch v-model:value="settings.search.watch" />
              </n-form-item-gi>
            </n-grid>
          </n-form>
          <n-alert v-if="dirty" type="info" :bordered="false" class="dirty-hint">有未保存的修改</n-alert>
        </n-card>
      </n-grid-item>

      <!-- 右侧：缓存与索引 -->
      <n-grid-item>
        <n-space vertical :size="16">
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
                  · 保留 {{ cache.thumb.ttl_hours }} 小时
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
                  · 保留 {{ cache.transcode.ttl_hours }} 小时
                </div>
              </div>
              <div class="kv"><span>缓存目录</span><code class="dir">{{ cache.dir }}</code></div>
              <div class="kv"><span>上次清理</span>{{ cache.last_cleanup ? formatRelative(cache.last_cleanup) : '从未' }}</div>
            </template>
            <n-skeleton v-else text :repeat="4" />
            <n-space vertical class="mt" :size="8">
              <n-popconfirm @positive-click="clean('expired', { kind: 'all', expired_only: true })">
                <template #trigger>
                  <n-button block :loading="cleaning === 'expired'">清理过期</n-button>
                </template>
                清理所有已过期的预览图与转码分片？
              </n-popconfirm>
              <div class="two-col">
                <n-button
                  secondary
                  type="warning"
                  :loading="cleaning === 'thumb'"
                  @click="confirmClean('thumb', '清空预览图缓存？', { kind: 'thumb', expired_only: false })"
                >
                  清空预览图
                </n-button>
                <n-button
                  secondary
                  type="error"
                  :loading="cleaning === 'transcode'"
                  @click="confirmClean('transcode', '清空转码缓存？', { kind: 'transcode', expired_only: false })"
                >
                  清空转码缓存
                </n-button>
              </div>
            </n-space>
          </n-card>
        </n-space>
      </n-grid-item>

      <n-grid-item span="1 l:3">
        <n-card title="转码会话" size="small">
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
  max-width: 1280px;
}
.dirty-hint {
  margin-top: 16px;
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
.two-col {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
</style>
