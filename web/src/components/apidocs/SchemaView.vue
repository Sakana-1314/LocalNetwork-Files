<script setup lang="ts">
import { computed } from 'vue'
import { buildSchemaNode, type OADocument, type OASchema } from '@/utils/openapi'
import SchemaFields from './SchemaFields.vue'

const props = defineProps<{ doc: OADocument; schema?: OASchema }>()
const node = computed(() => buildSchemaNode(props.doc, props.schema))
</script>

<template>
  <div class="schema">
    <div class="root">
      <span class="type">{{ node.type }}</span>
      <span v-if="node.description" class="desc">{{ node.description }}</span>
      <span v-if="node.enum" class="desc">可选值：{{ node.enum.join(' | ') }}</span>
    </div>
    <schema-fields v-if="node.children.length" :fields="node.children" />
  </div>
</template>

<style scoped>
.root {
  display: flex;
  gap: 10px;
  align-items: baseline;
  margin-bottom: 4px;
  flex-wrap: wrap;
}
.type {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
  color: #2080f0;
}
.desc {
  font-size: 12.5px;
  opacity: 0.7;
}
</style>
