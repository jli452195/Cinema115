const fs = require('fs');
const path = require('path');

function writeFile(p, content) {
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, content, 'utf8');
}

writeFile('frontend/tailwind.config.js', `/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: { extend: {} },
  plugins: [],
}`);

writeFile('frontend/src/composables/useTheme.ts', `import { ref, watch } from 'vue'

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
}`);

writeFile('frontend/src/style.css', `@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  body {
    @apply bg-slate-50 text-slate-900 transition-colors duration-300;
  }
  .dark body {
    @apply bg-slate-950 text-slate-200;
  }
}`);

writeFile('frontend/src/components/ThemeSwitcher.vue', `<script setup lang="ts">
import { useTheme } from '../composables/useTheme'

const { currentTheme } = useTheme()

function cycleTheme() {
  if (currentTheme.value === 'system') currentTheme.value = 'light'
  else if (currentTheme.value === 'light') currentTheme.value = 'dark'
  else currentTheme.value = 'system'
}
</script>

<template>
  <button 
    type="button" 
    class="icon-button" 
    :title="\`主题切换 (当前: \${currentTheme})\`" 
    @click="cycleTheme"
  >
    <svg v-if="currentTheme === 'light'" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
    <svg v-else-if="currentTheme === 'dark'" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
    <svg v-else class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><path d="M8 21h8M12 17v4"/></svg>
  </button>
</template>`);
