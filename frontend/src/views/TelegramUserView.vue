<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowRight, IconBrandTelegram, IconCheck, IconExternalLink, IconLogout, IconRefresh, IconShieldLock } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeFloatingDialog from '../components/AnimeFloatingDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import TelegramAvatar from '../components/TelegramAvatar.vue'
import { telegramUserRequest, type TelegramUserStatus } from '../types/telegramUser'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const status = ref<TelegramUserStatus>(), apiId = ref<number>(), apiHash = ref('')
const busy = ref('load'), error = ref(''), notice = ref(''), confirmDisconnect = ref(false), disconnectError = ref('')
const loginOpen = ref(false), loginReady = ref(false), loginBusy = ref(''), loginError = ref(''), loginNotice = ref('')
const step = ref<'phone' | 'code' | 'password'>('phone'), phone = ref(''), sentPhone = ref(''), code = ref(''), password = ref('')
const apiIDInput = ref<HTMLInputElement>(), phoneInput = ref<HTMLInputElement>(), codeInput = ref<HTMLInputElement>(), passwordInput = ref<HTMLInputElement>()
const lifecycle = new AbortController()
let loginController: AbortController | undefined, loginGeneration = 0

const locked = computed(() => !!busy.value || loginOpen.value)
const stateText = computed(() => status.value?.authorized ? '已连接' : status.value?.hasSession ? '会话待检查' : '未登录')
const pendingStep = computed(() => status.value?.stage === 'code' || status.value?.stage === 'password' ? status.value.stage : 'phone')
const stepNumber = computed(() => step.value === 'phone' ? 1 : step.value === 'code' ? 2 : 3)
const canSave = computed(() => !locked.value && !!status.value && Number.isSafeInteger(apiId.value) && (apiId.value || 0) > 0 && (status.value.configured || !!apiHash.value.trim()))
const canSubmit = computed(() => loginReady.value && !loginBusy.value && (step.value === 'phone' ? /^\+[1-9][0-9]{6,14}$/.test(phone.value.trim()) : step.value === 'code' ? !!code.value.trim() : !!password.value))
const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请稍后重试'
const request = <T,>(path: string, body?: object, signal = lifecycle.signal) => telegramUserRequest<T>(props.token, path, signal, body)

