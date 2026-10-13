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
import { AddOutline, PersonAddOutline, TrashOutline } from '@vicons/ionicons5'
import { RouterLink } from 'vue-router'
import type { BucketView, GrantInput, Role, User, UserInput } from '@/api/types'
import { adminApi } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import { useIsMobile } from '@/composables/useIsMobile'
import { formatDate } from '@/utils/format'

const message = useMessage()
const auth = useAuthStore()
const isMobile = useIsMobile()

const users = ref<User[]>([])
const buckets = ref<BucketView[]>([])
const loading = ref(false)

async function load(): Promise<void> {
  loading.value = true
  try {
    ;[users.value, buckets.value] = await Promise.all([adminApi.users(), adminApi.buckets()])
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

// 当前密码：进入编辑时读一次，管理员可以直接查看或转述给用户。
const currentPw = ref<string | null>(null)
const pwKnown = ref(false)
const pwVisible = ref(false)
const pwLoading = ref(false)

async function copyPw(): Promise<void> {
  if (!currentPw.value) return
  try {
    await navigator.clipboard.writeText(currentPw.value)
    message.success('密码已复制')
  } catch {
    message.error('复制失败，请手动选择')
  }
}

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
  grants: (Omit<GrantInput, 'bucket_id'> & { bucket_id: number | null; _key: number })[]
}
let keySeq = 0
const form = reactive<FormModel>({ username: '', password: '', role: 'user', disabled: false, grants: [] })

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
  Object.assign(form, { username: '', password: '', role: 'user', disabled: false, grants: [] })
  if (buckets.value.length) addGrant()
  modalOpen.value = true
}

function openEdit(u: User): void {
  editing.value = u
  currentPw.value = null
  pwKnown.value = false
  pwVisible.value = false
  pwLoading.value = true
  void adminApi
    .userPassword(u.id)
    .then((r) => {
      currentPw.value = r.password
      pwKnown.value = r.known
    })
    .catch(() => {
      currentPw.value = ''
      pwKnown.value = false
    })
    .finally(() => {
      pwLoading.value = false
    })
  Object.assign(form, {
    username: u.username,
    password: '',
    role: u.role,
    disabled: u.disabled,
    grants: u.grants.map((g) => ({ bucket_id: g.bucket_id, writable: g.writable, _key: ++keySeq })),
  })
  modalOpen.value = true
}

function addGrant(): void {
  form.grants.push({ bucket_id: null, writable: false, _key: ++keySeq })
}
function removeGrant(i: number): void {
  form.grants.splice(i, 1)
}

/** 每个桶只能授权一次：已选的桶在其它行里不可再选。 */
function bucketOptions(current: number | null): { label: string; value: number; disabled: boolean }[] {
  const taken = new Set(form.grants.map((g) => g.bucket_id))
  return buckets.value.map((b) => ({ label: b.name, value: b.id, disabled: b.id !== current && taken.has(b.id) }))
}

function bucketPaths(id: number): string {
  return buckets.value.find((b) => b.id === id)?.paths.join('\n') ?? ''
}

async function save(): Promise<void> {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  const payload: UserInput = {
    username: form.username.trim(),
    password: form.password,
    role: form.role,
    disabled: form.disabled,
    grants: form.grants
      .filter((g): g is typeof g & { bucket_id: number } => g.bucket_id !== null)
      .map((g) => ({ bucket_id: g.bucket_id, writable: g.writable })),
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
      title: '授权桶',
      key: 'grants',
      render: (u) =>
        u.grants.length
          ? h(
              'div',
              { style: 'display:flex;flex-wrap:wrap;gap:4px' },
              u.grants.map((m) =>
                h(
                  NTooltip,
                  {},
                  {
                    trigger: () =>
                      h(NTag, { size: 'small', bordered: false, type: m.writable ? 'success' : 'default' }, () => [
                        m.name,
                        h('span', { style: 'opacity:.65;margin-left:4px;font-size:11px' }, m.writable ? '可写' : '只读'),
                      ]),
                    default: () => bucketPaths(m.bucket_id) || m.name,
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
          <n-form-item v-if="editing" label="当前密码">
            <div class="pw-row">
              <n-input
                :value="pwLoading ? '读取中…' : !pwKnown ? '' : pwVisible ? currentPw ?? '' : '••••••••'"
                readonly
                :placeholder="pwLoading ? '读取中…' : pwKnown ? '' : '该账户没有留存密码，请设置新密码'"
                :input-props="{ autocomplete: 'off' }"
                class="pw-input"
              />
              <n-button v-if="pwKnown && !pwLoading" quaternary size="small" @click="pwVisible = !pwVisible">
                {{ pwVisible ? '隐藏' : '显示' }}
              </n-button>
              <n-button v-if="pwKnown && !pwLoading" quaternary size="small" @click="copyPw">复制</n-button>
            </div>
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
            <span class="label">授权桶</span>
            <n-button size="small" dashed :disabled="form.grants.length >= buckets.length" @click="addGrant">
              <template #icon><n-icon :component="AddOutline" /></template>
              添加桶
            </n-button>
          </div>
          <p class="hint">
            桶名即该用户虚拟根目录下的文件夹名（也是 SMB 共享名与 S3 Bucket 名）。桶在
            <router-link :to="{ name: 'admin-system' }">系统管理</router-link>
            中创建。
          </p>
          <n-empty
            v-if="!form.grants.length"
            :description="buckets.length ? '尚未授权任何桶' : '还没有桶，请先到系统管理中创建'"
            size="small"
            class="mounts-empty"
          />
          <div v-for="(g, i) in form.grants" :key="g._key" class="mount-row">
            <n-select
              v-model:value="g.bucket_id"
              :options="bucketOptions(g.bucket_id)"
              placeholder="选择桶"
              filterable
              class="m-name"
            />
            <div class="m-actions">
              <n-select
                class="m-perm"
                :value="g.writable ? 'rw' : 'ro'"
                :options="PERMISSION_OPTIONS"
                @update:value="(v: string) => (g.writable = v === 'rw')"
              />
              <n-button quaternary circle size="small" type="error" @click="removeGrant(i)">
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
.pw-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}
.pw-row .pw-input {
  flex: 1;
  min-width: 0;
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
  grid-template-columns: 1fr auto;
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
</style>
