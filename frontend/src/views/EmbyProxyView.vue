<script setup lang="ts">
import { IconCheck, IconChevronDown, IconRefresh, IconServer } from '@tabler/icons-vue'
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
const operation = ref<'load' | 'save'>()
const accountsLoaded = ref(false)
const readError = ref(''), accountError = ref(''), saveError = ref(''), notice = ref('')
const lifecycle = new AbortController()
const busy = computed(() => !!operation.value)
const loaded = computed(() => !!status.value)
const configDirty = computed(() => loaded.value && JSON.stringify(config.value) !== JSON.stringify(status.value?.config))
const downloadDirty = computed(() => loaded.value && allowDownload.value !== status.value?.allowDownload)
const dirty = computed(() => configDirty.value || downloadDirty.value)
const runtimeLabel = computed(() => {
  if (!status.value) return readError.value ? '状态未读取' : '读取中…'
  if (status.value.error) return '反代运行异常'
  return status.value.listening ? '反代正在监听' : '反代已停止'
})
const runtimeTone = computed(() => status.value?.error ? 'danger' : status.value?.listening ? 'success' : undefined)
const savedWithoutListener = computed(() => status.value?.config.enabled && !status.value.listening)
const pendingEnable = computed(() => loaded.value && config.value.enabled !== status.value?.config.enabled)
const hasUnfinishedWork = computed(() => dirty.value || devicePolicy.value?.dirty)

