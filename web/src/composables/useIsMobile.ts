import { onBeforeUnmount, ref, type Ref } from 'vue'

const QUERY = '(max-width: 768px)'
let shared: Ref<boolean> | null = null
let users = 0
let mql: MediaQueryList | null = null
const onChange = (e: MediaQueryListEvent) => {
  if (shared) shared.value = e.matches
}

/** 是否为窄屏（≤768px） */
export function useIsMobile(): Ref<boolean> {
  if (!shared) {
    mql = window.matchMedia(QUERY)
    shared = ref(mql.matches)
  }
  if (users++ === 0) mql?.addEventListener('change', onChange)
  onBeforeUnmount(() => {
    if (--users === 0) mql?.removeEventListener('change', onChange)
  })
  return shared
}
