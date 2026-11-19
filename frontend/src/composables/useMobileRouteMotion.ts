import { nextTick, onBeforeUnmount, onMounted, type Ref } from 'vue'
import type { RouteLocationNormalized, Router } from 'vue-router'
import { mobileWorkspaceSection } from '../utils/mobileWorkspace'

type ToolGroup = (route: RouteLocationNormalized) => string | undefined

function isMobileOverlay(route: RouteLocationNormalized) {
  const overlay = route.meta.mobileOverlay
  return typeof overlay === 'function' ? overlay(route) : overlay === true
}

function routeMotion(to: RouteLocationNormalized, from: RouteLocationNormalized, toolGroup: ToolGroup) {
  if (to.meta.public || from.meta.public) return

  // 弹层进出时由弹层自己播放动画，共用的正文保持原位。
  const page = to.matched.at(-1)?.components?.default
  if (page && page === from.matched.at(-1)?.components?.default && (isMobileOverlay(to) || isMobileOverlay(from))) return

  const nextTool = toolGroup(to)
  const previousTool = toolGroup(from)
  if (!nextTool && !previousTool) return
  if (!nextTool && !mobileWorkspaceSection(to.name)) return
  if (!previousTool && !mobileWorkspaceSection(from.name)) return

  if (!previousTool || (nextTool === previousTool && from.path === previousTool)) return 'push'
  if (!nextTool || (nextTool === previousTool && to.path === nextTool)) return 'pop'
  return 'switch'
}

export function useMobileRouteMotion(router: Router, workspace: Ref<HTMLElement | undefined>, toolGroup: ToolGroup) {
  let stop: (() => void) | undefined
  let mobile: MediaQueryList | undefined
  let reduced: MediaQueryList | undefined
  let clearMotion: (() => void) | undefined
  let navigation = 0

  function cancel() {
    navigation++
    clearMotion?.()
  }

  onMounted(() => {
    mobile = window.matchMedia('(max-width: 639px)')
    reduced = window.matchMedia('(prefers-reduced-motion: reduce)')
    mobile.addEventListener('change', cancel)
    reduced.addEventListener('change', cancel)
    document.addEventListener('visibilitychange', cancel)

    stop = router.afterEach(async (to, from, failure) => {
      if (failure || to.path === from.path) return
      cancel()
      const direction = routeMotion(to, from, toolGroup)
      if (!direction || !mobile?.matches || reduced?.matches || document.hidden) return

      const currentNavigation = navigation
      await nextTick()
      if (navigation !== currentNavigation || router.currentRoute.value.path !== to.path) return

      // 只动当前正文。导航栏和浮层继续固定，不等待动画即可点击。
      const content = workspace.value?.querySelector<HTMLElement>('main.workbench-content')
      if (!content || typeof content.animate !== 'function') return
      const frames = direction === 'switch'
        ? [{ opacity: 0.84 }, { opacity: 1 }]
        : [{ translate: direction === 'push' ? 'min(24px, 6vw) 0' : '-16px 0', opacity: 0.88 }, { translate: '0 0', opacity: 1 }]
      const current = content.animate(frames, {
        duration: direction === 'switch' ? 140 : 190,
        easing: 'cubic-bezier(0.32, 0.72, 0, 1)',
      })
      const clear = () => {
        if (clearMotion !== clear) return
        clearMotion = undefined
        content.removeEventListener('click', cancel, true)
        content.removeEventListener('keydown', cancel, true)
        current.onfinish = null
        current.cancel()
      }
      clearMotion = clear
      current.onfinish = clear
      // 控件定位浮层前恢复正文坐标；等点击命中后处理，避免移动抬手目标。
      content.addEventListener('click', cancel, true)
      content.addEventListener('keydown', cancel, true)
    })
  })

  onBeforeUnmount(() => {
    stop?.()
    cancel()
    mobile?.removeEventListener('change', cancel)
    reduced?.removeEventListener('change', cancel)
    document.removeEventListener('visibilitychange', cancel)
  })
}
