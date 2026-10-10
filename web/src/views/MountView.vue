<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NGrid,
  NGridItem,
  NIcon,
  NSelect,
  NSkeleton,
  NTabPane,
  NTabs,
  NTag,
  useThemeVars,
} from 'naive-ui'
import { CloudOutline, DesktopOutline, GlobeOutline, RefreshOutline, ServerOutline } from '@vicons/ionicons5'
import type { ServiceStatus, SystemInfo } from '@/api/types'
import { systemApi } from '@/api/system'
import CopyBlock from '@/components/common/CopyBlock.vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const vars = useThemeVars()

const info = ref<SystemInfo | null>(null)
const loading = ref(true)
const failed = ref(false)
const host = ref('')

async function load(): Promise<void> {
  loading.value = true
  failed.value = false
  try {
    info.value = await systemApi.info()
    if (!host.value || !hostOptions.value.some((o) => o.value === host.value)) host.value = hostOptions.value[0]?.value ?? ''
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

const hostOptions = computed(() => {
  const opts: { label: string; value: string }[] = []
  const i = info.value
  if (!i) return opts
  for (const a of i.addresses) opts.push({ label: a, value: a.includes(':') && !a.startsWith('[') ? `[${a}]` : a })
  if (i.hostname) {
    const h = /\.local\.?$/i.test(i.hostname) ? i.hostname.replace(/\.$/, '') : `${i.hostname}.local`
    opts.push({ label: `${h}（mDNS）`, value: h })
  }
  if (!opts.length) opts.push({ label: location.hostname, value: location.hostname })
  return opts
})

const username = computed(() => auth.user?.username ?? 'USER')
const mounts = computed(() => (auth.mountNames.length ? auth.mountNames : ['<目录名>']))
const firstMount = computed(() => mounts.value[0] ?? '<目录名>')

// ---------- WebDAV ----------
const webdav = computed<ServiceStatus | null>(() => info.value?.services.webdav ?? null)
const davUrl = computed(() => {
  const i = info.value
  if (!i) return ''
  const port = i.http_port || Number(location.port) || 80
  const prefix = (webdav.value?.path || '/dav').replace(/\/+$/, '')
  return `http://${host.value}${port === 80 ? '' : ':' + port}${prefix}/`
})
const davSnippets = computed(() => {
  const url = davUrl.value
  const u = username.value
  return {
    windowsGui: `1. 打开“此电脑” → 右键“网络” → “映射网络驱动器”\n2. 文件夹填写：${url}\n3. 勾选“使用其他凭据连接”，输入用户名 ${u} 与密码（或访问令牌）`,
    windowsCmd: `net use Z: ${url} /user:${u} *`,
    windowsReg: `reg add HKLM\\SYSTEM\\CurrentControlSet\\Services\\WebClient\\Parameters /v BasicAuthLevel /t REG_DWORD /d 2 /f\nreg add HKLM\\SYSTEM\\CurrentControlSet\\Services\\WebClient\\Parameters /v FileSizeLimitInBytes /t REG_DWORD /d 4294967295 /f\nnet stop WebClient && net start WebClient`,
    macos: url,
    linux: `sudo apt install davfs2        # Debian/Ubuntu\nsudo mkdir -p /mnt/lnfiles\nsudo mount -t davfs ${url} /mnt/lnfiles -o uid=$(id -u),gid=$(id -g)\n# 按提示输入用户名 ${u} 与密码（或访问令牌）`,
    fstab: `${url} /mnt/lnfiles davfs user,noauto,uid=1000,gid=1000 0 0\n# 凭据写入 /etc/davfs2/secrets：\n${url} ${u} <密码或访问令牌>`,
    rclone: `rclone config create lnfiles webdav url=${url} vendor=other user=${u} pass=$(rclone obscure '<密码或访问令牌>')\nrclone ls lnfiles:${firstMount.value}\nrclone mount lnfiles: ~/lnfiles --vfs-cache-mode writes`,
  }
})

// ---------- SMB ----------
const smb = computed<ServiceStatus | null>(() => info.value?.services.smb ?? null)
const smbPort = computed(() => smb.value?.port || 445)
const smbDefaultPort = computed(() => smbPort.value === 445)
/** SMB 共享名：优先使用服务端返回的 shares 映射，缺省为挂载名 */
const smbShares = computed(() => mounts.value.map((n) => smb.value?.shares?.[n] || n))
const smbSnippets = computed(() => {
  const hst = host.value
  const u = smb.value?.account || username.value
  const port = smbPort.value
  const m = smbShares.value[0] ?? firstMount.value
  return {
    windowsPaths: smbShares.value.map((n) => `\\\\${hst}\\${n}`).join('\n'),
    windowsCmd: `net use Z: \\\\${hst}\\${m} /user:${u} *`,
    macos: smbShares.value.map((n) => `smb://${u}@${hst}${smbDefaultPort.value ? '' : ':' + port}/${n}`).join('\n'),
    linux: `sudo apt install cifs-utils    # Debian/Ubuntu\nsudo mkdir -p /mnt/${m}\nsudo mount -t cifs //${hst}/${m} /mnt/${m} -o username=${u},port=${port},vers=3.0,uid=$(id -u),gid=$(id -g)`,
  }
})

// ---------- S3 ----------
const s3 = computed<ServiceStatus | null>(() => info.value?.services.s3 ?? null)
const s3Endpoint = computed(() => `http://${host.value}:${s3.value?.port || 9000}`)
const s3Region = computed(() => s3.value?.region || 'us-east-1')
const s3Snippets = computed(() => {
  const ep = s3Endpoint.value
  const region = s3Region.value
  const m = firstMount.value
  return {
    summary: `Endpoint:   ${ep}\nRegion:     ${region}\n寻址方式:   Path-style（force_path_style = true）\nBuckets:    ${mounts.value.join(', ')}`,
    awsConfig: `aws configure set aws_access_key_id <ACCESS_KEY> --profile lnfiles\naws configure set aws_secret_access_key <SECRET_KEY> --profile lnfiles\naws configure set region ${region} --profile lnfiles\naws configure set s3.addressing_style path --profile lnfiles`,
    awsUse: `aws --profile lnfiles --endpoint-url ${ep} s3 ls\naws --profile lnfiles --endpoint-url ${ep} s3 ls s3://${m}/\naws --profile lnfiles --endpoint-url ${ep} s3 cp ./photo.jpg s3://${m}/photo.jpg`,
    rcloneConf: `[lnfiles-s3]\ntype = s3\nprovider = Other\naccess_key_id = <ACCESS_KEY>\nsecret_access_key = <SECRET_KEY>\nendpoint = ${ep}\nregion = ${region}\nforce_path_style = true`,
    rcloneMount: `# rclone.conf 位置：rclone config file\nrclone lsd lnfiles-s3:\nrclone mount lnfiles-s3:${m} ~/mnt/${m} --vfs-cache-mode writes`,
  }
})

function statusTag(s: ServiceStatus | null): { type: 'success' | 'error' | 'default' | 'warning'; text: string } {
  if (!s || !s.enabled) return { type: 'default', text: '未启用' }
  if (s.error) return { type: 'error', text: '异常' }
  if (s.running) return { type: 'success', text: '运行中' }
  return { type: 'warning', text: '未运行' }
}

const services = computed(() => {
  const i = info.value
  if (!i) return []
  return [
    { key: 'http', name: 'Web / HTTP', icon: GlobeOutline, status: { enabled: true, running: true, port: i.http_port } as ServiceStatus, desc: '浏览器访问与 REST API' },
    { key: 'webdav', name: 'WebDAV', icon: CloudOutline, status: i.services.webdav, desc: `路径 ${i.services.webdav.path || '/dav'}（与 HTTP 同端口）` },
    { key: 'smb', name: 'SMB', icon: DesktopOutline, status: i.services.smb, desc: i.services.smb.port && i.services.smb.port !== 445 ? '非标准端口' : 'Windows / macOS 共享' },
    { key: 's3', name: 'S3', icon: ServerOutline, status: i.services.s3, desc: `区域 ${i.services.s3.region || '-'}` },
  ]
})
</script>

<template>
  <div class="page">
    <h2 class="page-title">挂载与服务</h2>
    <p class="page-desc">通过 WebDAV、SMB 或 S3 协议把文件挂载到电脑、手机或其他工具中使用。</p>

    <div v-if="loading && !info">
      <n-skeleton height="90px" :sharp="false" style="margin-bottom: 16px" />
      <n-skeleton height="320px" :sharp="false" />
    </div>
    <n-empty v-else-if="failed && !info" description="无法获取系统信息">
      <template #extra><n-button size="small" @click="load">重试</n-button></template>
    </n-empty>

    <template v-else-if="info">
      <n-card size="small" class="mb" :bordered="true">
        <div class="host-row">
          <div class="host-info">
            <div class="label">服务器</div>
            <div class="value">{{ info.hostname || '-' }} <span class="ver">v{{ info.version }}</span></div>
          </div>
          <div class="host-info">
            <div class="label">局域网地址</div>
            <div class="addr-tags">
              <n-tag v-for="a in info.addresses" :key="a" size="small" :bordered="false">{{ a }}</n-tag>
              <span v-if="!info.addresses.length" class="muted">未检测到</span>
            </div>
          </div>
          <div class="host-info">
            <div class="label">示例中使用的地址</div>
            <n-select v-model:value="host" size="small" :options="hostOptions" style="width: 220px" />
          </div>
          <n-button quaternary circle :loading="loading" @click="load">
            <template #icon><n-icon :component="RefreshOutline" /></template>
          </n-button>
        </div>
        <div v-if="!info.ffmpeg" class="ffmpeg-hint">⚠ 服务器未检测到 ffmpeg，视频转码与部分预览图不可用。</div>
      </n-card>

      <n-grid cols="1 s:2 l:4" responsive="screen" :x-gap="12" :y-gap="12" class="mb">
        <n-grid-item v-for="s in services" :key="s.key">
          <n-card size="small" class="svc">
            <div class="svc-head">
              <n-icon :component="s.icon" size="20" :color="vars.primaryColor" />
              <span class="svc-name">{{ s.name }}</span>
              <n-tag size="small" round :bordered="false" :type="statusTag(s.status).type">{{ statusTag(s.status).text }}</n-tag>
            </div>
            <div class="svc-body">
              <div>端口：<b>{{ s.status.port || '-' }}</b></div>
              <div class="muted">{{ s.desc }}</div>
              <div v-if="s.status.error" class="svc-err">{{ s.status.error }}</div>
            </div>
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-card size="small">
        <n-tabs type="line" animated>
          <!-- WebDAV -->
          <n-tab-pane name="webdav" tab="WebDAV">
            <n-alert v-if="!webdav?.enabled" type="warning" :bordered="false" class="mb">
              WebDAV 服务未启用<template v-if="auth.isAdmin">，可在「系统设置 → 服务」中开启</template>。
            </n-alert>
            <p class="tip">
              认证方式：<b>用户名 + 登录密码</b>，或 <b>用户名 + 访问令牌</b>（在「设置 → 访问令牌」中创建，推荐，可单独吊销）。
            </p>
            <copy-block label="WebDAV 地址" :code="davUrl" />
            <n-tabs type="segment" size="small" class="os-tabs">
              <n-tab-pane name="win" tab="Windows">
                <copy-block label="资源管理器" :code="davSnippets.windowsGui" />
                <copy-block label="命令行（cmd）" :code="davSnippets.windowsCmd" />
                <p class="tip">
                  Windows 默认禁止通过 http 使用基本认证，且单文件限制 50MB。如连接失败，请以管理员身份运行以下命令后重试：
                </p>
                <copy-block :code="davSnippets.windowsReg" />
              </n-tab-pane>
              <n-tab-pane name="mac" tab="macOS">
                <p class="tip">在访达中按 <b>⌘ K</b>（前往 → 连接服务器），输入以下地址并点击“连接”，选择“注册用户”后输入用户名与密码 / 访问令牌：</p>
                <copy-block :code="davSnippets.macos" />
              </n-tab-pane>
              <n-tab-pane name="linux" tab="Linux">
                <copy-block label="davfs2 挂载" :code="davSnippets.linux" />
                <copy-block label="/etc/fstab（可选）" :code="davSnippets.fstab" />
              </n-tab-pane>
              <n-tab-pane name="rclone" tab="rclone">
                <copy-block :code="davSnippets.rclone" />
              </n-tab-pane>
            </n-tabs>
          </n-tab-pane>

          <!-- SMB -->
          <n-tab-pane name="smb" tab="SMB">
            <n-alert v-if="!smb?.enabled" type="warning" :bordered="false" class="mb">
              SMB 服务未启用<template v-if="auth.isAdmin">，可在「系统设置 → 服务」中开启</template>。
            </n-alert>
            <p class="tip">认证方式：<b>用户名 + 登录密码</b>。每个授权目录对应一个共享：{{ smbShares.join('、') }}</p>
            <n-alert v-if="!smbDefaultPort" type="info" :bordered="false" class="mb">
              当前 SMB 端口为 {{ smbPort }}（非 445）。Windows 资源管理器只能连接 445 端口，请使用 macOS / Linux 或修改端口。
            </n-alert>
            <n-tabs type="segment" size="small" class="os-tabs">
              <n-tab-pane name="win" tab="Windows">
                <copy-block label="在资源管理器地址栏输入" :code="smbSnippets.windowsPaths" />
                <copy-block label="映射为网络驱动器（cmd）" :code="smbSnippets.windowsCmd" />
              </n-tab-pane>
              <n-tab-pane name="mac" tab="macOS">
                <p class="tip">在访达中按 <b>⌘ K</b>，输入以下地址：</p>
                <copy-block :code="smbSnippets.macos" />
              </n-tab-pane>
              <n-tab-pane name="linux" tab="Linux">
                <copy-block label="cifs 挂载" :code="smbSnippets.linux" />
              </n-tab-pane>
            </n-tabs>
          </n-tab-pane>

          <!-- S3 -->
          <n-tab-pane name="s3" tab="S3">
            <n-alert v-if="!s3?.enabled" type="warning" :bordered="false" class="mb">
              S3 服务未启用<template v-if="auth.isAdmin">，可在「系统设置 → 服务」中开启</template>。
            </n-alert>
            <p class="tip">
              认证方式：<b>S3 访问密钥</b>（Access Key / Secret Key），请在
              <router-link :to="{ name: 'settings', query: { tab: 's3' } }">设置 → S3 密钥</router-link>
              中创建。每个授权目录对应一个 Bucket。
            </p>
            <copy-block label="连接参数" :code="s3Snippets.summary" />
            <n-tabs type="segment" size="small" class="os-tabs">
              <n-tab-pane name="aws" tab="AWS CLI">
                <copy-block label="配置" :code="s3Snippets.awsConfig" />
                <copy-block label="使用" :code="s3Snippets.awsUse" />
              </n-tab-pane>
              <n-tab-pane name="rclone" tab="rclone">
                <copy-block label="rclone.conf" :code="s3Snippets.rcloneConf" />
                <copy-block label="挂载为本地目录" :code="s3Snippets.rcloneMount" />
              </n-tab-pane>
            </n-tabs>
            <p class="tip muted">提示：Bucket 名称即目录名，包含中文或空格时部分客户端可能不支持，可请管理员使用英文目录名。</p>
          </n-tab-pane>
        </n-tabs>
      </n-card>
    </template>
  </div>
</template>

<style scoped>
.mb {
  margin-bottom: 16px;
}
.host-row {
  display: flex;
  gap: 28px;
  align-items: flex-end;
  flex-wrap: wrap;
}
.host-info .label {
  font-size: 12px;
  opacity: 0.6;
  margin-bottom: 4px;
}
.host-info .value {
  font-weight: 600;
}
.ver {
  font-weight: 400;
  font-size: 12px;
  opacity: 0.6;
  margin-left: 4px;
}
.addr-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.ffmpeg-hint {
  margin-top: 10px;
  font-size: 13px;
  color: #f0a020;
}
.svc {
  height: 100%;
}
.svc-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.svc-name {
  flex: 1;
  font-weight: 600;
}
.svc-body {
  margin-top: 8px;
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.svc-err {
  color: #d03050;
  word-break: break-all;
}
.muted {
  opacity: 0.6;
}
.tip {
  font-size: 13px;
  line-height: 1.7;
  margin: 8px 0;
  opacity: 0.85;
}
.os-tabs {
  margin-top: 12px;
}
</style>
