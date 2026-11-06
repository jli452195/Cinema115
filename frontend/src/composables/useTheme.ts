import { computed, nextTick, ref, watch } from 'vue'
import { prepareThemeTransitionGeometry } from '../utils/themeTransitionGeometry'
import { syncBrowserTheme, waitForThemePaint } from '../utils/browserTheme'

export type ThemeMode = 'light' | 'dark' | 'system'
export interface ThemeTransitionOrigin { x: number; y: number }

const currentTheme = ref<ThemeMode>((localStorage.getItem('cinema115-theme') as ThemeMode) || 'system')
const systemDark = ref(window.matchMedia('(prefers-color-scheme: dark)').matches)
const resolvedTheme = computed(() => currentTheme.value === 'system' ? systemDark.value ? 'dark' : 'light' : currentTheme.value)
let isInitialized = false
let activeTransition: ViewTransition | undefined
let themeRequest = 0
let deferBrowserTheme = false

function transitionOrigin(event?: MouseEvent | ThemeTransitionOrigin): ThemeTransitionOrigin {
  if (event && !('clientX' in event)) return event
  // 菜单关闭后按钮可能卸载，必须在任何异步等待之前保存坐标。
  const button = event?.currentTarget instanceof HTMLElement ? event.currentTarget.getBoundingClientRect() : undefined
  return {
    x: button ? button.left + button.width / 2 : event && event.detail > 0 ? event.clientX : window.innerWidth / 2,
    y: button ? button.top + button.height / 2 : event && event.detail > 0 ? event.clientY : window.innerHeight / 2,
  }
}

async function setTheme(theme: ThemeMode, event?: MouseEvent | ThemeTransitionOrigin, beforeChange?: () => void | Promise<void>) {
  const request = ++themeRequest
  const origin = transitionOrigin(event)
  const previous = activeTransition
  if (previous) {
    previous.skipTransition()
    // 先完成旧快照的更新与清理，避免快速点击时两次转场交叉。
    await previous.finished.catch(() => {})
  }
  if (request !== themeRequest) return
  let prepared = true
  try {
    await beforeChange?.()
  } catch {
    prepared = false
  }
  if (request !== themeRequest) return
  const root = document.documentElement
  const dark = theme === 'dark' || (theme === 'system' && systemDark.value)
  const apply = async () => {
    if (request === themeRequest) currentTheme.value = theme
    await nextTick()
  }
  if (!prepared || dark === root.classList.contains('dark') || !document.startViewTransition || window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    await apply()
    // 上一次转场可能因新请求跳过同步；即使目标主题相同也需补齐系统栏。
    if (request === themeRequest) syncBrowserTheme()
    return
  }

  const x = Math.max(0, Math.min(origin.x, window.innerWidth))
  const y = Math.max(0, Math.min(origin.y, window.innerHeight))
  const softMask = typeof CSS.registerProperty === 'function'
    && CSS.supports('mask-image', 'radial-gradient(circle 1px at 0px 0px, #000 60%, transparent 100%)')
  const farthestCorner = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y))
  // 渐变前 60% 为实色，末帧需让实色区域覆盖最远角落。
  const radius = softMask ? (farthestCorner + 1) / 0.6 : farthestCorner
  root.style.setProperty('--theme-reveal-origin', `${x}px ${y}px`)
  root.style.setProperty('--theme-reveal-radius', `${radius}px`)
  root.dataset.themeReveal = dark ? 'expand' : 'contract'
  root.dataset.themeRevealMask = softMask ? 'soft' : 'clip'
  root.classList.add('theme-reveal')
  let transition: ViewTransition | undefined
  let geometry: ReturnType<typeof prepareThemeTransitionGeometry> | undefined
  try {
    // 登录页与站内共用整页快照，单独排查点阵、表单分层引起的交接闪动。
    geometry = prepareThemeTransitionGeometry({ x, y }, softMask)
    deferBrowserTheme = true
    transition = document.startViewTransition(apply)
    activeTransition = transition
    // 跳过快照会使 ready 拒绝，但仍需完成主题更新。
    void transition.ready.then(() => geometry?.align()).catch(() => {})
    await transition.finished
  } catch {
    if (!transition || activeTransition === transition) await apply()
  } finally {
    geometry?.cleanup()
    if (!transition || activeTransition === transition) {
      activeTransition = undefined
      root.classList.remove('theme-reveal')
      delete root.dataset.themeReveal
      delete root.dataset.themeRevealMask
      root.style.removeProperty('--theme-reveal-origin')
      root.style.removeProperty('--theme-reveal-radius')
      // 先让清理后的页面获得绘制机会，避免原生栏换色挤在快照交接的同一帧。
      deferBrowserTheme = false
      if (request === themeRequest) {
        await waitForThemePaint()
        // 等待绘制时可能收到新选择，只同步仍然有效的主题。
        if (request === themeRequest) syncBrowserTheme()
      }
    }
  }
}

export function useTheme() {
  if (!isInitialized && typeof window !== 'undefined') {
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    
    const applyTheme = () => {
      const isDark = resolvedTheme.value === 'dark'
      const root = document.documentElement
      root.classList.toggle('dark', isDark)
      // 主样式就绪后移除首屏兜底，浏览器颜色在圆形转场期间单独协调。
      document.getElementById('theme-bootstrap-style')?.remove()
      if (!deferBrowserTheme) syncBrowserTheme()
    }

    mediaQuery.addEventListener('change', () => {
      systemDark.value = mediaQuery.matches
      if (currentTheme.value === 'system') {
        activeTransition?.skipTransition()
        applyTheme()
      }
    })

    watch(currentTheme, (newTheme) => {
      localStorage.setItem('cinema115-theme', newTheme)
      applyTheme()
    }, { immediate: true })

    isInitialized = true
  }

  return { currentTheme, resolvedTheme, setTheme }
}
