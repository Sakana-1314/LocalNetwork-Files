<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NCollapse, NCollapseItem, NEmpty, NIcon, NInput, NSkeleton, NTag } from 'naive-ui'
import { SearchOutline } from '@vicons/ionicons5'
import { systemApi } from '@/api/system'
import { API_BASE } from '@/api/http'
import { collectOperations, type OADocument, type OpEntry } from '@/utils/openapi'
import { renderMarkdown } from '@/utils/markdown'
import OperationDetail from '@/components/apidocs/OperationDetail.vue'
import CopyBlock from '@/components/common/CopyBlock.vue'

const doc = ref<OADocument | null>(null)
const loading = ref(true)
const error = ref('')
const filter = ref('')

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const d = await systemApi.openapi()
    if (!d || typeof d !== 'object' || !('paths' in d)) throw new Error('文档格式不正确')
    doc.value = d as OADocument
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}
onMounted(load)

const base = computed(() => {
  const server = doc.value?.servers?.[0]?.url ?? API_BASE
  return server.startsWith('http') ? server : location.origin + server
})

const TAG_LABELS: Record<string, string> = {
  auth: '认证',
  files: '文件',
  media: '媒体',
  tokens: '令牌与密钥',
  system: '系统',
  admin: '管理',
}

const groups = computed(() => {
  const d = doc.value
  if (!d) return []
  const q = filter.value.trim().toLowerCase()
  const ops = collectOperations(d).filter(
    (o) =>
      !q ||
      o.path.toLowerCase().includes(q) ||
      o.method.toLowerCase().includes(q) ||
      (o.op.summary ?? '').toLowerCase().includes(q) ||
      (o.op.description ?? '').toLowerCase().includes(q),
  )
  const order = (d.tags ?? []).map((t) => t.name)
  const map = new Map<string, OpEntry[]>()
  for (const o of ops) {
    const tag = o.op.tags?.[0] ?? 'other'
    if (!map.has(tag)) map.set(tag, [])
    map.get(tag)!.push(o)
    if (!order.includes(tag)) order.push(tag)
  }
  return order
    .filter((t) => map.has(t))
    .map((t) => ({
      tag: t,
      label: TAG_LABELS[t] ? `${TAG_LABELS[t]} · ${t}` : t,
      description: d.tags?.find((x) => x.name === t)?.description,
      ops: map.get(t)!,
    }))
})

const total = computed(() => groups.value.reduce((s, g) => s + g.ops.length, 0))

const curlExample = computed(
  () => `# 1. 登录获取会话令牌（或在「设置 → 访问令牌」中创建长期令牌）
curl -X POST "${base.value}/auth/login" \\
  -H "Content-Type: application/json" \\
  -d '{"username":"<USER>","password":"<PASSWORD>"}'

# 2. 携带令牌调用接口
curl "${base.value}/fs/list?path=/" \\
  -H "Authorization: Bearer <TOKEN>"`,
)

const METHOD_COLORS: Record<string, string> = {
  GET: '#2080f0',
  POST: '#18a058',
  PUT: '#f0a020',
  PATCH: '#8a63d2',
  DELETE: '#d03050',
}
</script>

