import type { DialogApi, LoadingBarApi, MessageApi, NotificationApi } from 'naive-ui'

/**
 * 在组件 setup 之外（http 层、路由守卫）使用 Naive UI 的反馈 API。
 * 由 components/common/FeedbackBridge.vue 在 Provider 内部注册。
 */
export const feedback: {
  message: MessageApi | null
  dialog: DialogApi | null
  notification: NotificationApi | null
  loadingBar: LoadingBarApi | null
} = {
  message: null,
  dialog: null,
  notification: null,
  loadingBar: null,
}
