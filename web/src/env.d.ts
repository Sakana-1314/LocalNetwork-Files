/// <reference types="vite/client" />

import 'vue-router'

declare global {
  interface ImportMetaEnv {
    /** API 前缀，默认 /api/v1（同源后端）；演示站指向 Apifox 云端 Mock */
    readonly VITE_API_BASE?: string
    /** 为 '1' 时改用 hash 路由，用于没有服务端回退的静态托管 */
    readonly VITE_HASH_ROUTER?: string
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
