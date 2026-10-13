<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import LoginLayout from '../components/LoginLayout.vue'
import LoginForm from '../components/LoginForm.vue'
import AuthPasswordField from '../components/AuthPasswordField.vue'
import AuthSpinner from '../components/AuthSpinner.vue'
import { AuthRequestError, useAuthRequest } from '../composables/useAuthRequest'

type RegistrationInput = { username: string; password: string; code: string }
const emit = defineEmits<{ registered: [username: string]; loggedIn: [token: string] }>()
const route = useRoute(), router = useRouter()
const code = ref(new URLSearchParams(route.hash.slice(1)).get('invite') || '')
const username = ref(''), password = ref(''), confirmation = ref('')
const source = ref<'personal' | 'assigned' | ''>('')
const checkedCode = ref('')
const inviteError = ref(''), message = ref('')
const usernameError = ref(''), passwordError = ref(''), confirmationError = ref('')
const checking = ref(false), registering = ref(false), tryingLogin = ref(false)
const pendingInput = ref<RegistrationInput | null>(null)
const { request, retrySeconds, isActive } = useAuthRequest()
const verified = computed(() => !!source.value && checkedCode.value === code.value.trim())
const busy = computed(() => checking.value || registering.value)
let checkGeneration = 0

watch(code, () => {
  checkGeneration++
  source.value = ''
  checkedCode.value = ''
  inviteError.value = ''
  message.value = ''
}, { flush: 'sync' })
watch(username, () => { usernameError.value = '' })
watch(password, () => { passwordError.value = ''; confirmationError.value = '' })
watch(confirmation, () => { confirmationError.value = '' })

async function check() {
  if (busy.value || retrySeconds.value || pendingInput.value || !code.value.trim()) return
  const submittedCode = code.value.trim()
  const generation = ++checkGeneration
  checking.value = true
  inviteError.value = ''
  try {
    const data = await request<{ valid?: boolean; playbackSource?: string }>('/api/auth/invite/check', { code: submittedCode })
    if (!isActive() || generation !== checkGeneration || submittedCode !== code.value.trim()) return
    if (data?.valid !== true || !['personal', 'assigned'].includes(data.playbackSource || '')) throw new Error('邀请码验证响应异常，请重试')
    source.value = data.playbackSource as 'personal' | 'assigned'
    checkedCode.value = submittedCode
  } catch (error) {
    if (isActive() && generation === checkGeneration) inviteError.value = error instanceof Error && !['TypeError', 'TimeoutError'].includes(error.name) ? error.message : '暂时无法验证邀请码，请检查网络后重试'
  } finally { checking.value = false }
}

function changeInvite() {
  if (busy.value || pendingInput.value) return
  resetInvite()
}

function resetInvite() {
  source.value = ''
  checkedCode.value = ''
  inviteError.value = ''
  message.value = ''
}

function validate() {
  const name = username.value.trim()
  usernameError.value = /^[\p{L}\p{N}][\p{L}\p{N}_.-]{2,31}$/u.test(name) && !name.toLowerCase().startsWith('c115_') ? '' : '请输入 3 至 32 位用户名，以字母、汉字或数字开头；c115_ 为保留前缀'
  passwordError.value = password.value.length >= 8 && new TextEncoder().encode(password.value).length <= 128 ? '' : '密码至少 8 位；密码过长时请缩短后重试'
  confirmationError.value = password.value === confirmation.value ? '' : '两次输入的密码不一致，请重新确认'
  return !usernameError.value && !passwordError.value && !confirmationError.value
}

// The existing API returns messages rather than error codes. Only known
// preflight rejections may unlock the identity; all other failures retain the
// original input so an uncertain external creation cannot become a new one.
function rejectedBeforeCreation(error: unknown) {
  if (!(error instanceof AuthRequestError)) return false
  if (error.status === 429) return true
  return error.status === 400 && ['用户名需为', 'Emby 用户名已存在', '邀请码无效、已过期', '尚未配置新注册用户的默认媒体库', '请先配置并验证 Emby 服务', '无法检查 Emby 用户名', 'Emby 服务配置已变化'].some(text => error.message.includes(text))
}

async function submit() {
  if (busy.value || retrySeconds.value) return
  if (!verified.value && !pendingInput.value) { await check(); return }
  if (!pendingInput.value && !validate()) return
  const resuming = !!pendingInput.value
  const input = pendingInput.value || { username: username.value.trim(), password: password.value, code: checkedCode.value }
  registering.value = true
  message.value = ''
  try {
    const data = await request<{ user?: { username?: string } }>('/api/auth/register', input, 95000)
    if (typeof data?.user?.username !== 'string' || data.user.username !== input.username) throw new Error('暂未收到有效的账号创建结果')
    if (isActive()) emit('registered', data.user.username)
  } catch (error) {
    if (!isActive()) return
    pendingInput.value = !resuming && rejectedBeforeCreation(error) ? null : input
    if (!pendingInput.value && error instanceof AuthRequestError && error.message.includes('邀请码无效、已过期')) {
      resetInvite()
      inviteError.value = error.message
    } else {
      message.value = error instanceof Error && !['TypeError', 'TimeoutError'].includes(error.name) ? error.message : '连接中断或等待超时，账号创建结果尚未确认'
    }
  } finally { registering.value = false }
}

onMounted(async () => {
  // Invitation credentials live only in this component, never URL or storage.
  if (route.hash) await router.replace({ path: '/register', query: route.query, hash: '' })
  if (isActive() && code.value) await check()
})
</script>

