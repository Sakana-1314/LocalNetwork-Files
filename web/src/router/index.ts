import { createRouter, createWebHashHistory, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { feedback } from '@/composables/feedback'
import MainLayout from '@/layouts/MainLayout.vue'

const APP_NAME = 'MList'

/**
 * 静态托管（如 GitHub Pages 上的演示站）没有服务端回退：
 * 直接访问或刷新深层路径会 404。此时用 hash 路由（VITE_HASH_ROUTER=1），
 * 路径写在 # 后面，任何路径都由同一个 index.html 处理。
 * 默认仍是 History 路由，后端会为非 /api、/dav 的路径回退到 index.html。
 */
const useHashRouter = import.meta.env.VITE_HASH_ROUTER === '1'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录', public: true },
  },
  {
    path: '/',
    component: MainLayout,
    children: [
      { path: '', redirect: '/files' },
      {
        path: 'files/:path(.*)*',
        name: 'files',
        component: () => import('@/views/FilesView.vue'),
        meta: { title: '文件', fullHeight: true },
      },
      {
        path: 'search',
        name: 'search',
        component: () => import('@/views/SearchView.vue'),
        meta: { title: '搜索' },
      },
      {
        path: 'mount',
        name: 'mount',
        component: () => import('@/views/MountView.vue'),
        meta: { title: '挂载与服务' },
      },
      {
        path: 'admin/users',
        name: 'admin-users',
        component: () => import('@/views/admin/UsersView.vue'),
        meta: { title: '用户管理', admin: true },
      },
      {
        path: 'admin/system',
        name: 'admin-system',
        component: () => import('@/views/admin/SystemView.vue'),
        meta: { title: '系统管理', admin: true },
      },
      {
        path: ':pathMatch(.*)*',
        name: 'not-found',
        component: () => import('@/views/NotFoundView.vue'),
        meta: { title: '页面不存在' },
      },
    ],
  },
]

export const router = createRouter({
  // 部署到子路径时（Vite base）路由也必须带上同样的前缀。
  history: useHashRouter
    ? createWebHashHistory(import.meta.env.BASE_URL)
    : createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach(async (to) => {
  feedback.loadingBar?.start()
  const auth = useAuthStore()

  if (auth.token && !auth.user) await auth.fetchMe()

  if (to.meta.public) {
    if (to.name === 'login' && auth.isLoggedIn) {
      const redirect = typeof to.query.redirect === 'string' ? to.query.redirect : '/files'
      return redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/files'
    }
    return true
  }

  if (!auth.isLoggedIn) {
    return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : undefined }
  }

  if (to.meta.admin && !auth.isAdmin) {
    feedback.message?.warning('需要管理员权限')
    return { name: 'files', params: { path: [] } }
  }
  return true
})

router.afterEach((to, _from, failure) => {
  if (failure) feedback.loadingBar?.error()
  else feedback.loadingBar?.finish()
  let title = to.meta.title
  if (to.name === 'files') {
    const segs = Array.isArray(to.params.path) ? to.params.path : []
    title = segs[segs.length - 1] || '全部目录'
  }
  document.title = title ? `${title} · ${APP_NAME}` : APP_NAME
})

router.onError(() => feedback.loadingBar?.error())