async function load() {
  if (busy.value) return
  operation.value = 'load'
  readError.value = ''
  accountError.value = ''
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
    } else readError.value = proxy.reason instanceof Error ? proxy.reason.message : '加载反代配置失败'
    if (accountList.status === 'fulfilled') { accounts.value = accountList.value.accounts; accountsLoaded.value = true }
    else accountError.value = accountList.reason instanceof Error ? accountList.reason.message : '加载网盘账号失败'
  }
  operation.value = undefined
}
async function save() {
  if (busy.value || !loaded.value) return
  operation.value = 'save'
  saveError.value = ''
  notice.value = ''
  try {
    const result: Status = await embyRequest(props.token, 'proxy', lifecycle.signal, {
      ...config.value,
      allowDownload: downloadDirty.value ? allowDownload.value : undefined,
    })
    status.value = { ...result, config: { ...defaults, ...result.config }, allowDownload: !!result.allowDownload }
    config.value = { ...status.value.config }
    allowDownload.value = status.value.allowDownload
    readError.value = ''
    notice.value = '反代配置已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      saveError.value = reason instanceof Error ? reason.message : '保存反代配置失败'
    }
  } finally { operation.value = undefined }
}
function resetDraft() {
  if (!status.value || busy.value) return
  config.value = { ...status.value.config }
  allowDownload.value = status.value.allowDownload
  saveError.value = ''
  notice.value = ''
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
  <ManagementLayout
    class="proxy-ui"
    title="Emby 302 与媒体访问"
    description="按 Emby 权限和网盘绑定提供原文件播放。"
    :parent="{ label: 'Emby', to: '/emby' }"
    @logout="$emit('logout')"
  >
    <div class="proxy-page">
      <section class="proxy-card" aria-labelledby="proxy-service-title">
        <div class="proxy-status-heading">
          <div class="proxy-status-main">
            <span class="proxy-service-icon" aria-hidden="true"><IconServer :size="21" /></span>
            <div class="min-w-0">
              <div class="proxy-status-title">
                <h2 id="proxy-service-title" class="proxy-card-title">反代服务</h2>
                <span id="proxy-runtime-status" role="status" class="proxy-chip proxy-runtime-chip" :data-tone="runtimeTone">{{ runtimeLabel }}</span>
              </div>
              <p v-if="savedWithoutListener" class="proxy-help">配置已启用，当前未监听。</p>
              <p v-else-if="status?.listening" class="proxy-help">监听端口 {{ status.config.port }}</p>
            </div>
          </div>
          <button v-if="loaded" type="button" class="proxy-refresh-button" :disabled="busy" @click="load">
            <IconRefresh :size="17" :class="{ 'animate-spin motion-reduce:animate-none': operation === 'load' }" aria-hidden="true" />
            刷新状态
          </button>
        </div>
        <div class="proxy-status-feedback">
          <p v-if="status?.error" role="alert" class="proxy-alert is-error">{{ status.error }}</p>
          <p v-if="readError" role="alert" class="proxy-alert is-error">
            {{ loaded ? `刷新失败，当前显示上次读取的状态。${readError}` : readError }}
            <button v-if="!loaded" type="button" :disabled="busy" class="underline" @click="load">重试读取</button>
          </p>
          <p v-if="accountError" role="alert" class="proxy-alert is-error">
            网盘状态读取失败：{{ accountError }}
            <button type="button" :disabled="busy" class="underline" @click="load">重试读取</button>
          </p>
          <P115PrimaryStatus v-if="accountsLoaded" :accounts="accounts" />
        </div>
      </section>

      <section class="proxy-card min-w-0" aria-labelledby="proxy-config-title">
        <header class="proxy-card-heading">
          <h2 id="proxy-config-title" class="proxy-card-title">反代设置</h2>
          <span class="proxy-form-status" :data-tone="dirty ? 'warning' : undefined">
            <IconCheck v-if="loaded && !dirty" :size="14" aria-hidden="true" />
            {{ dirty ? '待保存' : loaded ? '已同步' : readError ? '读取失败' : '读取中…' }}
          </span>
        </header>
        <form class="proxy-form" @submit.prevent="save" @invalid.capture="revealInvalid">
          <div class="proxy-card-body">
            <p v-if="saveError" role="alert" class="proxy-alert is-error">{{ saveError }}</p>
            <fieldset :disabled="busy || !loaded" class="proxy-fields">
              <legend class="sr-only">反代连接与访问</legend>
              <div class="proxy-switch-list">
                <SettingSwitch
                  v-model="config.enabled"
                  trailing
                  label="启用 302 并接管播放"
                  description="保存后生效。播放遵循 Emby 权限和网盘条件，始终禁止转码与重新封装。"
                >
                  <p v-if="pendingEnable" class="proxy-pending">{{ config.enabled ? '待保存后启动入口' : '待保存后停止入口' }}</p>
                </SettingSwitch>
                <SettingSwitch
                  v-model="allowDownload"
                  trailing
                  label="允许下载原文件"
                  description="反代运行时开放下载，仍需 Emby 下载权限和网盘绑定。"
                />
              </div>
              <div class="proxy-field-grid">
                <div class="proxy-field">
                  <label for="proxy-url" class="proxy-field-label">Emby 原服务器地址</label>
                  <input id="proxy-url" v-model.trim="config.url" type="url" :required="config.enabled" placeholder="http://192.168.0.3:8096" class="proxy-input" aria-describedby="proxy-url-help" />
                  <p id="proxy-url-help" class="proxy-help">填写真实 Emby 地址，避开反代入口。Docker 中 localhost 指向 Cinema115 容器。</p>
                </div>
                <div class="proxy-field">
                  <label for="proxy-port" class="proxy-field-label">反代监听端口</label>
                  <input id="proxy-port" v-model.number="config.port" type="number" min="1024" max="65535" step="1" required class="proxy-input tabular-nums" aria-describedby="proxy-port-help" />
                  <p id="proxy-port-help" class="proxy-help">更改后需更新端口映射和播放器地址。</p>
                </div>
              </div>
              <details class="proxy-details proxy-advanced">
                <summary class="proxy-summary">
                  <span>高级设置 <span class="proxy-help font-normal">直链缓存与调试日志</span></span>
                  <IconChevronDown :size="17" class="proxy-chevron" aria-hidden="true" />
                </summary>
                <div class="proxy-details-body">
                  <div class="proxy-field">
                    <label for="proxy-cache-minutes" class="proxy-field-label">直链缓存时间（分钟）</label>
                    <input id="proxy-cache-minutes" v-model.number="config.directLinkCacheMinutes" type="number" min="0" max="30" step="1" required class="proxy-input proxy-input-compact tabular-nums" aria-describedby="proxy-cache-help" />
                    <p id="proxy-cache-help" class="proxy-help">默认 9 分钟，0 为关闭。命中不续期，也不能延长 115 直链的有效期。</p>
                  </div>
                  <SettingSwitch v-model="config.debug" trailing label="反代调试日志" description="新请求可展开取链、计时和请求详情。详情仅存内存，日志文件只保存摘要。Cookie、登录令牌及未知请求头值隐藏。" />
                </div>
              </details>
            </fieldset>
          </div>
          <footer class="proxy-card-footer">
            <p v-if="notice && !dirty" role="status" class="proxy-inline-success">{{ notice }}</p>
            <div class="proxy-footer-actions">
              <button v-if="dirty" type="button" class="proxy-secondary-button" :disabled="busy" @click="resetDraft">还原修改</button>
              <button type="submit" class="proxy-button" :disabled="busy || !loaded">
                {{ operation === 'save' ? '正在保存…' : '保存反代配置' }}
              </button>
            </div>
          </footer>
        </form>
      </section>
      <PlaybackDevicePolicy ref="devicePolicy" :token="token" class="min-w-0" />
    </div>
  </ManagementLayout>
</template>
