<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconBolt, IconBrandGithub, IconLoader2, IconSubtitles, IconWorld } from '@tabler/icons-vue'
import P115ActionDialog from './P115ActionDialog.vue'
import tmdbLogo from '../assets/tmdb.svg'
import mikanLogo from '../assets/anime-providers/mikan-mascot.webp'
import anibtLogo from '../assets/anime-providers/anibt-logo.webp'
import gardenLogo from '../assets/anime-providers/garden.svg'
import telegramLogo from '../assets/telegram/telegram.svg'
import gyingLogo from '../assets/gying/logo-gying.png'
import woniuLogo from '../assets/woniu.svg'
import type { NetworkConnectionTestRequest } from '../composables/useNetworkConnectionTest'
import {
  networkProxyRequest,
  type NetworkProxyConfig,
  type NetworkProxyInput,
  type NetworkProxyTest,
} from '../lib/network-proxy-api'

const props = defineProps<{
  request: NetworkConnectionTestRequest
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

// 测试行状态结构
interface TestRow {
  state: 'running' | 'done' | 'failed' | 'cancelled'
  result?: NetworkProxyTest
  error?: string
}

const lifecycle = new AbortController()
let batchController: AbortController | undefined

// 外部服务图标映射
const serviceLogos: Record<string, string> = {
  tmdb: tmdbLogo,
  mikan: mikanLogo,
  anibt: anibtLogo,
  garden: gardenLogo,
  telegram_bot: telegramLogo,
  telegram_user: telegramLogo,
  gying: gyingLogo,
  woniu: woniuLogo,
}

// 状态：输入配置快照、可用服务列表、加载与执行状态
const activeInput = ref<NetworkProxyInput>()
const availableServices = ref<NetworkProxyConfig['availableServices']>([])
const configLoading = ref(false)
const configError = ref('')
const batchBusy = ref(false)
const notice = ref('')
const testResults = ref<Record<string, TestRow>>({})

// 是否使用表单传入的快照进行测试
const isFormSnapshot = computed(() => Boolean(props.request.input && props.request.services))

// 初始化配置：若有快照直接使用，否则通过 GET 显式读取已保存配置
async function initConfig() {
  if (props.request.input && props.request.services) {
    activeInput.value = {
      enabled: props.request.input.enabled,
      url: props.request.input.url,
      username: props.request.input.username,
      ...(props.request.input.password !== undefined ? { password: props.request.input.password } : {}),
      services: [...props.request.input.services],
    }
    availableServices.value = props.request.services.map(service => ({ ...service }))
    configLoading.value = false
    configError.value = ''
    void testConnections(availableServices.value.map(service => service.id))
    return
  }

  configLoading.value = true
  configError.value = ''
  notice.value = ''
  try {
    const config = await networkProxyRequest<NetworkProxyConfig>(props.request.token, lifecycle.signal)
    if (lifecycle.signal.aborted) return
    // 构造显式 input，避免把 passwordConfigured/availableServices 等只读字段传入测试 POST 接口
    activeInput.value = {
      enabled: config.enabled,
      url: config.url,
      username: config.username,
      services: [...config.services],
    }
    availableServices.value = config.availableServices
    configLoading.value = false
    void testConnections(availableServices.value.map(service => service.id))
  } catch (err) {
    if (lifecycle.signal.aborted) return
    configLoading.value = false
    configError.value = err instanceof Error ? err.message : '读取网络代理配置失败'
  }
}

// 取消当前批次测试
function cancelBatch() {
  batchController?.abort()
}

// 关闭弹窗并释放请求
function handleClose() {
  batchController?.abort()
  lifecycle.abort()
  emit('close')
}

// 串行执行连通性测试
async function testConnections(services: string[]) {
  if (lifecycle.signal.aborted || batchBusy.value || !activeInput.value || !services.length) return
  batchController = new AbortController()
  const controller = batchController
  const input = activeInput.value
  const token = props.request.token
  const signal = AbortSignal.any([lifecycle.signal, controller.signal])
  const ids = [...new Set(services)].filter(id => availableServices.value.some(service => service.id === id))
  batchBusy.value = true
  notice.value = ''
  for (const id of ids) delete testResults.value[id]

  try {
    // 串行发送单项测试请求，避免慢服务排队失控
    for (const service of ids) {
      if (signal.aborted) break
      testResults.value[service] = { state: 'running' }
      try {
        const result = await networkProxyRequest<NetworkProxyTest>(token, signal, { ...input, service }, true)
        signal.throwIfAborted()
        if (batchController === controller && !lifecycle.signal.aborted) {
          testResults.value[service] = { state: 'done', result }
        }
      } catch (reason) {
        if (lifecycle.signal.aborted) break
        if (controller.signal.aborted) {
          testResults.value[service] = { state: 'cancelled', error: '测试已取消' }
          notice.value = '测试已取消，当前输入与结果已保留。'
          break
        }
        testResults.value[service] = {
          state: 'failed',
          error: reason instanceof Error ? reason.message : '连接测试失败，请重试',
        }
      }
    }
  } finally {
    if (batchController === controller) {
      batchController = undefined
      if (!lifecycle.signal.aborted) batchBusy.value = false
    }
  }
}

onMounted(() => {
  void initConfig()
})

onBeforeUnmount(() => {
  lifecycle.abort()
  batchController?.abort()
})
</script>

<template>
  <P115ActionDialog title="服务连通性" size="wide" compact @close="handleClose">
    <p class="np-test-top-note">
      {{ isFormSnapshot ? '当前表单测试，不保存配置；未启用代理或未勾选的服务直连。' : '使用已保存配置；未启用代理或未勾选的服务直连。' }}
    </p>
    <p v-if="configError" role="alert" class="np-feedback np-error mt-2">
      {{ configError }}
      <button type="button" class="underline ml-3" @click="initConfig">重试</button>
    </p>
    <p v-if="notice" role="status" class="np-feedback np-note mt-2">{{ notice }}</p>
    <p v-if="configLoading" role="status" class="np-muted mt-2">正在读取网络代理配置…</p>

    <ul v-if="availableServices.length" class="np-test-list" aria-label="服务连接测试">
      <li
        v-for="service in availableServices"
        :key="service.id"
        class="np-test-row"
        :data-service="service.id"
        :aria-busy="testResults[service.id]?.state === 'running'"
      >
        <span class="np-test-logo" aria-hidden="true">
          <img v-if="serviceLogos[service.id]" :src="serviceLogos[service.id]" alt="" draggable="false" />
          <IconBrandGithub v-else-if="service.id === 'github'" :size="24" />
          <IconSubtitles v-else-if="service.id === 'assrt'" :size="24" />
          <IconWorld v-else :size="24" />
        </span>
        <div class="np-test-info">
          <div class="np-test-name">
            {{ service.label }}
            <span class="np-test-route">{{ activeInput?.enabled && activeInput?.services.includes(service.id) ? '代理' : '直连' }}</span>
          </div>
          <p
            v-if="testResults[service.id]"
            role="status"
            class="np-test-result"
            :class="testResults[service.id].result?.reachable ? 'is-success' : testResults[service.id].state === 'failed' || testResults[service.id].result ? 'is-failed' : ''"
          >
            {{ testResults[service.id].state === 'running' ? '测试中…' : testResults[service.id].result ? (testResults[service.id].result!.reachable ? '连接成功' : '连接失败') : testResults[service.id].error }}
            <span v-if="testResults[service.id].result?.status"> · HTTP {{ testResults[service.id].result!.status }}</span>
            <span v-if="testResults[service.id].result && !testResults[service.id].result!.reachable" class="np-test-message">{{ testResults[service.id].result!.message }}</span>
          </p>
          <span v-else class="np-muted">{{ service.id === 'telegram_user' ? '数据中心 TCP／代理隧道' : '未测试' }}</span>
        </div>
        <span class="np-test-latency">{{ testResults[service.id]?.result ? `${testResults[service.id].result!.elapsedMs} ms` : '' }}</span>
        <button
          type="button"
          class="np-test-action"
          :aria-label="`测试 ${service.label}`"
          :disabled="batchBusy || configLoading"
          @click="testConnections([service.id])"
        >
          <IconLoader2 v-if="testResults[service.id]?.state === 'running'" :size="18" class="np-spin" aria-hidden="true" />
          <IconBolt v-else :size="18" aria-hidden="true" />
        </button>
      </li>
    </ul>

    <template #footer>
      <div class="np-test-footer">
        <p class="np-test-footer-note">仅测网络连通，不验证 API 凭据、Telegram 登录及频道权限；批量测试逐项执行，可取消。</p>
        <div class="np-test-footer-actions">
          <button v-if="batchBusy" type="button" class="np-button" @click="cancelBatch">取消测试</button>
          <button
            type="button"
            class="np-button np-primary"
            :disabled="batchBusy || configLoading || !availableServices.length"
            @click="testConnections(availableServices.map(service => service.id))"
          >
            重新测试全部
          </button>
        </div>
      </div>
    </template>
  </P115ActionDialog>
</template>

<style scoped>
.np-muted { color: rgb(var(--content-secondary)); font-size: 12px; line-height: 1.8; }
.np-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 40px; padding: 8px 16px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; transition: background-color 160ms; }
.np-primary { border-color: rgb(var(--action-primary)); background: rgb(var(--action-primary)); color: rgb(var(--action-on-primary)); }
.np-button:disabled { opacity: .5; cursor: not-allowed; }
.np-button:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 3px; }
.np-feedback { border-radius: 8px; padding: 12px 16px; font-size: 13px; line-height: 1.8; overflow-wrap: anywhere; }
.np-feedback button { margin-left: 12px; }
.np-error, .np-test-result.is-failed { color: rgb(var(--status-danger)); }
.np-error { background: rgb(var(--status-danger-surface)); }
.np-note { color: rgb(var(--content-secondary)); background: rgb(var(--surface-page)); }
.np-test-result.is-success { color: rgb(var(--status-success)); }
.np-test-list { display: grid; gap: 8px; max-height: 52dvh; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; margin: 12px 0 0; padding: 0; list-style: none; }
.np-test-row { display: grid; grid-template-columns: 36px minmax(0, 1fr) auto 36px; align-items: center; gap: 12px; min-height: 72px; padding: 12px; border-radius: 12px; background: rgb(var(--surface-page)); color: rgb(var(--content-primary)); }
.np-test-logo { display: flex; align-items: center; justify-content: center; width: 36px; height: 36px; flex-shrink: 0; border-radius: 8px; background: rgb(var(--surface-hover)); }
.np-test-logo img { max-width: 28px; max-height: 28px; object-fit: contain; }
.np-test-info { min-width: 0; }
.np-test-name { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; font-size: 14px; font-weight: 500; line-height: 1.6; overflow-wrap: anywhere; }
.np-test-route { font-size: 12px; font-weight: 400; color: rgb(var(--content-secondary)); }
.np-test-result { font-size: 12px; line-height: 1.7; color: rgb(var(--content-secondary)); overflow-wrap: anywhere; margin: 0; }
.np-test-message { display: block; }
.np-test-latency { font-size: 13px; white-space: nowrap; font-variant-numeric: tabular-nums; }
.np-test-action { display: flex; align-items: center; justify-content: center; width: 36px; height: 36px; border-radius: 8px; color: rgb(var(--content-secondary)); border: none; background: transparent; cursor: pointer; }
.np-test-action:disabled { opacity: .5; cursor: not-allowed; }
.np-test-action:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.np-spin { animation: np-spin 1s linear infinite; }
@keyframes np-spin { to { transform: rotate(360deg); } }
/* 连接测试紧凑模式顶部与底部说明及操作栏 */
.np-test-top-note { color: rgb(var(--content-secondary)); font-size: 12px; line-height: 18px; margin: 0; }
.np-test-footer { display: flex; align-items: center; justify-content: space-between; gap: 16px; width: 100%; }
.np-test-footer-note { flex: 1 1 auto; min-width: 0; margin: 0; color: rgb(var(--content-secondary)); font-size: 12px; line-height: 18px; }
.np-test-footer-actions { display: inline-flex; align-items: center; justify-content: flex-end; gap: 8px; flex-shrink: 0; }
@media (hover: hover) {
  .np-button:not(:disabled):hover { background: rgb(var(--surface-hover)); }
  .np-primary:not(:disabled):hover { border-color: rgb(var(--action-primary-hover)); background: rgb(var(--action-primary-hover)); }
  .np-test-action:not(:disabled):hover { color: rgb(var(--selection-content)); background: rgb(var(--selection-surface)); }
}
@media (max-width: 639px) {
  .np-button { min-height: 44px; }
  .np-test-row { grid-template-columns: 28px minmax(0, 1fr) auto 44px; gap: 8px; padding: 8px; }
  .np-test-logo { width: 28px; height: 28px; }
  .np-test-logo img { max-width: 24px; max-height: 24px; }
  .np-test-name { font-size: 13px; }
  .np-test-latency { font-size: 12px; }
  .np-test-action { width: 44px; height: 44px; }
  .np-test-footer { flex-direction: column; align-items: stretch; gap: 10px; }
  .np-test-footer-actions { justify-content: flex-end; }
}
@media (prefers-reduced-motion: reduce) { .np-spin { animation: none; } .np-button { transition: none; } }
</style>
