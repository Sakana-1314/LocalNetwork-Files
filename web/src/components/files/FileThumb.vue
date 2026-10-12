<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Entry } from '@/api/types'
import { mediaApi, type ThumbSize } from '@/api/media'
import FileIcon from '@/components/common/FileIcon.vue'

const props = withDefaults(defineProps<{ entry: Entry; size?: ThumbSize; iconSize?: number; mount?: boolean }>(), {
  size: 256,
  iconSize: 56,
  mount: false,
})

const failed = ref(false)
const loaded = ref(false)
watch(
  () => props.entry.path,
  () => {
    failed.value = false
    loaded.value = false
  },
)
const src = computed(() => (!failed.value ? mediaApi.thumbUrl(props.entry, props.size) : ''))
</script>

<template>
  <div class="thumb">
    <img
      v-if="src"
      :src="src"
      :alt="entry.name"
      loading="lazy"
      decoding="async"
      draggable="false"
      :class="{ loaded }"
      @load="loaded = true"
      @error="failed = true"
    />
    <file-icon v-if="!src || !loaded" :entry="entry" :size="iconSize" :mount="mount" class="icon" />
    <span v-if="entry.kind === 'video' && src && loaded" class="play">▶</span>
  </div>
</template>

<style scoped>
.thumb {
  position: relative;
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  opacity: 0;
  transition: opacity 0.2s;
}
img.loaded {
  opacity: 1;
}
.play {
  position: absolute;
  right: 6px;
  bottom: 6px;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font-size: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding-left: 2px;
}
</style>
