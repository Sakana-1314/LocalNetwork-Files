<script setup lang="ts">
import { computed } from 'vue'
import { NTable, NTag } from 'naive-ui'
import { deref, type OADocument, type OAResponse, type OpEntry } from '@/utils/openapi'
import { renderMarkdown } from '@/utils/markdown'
import SchemaView from './SchemaView.vue'
import CopyBlock from '@/components/common/CopyBlock.vue'

const props = defineProps<{ doc: OADocument; entry: OpEntry; base: string }>()

const body = computed(() => deref(props.doc, props.entry.op.requestBody))
const responses = computed(() =>
  Object.entries(props.entry.op.responses ?? {}).map(([code, r]) => ({ code, res: deref<OAResponse>(props.doc, r) ?? {} })),
)

function paramType(p: { schema?: { type?: string; $ref?: string; enum?: unknown[]; default?: unknown } }): string {
  const s = p.schema
  if (!s) return '-'
  if (s.$ref) return s.$ref.split('/').pop() ?? '-'
  return s.type ?? '-'
}

const curl = computed(() => {
  const e = props.entry
  const pathParams = e.parameters.filter((p) => p.in === 'path')
  let path = e.path
  for (const p of pathParams) path = path.replace(`{${p.name}}`, `<${p.name}>`)
  const q = e.parameters.filter((p) => p.in === 'query' && p.required)
  const qs = q.length ? '?' + q.map((p) => `${p.name}=<${p.name}>`).join('&') : ''
  const lines = [`curl -X ${e.method} "${props.base}${path}${qs}"`]
  if (!e.isPublic) lines.push(`  -H "Authorization: Bearer <TOKEN>"`)
  const ct = body.value?.content ? Object.keys(body.value.content)[0] : undefined
  if (ct === 'application/json') lines.push(`  -H "Content-Type: application/json"`, `  -d '{...}'`)
  else if (ct) lines.push(`  -H "Content-Type: ${ct}"`, `  --data-binary @./local-file`)
  return lines.join(' \\\n')
})
</script>

<template>
  <div class="op-detail">
    <div v-if="entry.op.description" class="md" v-html="renderMarkdown(entry.op.description)" />
    <div class="auth">
      <n-tag size="small" :bordered="false" :type="entry.isPublic ? 'success' : 'warning'">
        {{ entry.isPublic ? '无需认证' : '需要认证（Bearer Token）' }}
      </n-tag>
    </div>

    <template v-if="entry.parameters.length">
      <h4>参数</h4>
      <div class="table-wrap">
        <n-table size="small" :single-line="false">
          <thead>
            <tr>
              <th>名称</th>
              <th>位置</th>
              <th>类型</th>
              <th>必填</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in entry.parameters" :key="p.in + p.name">
              <td><code>{{ p.name }}</code></td>
              <td>{{ p.in }}</td>
              <td class="t">
                {{ paramType(p) }}
                <div v-if="deref(doc, p.schema)?.enum" class="sub">{{ deref(doc, p.schema)?.enum?.join(' | ') }}</div>
              </td>
              <td>{{ p.required ? '是' : '否' }}</td>
              <td>
                {{ p.description || '' }}
                <span v-if="p.schema?.default !== undefined" class="sub">默认 {{ String(p.schema.default) }}</span>
              </td>
            </tr>
          </tbody>
        </n-table>
      </div>
    </template>

    <template v-if="body">
      <h4>请求体 <span v-if="body.required" class="req">必填</span></h4>
      <div v-for="(mt, ct) in body.content" :key="ct" class="block">
        <div class="ct">{{ ct }}</div>
        <schema-view :doc="doc" :schema="mt.schema" />
      </div>
    </template>

    <h4>响应</h4>
    <div v-for="r in responses" :key="r.code" class="block">
      <div class="resp-head">
        <n-tag size="small" :bordered="false" :type="r.code.startsWith('2') ? 'success' : r.code.startsWith('4') ? 'warning' : 'error'">
          {{ r.code }}
        </n-tag>
        <span>{{ r.res.description }}</span>
      </div>
      <template v-for="(mt, ct) in r.res.content ?? {}" :key="ct">
        <div class="ct">{{ ct }}</div>
        <schema-view :doc="doc" :schema="mt.schema" />
      </template>
    </div>

    <h4>示例</h4>
    <copy-block :code="curl" />
  </div>
</template>

<style scoped>
.op-detail {
  font-size: 13px;
}
h4 {
  margin: 16px 0 8px;
  font-size: 13px;
  font-weight: 600;
}
.md :deep(p) {
  margin: 0 0 8px;
}
.auth {
  margin: 4px 0;
}
.table-wrap {
  overflow-x: auto;
}
.t {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}
.sub {
  display: block;
  font-size: 11.5px;
  opacity: 0.6;
}
.req {
  font-size: 11px;
  color: #d03050;
  font-weight: 400;
}
.block {
  padding: 8px 12px;
  border-radius: 8px;
  border: 1px solid rgba(128, 128, 128, 0.2);
  margin-bottom: 8px;
}
.ct {
  font-size: 11.5px;
  opacity: 0.55;
  margin: 4px 0;
  font-family: 'SF Mono', Menlo, Consolas, monospace;
}
.resp-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>
