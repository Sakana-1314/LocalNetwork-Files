<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NEmpty,
  NIcon,
  NModal,
  NPopconfirm,
  NSpace,
  NTag,
  NTooltip,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { CopyOutline, FolderOpenOutline, OpenOutline, TimeOutline, TrashOutline } from '@vicons/ionicons5'
import { useRouter } from 'vue-router'
import type { Share, ShareStatus } from '@/api/types'
import { sharesApi } from '@/api/shares'
import ExpiryPicker from '@/components/share/ExpiryPicker.vue'
import { useCopy } from '@/composables/useCopy'
import { useIsMobile } from '@/composables/useIsMobile'
import { expiryFrom, shareLink, type ExpiryPreset } from '@/utils/share'
import { filesRoute } from '@/utils/path'
import { formatDate, formatRelative } from '@/utils/format'

const message = useMessage()
const copy = useCopy()
const router = useRouter()
const isMobile = useIsMobile()

const shares = ref<Share[]>([])
const loading = ref(false)

async function load(): Promise<void> {
  loading.value = true
  try {
    shares.value = await sharesApi.list()
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function remove(s: Share): Promise<void> {
  try {
    await sharesApi.remove(s.id)
    message.success('已取消分享，链接已失效')
    await load()
  } catch {
    /* 错误已提示 */
  }
}

// ---------- 修改有效期 ----------
const editing = ref<Share | null>(null)
const preset = ref<ExpiryPreset>('7d')
const custom = ref<number | null>(null)
const saving = ref(false)

function openExpiry(s: Share): void {
  editing.value = s
  if (s.expires_at) {
    preset.value = 'custom'
    custom.value = Math.max(Date.parse(s.expires_at), Date.now() + 3600 * 1000)
  } else {
    preset.value = 'never'
    custom.value = null
  }
}

async function saveExpiry(): Promise<void> {
  const s = editing.value
  if (!s) return
  if (preset.value === 'custom' && !custom.value) return void message.warning('请选择过期时间')
  saving.value = true
  try {
    await sharesApi.setExpiry(s.id, expiryFrom(preset.value, custom.value))
    message.success('有效期已更新')
    editing.value = null
    await load()
  } catch {
    /* 错误已提示 */
  } finally {
    saving.value = false
  }
}

const STATUS: Record<ShareStatus, { label: string; type: 'success' | 'warning' | 'error'; tip: string }> = {
  active: { label: '有效', type: 'success', tip: '链接可以访问' },
  expired: { label: '已过期', type: 'warning', tip: '延长有效期后链接即可恢复' },
  unavailable: { label: '已失效', type: 'error', tip: '你已没有该文件夹的权限，或文件夹已被移动 / 删除' },
}

function expiryText(s: Share): string {
  if (!s.expires_at) return '永久有效'
  return `${formatDate(s.expires_at)}（${formatRelative(s.expires_at)}）`
}

const iconBtn = (icon: typeof CopyOutline, tip: string, onClick: () => void, extra: Record<string, unknown> = {}) =>
  h(NTooltip, {}, {
    trigger: () =>
      h(NButton, { size: 'small', quaternary: true, circle: true, onClick, ...extra }, { icon: () => h(NIcon, { component: icon }) }),
    default: () => tip,
  })

const columns = computed<DataTableColumns<Share>>(() => [
  {
    title: '文件夹',
    key: 'name',
    minWidth: 160,
    render: (s) =>
      h('div', { class: 'name-cell' }, [
        h('div', { class: 'name' }, s.name),
        h('div', { class: 'path' }, s.path),
      ]),
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (s) =>
      h(NTooltip, {}, {
        trigger: () => h(NTag, { size: 'small', bordered: false, type: STATUS[s.status].type }, () => STATUS[s.status].label),
        default: () => STATUS[s.status].tip,
      }),
  },
  { title: '有效期', key: 'expires_at', minWidth: 180, render: (s) => expiryText(s) },
  ...(isMobile.value ? [] : [{ title: '创建时间', key: 'created_at', width: 150, render: (s: Share) => formatDate(s.created_at) }]),
  {
    title: '操作',
    key: 'actions',
    width: 176,
    render: (s) =>
      h(NSpace, { size: 2, wrap: false }, () => [
        iconBtn(CopyOutline, '复制链接', () => void copy(shareLink(s.token), '链接已复制')),
        iconBtn(OpenOutline, '打开分享页', () => window.open(shareLink(s.token), '_blank')),
        iconBtn(FolderOpenOutline, '在文件中打开', () => void router.push(filesRoute(s.path))),
        iconBtn(TimeOutline, '修改有效期', () => openExpiry(s)),
        h(NPopconfirm, { positiveText: '取消分享', negativeText: '保留', positiveButtonProps: { type: 'error' }, onPositiveClick: () => remove(s) }, {
          trigger: () =>
            h(NButton, { size: 'small', quaternary: true, circle: true, type: 'error' }, { icon: () => h(NIcon, { component: TrashOutline }) }),
          default: () => `取消分享「${s.name}」？链接将立即失效。`,
        }),
      ]),
  },
])
</script>

<template>
  <div class="page">
    <h2 class="page-title">我的分享</h2>
    <p class="hint">分享的是文件夹路径：访问者只读，权限随你当前的权限变化。你失去该文件夹的权限后，链接随即失效。</p>
    <n-card size="small" content-style="padding: 0">
      <n-data-table
        :columns="columns"
        :data="shares"
        :loading="loading"
        :row-key="(s: Share) => s.id"
        :bordered="false"
        :scroll-x="isMobile ? 720 : undefined"
      >
        <template #empty><n-empty description="还没有分享。在文件列表中右键文件夹即可创建分享。" /></template>
      </n-data-table>
    </n-card>

    <n-modal
      :show="!!editing"
      preset="card"
      :title="`修改有效期 · ${editing?.name ?? ''}`"
      :style="{ width: isMobile ? 'calc(100vw - 24px)' : '480px' }"
      @update:show="(v: boolean) => !v && (editing = null)"
    >
      <expiry-picker v-model:preset="preset" v-model:custom="custom" />
      <p class="hint">新的有效期从现在开始计算；已过期的分享延长后即可重新访问，链接不变。</p>
      <template #footer>
        <n-space justify="end">
          <n-button @click="editing = null">取消</n-button>
          <n-button type="primary" :loading="saving" @click="saveExpiry">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<style scoped>
.hint {
  font-size: 13px;
  opacity: 0.65;
  margin: 0 0 12px;
}
:deep(.name-cell) {
  min-width: 0;
}
:deep(.name) {
  font-weight: 600;
}
:deep(.path) {
  font-size: 12px;
  opacity: 0.6;
  word-break: break-all;
}
</style>
