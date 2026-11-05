<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconBolt, IconBrandGithub, IconDeviceFloppy, IconLoader2, IconSubtitles, IconWorld } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import tmdbLogo from '../assets/tmdb.svg'
import mikanLogo from '../assets/anime-providers/mikan-mascot.webp'
import anibtLogo from '../assets/anime-providers/anibt-logo.webp'
import gardenLogo from '../assets/anime-providers/garden.svg'
import telegramLogo from '../assets/telegram/telegram.svg'
import gyingLogo from '../assets/gying/logo-gying.png'
import woniuLogo from '../assets/woniu.svg'
import { networkProxyRequest, type NetworkProxyConfig, type NetworkProxyInput, type NetworkProxyTest } from '../lib/network-proxy-api'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const form = reactive<NetworkProxyInput>({ enabled: false, url: '', username: '', services: [] })
const loaded = ref(false), busy = ref(''), error = ref(''), notice = ref(''), baseline = ref('')
const password = ref(''), passwordConfigured = ref(false), clearPassword = ref(false), authOpen = ref(false)
const availableServices = ref<NetworkProxyConfig['availableServices']>([])
type TestRow = { state: 'running' | 'done' | 'failed' | 'cancelled'; result?: NetworkProxyTest; error?: string }
const testDialog = ref(false), testResults = ref<Record<string, TestRow>>({})
const serviceLogos: Record<string, string> = { tmdb: tmdbLogo, mikan: mikanLogo, anibt: anibtLogo, garden: gardenLogo, telegram_bot: telegramLogo, telegram_user: telegramLogo, gying: gyingLogo, woniu: woniuLogo }
const leaveDialog = ref(false)
let leaveDecision: ((value: boolean) => void) | undefined
let testController: AbortController | undefined
function payload(): NetworkProxyInput {
  return { enabled: form.enabled, url: form.url.trim(), username: form.username.trim(), services: [...form.services].sort(),
    ...(clearPassword.value ? { password: '' } : password.value ? { password: password.value } : {}) }
}
const signature = computed(() => JSON.stringify(payload()))
const dirty = computed(() => loaded.value && signature.value !== baseline.value)
const credentialIssue = computed(() => !form.username.trim() && (password.value || passwordConfigured.value && !clearPassword.value) ? '填写代理用户名；若要取消认证，请同时清除密码。' : '')
watch(signature, () => { testResults.value = {}; notice.value = ''; error.value = '' }, { flush: 'sync' })
function apply(config: NetworkProxyConfig) {
  Object.assign(form, { enabled: config.enabled, url: config.url, username: config.username, services: [...config.services] })
  passwordConfigured.value = config.passwordConfigured; password.value = ''; clearPassword.value = false
  availableServices.value = config.availableServices
  baseline.value = signature.value; loaded.value = true
}
async function load() {
  if (busy.value) return
  busy.value = 'load'; error.value = ''
  try { apply(await networkProxyRequest<NetworkProxyConfig>(props.token, lifecycle.signal)) }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取网络代理配置失败' }
  finally { busy.value = '' }
}
async function save() {
  if (busy.value || !loaded.value || !dirty.value || credentialIssue.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''; testResults.value = {}
  try {
    apply(await networkProxyRequest<NetworkProxyConfig>(props.token, lifecycle.signal, payload()))
    notice.value = form.enabled ? '网络代理配置已保存。' : '网络代理已关闭，服务使用直连。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存结果尚未确认，输入已保留，可重试保存当前配置。' }
  finally { busy.value = '' }
}
function openTests() { if (!busy.value && loaded.value && !credentialIssue.value) testDialog.value = true }
function closeTests() { testController?.abort(); testDialog.value = false }
async function testConnections(services: string[]) {
  if (busy.value || !loaded.value || !testDialog.value || !services.length || credentialIssue.value) return
  testController = new AbortController()
  const controller = testController
  const input = payload(), signal = AbortSignal.any([lifecycle.signal, controller.signal])
  const ids = [...new Set(services)].filter(id => availableServices.value.some(service => service.id === id))
  busy.value = 'test'; error.value = ''; notice.value = ''
  for (const id of ids) delete testResults.value[id]
  try {
    // One explicit request at a time; a slow service never creates an unbounded queue.
    for (const service of ids) {
      if (signal.aborted || !testDialog.value) break
      testResults.value[service] = { state: 'running' }
      try {
        const result = await networkProxyRequest<NetworkProxyTest>(props.token, signal, { ...input, service }, true)
        signal.throwIfAborted()
        if (testController === controller && testDialog.value) testResults.value[service] = { state: 'done', result }
      } catch (reason) {
        if (lifecycle.signal.aborted) break
        if (controller.signal.aborted) {
          testResults.value[service] = { state: 'cancelled', error: '测试已取消' }
          notice.value = '测试已取消，当前输入已保留。'
          break
        }
        testResults.value[service] = { state: 'failed', error: reason instanceof Error ? reason.message : '连接测试失败，请重试' }
      }
    }
  } finally { if (testController === controller) { testController = undefined; busy.value = '' } }
}
function decideLeave(value: boolean) { leaveDialog.value = false; leaveDecision?.(value); leaveDecision = undefined }
function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value || busy.value === 'save') { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteLeave(() => {
  decideLeave(false)
  if (busy.value === 'save' || busy.value === 'test') { error.value = '请完成当前操作或取消测试后再离开。'; return false }
  if (!dirty.value) return true
  leaveDialog.value = true
  return new Promise<boolean>(resolve => { leaveDecision = resolve })
})
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void load() })
onBeforeUnmount(() => { lifecycle.abort(); testController?.abort(); password.value = ''; window.removeEventListener('beforeunload', beforeUnload); decideLeave(false) })
</script>

