<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ name: string; matches?: number[] }>()

/** matches 为 Unicode 字符下标，需用 Array.from 按码点切分 */
const parts = computed(() => {
  const chars = Array.from(props.name)
  const set = new Set(props.matches ?? [])
  const out: { text: string; hit: boolean }[] = []
  chars.forEach((ch, i) => {
    const hit = set.has(i)
    const last = out[out.length - 1]
    if (last && last.hit === hit) last.text += ch
    else out.push({ text: ch, hit })
  })
  return out
})
</script>

<template>
  <span class="hl-name"><template v-for="(p, i) in parts" :key="i"><mark v-if="p.hit" class="hl">{{ p.text }}</mark><template v-else>{{ p.text }}</template></template></span>
</template>
