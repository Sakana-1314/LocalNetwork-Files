import { onBeforeUnmount } from 'vue'

export function useDebounceFn<A extends unknown[]>(fn: (...args: A) => void, wait: number) {
  let timer: ReturnType<typeof setTimeout> | undefined
  const run = (...args: A) => {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => fn(...args), wait)
  }
  const cancel = () => {
    if (timer) clearTimeout(timer)
    timer = undefined
  }
  onBeforeUnmount(cancel)
  return Object.assign(run, { cancel })
}
