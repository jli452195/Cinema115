<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import MemberPlaybackAdmin from '../components/MemberPlaybackAdmin.vue'
import PlaybackDevicePolicy from '../components/PlaybackDevicePolicy.vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import InstantUploadPanel from '../components/InstantUploadPanel.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
import { embyRequest } from '../types/emby'
import { copyText } from '../utils/clipboard'
import { useToast } from '../composables/useToast'
import '../styles/emby-proxy.css'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
interface Config { enabled: boolean; debug: boolean; prewarm: boolean; prepareFiles: boolean; directLinkCacheMinutes: number; port: number; url: string; accountId: string; embyPathPrefix: string }
interface Status { config: Config; listening: boolean; error: string }
const defaults: Config = { enabled: false, debug: false, prewarm: false, prepareFiles: true, directLinkCacheMinutes: 9, port: 8097, url: '', accountId: '', embyPathPrefix: '' }
const config = ref<Config>({ ...defaults })
const status = ref<Status>()
const mediaSettings = ref<InstanceType<typeof MemberPlaybackAdmin>>()
const devicePolicy = ref<InstanceType<typeof PlaybackDevicePolicy>>()
const upload = ref<InstanceType<typeof InstantUploadPanel>>()
const accounts = ref<P115Account[]>([])
const busy = ref(false), loaded = ref(false), accountsLoaded = ref(false)
const error = ref(''), accountError = ref(''), notice = ref('')
const needsAccess = ref(false)
const lifecycle = new AbortController()
const toast = useToast()
const dirty = computed(() => loaded.value && JSON.stringify(config.value) !== JSON.stringify(status.value?.config))
const entry = computed(() => status.value ? `http://${window.location.hostname}:${status.value.config.port}` : '')
const mapping = computed(() => status.value ? `${status.value.config.port}:${status.value.config.port}` : '')
const pendingEnable = computed(() => loaded.value && config.value.enabled !== status.value?.config.enabled)
const hasUnfinishedWork = computed(() => dirty.value || mediaSettings.value?.dirty || devicePolicy.value?.dirty || upload.value?.dirty || upload.value?.inFlight)

