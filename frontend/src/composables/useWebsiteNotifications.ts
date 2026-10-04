import { onBeforeUnmount, onMounted, readonly, ref, watch } from 'vue'
import { portalToken } from './usePortalAuth'

const unread = ref(0)
export const websiteUnread = readonly(unread)
let countEpoch = 0, sequence = 0, appliedSequence = 0, pendingReads = 0
let readQueue: Promise<unknown> = Promise.resolve()
watch(portalToken, () => { unread.value = 0; countEpoch++ }, { flush: 'sync' })

// 跨标签页只广播失效信号，不传用户凭据、通知内容或其他用户的计数。
let channel: BroadcastChannel | undefined
const listeners = new Set<() => void>()
export function useWebsiteNotificationSync(refresh: () => void) {
  const visible = () => { if (!document.hidden) refresh() }
  onMounted(() => {
    listeners.add(refresh)
    if (!channel && typeof BroadcastChannel !== 'undefined') {
      channel = new BroadcastChannel('cinema115-notifications')
      channel.onmessage = () => { countEpoch++; for (const listener of listeners) listener() }
    }
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', visible)
  })
  onBeforeUnmount(() => {
    window.removeEventListener('focus', refresh)
    document.removeEventListener('visibilitychange', visible)
    listeners.delete(refresh)
    if (!listeners.size) { channel?.close(); channel = undefined }
  })
}

export interface WebsiteNotification { id: number; kind: 'movie' | 'tv'; tmdbId: number; title: string; message: string; createdAt: number; readAt: number }
export type NotificationRead = { id: number; all: boolean } | { ids: number[] }
export async function notificationRequest(path: string, signal: AbortSignal, body?: NotificationRead) {
  const token = portalToken.value
  const epoch = countEpoch, requestSequence = ++sequence
  const response = await fetch(`/api/me/notifications${path}`, {
    method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${token}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]), ...(body ? { body: JSON.stringify(body) } : {}),
  })
  const data = await response.json().catch(() => null)
  if (signal.aborted || token !== portalToken.value) throw new Error('登录状态已改变')
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || '通知读取失败，请重试')
  if (typeof data.unread === 'number' && epoch === countEpoch && requestSequence >= appliedSequence && (body || pendingReads === 0)) {
    unread.value = data.unread
    appliedSequence = requestSequence
  }
  return data
}

export function readWebsiteNotifications(body: NotificationRead, signal: AbortSignal) {
  const token = portalToken.value
  const task = readQueue.then(async () => {
    if (signal.aborted || token !== portalToken.value) throw new Error('登录状态已改变')
    const epoch = ++countEpoch
    pendingReads++
    try {
      const data = await notificationRequest('/read', signal, body)
      channel?.postMessage('changed')
      return data
    } finally {
      const invalidated = epoch !== countEpoch
      pendingReads--; countEpoch++
      if (invalidated && token === portalToken.value && !signal.aborted) void refreshWebsiteUnread(signal)
    }
  })
  readQueue = task.catch(() => {})
  return task
}
export async function refreshWebsiteUnread(signal: AbortSignal) {
  if (!portalToken.value || document.hidden) return
  try { await notificationRequest('/unread', signal) } catch { /* 临时请求失败保留已有计数，通知页提供重试入口。 */ }
}
