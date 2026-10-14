import { defineConfig } from 'vitepress'

/**
 * 文档站配置。
 *
 * base 固定为 /MList/：站点发布在 GitHub Pages 的项目路径下
 * （https://sakana-1314.github.io/MList/）。本地预览用 pnpm dev 即可，
 * 它同样按该 base 提供服务。
 */
export default defineConfig({
  base: '/MList/',
  lang: 'zh-CN',
  title: 'MList',
  description: '局域网文件管理：把服务器上的目录按用户授权，通过 Web / WebDAV / S3 / SMB / REST API 访问',
  cleanUrls: true,
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/MList/favicon.svg' }],
    ['meta', { name: 'color-scheme', content: 'light dark' }],
  ],
  themeConfig: {
    logo: '/favicon.svg',
    nav: [
      { text: '使用', link: '/guide/quickstart', activeMatch: '^/guide/' },
      { text: '部署', link: '/deploy/docker', activeMatch: '^/deploy/' },
      { text: '开发', link: '/dev/build', activeMatch: '^/dev/' },
      { text: '在线 Demo', link: '/demo' },
      { text: 'API 文档', link: '/api' },
    ],
    sidebar: {
      '/guide/': [
        {
          text: '使用',
          items: [
            { text: '快速开始', link: '/guide/quickstart' },
            { text: '功能', link: '/guide/features' },
            { text: '挂载与服务', link: '/guide/mount' },
            { text: '配置', link: '/guide/config' },
          ],
        },
      ],
      '/deploy/': [
        {
          text: '部署',
          items: [
            { text: 'Docker 与镜像', link: '/deploy/docker' },
            { text: 'systemd 与安全', link: '/deploy/systemd' },
          ],
        },
      ],
      '/dev/': [
        {
          text: '开发',
          items: [
            { text: '从源码构建', link: '/dev/build' },
            { text: '架构', link: '/dev/architecture' },
            { text: 'OpenAPI 与 Apifox', link: '/dev/openapi' },
            { text: '在线 Demo', link: '/dev/demo' },
          ],
        },
      ],
    },
    outline: { label: '本页目录', level: [2, 3] },
    search: { provider: 'local' },
    docFooter: { prev: '上一页', next: '下一页' },
    lastUpdated: { text: '最后更新于' },
    darkModeSwitchLabel: '主题',
    sidebarMenuLabel: '目录',
    returnToTopLabel: '回到顶部',
    notFound: { title: '页面不存在', quote: '这里没有你要找的内容', linkLabel: '回到首页', linkText: '回到首页' },
  },
})
