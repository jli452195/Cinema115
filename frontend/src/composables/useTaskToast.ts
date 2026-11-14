import { h } from 'vue'
import { IconLoader2 } from '@tabler/icons-vue'
import { toast } from 'vue-sonner'

export type TaskToastFinishType = 'success' | 'error' | 'warning' | 'info'

export interface TaskToastHandle {
  /** 更新进行中标题（用户手动关闭或 5s 自动收起后静默，不再弹出） */
  update: (title: string, nextDescription?: string) => void
  /** 销毁旧进度卡片并以新 ID 完整展示 6s 终态结果（仅触发一次，即使此前已隐藏也会提醒） */
  finish: (type: TaskToastFinishType, title: string, detail?: string, nextDescription?: string) => void
  /** 主动关闭当前进度卡片并标记为已隐藏 */
  dismiss: () => void
}

const PROGRESS_DURATION = 5000
const FINISH_DURATION = 6000
const TOAST_CLASSES = { description: 'task-resource-summary' }

/** 函数式图标组件，满足 vue-sonner 的 icon?: Component 类型签名 */
const SpinningIcon = () => h(IconLoader2, { class: 'animate-spin', size: 18 })

/**
 * 创建统一的任务进度通知控制器：
 * - 进行中使用普通 toast + 旋转函数组件图标，规避 Sonner 原生 loading 禁用关闭/滑动/计时的限制；
 * - 终态统一 dismiss 旧进度卡并以库自动生成的新 ID 创建终态卡，避免沿用旧卡剩余计时。
 */
export function createTaskToast(title: string, description?: string): TaskToastHandle {
  let currentDescription = description?.trim() || ''
  let hidden = false
  let finished = false

  const handleClose = () => {
    hidden = true
  }

  // 不传固定 id，由 vue-sonner 自动生成唯一进度 ID，避免多次重试或新旧实例冲突
  const progressId = toast(title, {
    description: currentDescription || undefined,
    icon: SpinningIcon,
    duration: PROGRESS_DURATION,
    classes: TOAST_CLASSES,
    onDismiss: handleClose,
    onAutoClose: handleClose,
  })

  function update(nextTitle: string, nextDescription?: string) {
    if (nextDescription !== undefined) {
      currentDescription = nextDescription.trim()
    }
    // 已关闭、已自动收起或已进入终态时静默，防止轮询复活已隐藏的提示
    if (hidden || finished) return

    toast(nextTitle, {
      id: progressId,
      description: currentDescription || undefined,
      icon: SpinningIcon,
      duration: PROGRESS_DURATION,
      classes: TOAST_CLASSES,
      onDismiss: handleClose,
      onAutoClose: handleClose,
    })
  }

  function finish(type: TaskToastFinishType, finishTitle: string, detail?: string, nextDescription?: string) {
    if (finished) return
    finished = true
    hidden = true

    if (nextDescription !== undefined) {
      currentDescription = nextDescription.trim()
    }

    // 先销毁旧进度卡，再以不带 id 的方式新建终态卡片，确保获得完整 6000ms 展示周期
    toast.dismiss(progressId)

    const finalDescription = [currentDescription, detail?.trim()].filter(Boolean).join(' · ')
    toast[type](finishTitle, {
      description: finalDescription || undefined,
      classes: TOAST_CLASSES,
      richColors: true,
      duration: FINISH_DURATION,
    })
  }

  function dismiss() {
    if (finished || hidden) return
    hidden = true
    toast.dismiss(progressId)
  }

  return { update, finish, dismiss }
}
