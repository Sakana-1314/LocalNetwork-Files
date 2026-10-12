<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton,
  NCheckbox,
  NCheckboxGroup,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NPopconfirm,
  NSpace,
  NTag,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import type { AccessToken, TokenScope } from '@/api/types'
import { tokensApi } from '@/api/tokens'
import SecretDialog from './SecretDialog.vue'
import { formatDate, formatRelative } from '@/utils/format'
import { useIsMobile } from '@/composables/useIsMobile'

const message = useMessage()
const isMobile = useIsMobile()
const list = ref<AccessToken[]>([])
const loading = ref(false)

const createOpen = ref(false)
const creating = ref(false)
const form = reactive<{ name: string; scopes: TokenScope[]; days: number | null }>({ name: '', scopes: ['read'], days: 0 })

const secretOpen = ref(false)
const secretItems = ref<{ label: string; value: string }[]>([])
const revealing = ref<number | null>(null)

/** Read the token back from the server: the plaintext is stored, not hashed. */
async function reveal(t: AccessToken): Promise<void> {
  revealing.value = t.id
  try {
    secretItems.value = [{ label: `访问令牌 · ${t.name}`, value: await tokensApi.reveal(t.id) }]
    secretOpen.value = true
  } catch {
    /* 错误已提示 */
  } finally {
    revealing.value = null
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    list.value = await tokensApi.list()
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openCreate(): void {
  Object.assign(form, { name: '', scopes: ['read'], days: 0 })
  createOpen.value = true
}

async function create(): Promise<void> {
  if (!form.name.trim()) {
    message.warning('请填写令牌名称')
    return
  }
  if (!form.scopes.length) {
    message.warning('请至少选择一个权限')
    return
  }
  creating.value = true
  try {
    const t = await tokensApi.create({
      name: form.name.trim(),
      scopes: form.scopes,
      expires_in_days: form.days && form.days > 0 ? form.days : 0,
    })
    createOpen.value = false
    secretItems.value = [{ label: `访问令牌 · ${t.name}`, value: t.token }]
    secretOpen.value = true
    await load()
  } catch {
    /* 错误已提示 */
  } finally {
    creating.value = false
  }
}

async function revoke(t: AccessToken): Promise<void> {
  try {
    await tokensApi.revoke(t.id)
    message.success('令牌已吊销')
    await load()
  } catch {
    /* 错误已提示 */
  }
}

function isExpired(t: AccessToken): boolean {
  return !!t.expires_at && new Date(t.expires_at).getTime() < Date.now()
}

const columns = computed<DataTableColumns<AccessToken>>(() => {
  const cols: DataTableColumns<AccessToken> = [
    {
      title: '名称',
      key: 'name',
      render: (r) =>
        h('div', [
          h('div', { style: 'font-weight:500' }, r.name),
          h('code', { style: 'font-size:12px;opacity:.6' }, `${r.prefix}…`),
        ]),
    },
    {
      title: '权限',
      key: 'scopes',
      width: 130,
      render: (r) =>
        h(NSpace, { size: 4 }, () =>
          r.scopes.map((s) =>
            h(NTag, { size: 'small', bordered: false, type: s === 'write' ? 'warning' : 'info' }, () => (s === 'write' ? '写入' : '读取')),
          ),
        ),
    },
    {
      title: '过期时间',
      key: 'expires_at',
      width: 150,
      render: (r) =>
        r.expires_at
          ? h(NTag, { size: 'small', bordered: false, type: isExpired(r) ? 'error' : 'default' }, () =>
              isExpired(r) ? '已过期' : formatDate(r.expires_at, 'YYYY-MM-DD'),
            )
          : h('span', { style: 'opacity:.6' }, '永不过期'),
    },
  ]
  if (!isMobile.value) {
    cols.push(
      { title: '最近使用', key: 'last_used_at', width: 120, render: (r) => (r.last_used_at ? formatRelative(r.last_used_at) : '从未使用') },
      { title: '创建时间', key: 'created_at', width: 150, render: (r) => formatDate(r.created_at) },
    )
  }
  cols.push({
    title: '',
    key: 'actions',
    width: 132,
    render: (r) =>
      h(NSpace, { size: 4, justify: 'end' }, () => [
        h(NButton, { size: 'small', quaternary: true, loading: revealing.value === r.id, onClick: () => reveal(r) }, () => '查看'),
        h(
          NPopconfirm,
          { onPositiveClick: () => revoke(r), positiveText: '吊销', negativeText: '取消' },
          {
            trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '吊销'),
            default: () => `吊销令牌「${r.name}」？使用它的客户端将立即失去访问权限。`,
          },
        ),
      ]),
  })
  return cols
})
</script>

<template>
  <div class="tab">
    <div class="head">
      <p class="desc">
        访问令牌可用于 API 调用（<code>Authorization: Bearer &lt;token&gt;</code>）以及 WebDAV 登录（用户名 + 令牌作为密码）。
      </p>
      <n-button type="primary" @click="openCreate">创建令牌</n-button>
    </div>
    <n-data-table :columns="columns" :data="list" :loading="loading" :row-key="(r: AccessToken) => r.id" size="small" :bordered="false">
      <template #empty><span class="muted">还没有访问令牌</span></template>
    </n-data-table>

    <n-modal v-model:show="createOpen" preset="card" title="创建访问令牌" style="width: 460px; max-width: calc(100vw - 24px)">
      <n-form label-placement="top" @submit.prevent="create">
        <n-form-item label="名称" required>
          <n-input v-model:value="form.name" placeholder="例如：NAS 备份脚本" maxlength="64" />
        </n-form-item>
        <n-form-item label="权限">
          <n-checkbox-group v-model:value="form.scopes">
            <n-space>
              <n-checkbox value="read">读取</n-checkbox>
              <n-checkbox value="write">写入</n-checkbox>
            </n-space>
          </n-checkbox-group>
        </n-form-item>
        <n-form-item label="有效期（天，0 表示永不过期）">
          <n-input-number v-model:value="form.days" :min="0" :max="3650" style="width: 100%" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="createOpen = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="create">创建</n-button>
        </n-space>
      </template>
    </n-modal>

    <secret-dialog v-model:show="secretOpen" title="访问令牌" :items="secretItems" />
  </div>
</template>

<style scoped>
.tab {
  padding-top: 8px;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 12px;
}
.desc {
  margin: 0;
  font-size: 13px;
  opacity: 0.75;
  line-height: 1.7;
}
.muted {
  opacity: 0.6;
}
</style>
