import { readonly, ref, watch } from 'vue'
import { websiteToken } from './useWebsiteAuth'

const unread = ref(0)
export const websiteUnread = readonly(unread)
watch(websiteToken, () => { unread.value = 0 })

export interface WebsiteNotification { id: number; kind: 'movie' | 'tv'; tmdbId: number; title: string; message: string; createdAt: number; readAt: number }
export async function notificationRequest(path: string, signal: AbortSignal, body?: { id: number; all: boolean }) {
  const token = websiteToken.value
  const response = await fetch(`/api/me/notifications${path}`, {
    method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${token}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]), ...(body ? { body: JSON.stringify(body) } : {}),
  })
  const data = await response.json().catch(() => null)
  if (signal.aborted || token !== websiteToken.value) throw new Error('登录状态已改变')
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || '通知读取失败，请重试')
  if (typeof data.unread === 'number') unread.value = data.unread
  return data
}
export async function refreshWebsiteUnread(signal: AbortSignal) {
  if (!websiteToken.value || document.hidden) return
  try { await notificationRequest('/unread', signal) } catch { /* 临时请求失败保留已有计数，通知页提供重试入口。 */ }
}
