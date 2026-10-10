import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { feedback } from '@/composables/feedback'
import MainLayout from '@/layouts/MainLayout.vue'

const APP_NAME = 'LocalNetwork Files'

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
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/SettingsView.vue'),
        meta: { title: '设置' },
      },
      {
        path: 'api-docs',
        name: 'api-docs',
        component: () => import('@/views/ApiDocsView.vue'),
        meta: { title: 'API 文档' },
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
        meta: { title: '系统设置', admin: true },
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
  history: createWebHistory(),
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