async function load() {
  if (busy.value && busy.value !== 'load') return
  busy.value = 'load'; error.value = ''
  try {
    const value = await request<TelegramUserStatus>('config')
    if (!lifecycle.signal.aborted) { status.value = value; apiId.value = value.apiId || undefined }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = message(reason) }
  finally { if (!lifecycle.signal.aborted) busy.value = '' }
}
async function act(action: 'config' | 'check' | 'disconnect') {
  if (locked.value || !status.value) return
  busy.value = action; error.value = ''; notice.value = ''; disconnectError.value = ''
  // Config is shared with the search page; keep its Bot value when only API fields change.
  const body = action === 'config' ? { apiId: apiId.value, apiHash: apiHash.value.trim(), bot: status.value.bot } : {}
  try {
    const value = await request<TelegramUserStatus>(action, body)
    if (lifecycle.signal.aborted) return
    status.value = value
    if (action === 'config') { apiId.value = value.apiId; apiHash.value = ''; notice.value = 'Telegram API 凭据已保存。' }
    else if (action === 'disconnect') { confirmDisconnect.value = false; notice.value = '已退出当前 Telegram 用户会话。'; clearLoginValues() }
    else notice.value = value.authorized ? 'Telegram 连接正常。' : '当前会话尚未登录，可以继续完成验证。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      if (action === 'disconnect') disconnectError.value = message(reason)
      else error.value = message(reason)
    }
  } finally { if (!lifecycle.signal.aborted) busy.value = '' }
}
function clearLoginValues() { phone.value = ''; sentPhone.value = ''; code.value = ''; password.value = '' }
function closeLogin() {
  loginGeneration++; loginController?.abort(); loginController = undefined
  loginOpen.value = false; loginReady.value = false; loginBusy.value = ''; loginError.value = ''; loginNotice.value = ''
  clearLoginValues()
}
async function focusStep() {
  await nextTick()
  if (!loginOpen.value || !loginReady.value || loginBusy.value) return
  const input = step.value === 'phone' ? phoneInput.value : step.value === 'code' ? codeInput.value : passwordInput.value
  input?.focus({ preventScroll: true })
}
function changePhone() {
  if (loginBusy.value) return
  step.value = 'phone'; code.value = ''; password.value = ''; loginError.value = ''; loginNotice.value = ''
  void focusStep()
}
function continueStep() {
  if (loginBusy.value) return
  step.value = pendingStep.value; loginError.value = ''; loginNotice.value = ''
  void focusStep()
}
function resendCode() {
  if (loginBusy.value || !sentPhone.value) return
  phone.value = sentPhone.value
  void loginAction('send-code')
}
function openLogin() {
  if (locked.value || !status.value) return
  if (!status.value.configured) { apiIDInput.value?.focus(); return }
  clearLoginValues(); loginOpen.value = true; loginReady.value = false
  loginError.value = ''; loginNotice.value = ''; step.value = 'phone'
  void loginAction('config')
}
async function loginAction(action: 'config' | 'send-code' | 'sign-in' | 'check') {
  if (!loginOpen.value || loginBusy.value) return
  const generation = loginGeneration, controller = new AbortController()
  loginController = controller; loginBusy.value = action; loginError.value = ''; loginNotice.value = ''
  const current = () => !lifecycle.signal.aborted && !controller.signal.aborted && loginOpen.value && generation === loginGeneration
  const submittedPhone = phone.value.trim()
  const body = action === 'config' ? undefined : action === 'send-code' ? { phone: submittedPhone } : action === 'sign-in' ? { code: step.value === 'code' ? code.value.trim() : '', password: step.value === 'password' ? password.value : '' } : {}
  try {
    const value = await request<TelegramUserStatus>(action, body, AbortSignal.any([lifecycle.signal, controller.signal]))
    if (!current()) return
    status.value = value; loginReady.value = true
    if (value.authorized) {
      notice.value = action === 'sign-in' ? 'Telegram 用户账号登录成功。' : 'Telegram 会话已连接。'
      closeLogin(); return
    }
    if (!value.configured) { loginReady.value = false; loginError.value = '请关闭窗口，先保存 Telegram API 凭据。'; return }
    step.value = value.stage === 'code' || value.stage === 'password' ? value.stage : 'phone'
    if (action === 'send-code') {
      sentPhone.value = submittedPhone; code.value = ''; password.value = ''
      loginNotice.value = '验证码已请求，请查看 Telegram 官方客户端或短信。'
    } else if (action === 'check') loginNotice.value = '会话尚未登录，请继续验证。'
  } catch (reason) { if (current()) loginError.value = message(reason) }
  finally {
    if (current()) {
      if (action === 'sign-in') { code.value = ''; password.value = '' }
      loginBusy.value = ''; loginController = undefined
      void focusStep()
    }
  }
}
function submitLogin() {
  if (canSubmit.value) void loginAction(step.value === 'phone' ? 'send-code' : 'sign-in')
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); closeLogin(); apiHash.value = '' })
</script>

