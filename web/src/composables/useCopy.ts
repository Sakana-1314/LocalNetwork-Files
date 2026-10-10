import { useMessage } from 'naive-ui'
import { copyText } from '@/utils/clipboard'

export function useCopy() {
  const message = useMessage()
  return async function copy(text: string, tip = '已复制到剪贴板'): Promise<void> {
    if (await copyText(text)) message.success(tip)
    else message.error('复制失败，请手动选择文本复制')
  }
}
