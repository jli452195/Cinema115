<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import PlaybackDevicePolicy from '../components/PlaybackDevicePolicy.vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
import { embyRequest } from '../types/emby'
import '../styles/emby-proxy.css'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
interface Config { enabled: boolean; debug: boolean; directLinkCacheMinutes: number; port: number; url: string; accountId: string }
interface Status { config: Config; allowDownload: boolean; listening: boolean; error: string }
const defaults: Config = { enabled: false, debug: false, directLinkCacheMinutes: 9, port: 8097, url: '', accountId: '' }
const config = ref<Config>({ ...defaults })
const allowDownload = ref(false)
const status = ref<Status>()
const devicePolicy = ref<InstanceType<typeof PlaybackDevicePolicy>>()
const accounts = ref<P115Account[]>([])
const busy = ref(false), accountsLoaded = ref(false)
const error = ref(''), accountError = ref(''), notice = ref('')
const lifecycle = new AbortController()
const loaded = computed(() => !!status.value)
const configDirty = computed(() => loaded.value && JSON.stringify(config.value) !== JSON.stringify(status.value?.config))
const downloadDirty = computed(() => loaded.value && allowDownload.value !== status.value?.allowDownload)
const dirty = computed(() => configDirty.value || downloadDirty.value)
const runtimeLabel = computed(() => status.value?.error ? '反代运行异常' : status.value?.listening ? '反代正在监听' : '反代已停止')
const pendingEnable = computed(() => loaded.value && config.value.enabled !== status.value?.config.enabled)
const hasUnfinishedWork = computed(() => dirty.value || devicePolicy.value?.dirty)

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
      const result: Status = { ...proxy.value, config: { ...defaults, ...proxy.value.config }, allowDownload: !!proxy.value.allowDownload }
      if (!configDirty.value) config.value = { ...result.config }
      if (!downloadDirty.value) allowDownload.value = result.allowDownload
      status.value = result
    } else error.value = proxy.reason instanceof Error ? proxy.reason.message : '加载反代配置失败'
    if (accountList.status === 'fulfilled') { accounts.value = accountList.value.accounts; accountsLoaded.value = true }
    else accountError.value = accountList.reason instanceof Error ? accountList.reason.message : '加载网盘账号失败'
  }
  busy.value = false
}
async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const result: Status = await embyRequest(props.token, 'proxy', lifecycle.signal, {
      ...config.value,
      allowDownload: downloadDirty.value ? allowDownload.value : undefined,
    })
    status.value = { ...result, config: { ...defaults, ...result.config }, allowDownload: !!result.allowDownload }
    config.value = { ...status.value.config }
    allowDownload.value = status.value.allowDownload
    notice.value = '反代配置已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      error.value = reason instanceof Error ? reason.message : '保存反代配置失败'
    }
  } finally { busy.value = false }
}
function revealInvalid(event: Event) {
  if (event.target instanceof HTMLElement) {
    const details = event.target.closest('details')
    if (details) details.open = true
  }
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (hasUnfinishedWork.value) { event.preventDefault(); event.returnValue = '' }
}
onBeforeRouteLeave(() => !hasUnfinishedWork.value || window.confirm('有未保存的设置。确定放弃修改并离开？'))
onMounted(() => { void load(); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { lifecycle.abort(); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <ManagementLayout class="proxy-ui" title="Emby 302 与媒体访问" description="按 Emby 权限和网盘绑定提供原文件播放。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <div class="proxy-settings-grid">
      <div class="min-w-0">
        <section class="proxy-card" aria-labelledby="proxy-config-title">
          <div class="proxy-card-heading flex-wrap">
            <h2 id="proxy-config-title">反代设置</h2>
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span v-if="dirty" class="proxy-draft-label">待保存</span>
              <span id="proxy-runtime-status" role="status" class="proxy-help">{{ loaded ? runtimeLabel : busy ? '读取中…' : '' }}</span>
              <button v-if="loaded" type="button" class="proxy-text-link" :disabled="busy" @click="load">刷新状态</button>
            </div>
          </div>
          <p v-if="status?.error" role="alert" class="proxy-alert is-error mt-4">{{ status.error }}</p>
          <p v-if="error" role="alert" class="proxy-alert is-error mt-4">{{ error }} <button v-if="!loaded" type="button" :disabled="busy" class="underline" @click="load">重试读取</button></p>
          <p v-if="notice && !dirty" role="status" class="proxy-alert is-success mt-4">{{ notice }}</p>
          <p v-if="accountError" role="alert" class="proxy-alert is-error mt-4">网盘状态读取失败：{{ accountError }} <button type="button" :disabled="busy" class="underline" @click="load">重试读取</button></p>
          <P115PrimaryStatus v-if="accountsLoaded" class="mt-4" :accounts="accounts" />
          <form class="mt-5 space-y-5" @submit.prevent="save" @invalid.capture="revealInvalid">
            <fieldset :disabled="busy || !loaded" class="space-y-5 disabled:opacity-50">
              <div class="proxy-switch-surface space-y-3">
                <div>
                  <SettingSwitch v-model="config.enabled" label="启用 302 并接管播放" description="按 Emby 权限和网盘条件提供原文件播放，始终禁止转码与重新封装。" />
                  <p v-if="pendingEnable" class="proxy-pending">{{ config.enabled ? '待保存后启动入口' : '待保存后停止入口' }}</p>
                </div>
                <SettingSwitch
                  v-model="allowDownload"
                  class="border-t border-[var(--proxy-border)] pt-3"
                  label="允许下载原文件"
                  description="反代运行时开放下载，仍需 Emby 下载权限和网盘绑定。"
                />
              </div>
              <label class="block text-sm">Emby 原服务器地址<input v-model.trim="config.url" type="url" :required="config.enabled" placeholder="http://192.168.0.3:8096" class="proxy-input mt-2" /><span class="proxy-help mt-2 block">填写实际 Emby 地址，不能填写本反代入口。Docker 中 localhost 指向 Cinema115 容器。</span></label>
              <label class="block text-sm">反代监听端口<input v-model.number="config.port" type="number" min="1024" max="65535" step="1" required class="proxy-input mt-2" /><span class="proxy-help mt-2 block">建议 8097。更换端口后需更新容器映射和播放器连接地址。</span></label>
              <details class="proxy-details proxy-advanced"><summary>高级设置<span>直链缓存与调试日志</span></summary>
                <div class="space-y-4 pt-4">
                  <label class="block text-sm">直链缓存时间（分钟）<input v-model.number="config.directLinkCacheMinutes" type="number" min="0" max="30" step="1" required class="proxy-input mt-2" /><span class="proxy-help mt-2 block">默认 9 分钟，0 为关闭。命中不续期，也不能延长 115 直链的有效期。</span></label>
                  <SettingSwitch v-model="config.debug" label="反代调试日志" description="开启后，新请求可展开取链步骤、连接计时、CDN 地址与请求响应头；详情仅存内存，日志文件始终只保存摘要。Cookie、登录令牌及未知请求头值隐藏。" />
                </div>
              </details>
              <button type="submit" class="proxy-button w-full sm:w-auto" :disabled="busy || !loaded">{{ busy ? '正在处理…' : '保存反代配置' }}</button>
            </fieldset>
          </form>
        </section>
      </div>
      <PlaybackDevicePolicy ref="devicePolicy" :token="token" class="min-w-0" />
    </div>
  </ManagementLayout>
</template>