<template>
  <ManagementLayout class="np-layout" title="网络代理" description="为选定的外部资源服务设置 NAS 出站代理。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <template #actions><button type="button" class="np-button" :disabled="!!busy || !loaded || !!credentialIssue" @click="openTests"><IconBolt :size="16" aria-hidden="true" />测试连接</button><button type="submit" form="network-proxy-form" class="np-button np-primary" :disabled="!!busy || !loaded || !dirty || !!credentialIssue"><IconDeviceFloppy :size="16" aria-hidden="true" />{{ busy === 'save' ? '保存中…' : '保存配置' }}</button></template>
    <p v-if="error" role="alert" class="np-feedback np-error mb-4">{{ error }}<button v-if="!loaded" type="button" :disabled="!!busy" class="underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="np-feedback np-note mb-4">{{ notice }}</p>
    <p v-if="busy === 'load'" role="status" class="mb-4 text-sm text-content-secondary">正在读取网络代理配置…</p>
    <div class="np-page">
      <form id="network-proxy-form" class="np-panel min-w-0 rounded-xl border border-line-default bg-surface-card p-4 shadow-sm sm:p-6" :aria-busy="!!busy" @submit.prevent="save">
        <fieldset :disabled="!!busy || !loaded" class="np-fields">
          <SettingSwitch v-model="form.enabled" label="启用网络代理" description="仅对下方选中的服务生效，保存后应用。" :disabled="!!busy || !loaded" />
          <div class="np-address"><label class="np-field">代理地址<input v-model="form.url" type="url" :required="form.enabled" maxlength="2048" inputmode="url" autocomplete="off" spellcheck="false" placeholder="http://192.168.0.2:7890" aria-describedby="np-url-help" /></label><p id="np-url-help" class="np-muted mt-2">支持 HTTP、HTTPS、SOCKS5、SOCKS5H，填写 NAS 可访问的地址。</p></div>
          <details class="np-auth np-section" :open="authOpen" @toggle="authOpen = ($event.target as HTMLDetailsElement).open">
            <summary><span>代理认证</span><small>{{ passwordConfigured ? '已保存密码' : '可选' }}</small></summary>
            <div class="np-auth-fields"><label class="np-field">代理用户名<input v-model="form.username" type="text" autocomplete="off" maxlength="256" :aria-invalid="credentialIssue ? true : undefined" :aria-describedby="credentialIssue ? 'np-auth-help np-credential-error' : 'np-auth-help'" /></label><label class="np-field">代理密码<input v-model="password" type="password" autocomplete="new-password" :disabled="clearPassword" maxlength="1024" :placeholder="passwordConfigured ? '留空保留已保存密码' : '可选'" aria-describedby="np-auth-help" /></label></div>
            <label v-if="passwordConfigured" class="np-check"><input v-model="clearPassword" type="checkbox" :disabled="!!password" /><span>清除已保存密码</span></label>
            <p id="np-auth-help" class="np-muted mt-2">密码不会回显，关闭此区域不会丢弃输入；修改或清除后需保存。</p>
          </details>
          <p v-if="credentialIssue" id="np-credential-error" role="alert" class="np-credential-error">{{ credentialIssue }}</p>
          <section class="np-section" aria-labelledby="np-services-title">
            <div class="np-section-heading"><h2 id="np-services-title">服务范围</h2><span class="np-muted">已选择 {{ form.services.length }} 项</span></div>
            <div class="np-services"><label v-for="service in availableServices" :key="service.id" class="np-check np-service" :class="{ 'is-selected': form.services.includes(service.id) }"><input v-model="form.services" type="checkbox" :value="service.id" /><span>{{ service.label }}</span></label></div>
            <p class="np-muted">未选中的服务直连。Telegram 用户与机器人分别配置，下一次操作生效。</p>
          </section>
          <p class="np-muted np-save-state">{{ dirty ? '有未保存的修改' : loaded ? '当前配置已保存' : '' }}</p>
        </fieldset>
      </form>
      <p class="np-boundary">115 网盘与播放取链、Emby 和内网服务保持直连。这里只控制 NAS 出站请求，浏览器打开外站或播放器访问 CDN 不受影响。</p>
    </div>
    <P115ActionDialog v-if="testDialog" title="连接测试" size="wide" @close="closeTests">
      <p class="np-muted">使用当前表单测试，不会保存配置。未启用代理或未勾选的服务测试直连。</p>
      <ul class="np-test-list" aria-label="服务连接测试">
        <li v-for="service in availableServices" :key="service.id" class="np-test-row" :data-service="service.id" :aria-busy="testResults[service.id]?.state === 'running'">
          <span class="np-test-logo" aria-hidden="true"><img v-if="serviceLogos[service.id]" :src="serviceLogos[service.id]" alt="" draggable="false" /><IconBrandGithub v-else-if="service.id === 'github'" :size="24" /><IconSubtitles v-else-if="service.id === 'assrt'" :size="24" /><IconWorld v-else :size="24" /></span>
          <div class="np-test-info"><div class="np-test-name">{{ service.label }}<span class="np-test-route">{{ form.enabled && form.services.includes(service.id) ? '代理' : '直连' }}</span></div>
            <p v-if="testResults[service.id]" role="status" class="np-test-result" :class="testResults[service.id].result?.reachable ? 'is-success' : testResults[service.id].state === 'failed' || testResults[service.id].result ? 'is-failed' : ''">{{ testResults[service.id].state === 'running' ? '测试中…' : testResults[service.id].result ? testResults[service.id].result!.reachable ? '连接成功' : '连接失败' : testResults[service.id].error }}<span v-if="testResults[service.id].result?.status"> · HTTP {{ testResults[service.id].result!.status }}</span><span v-if="testResults[service.id].result && !testResults[service.id].result!.reachable" class="np-test-message">{{ testResults[service.id].result!.message }}</span></p>
            <span v-else class="np-muted">{{ service.id === 'telegram_user' ? '数据中心 TCP／代理隧道' : '未测试' }}</span>
          </div>
          <span class="np-test-latency">{{ testResults[service.id]?.result ? `${testResults[service.id].result!.elapsedMs} ms` : '' }}</span>
          <button type="button" class="np-test-action" :aria-label="`测试 ${service.label}`" :disabled="!!busy" @click="testConnections([service.id])"><IconLoader2 v-if="testResults[service.id]?.state === 'running'" :size="18" class="np-spin" aria-hidden="true" /><IconBolt v-else :size="18" aria-hidden="true" /></button>
        </li>
      </ul>
      <p class="np-muted">只验证网络连通，不验证 API 凭据、Telegram 登录或频道权限。测试已选服务时逐项执行，可随时取消。</p>
      <template #footer><button v-if="busy === 'test'" type="button" class="np-button" @click="testController?.abort()">取消测试</button><button type="button" class="np-button np-primary" :disabled="!!busy || !form.services.length" @click="testConnections(form.services)">测试已选服务</button></template>
    </P115ActionDialog>
    <P115ActionDialog v-if="leaveDialog" title="放弃未保存的代理设置？" @close="decideLeave(false)"><p>离开后将丢弃当前修改，已保存的配置不会改变。</p><template #footer><button type="button" class="np-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="np-button np-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.np-page { display: grid; gap: 16px; color: rgb(var(--content-primary)); }