async function load() {
  if (busy.value) return
  busy.value = true; error.value = ''; accountError.value = ''
  const results = await Promise.allSettled([
    embyRequest(props.token, 'proxy', lifecycle.signal),
    p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal }),
  ])
  if (!lifecycle.signal.aborted) {
    const [proxy, accountList] = results
    if (proxy.status === 'fulfilled') {
      const result = { ...proxy.value, config: { ...defaults, ...proxy.value.config } } as Status
      if (!loaded.value || !dirty.value) config.value = { ...result.config }
      status.value = result; loaded.value = true
    } else error.value = proxy.reason instanceof Error ? proxy.reason.message : '加载反代配置失败'
    if (accountList.status === 'fulfilled') { accounts.value = accountList.value.accounts; accountsLoaded.value = true }
    else accountError.value = accountList.reason instanceof Error ? accountList.reason.message : '加载网盘账号失败'
  }
  busy.value = false
}
async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true; error.value = ''; notice.value = ''; needsAccess.value = false
  try {
    const result: Status = await embyRequest(props.token, 'proxy', lifecycle.signal, { ...config.value })
    status.value = { ...result, config: { ...defaults, ...result.config } }; config.value = { ...status.value.config }
    notice.value = result.listening ? '反代配置已保存，入口正在监听。请在播放器中验证实际播放。' : '反代配置已保存，入口已停止。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      error.value = reason instanceof Error ? reason.message : '保存反代配置失败'
      needsAccess.value = error.value.includes('媒体访问设置')
    }
  } finally { busy.value = false }
}
function focusAccess() {
  const card = document.getElementById('proxy-access')
  card?.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'center' })
  card?.querySelector<HTMLInputElement>('input')?.focus({ preventScroll: true })
}
function revealInvalid(event: Event) {
  if (event.target instanceof HTMLElement) {
    const details = event.target.closest('details')
    if (details) details.open = true
  }
}
async function copyEntry() {
  try { await copyText(entry.value); toast.success('入口地址已复制') }
  catch { toast.error('复制失败，请手动选择入口地址复制') }
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (hasUnfinishedWork.value) { event.preventDefault(); event.returnValue = '' }
}
onBeforeRouteLeave(() => !hasUnfinishedWork.value || window.confirm('有未保存设置或秒传验证正在执行。离开会丢弃页面草稿；已发出的任务需重新打开核查记录。是否离开？'))
onMounted(() => { void load(); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { lifecycle.abort(); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <ManagementLayout class="proxy-page proxy-ui" title="Emby 302 与媒体访问" description="按 Emby 权限和网盘绑定提供原文件播放，分别管理反代、访问设置与播放设备。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions>
      <RouterLink to="/emby/cache" class="proxy-tool">缓存管理</RouterLink>
      <RouterLink :to="{ path: '/logs', query: { module: 'Emby 302', level: 'ALL' } }" class="proxy-tool">查看日志</RouterLink>
      <button type="button" class="proxy-tool is-accent" @click="upload?.open()">{{ upload?.inFlight ? '秒传验证中' : '秒传验证' }}</button>
    </template>
    <div class="space-y-5">
      <section class="proxy-runtime" aria-labelledby="proxy-runtime-title">
        <div class="proxy-runtime-top">
          <div class="flex min-w-0 items-center gap-3">
            <span class="proxy-runtime-dot" :class="{ 'is-on': status?.listening, 'is-error': status?.error }" aria-hidden="true"></span>
            <div><h2 id="proxy-runtime-title">{{ loaded ? status?.error ? '反代运行异常' : status?.listening ? '反代正在监听' : '反代已停止' : '正在读取运行状态' }}</h2><p class="proxy-help">{{ loaded ? '监听端口 ' + status?.config.port + ' · 仅原文件播放' : '运行状态以服务端已保存配置为准' }}</p></div>
          </div>
          <button type="button" class="proxy-text-link" :disabled="busy" @click="load">{{ busy ? '读取中…' : '刷新状态' }}</button>
        </div>
        <p v-if="status?.error" role="alert" class="proxy-alert is-error mt-3">{{ status.error }}</p>
        <div v-if="loaded" class="proxy-entry">
          <div class="min-w-0"><span class="proxy-entry-label">客户端入口示例</span><code>{{ entry }}</code></div>
          <button type="button" class="proxy-copy" aria-label="复制客户端入口" @click="copyEntry"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="8" y="8" width="12" height="13" rx="2" /><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" /></svg>复制</button>
        </div>
        <p class="proxy-help mt-3">首次使用或提示身份未记录时，通过反代地址退出并重新登录 Emby。实际连接请使用对外域名或宿主机映射地址。</p>
      </section>
      <div class="proxy-settings-grid">
        <div class="min-w-0 space-y-5">
          <section class="proxy-card" aria-labelledby="proxy-config-title">
            <div class="proxy-card-heading"><h2 id="proxy-config-title">反代设置</h2><span class="proxy-draft-label">{{ dirty ? '待保存' : loaded ? '已同步' : '读取中' }}</span></div>
            <p v-if="error" role="alert" class="proxy-alert is-error mt-4">{{ error }} <button v-if="!loaded" type="button" :disabled="busy" class="underline" @click="load">重试读取</button><button v-if="needsAccess" type="button" class="mt-2 block underline" @click="focusAccess">前往保存访问设置</button></p>
            <p v-if="notice && !dirty" role="status" class="proxy-alert is-success mt-4">{{ notice }}</p>
            <p v-if="accountError" role="alert" class="proxy-alert is-error mt-4">网盘状态读取失败：{{ accountError }} <button type="button" :disabled="busy" class="underline" @click="load">重试读取</button></p>
            <P115PrimaryStatus v-if="accountsLoaded" class="mt-4" :accounts="accounts" />
            <form class="mt-5 space-y-5" @submit.prevent="save" @invalid.capture="revealInvalid">
              <fieldset :disabled="busy || !loaded" class="space-y-5 disabled:opacity-50">
                <div class="proxy-switch-surface">
                  <SettingSwitch v-model="config.enabled" label="启用 302 并接管播放" description="按 Emby 权限和网盘条件提供原文件播放，始终禁止转码与重新封装。" />
                  <p v-if="pendingEnable" class="proxy-pending">{{ config.enabled ? '待保存后启动入口' : '待保存后停止入口' }}</p>
                </div>
                <label class="block text-sm">Emby 原服务器地址<input v-model.trim="config.url" type="url" :required="config.enabled" placeholder="http://192.168.0.3:8096" class="proxy-input mt-2" /><span class="proxy-help mt-2 block">填写实际 Emby 地址，不能填写本反代入口。Docker 中 localhost 指向 Cinema115 容器。</span></label>
                <label class="block text-sm">反代监听端口<input v-model.number="config.port" type="number" min="1024" max="65535" step="1" required class="proxy-input mt-2" /><span class="proxy-help mt-2 block">建议 8097。更换端口后需更新容器映射和播放器连接地址。</span></label>
                <details class="proxy-details proxy-advanced"><summary>高级设置<span>缓存、STRM 路径与调试日志</span></summary>
                  <div class="space-y-4 pt-4">
                    <label class="block text-sm">直链缓存时间（分钟）<input v-model.number="config.directLinkCacheMinutes" type="number" min="0" max="30" step="1" required class="proxy-input mt-2" /><span class="proxy-help mt-2 block">默认 9 分钟，0 为关闭。命中不续期，也不能延长 115 直链的有效期。</span></label>
                    <label class="block text-sm">Emby 中对应 STRM 根目录的路径（选填）<input v-model.trim="config.embyPathPrefix" placeholder="例如 /media" class="proxy-input mt-2" /><span class="proxy-help mt-2 block">Emby 返回本地 .strm 路径时填写，其相对目录需与 Cinema115 STRM 根目录一致。返回 HTTP 链接时可留空。</span></label>
                    <SettingSwitch v-model="config.debug" label="反代调试日志" description="记录播放取链时间轴与连接计时，可查看完整 CDN 地址和请求响应头；Cookie、登录令牌及未知请求头值隐藏，不记录请求正文。" />
                    <p class="proxy-help">当前用户播放流程在正式取链时准备文件，跳过提前准备与直链预热。原有设置保留，不在此提供开关。</p>
                  </div>
                </details>
                <button type="submit" class="proxy-button" :disabled="busy || !loaded">{{ busy ? '正在处理…' : '保存反代配置' }}</button>
              </fieldset>
            </form>
          </section>
          <section class="proxy-card proxy-deployment">
            <details class="proxy-details"><summary>部署与 STRM 说明<span>NAS / Docker、原文件播放</span></summary>
              <div class="space-y-3 pt-4">
                <p v-if="mapping" class="proxy-help">桥接网络需添加 TCP 端口映射 <code>{{ mapping }}</code>（宿主机端口 : 容器端口）。示例使用已保存端口；host 网络通常无需映射。网页保存不会修改 Docker。</p>
                <p v-if="loaded && config.port !== status?.config.port" class="proxy-pending">草稿端口 {{ config.port }} 尚未保存；保存后再按实际端口更新部署。</p>
                <p class="proxy-help">支持 /api/redirect115/{pickcode} 与 /play115/{pickcode}/{文件ID}/{SHA1}/{大小}/{文件名}。完整 play115 路径作为本次文件声明使用，不预查主账号；目标取链与二次校验仍保留。redirect115 缺少可信资料时按来源规则核验，不访问原工具地址。</p>
                <p class="proxy-help">池用户按会话选择健康小号，池耗尽或繁忙不回退主网盘；个人网盘独立。直放视频由客户端访问 115，字幕、海报与播放进度继续由 Emby 处理；不兼容原文件格式时请更换播放器。</p>
              </div>
            </details>
          </section>
        </div>
        <div class="min-w-0 space-y-5">
          <MemberPlaybackAdmin ref="mediaSettings" :token="token" />
          <PlaybackDevicePolicy ref="devicePolicy" :token="token" />
        </div>
      </div>
      <InstantUploadPanel ref="upload" :token="token" :accounts="accounts" />
    </div>
  </ManagementLayout>
</template>
