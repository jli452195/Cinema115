<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconBolt, IconCloud, IconDeviceFloppy, IconFolder, IconLoader2 } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import CD2DirectoryDialog from '../components/CD2DirectoryDialog.vue'
import { cd2Request, type CD2Config, type CD2Input, type CD2Permissions, type CD2Test } from '../lib/cd2-api'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const form = reactive<CD2Input>({ enabled: false, url: '' })
const config = ref<CD2Config>(), tokenInput = ref(''), clearToken = ref(false)
const busy = ref(''), error = ref(''), notice = ref(''), baseline = ref('')
const testResult = ref<CD2Test>(), directoryOpen = ref(false), leaveDialog = ref(false)
let testController: AbortController | undefined, leaveDecision: ((value: boolean) => void) | undefined

function payload(): CD2Input {
  return { enabled: form.enabled, url: form.url.trim(), ...(clearToken.value ? { token: '' } : tokenInput.value.trim() ? { token: tokenInput.value.trim() } : {}) }
}
const signature = computed(() => JSON.stringify(payload()))
const dirty = computed(() => !!config.value && signature.value !== baseline.value)
const hasToken = computed(() => !!tokenInput.value.trim() || !!config.value?.tokenConfigured && !clearToken.value)
const canTest = computed(() => !!config.value && !busy.value && !!form.url.trim() && hasToken.value)
const canBrowse = computed(() => !!config.value?.enabled && !!config.value?.tokenConfigured && !!config.value?.url && !dirty.value && !busy.value)
const permissionLabels: { key: keyof CD2Permissions; label: string }[] = [
  { key: 'list', label: '列目录' }, { key: 'read', label: '读取文件' }, { key: 'createFolder', label: '创建目录' },
  { key: 'rename', label: '重命名' }, { key: 'move', label: '移动' }, { key: 'copy', label: '复制' },
  { key: 'delete', label: '删除' },
]
watch(signature, () => { testResult.value = undefined; notice.value = ''; error.value = '' }, { flush: 'sync' })

