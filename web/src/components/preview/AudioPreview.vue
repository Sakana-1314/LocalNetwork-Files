<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { NButton, NIcon, NSlider, NTag } from 'naive-ui'
import { MusicalNotes, PauseOutline, PlayOutline } from '@vicons/ionicons5'
import type { Entry, MediaInfo } from '@/api/types'
import { fsApi } from '@/api/fs'
import { mediaApi } from '@/api/media'
import { audioNeedsTranscode } from '@/utils/fileKind'
import { formatBitrate, formatDuration, formatSize } from '@/utils/format'

const props = defineProps<{ entry: Entry }>()

const audioRef = ref<HTMLAudioElement | null>(null)
const mode = ref<'direct' | 'stream'>(audioNeedsTranscode(props.entry.name, props.entry.mime) ? 'stream' : 'direct')
const info = ref<MediaInfo | null>(null)
const coverFailed = ref(false)
const error = ref('')

// 转码流模式下浏览器无法拖动进度，使用 start 参数重新请求
const offset = ref(0)
const position = ref(0)
const playing = ref(false)
const seeking = ref(false)

const src = computed(() =>
  mode.value === 'direct' ? fsApi.rawUrl(props.entry.path) : mediaApi.audioStreamUrl(props.entry.path, offset.value),
)
const cover = computed(() => (props.entry.thumb && !coverFailed.value ? mediaApi.thumbUrl(props.entry.path, 512) : ''))
const duration = computed(() => info.value?.duration ?? 0)

function onError(): void {
  if (mode.value === 'direct') {
    mode.value = 'stream'
    offset.value = 0
    void loadInfo()
  } else {
    error.value = '音频无法播放'
  }
}

async function loadInfo(): Promise<void> {
  if (info.value) return
  try {
    info.value = await mediaApi.info(props.entry.path, { silent: true })
  } catch {
    /* ignore */
  }
}

function onTime(): void {
  if (!seeking.value && audioRef.value) position.value = offset.value + audioRef.value.currentTime
}

function seekStream(v: number): void {
  seeking.value = false
  offset.value = Math.max(0, Math.min(v, duration.value || v))
  position.value = offset.value
  // src 计算属性变化后重新播放
  requestAnimationFrame(() => void audioRef.value?.play().catch(() => {}))
}

let slideTimer: ReturnType<typeof setTimeout> | undefined
function onSlide(v: number): void {
  seeking.value = true
  position.value = v
  if (slideTimer) clearTimeout(slideTimer)
  slideTimer = setTimeout(() => seekStream(position.value), 350)
}

function toggle(): void {
  const a = audioRef.value
  if (!a) return
  if (a.paused) void a.play().catch(() => {})
  else a.pause()
}

onMounted(() => {
  void loadInfo()
  void audioRef.value?.play().catch(() => {})
})
onBeforeUnmount(() => {
  if (slideTimer) clearTimeout(slideTimer)
  const a = audioRef.value
  if (a) {
    a.pause()
    a.removeAttribute('src')
    a.load()
  }
})
</script>

<template>
  <div class="audio-preview">
    <div class="cover">
      <img v-if="cover" :src="cover" :alt="entry.name" @error="coverFailed = true" />
      <n-icon v-else :component="MusicalNotes" size="96" color="#9b5de5" />
    </div>
    <div class="title">{{ entry.name }}</div>
    <div class="meta">
      <n-tag size="small" :bordered="false" :type="mode === 'direct' ? 'success' : 'info'">
        {{ mode === 'direct' ? '直连播放' : '实时转码 MP3' }}
      </n-tag>
      <span>{{ formatSize(entry.size) }}</span>
      <span v-if="info?.audio[0]">{{ info.audio[0].codec }}</span>
      <span v-if="info?.bit_rate">{{ formatBitrate(info.bit_rate) }}</span>
      <span v-if="duration">{{ formatDuration(duration) }}</span>
    </div>

    <audio
      ref="audioRef"
      :key="src"
      :src="src"
      :controls="mode === 'direct'"
      autoplay
      preload="auto"
      class="player"
      @error="onError"
      @timeupdate="onTime"
      @play="playing = true"
      @pause="playing = false"
      @ended="playing = false"
    />

    <div v-if="mode === 'stream'" class="stream-ctl">
      <n-button circle secondary @click="toggle">
        <template #icon><n-icon :component="playing ? PauseOutline : PlayOutline" /></template>
      </n-button>
      <span class="t">{{ formatDuration(position) }}</span>
      <n-slider
        class="slider"
        :value="position"
        :min="0"
        :max="Math.max(duration, position, 1)"
        :step="1"
        :tooltip="false"
        :disabled="!duration"
        @update:value="onSlide"
      />
      <span class="t">{{ formatDuration(duration) }}</span>
    </div>
    <div v-if="error" class="err">{{ error }}</div>
  </div>
</template>

<style scoped>
.audio-preview {
  height: 100%;
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  padding: 16px;
}
.cover {
  width: min(300px, 60vw);
  aspect-ratio: 1;
  border-radius: 14px;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(255, 255, 255, 0.06);
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}
.cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.title {
  font-size: 17px;
  font-weight: 600;
  text-align: center;
  word-break: break-all;
  max-width: 600px;
}
.meta {
  display: flex;
  gap: 10px;
  align-items: center;
  font-size: 13px;
  opacity: 0.75;
  flex-wrap: wrap;
  justify-content: center;
}
.player {
  width: min(560px, 100%);
}
.stream-ctl {
  width: min(560px, 100%);
  display: flex;
  align-items: center;
  gap: 10px;
}
.slider {
  flex: 1;
}
.t {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  opacity: 0.8;
}
.err {
  color: #e88080;
}
</style>
