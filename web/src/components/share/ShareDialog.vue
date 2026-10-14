<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NForm, NFormItem, NIcon, NInput, NModal, NSpace, useMessage } from 'naive-ui'
import { CopyOutline, OpenOutline } from '@vicons/ionicons5'
import { RouterLink } from 'vue-router'
import type { Share } from '@/api/types'
import { sharesApi } from '@/api/shares'
import ExpiryPicker from './ExpiryPicker.vue'
import { useCopy } from '@/composables/useCopy'
import { useIsMobile } from '@/composables/useIsMobile'
import { expiryFrom, shareLink, type ExpiryPreset } from '@/utils/share'
import { formatDate } from '@/utils/format'

/** 被分享文件夹的虚拟路径 */
const props = defineProps<{ path: string }>()
const show = defineModel<boolean>('show', { default: false })
const message = useMessage()
const copy = useCopy()
const isMobile = useIsMobile()

const preset = ref<ExpiryPreset>('7d')
const custom = ref<number | null>(null)
const creating = ref(false)
const created = ref<Share | null>(null)

const name = computed(() => props.path.split('/').filter(Boolean).pop() ?? '')
const link = computed(() => (created.value ? shareLink(created.value.token) : ''))

watch(show, (v) => {
  if (v) {
    created.value = null
    preset.value = '7d'
    custom.value = null
  }
})

async function create(): Promise<void> {
  if (preset.value === 'custom' && !custom.value) {
    message.warning('请选择过期时间')
    return
  }
  creating.value = true
  try {
    created.value = await sharesApi.create(props.path, expiryFrom(preset.value, custom.value))
    void copy(link.value, '分享已创建，链接已复制')
  } catch {
    /* 错误已提示 */
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <n-modal
    v-model:show="show"
    preset="card"
    :title="`分享文件夹 · ${name}`"
    :style="{ width: isMobile ? 'calc(100vw - 24px)' : '520px' }"
  >
    <template v-if="!created">
      <n-form label-placement="top">
        <n-form-item label="有效期">
          <expiry-picker v-model:preset="preset" v-model:custom="custom" />
        </n-form-item>
      </n-form>
      <n-alert type="info" :bordered="false">
        任何拿到链接的人都可以只读浏览和下载该文件夹（含子文件夹），无需登录。
        分享的是文件夹路径：你失去该文件夹的权限、或文件夹被移动 / 删除后，链接随即失效。
      </n-alert>
    </template>
    <template v-else>
      <n-form label-placement="top">
        <n-form-item label="分享链接">
          <n-input :value="link" readonly class="mono" />
        </n-form-item>
      </n-form>
      <p class="meta">
        {{ created.expires_at ? `有效期至 ${formatDate(created.expires_at)}` : '永久有效' }}，可在
        <router-link :to="{ name: 'shares' }" @click="show = false">我的分享</router-link>
        中修改有效期或取消分享。
      </p>
    </template>
    <template #footer>
      <n-space justify="end">
        <template v-if="!created">
          <n-button @click="show = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="create">创建链接</n-button>
        </template>
        <template v-else>
          <n-button tag="a" :href="link" target="_blank">
            <template #icon><n-icon :component="OpenOutline" /></template>
            打开
          </n-button>
          <n-button type="primary" @click="copy(link)">
            <template #icon><n-icon :component="CopyOutline" /></template>
            复制链接
          </n-button>
        </template>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.meta {
  font-size: 13px;
  opacity: 0.75;
  margin: 0;
}
</style>
