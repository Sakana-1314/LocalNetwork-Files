<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NTabPane, NTabs } from 'naive-ui'
import AppearanceTab from './settings/AppearanceTab.vue'
import TokensTab from './settings/TokensTab.vue'
import S3KeysTab from './settings/S3KeysTab.vue'

const route = useRoute()
const router = useRouter()
const TABS = ['appearance', 'tokens', 's3'] as const
type Tab = (typeof TABS)[number]

const tab = computed<Tab>({
  get: () => {
    const t = route.query.tab
    return typeof t === 'string' && (TABS as readonly string[]).includes(t) ? (t as Tab) : 'appearance'
  },
  set: (v) => void router.replace({ query: { ...route.query, tab: v } }),
})
</script>

<template>
  <div class="page settings-page">
    <h2 class="page-title">设置</h2>
    <p class="page-desc">个性化外观与访问凭据；账号信息、修改密码请用右上角的「账号设置」。</p>
    <n-card>
      <n-tabs v-model:value="tab" type="line" animated>
        <n-tab-pane name="appearance" tab="外观"><appearance-tab /></n-tab-pane>
        <n-tab-pane name="tokens" tab="访问令牌"><tokens-tab /></n-tab-pane>
        <n-tab-pane name="s3" tab="S3 密钥"><s3-keys-tab /></n-tab-pane>
      </n-tabs>
    </n-card>
  </div>
</template>

<style scoped>
.settings-page {
  max-width: 1000px;
}
</style>
