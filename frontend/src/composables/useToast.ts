import { readonly, ref } from 'vue'

export type ToastType = 'success' | 'error' | 'warning' | 'info'
export interface ToastOptions { title?: string; duration?: number }
interface ToastMessage { id: number; type: ToastType; title: string; message: string; duration: number }
type PauseReason = 'hover' | 'focus'
interface Countdown { timer?: ReturnType<typeof setTimeout>; remaining: number; deadline: number; paused: Set<PauseReason> }

const messages = ref<ToastMessage[]>([])
const countdowns = new Map<number, Countdown>()
let nextId = 0

function dismiss(id: number) {
  clearTimeout(countdowns.get(id)?.timer)
  countdowns.delete(id)
  messages.value = messages.value.filter(message => message.id !== id)
}

function schedule(id: number, countdown: Countdown) {
  if (countdown.paused.size || countdown.timer !== undefined || countdown.remaining <= 0) return
  countdown.deadline = Date.now() + countdown.remaining
  countdown.timer = setTimeout(() => dismiss(id), countdown.remaining)
}

function pause(id: number, reason: PauseReason = 'hover') {
  const countdown = countdowns.get(id)
  if (!countdown || countdown.paused.has(reason)) return
  countdown.paused.add(reason)
  if (countdown.timer !== undefined) {
    clearTimeout(countdown.timer)
    countdown.timer = undefined
    countdown.remaining = Math.max(1, countdown.deadline - Date.now())
  }
}

function resume(id: number, reason: PauseReason = 'hover') {
  const countdown = countdowns.get(id)
  if (!countdown) return
  countdown.paused.delete(reason)
  schedule(id, countdown)
}

// Existing calls such as toast.success(message, 3000) remain valid.
// Pass { title, duration } for a separate heading; duration: 0 keeps it open.
function show(message: string, type: ToastType = 'info', options?: number | ToastOptions) {
  message = message.trim()
  if (!message) return
  const settings = typeof options === 'number' ? { duration: options } : options
  const title = settings?.title?.trim() || ''
  const fallback = type === 'error' ? 5000 : 3000
  const duration = typeof settings?.duration === 'number' && Number.isFinite(settings.duration) ? Math.max(0, settings.duration) : fallback
  const existing = messages.value.find(item => item.type === type && item.message === message && item.title === title)
  if (existing) {
    const countdown = countdowns.get(existing.id)!
    clearTimeout(countdown.timer)
    countdown.timer = undefined
    countdown.remaining = existing.duration = duration
    schedule(existing.id, countdown)
    return existing.id
  }
  const id = ++nextId
  const countdown: Countdown = { remaining: duration, deadline: 0, paused: new Set() }
  countdowns.set(id, countdown)
  messages.value.push({ id, type, title, message, duration })
  if (messages.value.length > 3) dismiss(messages.value[0]!.id)
  schedule(id, countdown)
  return id
}

function clear() {
  for (const countdown of countdowns.values()) clearTimeout(countdown.timer)
  countdowns.clear()
  messages.value = []
}

const toast = {
  messages: readonly(messages), show, dismiss, pause, resume, clear,
  success: (message: string, options?: number | ToastOptions) => show(message, 'success', options),
  error: (message: string, options?: number | ToastOptions) => show(message, 'error', options),
  warning: (message: string, options?: number | ToastOptions) => show(message, 'warning', options),
  info: (message: string, options?: number | ToastOptions) => show(message, 'info', options),
}

export function useToast() { return toast }
