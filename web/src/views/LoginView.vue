<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NCard, NForm, NFormItem, NIcon, NInput, NTooltip, useMessage, type FormInst, type FormRules } from 'naive-ui'
import { ContrastOutline, LockClosedOutline, MoonOutline, PersonOutline, SunnyOutline } from '@vicons/ionicons5'
import AppLogo from '@/components/common/AppLogo.vue'
import { useAuthStore } from '@/stores/auth'
import { usePrefsStore } from '@/stores/prefs'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const prefs = usePrefsStore()
const message = useMessage()

const formRef = ref<FormInst | null>(null)
const loading = ref(false)
const form = reactive({ username: '', password: '' })
const rules: FormRules = {
  username: { required: true, message: '请输入用户名', trigger: 'blur' },
  password: { required: true, message: '请输入密码', trigger: 'blur' },
}

const themeIcon = computed(() => (prefs.theme === 'auto' ? ContrastOutline : prefs.theme === 'dark' ? MoonOutline : SunnyOutline))

async function submit(): Promise<void> {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  loading.value = true
  try {
    const user = await auth.login(form.username.trim(), form.password)
    message.success(`欢迎回来，${user.username}`)
    const r = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    await router.replace(r.startsWith('/') && !r.startsWith('//') && !r.startsWith('/login') ? r : '/files')
  } catch {
    /* 错误已提示 */
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <n-tooltip>
      <template #trigger>
        <n-button class="theme-btn" quaternary circle @click="prefs.cycleTheme()">
          <template #icon><n-icon :component="themeIcon" /></template>
        </n-button>
      </template>
      切换主题
    </n-tooltip>
    <div class="login-box">
      <app-logo class="brand" />
      <n-card class="login-card" :bordered="false" size="large">
        <h2 class="title">登录</h2>
        <p class="subtitle">局域网文件管理系统</p>
        <n-form ref="formRef" :model="form" :rules="rules" size="large" :show-label="false" @submit.prevent="submit">
          <n-form-item path="username">
            <n-input v-model:value="form.username" placeholder="用户名" autocomplete="username" :input-props="{ autocapitalize: 'off' }">
              <template #prefix><n-icon :component="PersonOutline" /></template>
            </n-input>
          </n-form-item>
          <n-form-item path="password">
            <n-input
              v-model:value="form.password"
              type="password"
              show-password-on="click"
              placeholder="密码"
              autocomplete="current-password"
            >
              <template #prefix><n-icon :component="LockClosedOutline" /></template>
            </n-input>
          </n-form-item>
          <n-button type="primary" block size="large" :loading="loading" attr-type="submit">登录</n-button>
        </n-form>
      </n-card>
      <p class="foot">MList · 浏览、预览、挂载你的局域网文件</p>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  position: fixed;
  inset: 0;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 16px;
  background:
    radial-gradient(900px 500px at 10% -10%, rgba(32, 128, 240, 0.18), transparent 60%),
    radial-gradient(800px 500px at 110% 110%, rgba(32, 128, 240, 0.15), transparent 60%);
}
.theme-btn {
  position: absolute;
  top: 16px;
  right: 16px;
}
.login-box {
  width: 380px;
  max-width: 100%;
}
.brand {
  justify-content: center;
  margin-bottom: 8px;
}
.login-card {
  border-radius: 14px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.08);
}
.title {
  margin: 0;
  font-size: 22px;
}
.subtitle {
  margin: 4px 0 20px;
  opacity: 0.6;
  font-size: 14px;
}
.foot {
  text-align: center;
  font-size: 12px;
  opacity: 0.5;
  margin-top: 18px;
}
</style>
