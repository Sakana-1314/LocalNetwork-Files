import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type ThemePref = 'auto' | 'light' | 'dark'
export type ViewMode = 'list' | 'grid'
export type VideoCodec = 'h264' | 'h265'

const KEY = 'ml_prefs'

interface PrefsData {
  theme: ThemePref
  viewMode: ViewMode
  siderCollapsed: boolean
  /** 转码输出的视频编码 */
  videoCodec: VideoCodec
  /** 转码视频码率（kbps），0 为按画质自动 */
  videoBitrate: number
}

function load(): Partial<PrefsData> {
  try {
    const raw = localStorage.getItem(KEY)
    return raw ? (JSON.parse(raw) as Partial<PrefsData>) : {}
  } catch {
    return {}
  }
}

export const usePrefsStore = defineStore('prefs', () => {
  const saved = load()
  const theme = ref<ThemePref>(saved.theme === 'light' || saved.theme === 'dark' ? saved.theme : 'auto')
  const viewMode = ref<ViewMode>(saved.viewMode === 'grid' ? 'grid' : 'list')
  const siderCollapsed = ref<boolean>(!!saved.siderCollapsed)
  const videoCodec = ref<VideoCodec>(saved.videoCodec === 'h265' ? 'h265' : 'h264')
  const videoBitrate = ref<number>(typeof saved.videoBitrate === 'number' && saved.videoBitrate > 0 ? saved.videoBitrate : 0)

  watch([theme, viewMode, siderCollapsed, videoCodec, videoBitrate], () => {
    try {
      const data: PrefsData = {
        theme: theme.value,
        viewMode: viewMode.value,
        siderCollapsed: siderCollapsed.value,
        videoCodec: videoCodec.value,
        videoBitrate: videoBitrate.value,
      }
      localStorage.setItem(KEY, JSON.stringify(data))
    } catch {
      /* ignore */
    }
  })

  function cycleTheme(): void {
    theme.value = theme.value === 'auto' ? 'light' : theme.value === 'light' ? 'dark' : 'auto'
  }

  return { theme, viewMode, siderCollapsed, videoCodec, videoBitrate, cycleTheme }
})