<template>
  <ManagementLayout title="Telegram 用户账号" description="配置 API 凭据并连接 Telegram，供资源搜索与频道转发共用。" :parent="{ label: 'Telegram 配置', to: '/telegram' }" @logout="$emit('logout')">
    <div class="tg-account-page">
      <p v-if="error" role="alert" class="tg-message tg-error">{{ error }}<button v-if="!status" type="button" class="tg-text-button" :disabled="!!busy" @click="load">重新读取</button></p>
      <p v-if="notice" role="status" class="tg-message tg-notice">{{ notice }}</p>
      <section class="tg-card tg-identity-card" aria-label="用户登录状态" :aria-busy="busy === 'load' || busy === 'check'">
        <div class="tg-identity">
          <TelegramAvatar :token="token" peer-id="self" :avatar-id="status?.avatarId" :size="40" class="tg-avatar"><IconBrandTelegram :size="23" aria-hidden="true" /></TelegramAvatar>
          <div class="tg-identity-text"><div class="tg-name-row"><h2>{{ status?.name || (status?.username ? `@${status.username}` : 'Telegram 账号') }}</h2><span v-if="status" class="tg-state" :class="{ 'is-connected': status.authorized }">{{ stateText }}</span></div><p>{{ busy === 'load' ? '正在读取账号状态' : status?.username ? `@${status.username}` : status?.hasSession ? '已有保存的会话，可检查连接并恢复。' : '使用自己的 Telegram 用户账号登录' }}</p></div>
        </div>
        <div class="tg-account-actions">
          <button v-if="status?.hasSession || status?.authorized" type="button" class="tg-button" :disabled="locked || !status.configured" @click="act('check')"><IconRefresh :size="16" aria-hidden="true" :class="{ 'tg-spinning': busy === 'check' }" />{{ busy === 'check' ? '检查中' : '检查连接' }}</button>
          <button v-if="status?.authorized || status?.hasSession" type="button" class="tg-button tg-exit" :disabled="locked" @click="confirmDisconnect = true; disconnectError = ''"><IconLogout :size="16" aria-hidden="true" />退出账号</button>
          <button v-if="status && !status.authorized" type="button" class="tg-button tg-primary" :disabled="locked || !status.configured" @click="openLogin">{{ status.stage !== 'idle' ? '继续登录' : '登录账号' }}<IconArrowRight :size="16" aria-hidden="true" /></button>
        </div>
        <p v-if="status && !status.configured" class="tg-setup-hint">先保存下方 API 凭据，再登录用户账号。</p>
      </section>
      <form class="tg-card tg-api-card" :aria-busy="busy === 'config'" @submit.prevent="canSave && act('config')">
        <div class="tg-card-heading"><div><h2>API 凭据</h2><p>使用在 Telegram 申请的应用凭据。</p></div><span v-if="status?.configured" class="tg-saved"><IconCheck :size="14" aria-hidden="true" />已配置</span></div>
        <fieldset :disabled="locked || !status" class="tg-api-fields"><legend class="sr-only">Telegram API 凭据</legend><label>API ID<input ref="apiIDInput" v-model.number="apiId" type="number" min="1" step="1" required inputmode="numeric" autocomplete="off" placeholder="填写 API ID" /></label><label>API Hash<input v-model="apiHash" type="password" autocomplete="new-password" spellcheck="false" :required="!status?.configured" pattern="[a-fA-F0-9]{32}" :placeholder="status?.configured ? '已保存，留空保留' : '填写 API Hash'" /></label></fieldset>
        <div class="tg-api-footer"><a href="https://my.telegram.org/apps" target="_blank" rel="noopener noreferrer">申请 API 凭据<IconExternalLink :size="13" aria-hidden="true" /></a><button type="submit" class="tg-button tg-primary" :disabled="!canSave">{{ busy === 'config' ? '保存中' : '保存 API' }}</button></div>
        <p class="tg-help">凭据与登录会话加密保存。更换 API 凭据前，需先退出当前账号。</p>
      </form>
      <p class="tg-page-help"><IconShieldLock :size="16" aria-hidden="true" /><span>验证码与二步验证密码仅用于本次登录，不会保存。资源 Bot 与频道在<RouterLink to="/resources/telegram" class="text-blue-600 dark:text-blue-300">搜索配置</RouterLink>中管理。</span></p>
    </div>
    <AnimeFloatingDialog v-if="loginOpen" title="登录 Telegram" close-label="关闭 Telegram 登录" class="tg-login-dialog" @close="closeLogin">
      <div class="tg-login-content">
        <ol class="tg-login-steps" aria-label="登录步骤"><li v-for="(label, index) in ['手机号', '验证码', '二步验证']" :key="label" :class="{ 'is-current': stepNumber === index + 1, 'is-complete': stepNumber > index + 1 }" :aria-current="stepNumber === index + 1 ? 'step' : undefined"><span><IconCheck v-if="stepNumber > index + 1" :size="13" aria-hidden="true" /><template v-else>{{ index + 1 }}</template></span>{{ label }}</li></ol>
        <div v-if="!loginReady" class="tg-login-loading" :aria-busy="!!loginBusy"><template v-if="loginBusy"><IconRefresh :size="21" class="tg-spinning" aria-hidden="true" /><p>正在读取登录进度</p></template><template v-else><p v-if="loginError" role="alert" class="tg-message tg-error">{{ loginError }}</p><button type="button" class="tg-button" @click="loginAction('config')"><IconRefresh :size="15" aria-hidden="true" />重新读取</button></template></div>
        <form v-else id="tg-login-form" class="tg-login-form" :aria-busy="!!loginBusy" @submit.prevent="submitLogin">
          <div class="tg-login-heading"><h3>{{ step === 'phone' ? '输入手机号' : step === 'code' ? '输入登录验证码' : '输入二步验证密码' }}</h3><p id="tg-login-description">{{ step === 'phone' ? '包含国家或地区代码，例如 +8613800000000。' : step === 'code' ? (sentPhone ? `请查看 ${sentPhone} 的 Telegram 官方客户端或短信。` : '请查看上次登录手机号的 Telegram 官方客户端或短信。') : '此账号已启用二步验证，请输入 Telegram 密码。' }}</p></div>
          <label v-if="step === 'phone'" class="tg-login-field">手机号<input aria-describedby="tg-login-description" ref="phoneInput" v-model="phone" type="tel" autocomplete="tel" inputmode="tel" required pattern="\+[1-9][0-9]{6,14}" placeholder="+8613800000000" :disabled="!!loginBusy" /></label>
          <label v-else-if="step === 'code'" class="tg-login-field">验证码<input aria-describedby="tg-login-description" ref="codeInput" v-model="code" type="text" inputmode="numeric" autocomplete="one-time-code" spellcheck="false" required class="tg-code-input" placeholder="输入或粘贴验证码" :disabled="!!loginBusy" /></label>
          <label v-else class="tg-login-field">二步验证密码<input aria-describedby="tg-login-description" ref="passwordInput" v-model="password" type="password" autocomplete="off" required placeholder="输入密码" :disabled="!!loginBusy" /></label>
          <p v-if="loginError" role="alert" class="tg-message tg-error">{{ loginError }}</p><p v-if="loginNotice" role="status" class="tg-message tg-notice">{{ loginNotice }}</p>
          <div v-if="step === 'phone' && status?.hasSession" class="tg-existing-session"><span>已有保存的会话，可先尝试恢复。</span><button type="button" class="tg-text-button" :disabled="!!loginBusy" @click="loginAction('check')">{{ loginBusy === 'check' ? '检查中' : '检查并恢复' }}</button></div>
          <div class="tg-login-links"><button v-if="step !== 'phone'" type="button" class="tg-text-button" :disabled="!!loginBusy" @click="changePhone">修改手机号</button><button v-if="step === 'code' && sentPhone" type="button" class="tg-text-button" :disabled="!!loginBusy" @click="resendCode">{{ loginBusy === 'send-code' ? '发送中' : '重新发送验证码' }}</button><button v-if="step === 'phone' && pendingStep !== 'phone'" type="button" class="tg-text-button" :disabled="!!loginBusy" @click="continueStep">继续上次验证</button><span v-if="step === 'code' && !sentPhone" class="tg-help">需要重发时，请先确认手机号。</span></div>
        </form>
        <p class="tg-login-close-note">关闭后可重新打开，继续尚未完成的验证。</p>
      </div>
      <template #footer><div class="tg-login-footer"><button type="button" class="tg-button" @click="closeLogin">关闭</button><button v-if="loginReady" type="submit" form="tg-login-form" class="tg-button tg-primary" :disabled="!canSubmit">{{ loginBusy === 'send-code' ? '发送中' : loginBusy === 'sign-in' ? '验证中' : step === 'phone' ? '发送验证码' : step === 'code' ? '验证并继续' : '完成登录' }}<IconArrowRight :size="16" aria-hidden="true" /></button></div></template>
    </AnimeFloatingDialog>
    <P115ActionDialog v-if="confirmDisconnect" title="退出 Telegram 用户账号？" :busy="busy === 'disconnect'" @close="confirmDisconnect = false"><p>将结束本项目保存的用户登录会话。频道搜索与转发需要重新登录后使用，API 凭据和资源 Bot 配置会保留。</p><p v-if="disconnectError" role="alert" class="tg-message tg-error">{{ disconnectError }}</p><template #footer><button type="button" class="tg-button" :disabled="!!busy" @click="confirmDisconnect = false">保留登录</button><button type="button" class="tg-button tg-danger" :disabled="!!busy" @click="act('disconnect')">{{ busy === 'disconnect' ? '退出中' : '确认退出' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.tg-account-page { @apply text-slate-800 dark:text-slate-200; display: grid; gap: 16px; max-width: 920px; }
