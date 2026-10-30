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
    <p v-if="busy === 'load'" role="status" class="mb-4 text-sm text-slate-500 dark:text-slate-400">正在读取网络代理配置…</p>
    <div class="np-page">
      <form id="network-proxy-form" class="np-panel min-w-0 rounded-2xl bg-white p-5 shadow-sm dark:bg-slate-900 sm:p-6" @submit.prevent="save">
        <fieldset :disabled="!!busy || !loaded" class="np-fields">
          <SettingSwitch v-model="form.enabled" label="启用网络代理" description="仅对下方选中的服务生效，保存后应用。" :disabled="!!busy || !loaded" />
          <div class="np-address"><label class="np-field">代理地址<input v-model="form.url" type="url" :required="form.enabled" maxlength="2048" inputmode="url" autocomplete="off" spellcheck="false" placeholder="http://192.168.0.2:7890" aria-describedby="np-url-help" /></label><p id="np-url-help" class="np-muted mt-2">支持 HTTP、HTTPS、SOCKS5、SOCKS5H，填写 NAS 可访问的地址。</p></div>
          <details class="np-auth np-section" :open="authOpen" @toggle="authOpen = ($event.target as HTMLDetailsElement).open">
            <summary><span>代理认证</span><small>{{ passwordConfigured ? '已保存密码' : '可选' }}</small></summary>
            <div class="np-auth-fields"><label class="np-field">代理用户名<input v-model="form.username" type="text" autocomplete="off" maxlength="256" /></label><label class="np-field">代理密码<input v-model="password" type="password" autocomplete="new-password" :disabled="clearPassword" maxlength="1024" :placeholder="passwordConfigured ? '留空保留已保存密码' : '可选'" /></label></div>
            <label v-if="passwordConfigured" class="np-check"><input v-model="clearPassword" type="checkbox" :disabled="!!password" /><span>清除已保存密码</span></label>
            <p class="np-muted mt-2">密码不会回显，关闭此区域不会丢弃输入；修改或清除后需保存。</p>
          </details>
          <p v-if="credentialIssue" role="alert" class="np-credential-error">{{ credentialIssue }}</p>
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
.np-page { display:grid;gap:16px;color:rgb(var(--color-slate-700)); }.dark .np-page { color:rgb(var(--color-slate-200)); }
.np-fields { display:grid;gap:20px;min-width:0; }.np-fields:disabled { opacity:.65; }
.np-fields :deep(input[role='switch']:checked + span) { background-color:#2563eb; }.np-fields :deep(input[role='switch']:focus-visible + span) { --tw-ring-color:#60a5fa; }
.np-field { display:grid;min-width:0;gap:8px;font-size:14px;font-weight:500; }
.np-field input { width:100%;min-width:0;min-height:44px;padding:9px 12px;border:1px solid rgb(var(--color-slate-200));border-radius:12px;background:rgb(var(--color-slate-50));color:inherit;font-size:14px;font-weight:400;outline:none; }.dark .np-field input { border-color:rgb(var(--color-slate-700));background:rgb(var(--color-slate-950)); }
.np-field input:focus { border-color:#3b82f6;box-shadow:0 0 0 2px rgb(59 130 246/.12); }
.np-muted,.np-boundary { color:rgb(var(--color-slate-500));font-size:12px;line-height:1.8; }.dark .np-muted,.dark .np-boundary { color:rgb(var(--color-slate-400)); }
.np-section { border-top:1px solid rgb(var(--color-slate-100));padding-top:20px; }.dark .np-section { border-color:rgb(var(--color-slate-800)); }h2 { font-size:16px;font-weight:600;color:rgb(var(--color-slate-900)); }.dark h2 { color:rgb(var(--color-slate-100)); }.np-auth summary { font-size:14px;font-weight:500; }.np-section-heading { display:flex;align-items:center;justify-content:space-between;gap:12px; }
.np-services { display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:12px 0; }.np-check { display:flex;min-width:0;min-height:40px;align-items:center;gap:9px;font-size:14px;cursor:pointer; }.np-check input { width:16px;height:16px;flex-shrink:0;accent-color:#2563eb; }.np-check span { overflow-wrap:anywhere; }.np-service { padding:8px 10px;border:1px solid rgb(var(--color-slate-200));border-radius:10px; }.np-service.is-selected { border-color:rgb(59 130 246/.3);background:rgb(59 130 246/.05); }.dark .np-service { border-color:rgb(var(--color-slate-700)); }.dark .np-service.is-selected { border-color:rgb(96 165 250/.4);background:rgb(59 130 246/.08); }
.np-auth summary { display:list-item;cursor:pointer; }.np-auth summary small { margin-left:10px;font-size:12px;font-weight:400;color:rgb(var(--color-slate-500)); }.np-auth-fields { display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px;margin:14px 0 8px; }
.np-save-state { padding-top:2px; }
.np-button { display:inline-flex;align-items:center;justify-content:center;gap:7px;min-height:40px;padding:8px 14px;border:1px solid rgb(var(--color-slate-200));border-radius:12px;background:#fff;font-size:14px;font-weight:500;color:rgb(var(--color-slate-700)); }.dark .np-button { border-color:rgb(var(--color-slate-700));background:rgb(var(--color-slate-900));color:rgb(var(--color-slate-200)); }.np-primary,.dark .np-primary { border-color:#2563eb;background:#2563eb;color:#fff; }.np-button:disabled { opacity:.45;cursor:not-allowed; }.np-button:focus-visible,.np-check input:focus-visible,.np-auth summary:focus-visible { outline:2px solid #3b82f6;outline-offset:3px; }
.np-feedback { border-radius:9px;padding:11px 13px;font-size:13px;line-height:1.8;overflow-wrap:anywhere; }.np-feedback button { margin-left:12px; }.np-error,.np-credential-error,.np-test-result.is-failed { color:#b91c1c; }.np-error { background:rgb(239 68 68/.07); }.dark .np-error,.dark .np-credential-error,.dark .np-test-result.is-failed { color:#fca5a5; }.np-credential-error { font-size:12px; }.np-note { color:rgb(var(--color-slate-600));background:rgb(var(--color-slate-100)); }.dark .np-note { color:rgb(var(--color-slate-300));background:rgb(var(--color-slate-800)); }.np-test-result.is-success { color:#047857; }.dark .np-test-result.is-success { color:#6ee7b7; }
.np-test-list { display:grid;gap:8px;max-height:52dvh;overflow-y:auto;overscroll-behavior:contain;scrollbar-gutter:stable; }.np-test-row { display:grid;grid-template-columns:36px minmax(0,1fr) auto 36px;align-items:center;gap:12px;min-height:70px;padding:12px;border-radius:12px;background:rgb(var(--color-slate-100));color:rgb(var(--color-slate-700)); }.dark .np-test-row { background:rgb(var(--color-slate-800));color:rgb(var(--color-slate-200)); }.np-test-logo { display:flex;align-items:center;justify-content:center;width:36px;height:36px;flex-shrink:0;border-radius:10px;background:rgb(var(--color-slate-200)); }.np-test-logo img { max-width:28px;max-height:28px;object-fit:contain; }.dark .np-test-logo { background:rgb(var(--color-slate-700)); }.np-test-info { min-width:0; }.np-test-name { display:flex;align-items:center;flex-wrap:wrap;gap:7px;font-size:14px;font-weight:500;line-height:1.6;overflow-wrap:anywhere; }.np-test-route { font-size:11px;font-weight:400;color:rgb(var(--color-slate-500)); }.dark .np-test-route { color:rgb(var(--color-slate-400)); }.np-test-result { font-size:12px;line-height:1.7;color:rgb(var(--color-slate-500));overflow-wrap:anywhere; }.np-test-message { display:block; }.np-test-latency { font-size:13px;white-space:nowrap;font-variant-numeric:tabular-nums; }.np-test-action { display:flex;align-items:center;justify-content:center;width:36px;height:36px;border-radius:9px;color:rgb(var(--color-slate-500)); }.np-test-action:not(:disabled):hover { color:#2563eb;background:rgb(59 130 246/.1); }.np-test-action:disabled { opacity:.45;cursor:default; }.np-test-action:focus-visible { outline:2px solid #60a5fa;outline-offset:2px; }.np-spin { animation:np-spin 1s linear infinite; }@keyframes np-spin { to { transform:rotate(360deg); } }@media(prefers-reduced-motion:reduce) { .np-spin { animation:none; } }
@media(hover:hover) { .np-button:not(:disabled):hover { filter:brightness(.96); }.dark .np-button:not(:disabled):hover { filter:brightness(1.12); } }
@media(max-width:639px) { .np-layout :deep(.management-heading) { grid-template-columns:minmax(0,1fr); }.np-layout :deep(.management-heading > div) { grid-row:3;flex-wrap:wrap; }.np-layout :deep(.management-heading > p) { grid-column:1; }.np-fields { gap:16px; }.np-section { padding-top:16px; }.np-auth-fields { grid-template-columns:minmax(0,1fr); }.np-field input { font-size:16px; }.np-button,.np-check { min-height:44px; }.np-services { grid-template-columns:repeat(2,minmax(0,1fr));gap:8px; }.np-service { padding:8px;font-size:13px; }.np-test-row { grid-template-columns:30px minmax(0,1fr) auto 32px;gap:8px;padding:10px 8px; }.np-test-logo { width:30px;height:30px; }.np-test-logo img { max-width:24px;max-height:24px; }.np-test-name { font-size:13px; }.np-test-latency { font-size:12px; }.np-test-action { width:32px;min-height:44px; } }
@media(max-width:359px) { .np-services { grid-template-columns:minmax(0,1fr); } }
</style>
