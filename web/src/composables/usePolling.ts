import { onBeforeUnmount } from 'vue'

/** 简单轮询：start() 开始，fn 返回 false 时停止 */
export function usePolling(fn: () => Promise<boolean>, interval: number) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  const tick = async () => {
    if (!running) return
    let cont = false
    try {
      cont = await fn()
    } catch {
      cont = false
    }
    if (running && cont) timer = setTimeout(tick, interval)
    else running = false
  }
  function start(): void {
    if (running) return
    running = true
    timer = setTimeout(tick, interval)
  }
  function stop(): void {
    running = false
    if (timer) clearTimeout(timer)
  }
  onBeforeUnmount(stop)
  return { start, stop }
}
