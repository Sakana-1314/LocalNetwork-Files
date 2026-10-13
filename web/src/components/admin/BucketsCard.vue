<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NEmpty,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NModal,
  NPopconfirm,
  NSpace,
  NTag,
  NTooltip,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { AddOutline, ArrowUpOutline, FolderOpenOutline, RefreshOutline, SyncOutline, TrashOutline } from '@vicons/ionicons5'
import type { BucketConflict, BucketView, SyncResult } from '@/api/types'
import { adminApi } from '@/api/admin'
import { ApiError, reportError } from '@/api/http'
import ServerDirPickerModal from '@/components/admin/ServerDirPickerModal.vue'
import { useIsMobile } from '@/composables/useIsMobile'
import { formatNumber, formatSize } from '@/utils/format'

const message = useMessage()
const isMobile = useIsMobile()

const buckets = ref<BucketView[]>([])
const loading = ref(false)

async function load(): Promise<void> {
  loading.value = true
  try {
    buckets.value = await adminApi.buckets()
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
onMounted(load)

function syncText(r: SyncResult): string {
  return r.copied ? `已复制 ${formatNumber(r.copied)} 个文件（${formatSize(r.bytes)}）` : '各目录内容已一致'
}

// ---------- 同步 / 删除 ----------
const syncing = ref<number | null>(null)
async function sync(b: BucketView): Promise<void> {
  syncing.value = b.id
  try {
    const r = await adminApi.syncBucket(b.id)
    if (r.conflicts.length) message.warning(`${syncText(r)}；${r.conflicts.length} 处同名的文件与目录无法合并`)
    else message.success(`「${b.name}」${syncText(r)}`)
    await load()
  } catch {
    /* 错误已提示 */
  } finally {
    syncing.value = null
  }
}

async function remove(b: BucketView): Promise<void> {
  try {
    await adminApi.deleteBucket(b.id)
    message.success(`已删除桶「${b.name}」`)
    await load()
  } catch {
    /* 错误已提示 */
  }
}

// ---------- 编辑弹窗 ----------
const modalOpen = ref(false)
const editing = ref<BucketView | null>(null)
const saving = ref(false)
const conflicts = ref<BucketConflict[]>([])
let keySeq = 0
const form = reactive<{ name: string; paths: { value: string; _key: number }[] }>({ name: '', paths: [] })

function openCreate(): void {
  editing.value = null
  conflicts.value = []
  Object.assign(form, { name: '', paths: [{ value: '', _key: ++keySeq }] })
  modalOpen.value = true
}

function openEdit(b: BucketView): void {
  editing.value = b
  conflicts.value = []
  Object.assign(form, { name: b.name, paths: b.paths.map((p) => ({ value: p, _key: ++keySeq })) })
  modalOpen.value = true
}

function addPath(): void {
  form.paths.push({ value: '', _key: ++keySeq })
}
function removePath(i: number): void {
  form.paths.splice(i, 1)
}
function moveUp(i: number): void {
  if (i > 0) form.paths.splice(i - 1, 2, form.paths[i]!, form.paths[i - 1]!)
}

const pickerOpen = ref(false)
const pickerIndex = ref(-1)
const pickerInitial = ref('')
function browseFor(i: number): void {
  pickerIndex.value = i
  pickerInitial.value = form.paths[i]?.value || form.paths.find((p) => p.value)?.value || ''
  pickerOpen.value = true
}
function onPicked(path: string): void {
  const p = form.paths[pickerIndex.value]
  if (!p) return
  p.value = path
  if (!form.name.trim()) {
    const trimmed = path.replace(/[\\/]+$/, '')
    const idx = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
    form.name = (idx >= 0 ? trimmed.slice(idx + 1) : trimmed).replace(/:$/, '')
  }
}

const isNew = (p: string): boolean => !editing.value || !editing.value.paths.includes(p)
const merging = computed(() => {
  const ps = form.paths.map((p) => p.value.trim()).filter(Boolean)
  return ps.length > 1 && ps.some(isNew)
})

async function save(): Promise<void> {
  const name = form.name.trim()
  const paths = form.paths.map((p) => p.value.trim()).filter(Boolean)
  if (!name) return void message.warning('请输入桶名称')
  if (/[\\/]/.test(name)) return void message.warning('桶名称不能包含 / 或 \\')
  if (!paths.length) return void message.warning('请至少选择一个目录')
  if (new Set(paths).size !== paths.length) return void message.warning('目录不能重复')
  saving.value = true
  conflicts.value = []
  try {
    const r = editing.value
      ? await adminApi.updateBucket(editing.value.id, { name, paths })
      : await adminApi.createBucket({ name, paths })
    message.success(`${editing.value ? '桶已更新' : '桶已创建'}${r.sync.copied ? '，' + syncText(r.sync) : ''}`)
    modalOpen.value = false
    await load()
  } catch (e) {
    const data = e instanceof ApiError ? (e.data as { conflicts?: BucketConflict[] } | undefined) : undefined
    if (data?.conflicts?.length) conflicts.value = data.conflicts
    else if (e instanceof ApiError) reportError(e.message)
  } finally {
    saving.value = false
  }
}

// ---------- 表格 ----------
const columns = computed<DataTableColumns<BucketView>>(() => [
  { title: '名称', key: 'name', width: 140, render: (b) => h('span', { style: 'font-weight:600' }, b.name) },
  {
    title: '目录',
    key: 'dirs',
    render: (b) =>
      h(
        'div',
        { class: 'dirs' },
        b.dirs.map((d) =>
          h('div', { class: 'dir-row' }, [
            h(
              NTag,
              { size: 'tiny', bordered: false, type: !d.available ? 'error' : d.active ? 'success' : 'info' },
              () => (!d.available ? '离线' : d.active ? '主目录' : '备份'),
            ),
            h('code', { class: 'dir' }, d.path),
          ]),
        ),
      ),
  },
  { title: '用户', key: 'users', width: 60, render: (b) => String(b.users) },
  {
    title: '操作',
    key: 'actions',
    width: 132,
    render: (b) =>
      h(NSpace, { size: 4, wrap: false }, () => [
        h(NButton, { size: 'small', quaternary: true, onClick: () => openEdit(b) }, () => '编辑'),
        h(
          NTooltip,
          {},
          {
            trigger: () =>
              h(
                NButton,
                {
                  size: 'small',
                  quaternary: true,
                  circle: true,
                  disabled: b.paths.length < 2,
                  loading: syncing.value === b.id,
                  onClick: () => sync(b),
                },
                { icon: () => h(NIcon, { component: SyncOutline }) },
              ),
            default: () => '立即同步各目录',
          },
        ),
        h(
          NPopconfirm,
          { positiveText: '删除', negativeText: '取消', positiveButtonProps: { type: 'error' }, onPositiveClick: () => remove(b) },
          {
            trigger: () =>
              h(NButton, { size: 'small', quaternary: true, circle: true, type: 'error' }, { icon: () => h(NIcon, { component: TrashOutline }) }),
            default: () => `删除桶「${b.name}」？所有用户对它的授权将被移除，服务器上的文件不会删除。`,
          },
        ),
      ]),
  },
])
</script>

<template>
  <n-card title="桶" size="small" content-style="padding: 0">
    <template #header-extra>
      <n-space :size="4" :wrap="false">
        <n-button size="small" quaternary circle :loading="loading" @click="load">
          <template #icon><n-icon :component="RefreshOutline" /></template>
        </n-button>
        <n-button size="small" type="primary" @click="openCreate">
          <template #icon><n-icon :component="AddOutline" /></template>
          新建桶
        </n-button>
      </n-space>
    </template>
    <n-data-table
      :columns="columns"
      :data="buckets"
      :loading="loading"
      :row-key="(b: BucketView) => b.id"
      :bordered="false"
      size="small"
      :scroll-x="isMobile ? 640 : undefined"
    >
      <template #empty><n-empty description="还没有桶，新建一个并在用户管理中授权给用户" /></template>
    </n-data-table>

    <n-modal
      v-model:show="modalOpen"
      preset="card"
      :title="editing ? `编辑桶 · ${editing.name}` : '新建桶'"
      :style="{ width: isMobile ? 'calc(100vw - 24px)' : '640px' }"
      :mask-closable="!saving"
    >
      <div class="modal-body">
        <n-form label-placement="top">
          <n-form-item label="名称">
            <n-input v-model:value="form.name" placeholder="如 电影；用户看到的文件夹名，也是 SMB 共享名与 S3 Bucket 名" />
          </n-form-item>
          <div class="paths-head">
            <span>目录</span>
            <n-button size="small" dashed @click="addPath">
              <template #icon><n-icon :component="AddOutline" /></template>
              添加备份目录
            </n-button>
          </div>
          <p class="hint">
            多个目录互为备份：文件会复制到每个目录（不是软链接），第一个可用目录作为主目录读写。
            已有内容的目录会自动合并；同名但内容不同的条目会被拒绝。移出的目录不会被删除。
          </p>
          <div v-for="(p, i) in form.paths" :key="p._key" class="path-row">
            <n-tag size="small" :bordered="false" :type="i === 0 ? 'success' : 'info'" class="path-tag">
              {{ i === 0 ? '主目录' : '备份' }}
            </n-tag>
            <n-input v-model:value="p.value" placeholder="服务器本地绝对路径" class="mono">
              <template #suffix>
                <n-button text size="small" @click="browseFor(i)">
                  <template #icon><n-icon :component="FolderOpenOutline" /></template>
                  浏览
                </n-button>
              </template>
            </n-input>
            <n-space :size="0" :wrap="false">
              <n-button quaternary circle size="small" :disabled="i === 0" title="设为更优先" @click="moveUp(i)">
                <template #icon><n-icon :component="ArrowUpOutline" /></template>
              </n-button>
              <n-button quaternary circle size="small" type="error" :disabled="form.paths.length < 2" @click="removePath(i)">
                <template #icon><n-icon :component="TrashOutline" /></template>
              </n-button>
            </n-space>
          </div>
          <n-alert v-if="merging && !conflicts.length" type="info" :bordered="false" class="mt">
            保存时会合并这些目录的内容，文件较多时可能需要一些时间。
          </n-alert>
          <n-alert v-if="conflicts.length" type="error" title="存在同名冲突，已拒绝操作" class="mt">
            <p class="conflict-hint">以下条目在多个目录中同名但内容不同。请先在服务器上重命名或删除其中一份，再重试。</p>
            <ul class="conflicts">
              <li v-for="c in conflicts" :key="c.path">
                <code>{{ c.path }}</code>
                <span class="muted">（{{ c.reason }}）</span>
                <div v-for="d in c.dirs" :key="d" class="conflict-dir">{{ d }}</div>
              </li>
            </ul>
          </n-alert>
        </n-form>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button :disabled="saving" @click="modalOpen = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">{{ editing ? '保存' : '创建' }}</n-button>
        </n-space>
      </template>
    </n-modal>

    <server-dir-picker-modal v-model:show="pickerOpen" :initial-path="pickerInitial" @confirm="onPicked" />
  </n-card>
</template>

<style scoped>
:deep(.dirs) {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
:deep(.dir-row) {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
:deep(.dir) {
  font-size: 12px;
  word-break: break-all;
}
.modal-body {
  max-height: 62vh;
  overflow-y: auto;
  padding-right: 4px;
}
.paths-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 14px;
}
.hint {
  font-size: 12px;
  opacity: 0.6;
  margin: 4px 0 10px;
  line-height: 1.6;
}
.path-row {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.path-tag {
  width: 52px;
  justify-content: center;
}
.mt {
  margin-top: 12px;
}
.conflict-hint {
  margin: 0 0 6px;
}
.conflicts {
  margin: 0;
  padding-left: 18px;
  max-height: 220px;
  overflow-y: auto;
  font-size: 12px;
}
.conflicts li {
  margin-bottom: 6px;
}
.conflict-dir {
  opacity: 0.7;
  word-break: break-all;
}
.muted {
  opacity: 0.6;
}
@media (max-width: 560px) {
  .path-row {
    grid-template-columns: 1fr auto;
  }
  .path-row .mono {
    grid-column: 1 / -1;
    grid-row: 2;
  }
}
</style>