<template>
  <LoginLayout subtitle="发现影视，管理你的观影生活">
    <LoginForm v-if="tryingLogin" endpoint="/api/auth/login" title="尝试登录" description="使用刚提交的 Emby 账号确认是否已创建成功" username-label="Emby 用户名" submit-label="登录个人中心" :initial-username="pendingInput?.username" :initial-password="pendingInput?.password" username-readonly password-readonly @logged-in="emit('loggedIn', $event)">
      <template #before-fields>
        <p role="status" class="auth-notice auth-notice-warning">若暂时无法登录，可返回继续确认注册。</p>
      </template>
      <div class="auth-bottom-link"><button type="button" class="auth-link" @click="tryingLogin = false">返回继续确认注册</button></div>
    </LoginForm>
    <form v-else class="auth-card" :aria-busy="busy" @submit.prevent="submit">
      <div class="auth-heading">
        <h2>创建 Emby 账号</h2>
        <p>使用管理员发给你的邀请码注册，同一账号用于登录个人中心和 Emby 客户端。</p>
      </div>
      <div class="auth-field">
        <div class="auth-label-row">
          <label for="register-invite">邀请码</label>
          <button v-if="verified" type="button" class="auth-link" :disabled="busy || !!pendingInput" @click="changeInvite">更换邀请码</button>
        </div>
        <input id="register-invite" v-model="code" name="invite" required maxlength="64" autocomplete="off" autocapitalize="none" autocorrect="off" :spellcheck="false" class="auth-input auth-invite-input" placeholder="粘贴管理员提供的邀请码" :readonly="verified || !!pendingInput" :disabled="busy" :aria-invalid="inviteError ? true : undefined" :aria-describedby="inviteError ? 'register-invite-error' : undefined" />
        <p v-if="inviteError" id="register-invite-error" role="alert" class="auth-field-error">{{ inviteError }}</p>
      </div>
      <div v-if="verified" role="status" class="auth-notice auth-notice-success auth-invite-summary">
        <strong>{{ source === 'personal' ? '邀请码有效：自备个人网盘' : '邀请码有效：无需自备网盘' }}</strong>
        <p>{{ source === 'personal' ? '注册后可浏览，播放前需在个人中心绑定自己的 115 网盘。' : '播放所需网盘由管理员提供，服务繁忙或暂不可用时需等待。' }}</p>
      </div>
      <p v-if="verified" class="auth-hint">实际播放以 Emby 授权和网盘可用状态为准。</p>
      <fieldset v-if="verified || pendingInput" class="auth-fields auth-register-fields" :disabled="registering">
        <div class="auth-field">
          <label for="register-username">Emby 用户名</label>
          <input id="register-username" v-model="username" name="username" required minlength="3" maxlength="32" autocomplete="username" autocapitalize="none" autocorrect="off" :spellcheck="false" class="auth-input" placeholder="设置 Emby 用户名" :readonly="!!pendingInput" :aria-invalid="usernameError ? true : undefined" :aria-describedby="`register-username-hint${usernameError ? ' register-username-error' : ''}`" />
          <p id="register-username-hint" class="auth-hint">3 至 32 位，支持字母、汉字、数字、下划线、点和短横线，以字母、汉字或数字开头。</p>
          <p v-if="usernameError" id="register-username-error" role="alert" class="auth-field-error">{{ usernameError }}</p>
        </div>
        <AuthPasswordField id="register-password" v-model="password" label="设置密码" autocomplete="new-password" placeholder="至少 8 位" :minlength="8" :readonly="!!pendingInput" :error="passwordError" />
        <AuthPasswordField id="register-confirmation" v-model="confirmation" label="确认密码" autocomplete="new-password" placeholder="再次输入密码" :minlength="8" :readonly="!!pendingInput" :error="confirmationError" />
      </fieldset>
      <div v-if="pendingInput" role="alert" class="auth-notice auth-notice-warning !mb-0 mt-6">
        <strong>尚未确认创建结果</strong>
        <p>{{ message }}</p>
        <p>请先尝试登录；若无法登录，使用原邀请码、用户名和密码继续确认注册。</p>
      </div>
      <p v-else-if="message" role="alert" class="auth-notice auth-notice-error !mb-0 mt-6">{{ message }}</p>
      <div class="auth-actions">
        <button type="submit" class="auth-button" :disabled="busy || retrySeconds > 0 || (!verified && !pendingInput && !code.trim())">
          <AuthSpinner v-if="busy" />{{ checking ? '正在验证邀请码' : registering ? '正在创建 Emby 账号' : pendingInput ? '继续确认注册' : verified ? '创建 Emby 账号' : '验证邀请码' }}
        </button>
        <button v-if="pendingInput" type="button" class="auth-button auth-button-secondary" :disabled="busy" @click="tryingLogin = true">尝试登录</button>
      </div>
      <p v-if="retrySeconds" role="status" class="auth-cooldown">请在 {{ retrySeconds }} 秒后重试</p>
      <div v-if="!pendingInput" class="auth-bottom-link"><RouterLink :to="{ path: '/login', query: route.query }" class="auth-link">已有 Emby 账号？返回登录</RouterLink></div>
    </form>
    <template #footer>
      <RouterLink :to="{ path: '/admin/login', query: route.query }" class="auth-link">管理员登录</RouterLink>
    </template>
  </LoginLayout>
</template>
