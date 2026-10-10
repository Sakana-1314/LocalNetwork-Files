import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type ThemePref = 'auto' | 'light' | 'dark'
export type ViewMode = 'list' | 'grid'

const KEY = 'lnf_prefs'

interface PrefsData {
  theme: ThemePref
  viewMode: ViewMode
  siderCollapsed: boolean
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

  watch([theme, viewMode, siderCollapsed], () => {
    try {
      const data: PrefsData = { theme: theme.value, viewMode: viewMode.value, siderCollapsed: siderCollapsed.value }
      localStorage.setItem(KEY, JSON.stringify(data))
    } catch {
      /* ignore */
    }
  })

  function cycleTheme(): void {
    theme.value = theme.value === 'auto' ? 'light' : theme.value === 'light' ? 'dark' : 'auto'
  }

  return { theme, viewMode, siderCollapsed, cycleTheme }
})
