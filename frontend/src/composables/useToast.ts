import { readonly, ref } from 'vue'

export type ToastType = 'success' | 'error' | 'warning' | 'info'
interface ToastMessage { id: number; type: ToastType; message: string; duration: number }

const messages = ref<ToastMessage[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 0

function pause(id: number) {
  clearTimeout(timers.get(id))
  timers.delete(id)
}

function dismiss(id: number) {
  pause(id)
  messages.value = messages.value.filter(message => message.id !== id)
}

function resume(id: number) {
  pause(id)
  const message = messages.value.find(message => message.id === id)
  if (message && message.duration > 0) timers.set(id, setTimeout(() => dismiss(id), message.duration))
}

function show(message: string, type: ToastType = 'info', duration = type === 'error' ? 5000 : 3000) {
  if (!message.trim()) return
  const existing = messages.value.find(item => item.type === type && item.message === message)
  if (existing) { resume(existing.id); return existing.id }
  const id = ++nextId
  messages.value.push({ id, type, message, duration })
  if (messages.value.length > 3) dismiss(messages.value[0]!.id)
  resume(id)
  return id
}

function clear() {
  for (const message of messages.value) pause(message.id)
  messages.value = []
}

const toast = {
  messages: readonly(messages), show, dismiss, pause, resume, clear,
  success: (message: string, duration?: number) => show(message, 'success', duration),
  error: (message: string, duration?: number) => show(message, 'error', duration),
  warning: (message: string, duration?: number) => show(message, 'warning', duration),
  info: (message: string, duration?: number) => show(message, 'info', duration),
}

export function useToast() { return toast }
