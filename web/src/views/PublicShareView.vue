<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NBreadcrumb, NBreadcrumbItem, NButton, NCard, NEmpty, NIcon, NResult, NSpin, NTooltip, useThemeVars } from 'naive-ui'
import { DownloadOutline, HomeOutline } from '@vicons/ionicons5'
import type { Entry, PublicShareInfo } from '@/api/types'
import { ApiError } from '@/api/http'
import { publicShareApi } from '@/api/shares'
import AppLogo from '@/components/common/AppLogo.vue'
import FileThumb from '@/components/files/FileThumb.vue'
import { sortEntries } from '@/components/files/sort'
import { useIsMobile } from '@/composables/useIsMobile'
import { formatDate, formatEntrySize, formatRelative } from '@/utils/format'
import { normalizePath, splitPath } from '@/utils/path'

const route = useRoute()
const router = useRouter()
const vars = useThemeVars()
const isMobile = useIsMobile()

const token = computed(() => String(route.params.token ?? ''))
/** 当前浏览的路径，相对于分享的文件夹 */
const path = computed(() => normalizePath(typeof route.query.path === 'string' ? route.query.path : '/'))

const info = ref<PublicShareInfo | null>(null)
const entries = ref<Entry[]>([])
const loading = ref(true)
const error = ref<{ title: string; desc: string } | null>(null)

function describe(e: unknown): { title: string; desc: string } {
  if (e instanceof ApiError && e.status === 410) return { title: '分享已过期', desc: '请联系分享者延长有效期。' }
  if (e instanceof ApiError && e.status === 404)
    return { title: '分享不存在或已失效', desc: '链接可能已被取消，或分享者已无权访问该文件夹。' }
  return { title: '无法打开分享', desc: e instanceof Error ? e.message : '请稍后重试。' }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    if (!info.value) info.value = await publicShareApi.info(token.value, { silent: true })
    const r = await publicShareApi.list(token.value, path.value, { silent: true })
    entries.value = sortEntries(r.entries, { key: 'name', order: 'ascend' })
    document.title = `${info.value.name} · 分享 · MList`
  } catch (e) {
    error.value = describe(e)
  } finally {
    loading.value = false
  }
}
watch([token, path], () => void load(), { immediate: true })

const crumbs = computed(() => {
  const segs = splitPath(path.value)
  return segs.map((name, i) => ({ name, path: '/' + segs.slice(0, i + 1).join('/') }))
})

function go(p: string): void {
  void router.push({ query: p === '/' ? {} : { path: p } })
}

function open(e: Entry): void {
  if (e.is_dir) go(e.path)
  else window.open(publicShareApi.rawUrl(token.value, e.path), '_blank', 'noopener')
}

function download(e: Entry): void {
  const a = document.createElement('a')
  a.href = publicShareApi.rawUrl(token.value, e.path, true)
  a.download = e.name
  document.body.appendChild(a)
  a.click()
  a.remove()
}
</script>

<template>
  <div class="share-page" :style="{ background: vars.bodyColor }">
    <header class="top" :style="{ borderColor: vars.dividerColor }">
      <app-logo />
      <span class="tag">文件分享</span>
    </header>

    <main class="main">
      <div v-if="loading && !info" class="center"><n-spin /></div>
      <n-result v-else-if="error" status="warning" :title="error.title" :description="error.desc" class="center" />
      <template v-else-if="info">
        <div class="head">
          <h1 class="title">{{ info.name }}</h1>
          <div class="meta">
            由 {{ info.owner }} 分享 ·
            {{ info.expires_at ? `有效期至 ${formatDate(info.expires_at)}` : '永久有效' }} · 只读
          </div>
        </div>

        <n-breadcrumb class="crumbs">
          <n-breadcrumb-item @click="go('/')">
            <n-icon :component="HomeOutline" /> {{ info.name }}
          </n-breadcrumb-item>
          <n-breadcrumb-item v-for="c in crumbs" :key="c.path" @click="go(c.path)">{{ c.name }}</n-breadcrumb-item>
        </n-breadcrumb>

        <n-card size="small" content-style="padding: 0">
          <n-spin :show="loading">
            <n-empty v-if="!entries.length" description="文件夹是空的" class="empty" />
            <ul v-else class="list">
              <li
                v-for="e in entries"
                :key="e.path"
                class="row"
                :style="{ borderColor: vars.dividerColor }"
                @click="open(e)"
              >
                <file-thumb :entry="e" :size="128" :icon-size="28" class="thumb" />
                <div class="info">
                  <div class="name" :title="e.name">{{ e.name }}</div>
                  <div class="sub">{{ formatEntrySize(e) }} · {{ isMobile ? formatRelative(e.mod_time) : formatDate(e.mod_time) }}</div>
                </div>
                <n-tooltip v-if="!e.is_dir">
                  <template #trigger>
                    <n-button quaternary circle size="small" @click.stop="download(e)">
                      <template #icon><n-icon :component="DownloadOutline" /></template>
                    </n-button>
                  </template>
                  下载
                </n-tooltip>
              </li>
            </ul>
          </n-spin>
        </n-card>
        <p class="foot">点击文件夹进入，点击文件在新标签页中打开。</p>
      </template>
    </main>
  </div>
</template>

<style scoped>
.share-page {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}
.top {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 20px;
  border-bottom: 1px solid;
}
.tag {
  font-size: 13px;
  opacity: 0.6;
}
.main {
  width: min(960px, 100%);
  margin: 0 auto;
  padding: 24px 16px 40px;
  flex: 1;
  display: flex;
  flex-direction: column;
}
.center {
  margin: auto;
}
.head {
  margin-bottom: 12px;
}
.title {
  font-size: 22px;
  margin: 0 0 4px;
  word-break: break-all;
}
.meta {
  font-size: 13px;
  opacity: 0.65;
}
.crumbs {
  margin: 8px 0 12px;
}
.crumbs :deep(.n-breadcrumb-item__link) {
  cursor: pointer;
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  cursor: pointer;
  border-bottom: 1px solid;
}
.row:last-child {
  border-bottom: none;
}
.row:hover {
  background: rgba(128, 128, 128, 0.08);
}
.thumb {
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  border-radius: 6px;
  overflow: hidden;
}
.info {
  flex: 1;
  min-width: 0;
}
.name {
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sub {
  font-size: 12px;
  opacity: 0.6;
}
.empty {
  padding: 40px 0;
}
.foot {
  font-size: 12px;
  opacity: 0.5;
  margin-top: 12px;
}
</style>
