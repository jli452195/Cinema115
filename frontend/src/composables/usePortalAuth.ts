import { computed, readonly, ref } from 'vue'

export interface PortalUser { id: string; username: string; role: 'admin' | 'user'; disabled: boolean; createdAt: string }
const token = ref(localStorage.getItem('admin_token') || '')
const user = ref<PortalUser>()
const error = ref('')
let pending: { token: string; promise: Promise<PortalUser | undefined> } | undefined
let checkedAt = 0

export const portalToken = readonly(token)
export const portalUser = readonly(user)
export const portalAuthError = readonly(error)
export const isPortalAdmin = computed(() => user.value?.role === 'admin')
export const portalBrowseRoutes = new Set(['home', 'tv', 'movie', 'updates', 'search', 'person', 'collection', 'tv-detail', 'movie-detail', 'my-follows', 'notifications', 'personal-playback'])

function adoptSession(value: string, verifiedUser?: PortalUser) {
  token.value = value; user.value = verifiedUser; error.value = ''
  checkedAt = verifiedUser ? Date.now() : 0; pending = undefined
}
function syncPortalSession() {
  const stored = localStorage.getItem('admin_token') || ''
  // Verify a replacement before exposing it to the mounted page. A same-user
  // renewal in another tab must not unmount forms or erase unsaved edits.
  if (!stored && token.value) adoptSession('')
  return stored
}
export function clearPortalSession(expectedToken?: string) {
  // A late 401 from another tab must not erase a newer login or renewal.
  if (expectedToken !== undefined && syncPortalSession() !== expectedToken) return
  localStorage.removeItem('admin_token')
  adoptSession('')
}
export function setPortalToken(value: string) {
  localStorage.setItem('admin_token', value)
  adoptSession(value)
}
// 角色只采用服务器验证结果，不从本地存储或未经验证的 JWT 推断权限。
export async function ensurePortalSession(force = false, options: { background?: boolean } = {}): Promise<PortalUser | undefined> {
  const current = syncPortalSession()
  if (!current) return undefined
  if (pending?.token === current) return pending.promise
  if (!force && current === token.value && user.value && Date.now() - checkedAt < 30000) return user.value
  const verifiedUser = user.value
  const currentSession = () => syncPortalSession() === current
  const latestSession = () => ensurePortalSession(force, options)
  const operation = (async () => {
    let transientFailure = true
    try {
      const response = await fetch('/api/auth/me', { headers: { Authorization: `Bearer ${current}` }, signal: AbortSignal.timeout(10000) })
      if (!currentSession()) return latestSession()
      if (response.status === 401) { clearPortalSession(current); return latestSession() }
      transientFailure = response.status >= 500 || response.status === 408 || response.status === 429
      const data = await response.json().catch(() => null)
      if (!currentSession()) return latestSession()
      if (!response.ok || !data?.user || !['admin', 'user'].includes(data.user.role)) throw new Error('登录状态验证失败，请重试')
      const nextToken = typeof data.token === 'string' && data.token ? data.token : current
      if (nextToken !== current) localStorage.setItem('admin_token', nextToken)
      adoptSession(nextToken, data.user)
      return user.value
    } catch (e) {
      // A failed background probe must not unmount an already verified page.
      // Invalid credentials and non-transient responses still fail closed.
      if (!currentSession()) return latestSession()
      if (!(options.background && verifiedUser && user.value === verifiedUser && transientFailure)) {
        error.value = e instanceof Error && e.name === 'TimeoutError' ? '登录状态验证超时，请重试' : e instanceof Error ? e.message : '无法验证登录状态'
      }
      return undefined
    }
  })()
  pending = { token: current, promise: operation }
  try { return await operation }
  finally { if (pending?.promise === operation) pending = undefined }
}
export function catalogAPI(path: string) {
  return `${isPortalAdmin.value ? '/api/admin' : '/api/catalog'}/${path}`
}
