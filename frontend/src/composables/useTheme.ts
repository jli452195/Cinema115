import { ref, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const currentTheme = ref<ThemeMode>((localStorage.getItem('cinema115-theme') as ThemeMode) || 'system')
let isInitialized = false

export function useTheme() {
  if (!isInitialized && typeof window !== 'undefined') {
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    
    const applyTheme = () => {
      const isDark = currentTheme.value === 'dark' || (currentTheme.value === 'system' && mediaQuery.matches)
      if (isDark) {
        document.documentElement.classList.add('dark')
      } else {
        document.documentElement.classList.remove('dark')
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