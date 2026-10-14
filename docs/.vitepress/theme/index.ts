import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import DemoFrame from './DemoFrame.vue'

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    // 用于在文档里内嵌演示站与 Apifox 在线文档。
    app.component('DemoFrame', DemoFrame)
  },
} satisfies Theme
