/// <reference types="vite/client" />

import 'vue-router'

declare global {
  interface ImportMetaEnv {
    /** API 前缀，默认 /api/v1（同源后端）；演示站指向 Apifox 云端 Mock */
    readonly VITE_API_BASE?: string
    /** 「API 文档」页内嵌的在线文档地址，默认 https://mlist.apifox.cn/ */
    readonly VITE_APIFOX_DOC_URL?: string
  }
}

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    public?: boolean
    admin?: boolean
    fullHeight?: boolean
  }
}
