<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NEmpty,
  NForm,
  NFormItem,
  NFormItemGi,
  NGrid,
  NIcon,
  NInput,
  NModal,
  NPopconfirm,
  NSelect,
  NSpace,
  NTag,
  NTooltip,
  useMessage,
  type DataTableColumns,
  type FormInst,
  type FormRules,
} from 'naive-ui'
import { AddOutline, FolderOpenOutline, PersonAddOutline, TrashOutline } from '@vicons/ionicons5'
import type { MountInput, Role, User, UserInput } from '@/api/types'
import { adminApi } from '@/api/admin'
import ServerDirPickerModal from '@/components/admin/ServerDirPickerModal.vue'
import { useAuthStore } from '@/stores/auth'
import { useIsMobile } from '@/composables/useIsMobile'
import { formatDate } from '@/utils/format'

const message = useMessage()
const auth = useAuthStore()
const isMobile = useIsMobile()

const users = ref<User[]>([])
const loading = ref(false)

async function load(): Promise<void> {
  loading.value = true
  try {
    users.value = await adminApi.users()
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---------- 编辑弹窗 ----------
const modalOpen = ref(false)
const editing = ref<User | null>(null)
const saving = ref(false)
const formRef = ref<FormInst | null>(null)

const ROLE_OPTIONS = [
  { label: '普通用户', value: 'user' as Role },
  { label: '管理员', value: 'admin' as Role },
]
const STATUS_OPTIONS = [
  { label: '正常', value: 'active' },
  { label: '已禁用', value: 'disabled' },
]
const PERMISSION_OPTIONS = [
  { label: '读写', value: 'rw' },
  { label: '只读', value: 'ro' },
]

interface FormModel {
  username: string
  password: string
  role: Role
  disabled: boolean
  mounts: (MountInput & { _key: number })[]
}
let keySeq = 0
const form = reactive<FormModel>({ username: '', password: '', role: 'user', disabled: false, mounts: [] })

const rules = computed<FormRules>(() => ({
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { pattern: /^[^\s/\\:]+$/, message: '用户名不能包含空格、/、\\ 或 :', trigger: 'blur' },
  ],
  password: editing.value
    ? [{ min: 6, message: '密码至少 6 位', trigger: 'blur' }]
    : [
        { required: true, message: '请输入初始密码', trigger: 'blur' },
        { min: 6, message: '密码至少 6 位', trigger: 'blur' },
      ],
}))

function openCreate(): void {
  editing.value = null
  Object.assign(form, { username: '', password: '', role: 'user', disabled: false, mounts: [] })
  addMount()
  modalOpen.value = true
}

function openEdit(u: User): void {
  editing.value = u
  Object.assign(form, {
    username: u.username,
    password: '',
    role: u.role,
    disabled: u.disabled,
    mounts: u.mounts.map((m) => ({ name: m.name, path: m.path ?? '', writable: m.writable, _key: ++keySeq })),
  })
  modalOpen.value = true
}

function addMount(): void {
  form.mounts.push({ name: '', path: '', writable: false, _key: ++keySeq })
}
function removeMount(i: number): void {
  form.mounts.splice(i, 1)
}

// 服务器目录选择
const pickerOpen = ref(false)
const pickerIndex = ref(-1)
const pickerInitial = ref('')
function browseFor(i: number): void {
  pickerIndex.value = i
  pickerInitial.value = form.mounts[i]?.path ?? ''
  pickerOpen.value = true
}
function onPicked(path: string): void {
  const m = form.mounts[pickerIndex.value]
  if (!m) return
  m.path = path
  if (!m.name.trim()) {
    const trimmed = path.replace(/[\\/]+$/, '')
    const idx = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
    m.name = (idx >= 0 ? trimmed.slice(idx + 1) : trimmed).replace(/:$/, '') || ''
  }
}

function validateMounts(): string | null {
  const names = new Set<string>()
  for (const m of form.mounts) {
    const n = m.name.trim()
    if (!n && !m.path.trim()) continue
    if (!n) return '授权目录名称不能为空'
    if (/[\\/]/.test(n)) return `目录名称「${n}」不能包含 / 或 \\`
    if (!m.path.trim()) return `请为「${n}」选择本地路径`
    if (names.has(n)) return `目录名称「${n}」重复`
    names.add(n)
  }
  return null
}

async function save(): Promise<void> {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  const err = validateMounts()
  if (err) {
    message.warning(err)
    return
  }
  const payload: UserInput = {
    username: form.username.trim(),
    password: form.password,
    role: form.role,
    disabled: form.disabled,
    mounts: form.mounts
      .filter((m) => m.name.trim() || m.path.trim())
      .map((m) => ({ name: m.name.trim(), path: m.path.trim(), writable: m.writable })),
  }
  saving.value = true
  try {
    if (editing.value) {
      const u = await adminApi.updateUser(editing.value.id, payload)
      message.success('用户已更新')
      if (u.id === auth.user?.id) void auth.fetchMe()
    } else {
      await adminApi.createUser(payload)
      message.success('用户已创建')
    }
    modalOpen.value = false
    await load()
  } catch {
    /* 错误已提示 */
  } finally {
    saving.value = false
  }
}

async function removeEditing(): Promise<void> {
  const u = editing.value
  if (!u) return
  try {
    await adminApi.deleteUser(u.id)
    message.success(`已删除用户 ${u.username}`)
    modalOpen.value = false
    await load()
  } catch {
    /* 错误已提示 */
  }
}

// ---------- 表格 ----------
const columns = computed<DataTableColumns<User>>(() => {
  const cols: DataTableColumns<User> = [
    {
      title: '用户名',
      key: 'username',
      width: 160,
      render: (u) =>
        h('div', { style: 'display:flex;align-items:center;gap:6px' }, [
          h('span', { style: 'font-weight:600' }, u.username),
          u.id === auth.user?.id ? h(NTag, { size: 'tiny', bordered: false, type: 'info' }, () => '我') : null,
        ]),
    },
    {
      title: '角色',
      key: 'role',
      width: 90,
      render: (u) =>
        h(NTag, { size: 'small', bordered: false, type: u.role === 'admin' ? 'warning' : 'default' }, () =>
          u.role === 'admin' ? '管理员' : '用户',
        ),
    },
    {
      title: '状态',
      key: 'disabled',
      width: 80,
      render: (u) =>
        h(NTag, { size: 'small', bordered: false, type: u.disabled ? 'error' : 'success' }, () => (u.disabled ? '已禁用' : '正常')),
    },
    {
      title: '授权目录',
      key: 'mounts',
      render: (u) =>
        u.mounts.length
          ? h(
              'div',
              { style: 'display:flex;flex-wrap:wrap;gap:4px' },
              u.mounts.map((m) =>
                h(
                  NTooltip,
                  {},
                  {
                    trigger: () =>
                      h(NTag, { size: 'small', bordered: false, type: m.writable ? 'success' : 'default' }, () => [
                        m.name,
                        h('span', { style: 'opacity:.65;margin-left:4px;font-size:11px' }, m.writable ? '可写' : '只读'),
                      ]),
                    default: () => m.path || m.name,
                  },
                ),
              ),
            )
          : h('span', { style: 'opacity:.5' }, '无'),
    },
  ]
  if (!isMobile.value) cols.push({ title: '创建时间', key: 'created_at', width: 150, render: (u) => formatDate(u.created_at) })
  return cols
})

/** Clicking anywhere on a row opens the editor. */
function rowProps(u: User): { style: string; onClick: () => void } {
  return { style: 'cursor: pointer', onClick: () => openEdit(u) }
}
</script>

<template>
  <div class="page">
    <div class="title-row">
      <div>
        <h2 class="page-title">用户管理</h2>
      </div>
      <n-button type="primary" @click="openCreate">
        <template #icon><n-icon :component="PersonAddOutline" /></template>
        新建用户
      </n-button>
    </div>

    <n-card size="small" content-style="padding: 0">
      <n-data-table
        :columns="columns"
        :data="users"
        :loading="loading"
        :row-key="(u: User) => u.id"
        :row-props="rowProps"
        :bordered="false"
        :scroll-x="isMobile ? 620 : undefined"
      >
        <template #empty><n-empty description="暂无用户" /></template>
      </n-data-table>
    </n-card>

    <n-modal
      v-model:show="modalOpen"
      preset="card"
      :title="editing ? `编辑用户 · ${editing.username}` : '新建用户'"
      :style="{ width: isMobile ? 'calc(100vw - 24px)' : '640px' }"
    >
      <div class="modal-body">
        <n-form ref="formRef" :model="form" :rules="rules" label-placement="top">
          <n-form-item label="用户名" path="username">
            <n-input v-model:value="form.username" placeholder="用于登录、WebDAV、SMB" :input-props="{ autocomplete: 'off' }" />
          </n-form-item>
          <n-form-item :label="editing ? '新密码（留空则不修改）' : '密码'" path="password">
            <n-input
              v-model:value="form.password"
              type="password"
              show-password-on="click"
              :placeholder="editing ? '不修改请留空' : '至少 6 位'"
              :input-props="{ autocomplete: 'new-password' }"
            />
          </n-form-item>
          <n-grid cols="1 s:2" responsive="screen" :x-gap="16" :y-gap="2">
            <n-form-item-gi label="角色">
              <n-select v-model:value="form.role" :options="ROLE_OPTIONS" />
            </n-form-item-gi>
            <n-form-item-gi label="账户状态">
              <n-select
                :value="form.disabled ? 'disabled' : 'active'"
                :options="STATUS_OPTIONS"
                :disabled="editing?.id === auth.user?.id"
                @update:value="(v: string) => (form.disabled = v === 'disabled')"
              />
            </n-form-item-gi>
          </n-grid>

          <div class="mounts-head">
            <span class="label">授权目录</span>
            <n-button size="small" dashed @click="addMount">
              <template #icon><n-icon :component="AddOutline" /></template>
              添加目录
            </n-button>
          </div>
          <p class="hint">名称将作为该用户虚拟根目录下的文件夹名（也是 SMB 共享名与 S3 Bucket 名）。</p>
          <n-empty v-if="!form.mounts.length" description="尚未授权任何目录" size="small" class="mounts-empty" />
          <div v-for="(m, i) in form.mounts" :key="m._key" class="mount-row">
            <n-input v-model:value="m.name" placeholder="名称，如 电影" class="m-name" />
            <n-input v-model:value="m.path" placeholder="服务器本地绝对路径" class="m-path mono">
              <template #suffix>
                <n-button text size="small" @click="browseFor(i)">
                  <template #icon><n-icon :component="FolderOpenOutline" /></template>
                  浏览
                </n-button>
              </template>
            </n-input>
            <div class="m-actions">
              <n-select
                class="m-perm"
                :value="m.writable ? 'rw' : 'ro'"
                :options="PERMISSION_OPTIONS"
                @update:value="(v: string) => (m.writable = v === 'rw')"
              />
              <n-button quaternary circle size="small" type="error" @click="removeMount(i)">
                <template #icon><n-icon :component="TrashOutline" /></template>
              </n-button>
            </div>
          </div>
        </n-form>
      </div>
      <template #footer>
        <div class="modal-footer">
          <n-popconfirm
            v-if="editing"
            positive-text="删除"
            negative-text="取消"
            :positive-button-props="{ type: 'error' }"
            @positive-click="removeEditing"
          >
            <template #trigger>
              <n-button type="error" secondary :disabled="editing.id === auth.user?.id">
                <template #icon><n-icon :component="TrashOutline" /></template>
                删除用户
              </n-button>
            </template>
            确定删除用户「{{ editing.username }}」？其访问令牌与 S3 密钥也将失效。
          </n-popconfirm>
          <n-space>
            <n-button @click="modalOpen = false">取消</n-button>
            <n-button type="primary" :loading="saving" @click="save">{{ editing ? '保存' : '创建' }}</n-button>
          </n-space>
        </div>
      </template>
    </n-modal>

    <server-dir-picker-modal v-model:show="pickerOpen" :initial-path="pickerInitial" @confirm="onPicked" />
  </div>
</template>

<style scoped>
.title-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}
.modal-body {
  max-height: 62vh;
  overflow-y: auto;
  padding-right: 4px;
}
.modal-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.mounts-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 4px;
}
.mounts-head .label {
  font-size: 14px;
}
.hint {
  font-size: 12px;
  opacity: 0.6;
  margin: 4px 0 10px;
}
.mounts-empty {
  padding: 16px 0;
}
.mount-row {
  display: grid;
  grid-template-columns: 120px 1fr auto;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.m-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}
.m-perm {
  width: 96px;
}
@media (max-width: 560px) {
  .mount-row {
    grid-template-columns: 1fr auto;
    padding-bottom: 8px;
    border-bottom: 1px dashed rgba(128, 128, 128, 0.25);
  }
  .m-path {
    grid-column: 1 / -1;
    grid-row: 2;
  }
}
</style>
