import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { setHttpHooks } from './api/http'
import { feedback } from './composables/feedback'
import { useAuthStore } from './stores/auth'
import './styles/global.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(router)

setHttpHooks({
  onError: (msg) => feedback.message?.error(msg),
  onUnauthorized: () => {
    const auth = useAuthStore(pinia)
    auth.user = null
    const current = router.currentRoute.value
    if (current.name !== 'login') {
      void router.replace({
        name: 'login',
        query: current.fullPath && current.fullPath !== '/' ? { redirect: current.fullPath } : undefined,
      })
    }
  },
})

app.mount('#app')