<template>
  <div class="page api-page">
    <h2 class="page-title">API 文档</h2>
    <p class="page-desc">
      基于服务端 OpenAPI 规范自动生成。原始文档：<a href="/api/openapi.json" target="_blank" rel="noopener">/api/openapi.json</a>
    </p>

    <div v-if="loading">
      <n-skeleton height="140px" :sharp="false" style="margin-bottom: 16px" />
      <n-skeleton text :repeat="8" />
    </div>
    <n-empty v-else-if="error || !doc" :description="`无法加载 API 文档：${error}`">
      <template #extra><n-button size="small" @click="load">重试</n-button></template>
    </n-empty>

    <template v-else>
      <n-card size="small" class="intro">
        <div class="intro-head">
          <h3>{{ doc.info?.title ?? 'API' }}</h3>
          <n-tag size="small" :bordered="false">v{{ doc.info?.version }}</n-tag>
          <n-tag size="small" :bordered="false" type="info">Base URL：{{ base }}</n-tag>
        </div>
        <div class="md" v-html="renderMarkdown(doc.info?.description)" />
        <n-alert type="info" :bordered="false" title="认证" class="auth">
          除登录等公开接口外，所有请求都需要在请求头中携带
          <code>Authorization: Bearer &lt;token&gt;</code>。令牌可以是登录返回的会话令牌，也可以是在「设置 →
          访问令牌」中创建的长期令牌（以 <code>lnf_</code> 开头）。对于 <code>&lt;img&gt;</code>、<code>&lt;video&gt;</code>
          等无法设置请求头的场景，可使用查询参数 <code>access_token=&lt;token&gt;</code>。错误响应体统一为
          <code>{"error": "message"}</code>。
        </n-alert>
        <copy-block :code="curlExample" />
      </n-card>

      <n-input v-model:value="filter" clearable placeholder="筛选接口（路径 / 方法 / 描述）" class="filter">
        <template #prefix><n-icon :component="SearchOutline" /></template>
      </n-input>
      <div class="count">{{ total }} 个接口</div>

      <n-empty v-if="!groups.length" description="没有匹配的接口" style="padding: 40px 0" />

      <section v-for="g in groups" :key="g.tag" class="group">
        <h3 class="group-title">{{ g.label }}</h3>
        <p v-if="g.description" class="group-desc">{{ g.description }}</p>
        <n-collapse :default-expanded-names="[]" arrow-placement="right" class="ops">
          <n-collapse-item v-for="o in g.ops" :key="o.id" :name="o.id">
            <template #header>
              <div class="op-head">
                <span class="method" :style="{ background: METHOD_COLORS[o.method] ?? '#888' }">{{ o.method }}</span>
                <code class="path">{{ o.path }}</code>
                <span class="summary">{{ o.op.summary }}</span>
                <n-tag v-if="o.isPublic" size="tiny" :bordered="false" type="success">公开</n-tag>
              </div>
            </template>
            <operation-detail :doc="doc" :entry="o" :base="base" />
          </n-collapse-item>
        </n-collapse>
      </section>
    </template>
  </div>
</template>

<style scoped>
.api-page {
  max-width: 1080px;
}
.intro {
  margin-bottom: 20px;
}
.intro-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.intro-head h3 {
  margin: 0;
  font-size: 17px;
}
.md {
  font-size: 13.5px;
  line-height: 1.75;
  margin: 8px 0;
}
.md :deep(p) {
  margin: 0 0 8px;
}
.md :deep(ul) {
  margin: 0 0 8px;
  padding-left: 20px;
}
.md :deep(code),
.auth code {
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(128, 128, 128, 0.16);
  font-size: 12.5px;
}
.auth {
  margin: 12px 0 8px;
  line-height: 1.75;
}
.filter {
  max-width: 420px;
}
.count {
  font-size: 12px;
  opacity: 0.55;
  margin: 6px 2px 0;
}
.group {
  margin-top: 24px;
}
.group-title {
  font-size: 16px;
  margin: 0 0 4px;
}
.group-desc {
  margin: 0 0 8px;
  font-size: 13px;
  opacity: 0.65;
}
.ops :deep(.n-collapse-item) {
  margin: 0 !important;
  padding: 10px 4px;
  border-top: 1px solid rgba(128, 128, 128, 0.18);
}
.ops :deep(.n-collapse-item .n-collapse-item__header) {
  padding-top: 0 !important;
}
.op-head {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex-wrap: wrap;
}
.method {
  display: inline-block;
  min-width: 62px;
  text-align: center;
  color: #fff;
  font-size: 11.5px;
  font-weight: 700;
  border-radius: 4px;
  padding: 2px 6px;
  letter-spacing: 0.5px;
}
.path {
  font-size: 13.5px;
  font-weight: 600;
  word-break: break-all;
}
.summary {
  font-size: 13px;
  opacity: 0.7;
}
</style>
