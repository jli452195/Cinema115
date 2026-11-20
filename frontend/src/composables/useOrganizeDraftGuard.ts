import { onBeforeUnmount, shallowRef, watchPostEffect } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRouter, type RouteLocationNormalized } from 'vue-router'
import type { DismissDecision } from './useMobileSheet'

const discardPrompts = {
  close: {
    message: '关闭窗口会丢失未保存的整理改动。要放弃吗？',
    confirmLabel: '放弃并关闭',
  },
  navigate: {
    message: '离开页面会丢失未保存的整理改动。要放弃吗？',
    confirmLabel: '放弃并离开',
  },
  replace: {
    message: '整理其他资源会替换当前草稿。要继续吗？',
    confirmLabel: '放弃并继续',
  },
  download: {
    message: '普通云下载提交成功后，当前整理草稿将失效。要继续吗？',
    confirmLabel: '放弃并下载',
  },
}

export type ConfirmOrganizeAction = (reason: 'replace' | 'download') => boolean | Promise<boolean>

interface HistoryExit {
  from: string
  to: string
  delta: number
}

export function useOrganizeDraftGuard(hasDraft: () => boolean, isBlocked: () => boolean) {
  const router = useRouter()
  const history = router.options.history
  const prompt = shallowRef<(typeof discardPrompts)[keyof typeof discardPrompts]>()
  let resolveDiscard: ((confirmed: boolean) => void) | undefined
  let disposed = false
  let replay: HistoryExit | undefined
  let approved: HistoryExit | undefined

  function historyPosition() {
    const position = history.state.position
    return typeof position === 'number' ? position : undefined
  }
  let currentPosition = historyPosition()

  function confirmDiscard(reason: keyof typeof discardPrompts): boolean | Promise<boolean> {
    if (disposed || isBlocked() || prompt.value) return false
    if (!hasDraft()) return true
    return new Promise(resolve => {
      resolveDiscard = resolve
      prompt.value = discardPrompts[reason]
    })
  }

  function settleDiscard(confirmed: boolean) {
    const resolve = resolveDiscard
    resolveDiscard = undefined
    prompt.value = undefined
    resolve?.(confirmed && !isBlocked())
  }

  async function beforeClose(): Promise<DismissDecision> {
    return await confirmDiscard('close') ? 'dismiss' : 'blocked'
  }

  function matchesExit(to: RouteLocationNormalized, from: RouteLocationNormalized, exit: HistoryExit) {
    return from.fullPath === exit.from && (to.fullPath === exit.to || to.redirectedFrom?.fullPath === exit.to)
  }

  function replayHistoryExit() {
    if (!replay || history.location !== replay.from) return
    const exit = replay
    replay = undefined
    if (disposed || isBlocked() || router.currentRoute.value.fullPath !== exit.from) return
    approved = exit
    if (exit.delta) router.go(exit.delta)
    else void router.push(exit.to).catch(() => { approved = undefined })
  }

  function guard(to: RouteLocationNormalized, from: RouteLocationNormalized) {
    if (isBlocked() || prompt.value || replay) return false
    if (approved) {
      const isApproved = matchesExit(to, from, approved)
      approved = undefined
      if (isApproved) return true
    }
    if (!hasDraft() || to.path === from.path) return true
    if (history.location === from.fullPath) return confirmDiscard('navigate')

    // 浏览器先移动历史条目，立即取消，让路由恢复原地址后再处理用户选择。
    const targetPosition = historyPosition()
    const exit = {
      from: from.fullPath,
      to: to.fullPath,
      delta: targetPosition !== undefined && currentPosition !== undefined ? targetPosition - currentPosition : 0,
    }
    const decision = confirmDiscard('navigate')
    void Promise.resolve(decision).then(confirmed => {
      if (!confirmed || disposed || router.currentRoute.value.fullPath !== exit.from) return
      replay = exit
      replayHistoryExit()
    })
    return false
  }

  function beforeUnload(event: BeforeUnloadEvent) { event.preventDefault() }

  watchPostEffect(onCleanup => {
    if (!hasDraft() && !isBlocked()) return
    window.addEventListener('beforeunload', beforeUnload)
    onCleanup(() => window.removeEventListener('beforeunload', beforeUnload))
  })
  window.addEventListener('popstate', replayHistoryExit)
  const stopAfterEach = router.afterEach((to, from, failure) => {
    if (!failure) currentPosition = historyPosition()
    if (replay && !failure) replay = undefined
    if (approved && matchesExit(to, from, approved)) approved = undefined
  })
  onBeforeRouteLeave(guard)
  onBeforeRouteUpdate(guard)
  onBeforeUnmount(() => {
    disposed = true
    settleDiscard(false)
    window.removeEventListener('popstate', replayHistoryExit)
    stopAfterEach()
  })

  return { prompt, settleDiscard, beforeClose, confirmDiscard }
}
