import { ref, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const currentTheme = ref<ThemeMode>((localStorage.getItem('cinema115-theme') as ThemeMode) || 'system')
let isInitialized = false

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

  return { currentTheme }
}
