import { nextTick, ref, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const currentTheme = ref<ThemeMode>((localStorage.getItem('cinema115-theme') as ThemeMode) || 'system')
let isInitialized = false
let activeTransition: ViewTransition | undefined
let themeRequest = 0

async function setTheme(theme: ThemeMode, event?: MouseEvent) {
  const request = ++themeRequest
  activeTransition?.skipTransition()
  const root = document.documentElement
  const dark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  const apply = async () => { if (request === themeRequest) currentTheme.value = theme; await nextTick() }
  if (dark === root.classList.contains('dark') || !document.startViewTransition || window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    await apply()
    return
  }

  // Keyboard activation has no pointer position; use the focused button's center.
  const button = (event?.currentTarget as HTMLElement | null)?.getBoundingClientRect()
  const x = event && event.detail > 0 ? event.clientX : button ? button.left + button.width / 2 : window.innerWidth / 2
  const y = event && event.detail > 0 ? event.clientY : button ? button.top + button.height / 2 : window.innerHeight / 2
  const radius = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y))
  root.style.setProperty('--theme-reveal-origin', `${x}px ${y}px`)
  root.style.setProperty('--theme-reveal-radius', `${radius}px`)
  root.classList.add('theme-reveal')
  let transition: ViewTransition | undefined
  try {
    transition = document.startViewTransition(apply)
    activeTransition = transition
    // A skipped snapshot rejects ready, but the theme update must still complete.
    void transition.ready.catch(() => {})
    await transition.finished
  } catch {
    if (!transition || activeTransition === transition) await apply()
  } finally {
    if (!transition || activeTransition === transition) {
      activeTransition = undefined
      root.classList.remove('theme-reveal')
      root.style.removeProperty('--theme-reveal-origin')
      root.style.removeProperty('--theme-reveal-radius')
    }
  }
}

export function useTheme() {
  if (!isInitialized && typeof window !== 'undefined') {
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    
    const applyTheme = () => {
      const isDark = currentTheme.value === 'dark' || (currentTheme.value === 'system' && mediaQuery.matches)
      const root = document.documentElement
      root.classList.toggle('dark', isDark)
      const background = getComputedStyle(root).getPropertyValue(isDark ? '--color-slate-950' : '--color-slate-50').trim()
      // Update both media defaults so a manual theme also overrides the system color.
      for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
        meta.content = `rgb(${background})`
      }
    }

    mediaQuery.addEventListener('change', () => {
      if (currentTheme.value === 'system') applyTheme()
    })

    watch(currentTheme, (newTheme) => {
      localStorage.setItem('cinema115-theme', newTheme)
      applyTheme()
    }, { immediate: true })

    isInitialized = true
  }

  return { currentTheme, setTheme }
}
