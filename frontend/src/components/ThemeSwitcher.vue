<script setup lang="ts">
import { useTheme } from '../composables/useTheme'

defineProps<{ showLabel?: boolean }>()
const { currentTheme, setTheme } = useTheme()
const themeLabels = { system: '跟随系统', light: '浅色', dark: '深色' }

function cycleTheme(event: MouseEvent) {
  const next = currentTheme.value === 'system' ? 'light' : currentTheme.value === 'light' ? 'dark' : 'system'
  void setTheme(next, event)
}
</script>

<template>
  <button 
    type="button" 
    :class="showLabel ? 'theme-menu-button flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800' : 'icon-button'"
    :title="`切换主题，当前：${themeLabels[currentTheme]}`"
    :aria-label="`切换主题，当前：${themeLabels[currentTheme]}`"
    @click="cycleTheme"
  >
    <svg v-if="currentTheme === 'light'" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
    <svg v-else-if="currentTheme === 'dark'" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
    <svg v-else class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><path d="M8 21h8M12 17v4"/></svg>
    <span v-if="showLabel">主题：{{ themeLabels[currentTheme] }}</span>
  </button>
</template>
