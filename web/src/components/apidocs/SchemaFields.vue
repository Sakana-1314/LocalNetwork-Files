<script setup lang="ts">
import type { SchemaField } from '@/utils/openapi'

defineOptions({ name: 'SchemaFields' })
defineProps<{ fields: SchemaField[] }>()

function fmt(v: unknown): string {
  return typeof v === 'string' ? v : JSON.stringify(v)
}
</script>

<template>
  <ul class="fields">
    <li v-for="f in fields" :key="f.name" class="field">
      <div class="line">
        <code class="fname">{{ f.name }}</code>
        <span class="ftype">{{ f.type }}</span>
        <span v-if="f.required" class="req">必填</span>
        <span v-if="f.nullable" class="nullable">可为 null</span>
      </div>
      <div v-if="f.description || f.enum || f.default !== undefined || f.constraints" class="fdesc">
        <span v-if="f.description">{{ f.description }}</span>
        <span v-if="f.enum" class="extra">可选值：<code v-for="e in f.enum" :key="fmt(e)" class="enum">{{ fmt(e) }}</code></span>
        <span v-if="f.default !== undefined" class="extra">默认：<code>{{ fmt(f.default) }}</code></span>
        <span v-if="f.constraints" class="extra">{{ f.constraints }}</span>
      </div>
      <schema-fields v-if="f.children.length" :fields="f.children" class="nested" />
    </li>
  </ul>
</template>

<style scoped>
.fields {
  list-style: none;
  margin: 0;
  padding: 0;
}
.field {
  padding: 6px 0;
  border-bottom: 1px dashed rgba(128, 128, 128, 0.18);
}
.field:last-child {
  border-bottom: 0;
}
.line {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}
.fname {
  font-weight: 600;
  font-size: 13px;
}
.ftype {
  font-size: 12px;
  color: #2080f0;
  font-family: 'SF Mono', Menlo, Consolas, monospace;
}
.req {
  font-size: 11px;
  color: #d03050;
}
.nullable {
  font-size: 11px;
  opacity: 0.6;
}
.fdesc {
  font-size: 12.5px;
  opacity: 0.75;
  margin-top: 2px;
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
}
.enum {
  margin-right: 4px;
  padding: 0 4px;
  border-radius: 3px;
  background: rgba(128, 128, 128, 0.14);
}
.nested {
  margin: 4px 0 0 10px;
  padding-left: 12px;
  border-left: 2px solid rgba(128, 128, 128, 0.2);
}
</style>
