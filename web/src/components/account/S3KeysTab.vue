<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NPopconfirm,
  NSpace,
  NSwitch,
  NTag,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import type { S3Key } from '@/api/types'
import { s3KeysApi } from '@/api/tokens'
import SecretDialog from './SecretDialog.vue'
import { formatDate } from '@/utils/format'

const message = useMessage()
const list = ref<S3Key[]>([])
const loading = ref(false)
const createOpen = ref(false)
const creating = ref(false)
const form = reactive({ name: '', read_only: false })
const secretOpen = ref(false)
const secretItems = ref<{ label: string; value: string }[]>([])
const revealing = ref<string | null>(null)

/** Read the secret back from the server: it is stored encrypted, not hashed. */
async function reveal(k: S3Key): Promise<void> {
  revealing.value = k.access_key
  try {
    secretItems.value = [
      { label: 'Access Key', value: k.access_key },
      { label: 'Secret Key', value: await s3KeysApi.reveal(k.access_key) },
    ]
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
    list.value = await s3KeysApi.list()
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openCreate(): void {
  Object.assign(form, { name: '', read_only: false })
  createOpen.value = true
}

async function create(): Promise<void> {
  creating.value = true
  try {
    const k = await s3KeysApi.create({ name: form.name.trim() || undefined, read_only: form.read_only })
    createOpen.value = false
    secretItems.value = [
      { label: 'Access Key', value: k.access_key },
      { label: 'Secret Key', value: k.secret_key },
    ]
    secretOpen.value = true
    await load()
  } catch {
    /* 错误已提示 */
  } finally {
    creating.value = false
  }
}

async function remove(k: S3Key): Promise<void> {
  try {
    await s3KeysApi.remove(k.access_key)
    message.success('密钥已删除')
    await load()
  } catch {
    /* 错误已提示 */
  }
}

const columns = computed<DataTableColumns<S3Key>>(() => [
  {
    title: 'Access Key',
    key: 'access_key',
    render: (r) =>
      h('div', [h('code', { style: 'font-size:13px' }, r.access_key), r.name ? h('div', { style: 'font-size:12px;opacity:.6' }, r.name) : null]),
  },
  {
    title: '权限',
    key: 'read_only',
    width: 90,
    render: (r) => h(NTag, { size: 'small', bordered: false, type: r.read_only ? 'info' : 'warning' }, () => (r.read_only ? '只读' : '读写')),
  },
  { title: '创建时间', key: 'created_at', width: 150, render: (r) => formatDate(r.created_at) },
  {
    title: '',
    key: 'actions',
    width: 132,
    render: (r) =>
      h(NSpace, { size: 4, justify: 'end' }, () => [
        h(NButton, { size: 'small', quaternary: true, loading: revealing.value === r.access_key, onClick: () => reveal(r) }, () => '查看'),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(r), positiveText: '删除', negativeText: '取消' },
          {
            trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
            default: () => '删除该密钥？使用它的 S3 客户端将无法再访问。',
          },
        ),
      ]),
  },
])
</script>

<template>
  <div class="tab">
    <div class="head">
      <n-button type="primary" @click="openCreate">创建密钥</n-button>
    </div>
    <n-data-table :columns="columns" :data="list" :loading="loading" :row-key="(r: S3Key) => r.access_key" size="small" :bordered="false">
      <template #empty><span class="muted">还没有 S3 密钥</span></template>
    </n-data-table>

    <n-modal v-model:show="createOpen" preset="card" title="创建 S3 密钥" style="width: 440px; max-width: calc(100vw - 24px)">
      <n-form label-placement="top" @submit.prevent="create">
        <n-form-item label="备注名称（可选）">
          <n-input v-model:value="form.name" placeholder="例如：rclone 同步" maxlength="64" />
        </n-form-item>
        <n-form-item label="只读">
          <n-switch v-model:value="form.read_only" />
          <span class="sw-hint">{{ form.read_only ? '仅允许读取' : '允许读写（仍受目录本身权限限制）' }}</span>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="createOpen = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="create">创建</n-button>
        </n-space>
      </template>
    </n-modal>

    <secret-dialog v-model:show="secretOpen" title="S3 密钥" :items="secretItems" />
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
.sw-hint {
  margin-left: 10px;
  font-size: 13px;
  opacity: 0.65;
}
.muted {
  opacity: 0.6;
}
</style>