function apply(value: CD2Config) {
  config.value = value
  form.enabled = value.enabled; form.url = value.url
  tokenInput.value = ''; clearToken.value = false
  baseline.value = signature.value
}
async function load() {
  if (busy.value) return
  busy.value = 'load'; error.value = ''
  try { apply(await cd2Request<CD2Config>(props.token, '', { signal: lifecycle.signal })) }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取 CloudDrive2 配置失败' }
  finally { busy.value = '' }
}
async function save() {
  if (busy.value || !config.value || !dirty.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''; testResult.value = undefined
  try {
    apply(await cd2Request<CD2Config>(props.token, '', { signal: lifecycle.signal, method: 'POST', body: JSON.stringify(payload()) }))
    notice.value = 'CloudDrive2 配置已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存结果尚未确认，输入已保留，可重试保存当前配置。'
  } finally { busy.value = '' }
}
async function test() {
  if (!canTest.value) return
  const controller = new AbortController()
  testController = controller
  const signal = AbortSignal.any([lifecycle.signal, controller.signal])
  busy.value = 'test'; error.value = ''; notice.value = ''; testResult.value = undefined
  try {
    const result = await cd2Request<CD2Test>(props.token, '/test', { signal, method: 'POST', body: JSON.stringify(payload()) })
    signal.throwIfAborted()
    if (testController === controller) testResult.value = result
  } catch (reason) {
    if (lifecycle.signal.aborted) return
    if (controller.signal.aborted) notice.value = '测试已取消，当前输入已保留。'
    else error.value = reason instanceof Error ? reason.message : 'CloudDrive2 连接测试失败'
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
onBeforeUnmount(() => { lifecycle.abort(); testController?.abort(); tokenInput.value = ''; window.removeEventListener('beforeunload', beforeUnload); decideLeave(false) })
</script>

<template>
  <ManagementLayout class="cd2-layout" title="CloudDrive2" description="通过官方 gRPC 连接 CloudDrive2，供目录浏览和自动整理等功能共用。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <template #actions>
      <button v-if="busy === 'test'" type="button" class="cd2-button" @click="testController?.abort()">取消测试</button>
      <button v-else type="button" class="cd2-button" :disabled="!canTest" @click="test"><IconBolt :size="16" aria-hidden="true" />测试连接</button>
      <button type="submit" form="cd2-config-form" class="cd2-button cd2-primary" :disabled="!!busy || !config || !dirty"><IconDeviceFloppy :size="16" aria-hidden="true" />{{ busy === 'save' ? '保存中…' : '保存配置' }}</button>
    </template>
    <p v-if="error" role="alert" class="cd2-feedback cd2-error">{{ error }}<button v-if="!config" type="button" :disabled="!!busy" class="underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="cd2-feedback cd2-note">{{ notice }}</p>
    <p v-if="busy === 'load'" role="status" class="cd2-muted mb-4">正在读取 CloudDrive2 配置…</p>
    <div class="cd2-page">
      <form id="cd2-config-form" class="cd2-panel" @submit.prevent="save">
        <fieldset :disabled="!!busy || !config" class="cd2-fields">
          <SettingSwitch v-model="form.enabled" label="启用 CloudDrive2" description="保存后，相关功能可使用此连接访问 CD2 授权范围内的存储。" :disabled="!!busy || !config" />
          <label class="cd2-field">服务地址<input v-model="form.url" type="url" :required="form.enabled" maxlength="2048" inputmode="url" autocomplete="off" spellcheck="false" placeholder="http://192.168.0.2:19798" aria-describedby="cd2-url-help" /></label>
          <p id="cd2-url-help" class="cd2-muted cd2-field-help">填写 Cinema115 后端可访问的 CD2 地址。HTTP 使用明文 gRPC，HTTPS 使用 TLS；反向代理需支持 HTTP/2 gRPC。</p>
          <label class="cd2-field">API Token<input v-model="tokenInput" type="password" autocomplete="new-password" maxlength="16384" :disabled="clearToken" :placeholder="config?.tokenConfigured ? '留空保留已保存 Token' : '填写在 CloudDrive2 中创建的 API Token'" aria-describedby="cd2-token-help" /></label>
          <label v-if="config?.tokenConfigured" class="cd2-check"><input v-model="clearToken" type="checkbox" :disabled="!!tokenInput" /><span>清除已保存 Token</span></label>
          <p id="cd2-token-help" class="cd2-muted cd2-field-help">Token 加密保存在后端，保存后不回显。按需要在 CD2 中授予目录读取、移动、重命名和清理等权限。</p>
          <div class="cd2-config-state"><span class="cd2-muted">{{ dirty ? '有未保存的修改' : config ? '当前配置已保存' : '' }}</span><span v-if="config" class="cd2-muted">{{ config.tokenConfigured ? '已保存 Token' : '未配置 Token' }}</span></div>
        </fieldset>
      </form>

      <section class="cd2-panel" aria-labelledby="cd2-test-title">
        <div class="cd2-section-heading"><div><h2 id="cd2-test-title">连接检查</h2><p class="cd2-muted">使用当前输入进行只读检查，测试结果仅代表本次请求。</p></div><IconCloud :size="23" class="cd2-section-icon" aria-hidden="true" /></div>
        <p v-if="busy === 'test'" role="status" class="cd2-testing"><IconLoader2 :size="18" class="cd2-spin" aria-hidden="true" />正在检查服务、授权和目录访问…</p>
        <template v-else-if="testResult">
          <dl class="cd2-checks" aria-label="连接检查结果">
            <div><dt>服务连接</dt><dd :class="testResult.reachable ? 'cd2-success' : 'cd2-error'">{{ testResult.reachable ? '可达' : '失败' }}</dd></div>
            <div><dt>Token 授权</dt><dd :class="testResult.authorized ? 'cd2-success' : 'cd2-error'">{{ testResult.authorized ? '有效' : testResult.reachable ? '未通过' : '未确认' }}</dd></div>
            <div><dt>目录读取</dt><dd :class="testResult.listable ? 'cd2-success' : 'cd2-error'">{{ testResult.listable ? '可读取' : testResult.authorized ? '未通过' : '未确认' }}</dd></div>
            <div><dt>服务版本</dt><dd>{{ testResult.version || '未获取' }}</dd></div>
            <div><dt>检查耗时</dt><dd>{{ testResult.elapsedMs }} ms</dd></div>
          </dl>
          <p role="status" class="cd2-muted cd2-result-message">{{ testResult.message }}</p>
          <div v-if="testResult.permissions" class="cd2-permissions" aria-label="Token 声明的权限"><span v-for="permission in permissionLabels" :key="permission.key" class="cd2-permission" :class="{ 'is-allowed': testResult.permissions[permission.key] }">{{ permission.label }}<small>{{ testResult.permissions[permission.key] ? '允许' : '未授予' }}</small></span></div>
          <p v-if="testResult.permissions" class="cd2-muted mt-3">权限来自 Token 声明。实际操作还取决于对应存储的能力和目录权限。</p>
        </template>
        <p v-else class="cd2-empty">填写地址和 Token 后点击“测试连接”，查看服务状态和授权范围。</p>
      </section>

      <section class="cd2-panel" aria-labelledby="cd2-use-title">
        <div class="cd2-section-heading"><div><h2 id="cd2-use-title">目录与功能</h2><p class="cd2-muted">浏览 CD2 中可访问的网盘和账号，在各功能中分别选择业务目录。</p></div></div>
        <div class="cd2-use-actions"><button type="button" class="cd2-button" :disabled="!canBrowse" @click="directoryOpen = true"><IconFolder :size="17" aria-hidden="true" />浏览目录</button><RouterLink to="/media/organize" class="cd2-button">前往自动整理</RouterLink></div>
        <p class="cd2-muted">{{ dirty ? '请先保存当前修改，再浏览目录。' : !config?.enabled ? '启用并保存连接后，可浏览 CD2 目录。' : !config?.tokenConfigured ? '请配置并保存 API Token。' : '目录浏览使用已保存的连接。自动整理中选择 CloudDrive2 即可使用。' }}</p>
        <p class="cd2-muted mt-2">自动整理支持自定义轮询间隔，可开关未匹配附件清理并设置宽限；目录读完立即整理，来源空子目录始终清理。</p>
        <p class="cd2-muted mt-2">更换地址或 Token 后，已有整理规则需重新选择目录。正在运行的任务使用启动时的连接设置。</p>
      </section>
    </div>
    <CD2DirectoryDialog v-if="directoryOpen && config" :token="token" :connection-id="config.id" :revision="config.revision" title="浏览 CloudDrive2 目录" confirm-label="完成浏览" @close="directoryOpen = false" @select="directoryOpen = false" />
    <P115ActionDialog v-if="leaveDialog" title="放弃未保存的 CloudDrive2 设置？" @close="decideLeave(false)"><p>离开后将丢弃当前修改。</p><template #footer><button type="button" class="cd2-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="cd2-button cd2-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.cd2-page{display:grid;gap:16px;max-width:1040px;color:rgb(var(--color-slate-700))}.cd2-panel{min-width:0;padding:24px;border:1px solid rgb(var(--color-slate-200)/.75);border-radius:16px;background:#fff;box-shadow:0 1px 3px rgb(15 23 42/.025)}.cd2-fields{display:grid;min-width:0;gap:20px}.cd2-fields:disabled{opacity:.65}.cd2-field{display:grid;min-width:0;gap:8px;font-size:14px;font-weight:500}.cd2-field input{width:100%;min-width:0;min-height:44px;padding:10px 12px;border:1px solid rgb(var(--color-slate-200));border-radius:11px;background:rgb(var(--color-slate-50));color:inherit;font-size:14px;font-weight:400;outline:none}.cd2-field input:focus{border-color:#3b82f6;box-shadow:0 0 0 2px rgb(59 130 246/.12)}.cd2-muted{font-size:12px;line-height:1.8;color:rgb(var(--color-slate-500));overflow-wrap:anywhere}.cd2-field-help{margin-top:-12px}.cd2-check{display:flex;align-items:center;gap:8px;font-size:13px}.cd2-check input{width:16px;height:16px;accent-color:#2563eb}.cd2-check:has(input:disabled){opacity:.55}.cd2-config-state{display:flex;flex-wrap:wrap;justify-content:space-between;gap:8px;padding-top:4px;border-top:1px solid rgb(var(--color-slate-100))}.cd2-section-heading{display:flex;justify-content:space-between;gap:12px}.cd2-section-heading h2{font-size:15px;font-weight:600;color:rgb(var(--color-slate-900));margin-bottom:4px}.cd2-section-icon{flex:none;color:#3b82f6}.cd2-button{display:inline-flex;align-items:center;justify-content:center;gap:7px;min-height:40px;padding:8px 14px;border:1px solid rgb(var(--color-slate-200));border-radius:11px;background:#fff;font-size:14px;font-weight:500;color:rgb(var(--color-slate-700))}.cd2-primary{border-color:#2563eb;background:#2563eb;color:#fff}.cd2-button:disabled{opacity:.45;cursor:not-allowed}.cd2-button:focus-visible,.cd2-check input:focus-visible{outline:2px solid #3b82f6;outline-offset:3px}.cd2-feedback{padding:12px 14px;margin-bottom:16px;border-radius:10px;font-size:13px;line-height:1.8;overflow-wrap:anywhere}.cd2-feedback button{margin-left:12px}.cd2-feedback.cd2-error{background:rgb(239 68 68/.07)}.cd2-error{color:#b91c1c}.cd2-success{color:#047857}.cd2-note{color:rgb(var(--color-slate-600));background:rgb(var(--color-slate-100))}.cd2-empty{padding:24px 0 4px;font-size:13px;line-height:1.8;color:rgb(var(--color-slate-500))}.cd2-testing{display:flex;align-items:center;gap:10px;padding-top:24px;font-size:13px;color:rgb(var(--color-slate-500))}.cd2-checks{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:16px;margin-top:24px}.cd2-checks dt{font-size:12px;color:rgb(var(--color-slate-500))}.cd2-checks dd{margin-top:5px;font-size:14px;overflow-wrap:anywhere}.cd2-result-message{margin-top:18px}.cd2-permissions{display:flex;flex-wrap:wrap;gap:8px;margin-top:16px}.cd2-permission{display:inline-flex;align-items:center;gap:7px;padding:6px 9px;border:1px solid rgb(var(--color-slate-200));border-radius:8px;font-size:12px;color:rgb(var(--color-slate-500))}.cd2-permission small{font-size:11px;color:inherit}.cd2-permission.is-allowed{color:#1d4ed8;border-color:rgb(59 130 246/.25);background:rgb(59 130 246/.04)}.cd2-use-actions{display:flex;flex-wrap:wrap;gap:10px;margin:18px 0 12px}.cd2-spin{animation:cd2-spin 1s linear infinite}@keyframes cd2-spin{to{transform:rotate(360deg)}}
:global(.dark .cd2-page){color:rgb(var(--color-slate-200))}
:global(.dark .cd2-panel){border-color:rgb(var(--color-slate-700)/.75);background:rgb(var(--color-slate-900))}
:global(.dark .cd2-field input){border-color:rgb(var(--color-slate-700));background:rgb(var(--color-slate-950))}
:global(.dark .cd2-muted),:global(.dark .cd2-empty),:global(.dark .cd2-testing),:global(.dark .cd2-checks dt){color:rgb(var(--color-slate-400))}
:global(.dark .cd2-section-heading h2){color:rgb(var(--color-slate-100))}
:global(.dark .cd2-config-state){border-color:rgb(var(--color-slate-800))}
:global(.dark .cd2-button){border-color:rgb(var(--color-slate-700));background:rgb(var(--color-slate-900));color:rgb(var(--color-slate-200))}
:global(.dark .cd2-primary){border-color:#2563eb;background:#2563eb;color:#fff}
:global(.dark .cd2-error){color:#fca5a5}
:global(.dark .cd2-success){color:#6ee7b7}
:global(.dark .cd2-note){color:rgb(var(--color-slate-300));background:rgb(var(--color-slate-800))}
:global(.dark .cd2-permission){border-color:rgb(var(--color-slate-700));color:rgb(var(--color-slate-400))}
:global(.dark .cd2-permission.is-allowed){color:#93c5fd;border-color:rgb(96 165 250/.3);background:rgb(59 130 246/.08)}
@media(hover:hover){.cd2-button:not(:disabled):hover{filter:brightness(.96)}}@media(prefers-reduced-motion:reduce){.cd2-spin{animation:none}}@media(max-width:639px){.cd2-panel{padding:18px}.cd2-layout :deep(.management-heading){grid-template-columns:minmax(0,1fr)}.cd2-layout :deep(.management-heading>div){grid-row:3;flex-wrap:wrap}.cd2-layout :deep(.management-heading>p){grid-column:1}.cd2-field input{font-size:16px}.cd2-button,.cd2-check{min-height:44px}.cd2-checks{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