.np-fields { display: grid; gap: 24px; min-width: 0; }
.np-field { display: grid; min-width: 0; gap: 8px; color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; }
.np-field input { width: 100%; min-width: 0; min-height: 44px; padding: 9px 12px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-page)); color: rgb(var(--content-primary)); font-size: 14px; font-weight: 400; }
.np-field input::placeholder { color: rgb(var(--content-secondary)); opacity: 1; }
.np-field input:focus { border-color: rgb(var(--focus)); outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.np-field input:disabled { opacity: .5; cursor: not-allowed; }
.np-field input[aria-invalid=true] { border-color: rgb(var(--status-danger)); }
.np-muted, .np-boundary { color: rgb(var(--content-secondary)); font-size: 12px; line-height: 1.8; }
.np-section { border-top: 1px solid rgb(var(--line-default)); padding-top: 24px; }
h2 { font-size: 16px; font-weight: 600; color: rgb(var(--content-primary)); }
.np-section-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.np-services { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin: 12px 0; }
.np-check { display: flex; min-width: 0; min-height: 40px; align-items: center; gap: 8px; font-size: 14px; cursor: pointer; }
.np-check input { width: 18px; height: 18px; flex-shrink: 0; accent-color: rgb(var(--action-primary)); }
.np-check:has(input:disabled) { cursor: not-allowed; }
.np-check input:disabled { opacity: .5; }
.np-check span { overflow-wrap: anywhere; }
.np-service { padding: 8px 12px; border: 1px solid rgb(var(--line-default)); border-radius: 8px; }
.np-service.is-selected { border-color: rgb(var(--selection-content)); background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); }
.np-auth summary { display: list-item; min-height: 40px; align-content: center; font-size: 14px; font-weight: 500; cursor: pointer; }
.np-auth summary small { margin-left: 12px; font-size: 12px; font-weight: 400; color: rgb(var(--content-secondary)); }
.np-auth-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; margin: 16px 0 8px; }
.np-save-state { padding-top: 4px; }
.np-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 40px; padding: 8px 16px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; transition: background-color 160ms; }
.np-primary { border-color: rgb(var(--action-primary)); background: rgb(var(--action-primary)); color: rgb(var(--action-on-primary)); }
.np-button:disabled { opacity: .5; cursor: not-allowed; }
.np-button:focus-visible, .np-check input:focus-visible, .np-auth summary:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 3px; }
.np-feedback { border-radius: 8px; padding: 12px 16px; font-size: 13px; line-height: 1.8; overflow-wrap: anywhere; }
.np-feedback button { margin-left: 12px; }
.np-error, .np-credential-error, .np-test-result.is-failed { color: rgb(var(--status-danger)); }
.np-error { background: rgb(var(--status-danger-surface)); }
.np-credential-error { font-size: 12px; }
.np-note { color: rgb(var(--content-secondary)); background: rgb(var(--surface-page)); }
.np-test-result.is-success { color: rgb(var(--status-success)); }
.np-test-list { display: grid; gap: 8px; max-height: 52dvh; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.np-test-row { display: grid; grid-template-columns: 36px minmax(0, 1fr) auto 36px; align-items: center; gap: 12px; min-height: 72px; padding: 12px; border-radius: 12px; background: rgb(var(--surface-page)); color: rgb(var(--content-primary)); }
.np-test-logo { display: flex; align-items: center; justify-content: center; width: 36px; height: 36px; flex-shrink: 0; border-radius: 8px; background: rgb(var(--surface-hover)); }
.np-test-logo img { max-width: 28px; max-height: 28px; object-fit: contain; }
.np-test-info { min-width: 0; }
.np-test-name { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; font-size: 14px; font-weight: 500; line-height: 1.6; overflow-wrap: anywhere; }
.np-test-route { font-size: 12px; font-weight: 400; color: rgb(var(--content-secondary)); }
.np-test-result { font-size: 12px; line-height: 1.7; color: rgb(var(--content-secondary)); overflow-wrap: anywhere; }
.np-test-message { display: block; }
.np-test-latency { font-size: 13px; white-space: nowrap; font-variant-numeric: tabular-nums; }
.np-test-action { display: flex; align-items: center; justify-content: center; width: 36px; height: 36px; border-radius: 8px; color: rgb(var(--content-secondary)); }
.np-test-action:disabled { opacity: .5; cursor: not-allowed; }
.np-test-action:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.np-spin { animation: np-spin 1s linear infinite; }
@keyframes np-spin { to { transform: rotate(360deg); } }
@media (hover: hover) {
  .np-button:not(:disabled):hover { background: rgb(var(--surface-hover)); }
  .np-primary:not(:disabled):hover { border-color: rgb(var(--action-primary-hover)); background: rgb(var(--action-primary-hover)); }
  .np-service:has(input:enabled):not(.is-selected):hover { background: rgb(var(--surface-hover)); }
  .np-test-action:not(:disabled):hover { color: rgb(var(--selection-content)); background: rgb(var(--selection-surface)); }
}
@media (max-width: 639px) {
  .np-layout :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); }
  .np-layout :deep(.management-heading > div) { grid-row: 3; flex-wrap: wrap; }
  .np-layout :deep(.management-heading > p) { grid-column: 1; }
  .np-fields { gap: 16px; }
  .np-section { padding-top: 16px; }
  .np-auth-fields { grid-template-columns: minmax(0, 1fr); }
  .np-field input { font-size: 16px; }
  .np-button, .np-check, .np-auth summary { min-height: 44px; }
  .np-services { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .np-service { padding: 8px; font-size: 13px; }
  .np-test-row { grid-template-columns: 28px minmax(0, 1fr) auto 44px; gap: 8px; padding: 8px; }
  .np-test-logo { width: 28px; height: 28px; }
  .np-test-logo img { max-width: 24px; max-height: 24px; }
  .np-test-name { font-size: 13px; }
  .np-test-latency { font-size: 12px; }
  .np-test-action { width: 44px; height: 44px; }
}
@media (max-width: 359px) { .np-services { grid-template-columns: minmax(0, 1fr); } }
@media (prefers-reduced-motion: reduce) { .np-spin { animation: none; } .np-button { transition: none; } }
</style>
