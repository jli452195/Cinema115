import { toast as sonner } from 'vue-sonner'

export type ToastType = 'success' | 'error' | 'warning' | 'info'
export interface ToastOptions {
  title?: string
  duration?: number
  key?: string
  action?: { label: string; onClick: (event: MouseEvent) => void }
}

const labels = { success: '操作成功', error: '操作失败', warning: '请注意', info: '提示' }
const active = new Map<string, number | string>()

function show(message: string, type: ToastType = 'info', options?: number | ToastOptions) {
  message = message.trim()
  if (!message) return
  const settings = typeof options === 'number' ? { duration: options } : options
  const action = settings?.action
  const title = settings?.title?.trim() || labels[type]
  const requestedDuration = settings?.duration
  const duration = typeof requestedDuration === 'number' && Number.isFinite(requestedDuration)
    ? Math.max(0, requestedDuration) || Infinity
    : type === 'error' ? 5000 : 3000
  const key = JSON.stringify([type, title, message, settings?.key, action?.label])
  const previous = active.get(key)
  // 组件不会重置同 ID 的剩余时长，替换通知以重新开始倒计时。
  if (previous !== undefined) sonner.dismiss(previous)
  const release = () => { if (active.get(key) === id) active.delete(key) }
  const id = sonner[type](title, {
    description: message, duration,
    action: action && {
      label: action.label,
      onClick: (event: MouseEvent) => {
        action.onClick(event)
        // 操作按钮关闭通知时不会触发 onDismiss，需同步释放去重记录。
        if (!event.defaultPrevented) release()
      },
    },
    onDismiss: release, onAutoClose: release,
  })
  active.set(key, id)
  return id
}

function dismiss(id: number | string) {
  for (const [key, value] of active) if (value === id) active.delete(key)
  sonner.dismiss(id)
}

function clear() {
  active.clear()
  sonner.dismiss()
}

const toast = {
  show, dismiss, clear,
  success: (message: string, options?: number | ToastOptions) => show(message, 'success', options),
  error: (message: string, options?: number | ToastOptions) => show(message, 'error', options),
  warning: (message: string, options?: number | ToastOptions) => show(message, 'warning', options),
  info: (message: string, options?: number | ToastOptions) => show(message, 'info', options),
}

export function useToast() { return toast }
