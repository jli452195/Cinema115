import { computed, readonly, ref } from 'vue'

export interface PortalUser { id: string; username: string; role: 'admin' | 'user'; disabled: boolean; createdAt: string }
const token = ref(localStorage.getItem('admin_token') || '')
const user = ref<PortalUser>()
const error = ref('')
let pending: Promise<PortalUser | undefined> | undefined
let checkedAt = 0

export const portalToken = readonly(token)
export const portalUser = readonly(user)
export const portalAuthError = readonly(error)
export const isPortalAdmin = computed(() => user.value?.role === 'admin')
export const portalBrowseRoutes = new Set(['home', 'tv', 'movie', 'updates', 'search', 'person', 'collection', 'tv-detail', 'movie-detail', 'my-follows', 'notifications', 'personal-playback'])

export function clearPortalSession() {
  token.value = ''; user.value = undefined; error.value = ''; checkedAt = 0
  localStorage.removeItem('admin_token'); localStorage.removeItem('admin_username')
}
export function setPortalToken(value: string) {
  token.value = value; user.value = undefined; error.value = ''; checkedAt = 0
  localStorage.setItem('admin_token', value)
}
// 角色只采用服务器验证结果，不从本地存储或未经验证的 JWT 推断权限。
export async function ensurePortalSession(force = false, options: { background?: boolean } = {}): Promise<PortalUser | undefined> {
  if (!token.value) return undefined
  if (pending) return pending
  if (!force && user.value && Date.now() - checkedAt < 30000) return user.value
  const current = token.value
  const verifiedUser = user.value
  pending = (async () => {
    let transientFailure = true
    try {
      const response = await fetch('/api/auth/me', { headers: { Authorization: `Bearer ${current}` }, signal: AbortSignal.timeout(10000) })
      if (current !== token.value) return undefined
      if (response.status === 401) { clearPortalSession(); return undefined }
      transientFailure = response.status >= 500 || response.status === 408 || response.status === 429
      const data = await response.json().catch(() => null)
      if (current !== token.value) return undefined
      if (!response.ok || !data?.user || !['admin', 'user'].includes(data.user.role)) throw new Error('登录状态验证失败，请重试')
      user.value = data.user; checkedAt = Date.now(); error.value = ''
      return user.value
    } catch (e) {
      // A failed background probe must not unmount an already verified page.
      // Invalid credentials and non-transient responses still fail closed.
      if (current === token.value && !(options.background && verifiedUser && user.value === verifiedUser && transientFailure)) {
        error.value = e instanceof Error && e.name === 'TimeoutError' ? '登录状态验证超时，请重试' : e instanceof Error ? e.message : '无法验证登录状态'
      }
      return undefined
    } finally { pending = undefined }
  })()
  return pending
}
export function catalogAPI(path: string) {
  return `${isPortalAdmin.value ? '/api/admin' : '/api/catalog'}/${path}`
}