.tg-card { @apply border-slate-200/80 bg-white dark:border-slate-800 dark:bg-slate-900; min-width: 0; border-width: 1px; border-style: solid; border-radius: 20px; padding: 22px 24px; box-shadow: 0 5px 22px rgb(15 23 42 / .035), 0 1px 3px rgb(15 23 42 / .02); }
.tg-identity-card { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 18px 16px; }
.tg-identity { display: flex; align-items: center; gap: 12px; min-width: 0; flex: 1 1 270px; }
.tg-avatar { @apply bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300; flex: 0 0 40px; }
.tg-identity-text { min-width: 0; }.tg-name-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.tg-name-row h2 { min-width: 0; overflow-wrap: anywhere; font-size: 14px; font-weight: 600; }.tg-identity-text > p { @apply text-slate-500 dark:text-slate-400; margin-top: 5px; font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
.tg-state { @apply bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400; flex-shrink: 0; padding: 3px 7px; border-radius: 6px; font-size: 10px; line-height: 1.4; }.tg-state.is-connected { @apply bg-green-50 text-green-700 dark:bg-green-500/10 dark:text-green-300; }
.tg-account-actions { display: flex; flex-wrap: wrap; gap: 8px; }.tg-button { @apply bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200; display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 36px; padding: 8px 12px; border-radius: 10px; font-size: 12px; font-weight: 500; line-height: 1.5; transition: background-color .15s; }
.tg-button:hover:not(:disabled) { @apply bg-slate-200/70 dark:bg-slate-700; }.tg-button.tg-primary { background: #2563eb; color: #fff; }.tg-button.tg-primary:hover:not(:disabled) { background: #1d4ed8; }.tg-button.tg-exit { @apply bg-transparent text-slate-500 dark:text-slate-400; }.tg-button.tg-danger { background: #dc2626; color: #fff; }.tg-button.tg-danger:hover:not(:disabled) { background: #b91c1c; }.tg-button:disabled, .tg-text-button:disabled { opacity: .45; cursor: default; }.tg-button svg { flex-shrink: 0; }.tg-button:focus-visible, .tg-text-button:focus-visible, .tg-api-footer a:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
.tg-setup-hint { @apply text-slate-500 dark:text-slate-400; width: 100%; font-size: 12px; line-height: 1.7; }
.tg-card-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }.tg-card-heading h2 { font-size: 14px; font-weight: 600; }.tg-card-heading p { @apply text-slate-500 dark:text-slate-400; margin-top: 5px; font-size: 12px; line-height: 1.6; }.tg-saved { @apply text-slate-500 dark:text-slate-400; display: inline-flex; align-items: center; gap: 4px; padding-top: 2px; font-size: 11px; white-space: nowrap; }
.tg-api-fields { display: grid; grid-template-columns: minmax(100px, .75fr) minmax(0, 1.65fr); gap: 14px; margin-top: 20px; }.tg-api-fields label, .tg-login-field { display: grid; min-width: 0; gap: 7px; font-size: 12px; font-weight: 500; }
.tg-api-fields input, .tg-login-field input { @apply border-slate-200 bg-slate-50/80 text-slate-800 placeholder:text-slate-400 dark:border-slate-700 dark:bg-slate-950/60 dark:text-slate-100 dark:placeholder:text-slate-500; width: 100%; min-width: 0; min-height: 40px; padding: 9px 11px; border-width: 1px; border-style: solid; border-radius: 10px; outline: none; font-size: 13px; font-weight: 400; transition: border-color .15s, box-shadow .15s; }.tg-api-fields input:focus, .tg-login-field input:focus { border-color: #3b82f6; box-shadow: 0 0 0 3px rgb(59 130 246 / .1); }.tg-api-fields:disabled { opacity: .6; }.tg-api-fields input:disabled, .tg-login-field input:disabled { cursor: default; }
.tg-api-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 16px; }.tg-api-footer a { @apply text-blue-600 dark:text-blue-300; display: inline-flex; align-items: center; gap: 5px; font-size: 12px; }.tg-help, .tg-page-help { @apply text-slate-500 dark:text-slate-400; font-size: 11px; line-height: 1.8; }.tg-api-card > .tg-help { margin-top: 14px; }.tg-page-help { display: flex; align-items: flex-start; gap: 8px; padding: 0 4px; }.tg-page-help svg { flex-shrink: 0; margin-top: 2px; }
.tg-message { border-radius: 10px; padding: 11px 13px; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }.tg-error { @apply bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300; }.tg-notice { @apply bg-blue-50 text-blue-700 dark:bg-blue-500/10 dark:text-blue-300; }.tg-message .tg-text-button { margin-left: 10px; }.tg-text-button { @apply text-blue-600 dark:text-blue-300; min-height: 32px; font-size: 12px; border-radius: 5px; }.tg-text-button:hover:not(:disabled) { text-decoration: underline; }
:global(dialog.anime-floating.tg-login-dialog) { width: min(470px, calc(100vw - 24px)); border-radius: 22px; }.tg-login-content { padding-block: 8px 0; }.tg-login-steps { display: flex; gap: 8px; padding-bottom: 26px; }.tg-login-steps li { @apply text-slate-400 dark:text-slate-500; display: flex; flex: 1; align-items: center; gap: 7px; font-size: 11px; white-space: nowrap; }.tg-login-steps li > span { @apply bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400; display: grid; place-items: center; width: 23px; height: 23px; flex-shrink: 0; border-radius: 50%; font-size: 10px; }.tg-login-steps li.is-current { @apply text-blue-600 dark:text-blue-300; }.tg-login-steps li.is-current > span { background: #2563eb; color: #fff; }.tg-login-steps li.is-complete > span { @apply bg-blue-50 text-blue-600 dark:bg-blue-500/15 dark:text-blue-300; }
.tg-login-form { display: grid; gap: 18px; }.tg-login-heading h3 { font-size: 21px; font-weight: 600; letter-spacing: -.025em; }.tg-login-heading p { @apply text-slate-500 dark:text-slate-400; margin-top: 8px; font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }.tg-login-field input { min-height: 46px; }.tg-login-field .tg-code-input { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 19px; letter-spacing: .2em; }.tg-code-input::placeholder { font-family: inherit; font-size: 13px; letter-spacing: normal; }.tg-login-links { display: flex; align-items: center; flex-wrap: wrap; justify-content: space-between; gap: 8px; min-height: 32px; margin-top: -6px; }.tg-login-close-note { @apply text-slate-400 dark:text-slate-500; margin-top: 16px; font-size: 10px; line-height: 1.8; }.tg-login-footer { display: flex; justify-content: flex-end; gap: 8px; }.tg-login-loading { @apply text-slate-500 dark:text-slate-400; display: grid; min-height: 175px; align-content: center; justify-items: center; gap: 12px; font-size: 12px; }.tg-login-loading .tg-message { width: 100%; }.tg-existing-session { @apply bg-slate-50 text-slate-500 dark:bg-slate-800/60 dark:text-slate-400; display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 4px 10px; padding: 9px 11px; border-radius: 10px; font-size: 11px; }
.tg-spinning { animation: tg-spin 1s linear infinite; }@keyframes tg-spin { to { transform: rotate(360deg); } }@media(prefers-reduced-motion: reduce) { .tg-spinning { animation: none; } }
@media(max-width: 639px) { .tg-card { padding: 18px; border-radius: 18px; }.tg-identity { flex-basis: 100%; }.tg-account-actions { width: 100%; }.tg-account-actions .tg-button { flex: 1 1 auto; }.tg-button { min-height: 42px; }.tg-api-fields { gap: 11px; grid-template-columns: minmax(90px, .75fr) minmax(0, 1.35fr); margin-top: 17px; }.tg-api-fields input, .tg-login-field input { font-size: 16px; }.tg-api-fields input::placeholder { font-size: 12px; }.tg-login-links .tg-text-button { min-height: 40px; }.tg-login-steps { gap: 6px; padding-bottom: 22px; }.tg-login-steps li { gap: 5px; font-size: 10px; }.tg-login-footer .tg-button { min-height: 44px; }.tg-page-help { padding-inline: 1px; } }
</style>
