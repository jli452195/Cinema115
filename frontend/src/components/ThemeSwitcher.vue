<script setup lang="ts">
import { computed } from 'vue'
import { useTheme, type ThemeMode } from '../composables/useTheme'
import { vTooltip } from '../directives/tooltip'

interface Props {
  showLabel?: boolean
  beforeChange?: () => void | Promise<void>
}

const props = withDefaults(defineProps<Props>(), {
  showLabel: false,
})

const { currentTheme, setTheme } = useTheme()

// 按用户选择的模式循环，避免跟随系统时跳过某一种模式。
const nextTheme: Record<ThemeMode, ThemeMode> = { light: 'dark', dark: 'system', system: 'light' }
const themeNames: Record<ThemeMode, string> = { light: '浅色', dark: '深色', system: '跟随系统' }
const targetTheme = computed(() => nextTheme[currentTheme.value])
const toggleLabel = computed(() => `当前：${themeNames[currentTheme.value]}，点击切换为${themeNames[targetTheme.value]}`)
const displayLabel = computed(() => `主题：${themeNames[currentTheme.value]}`)

function toggleTheme(event: MouseEvent) {
  void setTheme(targetTheme.value, event, props.beforeChange)
}
</script>

<template>
  <div class="theme-switcher" :class="{ 'theme-switcher-menu': showLabel }">
    <!-- 单按钮依次切换浅色、深色、跟随系统。 -->
    <button
      type="button"
      class="theme-toggle"
      :class="showLabel ? 'theme-menu-button' : 'icon-button'"
      v-tooltip="toggleLabel"
      :aria-label="toggleLabel"
      @click="toggleTheme"
    >
      <!-- 保留同一 SVG，仅切换内部编组的透明度和旋转。 -->
      <svg
        class="theme-glyph"
        :class="`is-${currentTheme}`"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <g class="theme-glyph-sun">
          <circle cx="12" cy="12" r="5" />
          <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
        </g>
        <g class="theme-glyph-moon">
          <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
        </g>
        <g class="theme-glyph-system">
          <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
          <path d="M8 21h8M12 17v4" />
        </g>
      </svg>
      <span v-if="showLabel" class="theme-label">{{ displayLabel }}</span>
    </button>
  </div>
</template>

<style scoped>
/* 基础行内容器布局 */
.theme-switcher {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  position: relative;
  box-sizing: border-box;
}

/* 主切换按钮基础尺寸、颜色与无障碍焦点样式（脱离祖先 .icon-button 亦能自洽） */
.theme-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.25rem;
  height: 2.25rem;
  min-width: 2.25rem;
  min-height: 2.25rem;
  padding: 0;
  border: none;
  border-radius: 0.6rem;
  background: transparent;
  color: rgb(var(--content-secondary));
  cursor: pointer;
  outline: none;
  transition: color 150ms ease, background-color 150ms ease;
  -webkit-tap-highlight-color: transparent;
}

.theme-toggle:hover {
  color: rgb(var(--content-primary));
  background-color: rgb(var(--surface-hover));
}

.theme-toggle:focus-visible {
  outline: 2px solid rgb(var(--focus, 37 99 235));
  outline-offset: 2px;
}

/* 菜单展开模式（showLabel 为真） */
.theme-switcher-menu {
  display: flex;
  align-items: center;
  width: 100%;
  gap: 0.5rem;
}

.theme-switcher-menu .theme-toggle.theme-menu-button {
  flex: 1 1 auto;
  display: flex;
  justify-content: flex-start;
  width: auto;
  min-height: 2.75rem;
  align-items: center;
  gap: 0.75rem;
  padding: 0.625rem 0.75rem;
  border-radius: 0.625rem;
  font-size: 0.875rem;
  line-height: 1.5rem;
  text-align: left;
  color: rgb(var(--content-secondary, var(--color-slate-600)));
}

.theme-switcher-menu .theme-toggle.theme-menu-button:hover {
  color: rgb(var(--content-primary, var(--color-slate-900)));
  background-color: rgb(var(--surface-hover, var(--color-slate-100)));
}

/* 图标持久化与微动效控制 */
.theme-glyph {
  width: 1.25rem;
  height: 1.25rem;
  flex-shrink: 0;
  overflow: visible;
}

.theme-glyph-sun,
.theme-glyph-moon,
.theme-glyph-system {
  transform-box: fill-box;
  transform-origin: center;
  opacity: 0;
  transform: scale(0.6) rotate(-45deg);
  transition: transform 260ms cubic-bezier(0.22, 1, 0.36, 1), opacity 200ms ease;
}

.theme-glyph.is-light .theme-glyph-sun {
  opacity: 1;
  transform: scale(1) rotate(0deg);
}

.theme-glyph.is-light .theme-glyph-moon {
  opacity: 0;
  transform: scale(0.6) rotate(-45deg);
  pointer-events: none;
}

.theme-glyph.is-dark .theme-glyph-sun {
  opacity: 0;
  transform: scale(0.6) rotate(45deg);
  pointer-events: none;
}

.theme-glyph.is-dark .theme-glyph-moon {
  opacity: 1;
  transform: scale(1) rotate(0deg);
}

.theme-glyph.is-system .theme-glyph-system {
  opacity: 1;
  transform: scale(1) rotate(0deg);
}

.theme-label {
  display: inline-block;
  white-space: nowrap;
}

/* 移动端保留完整触控面积，图标本身维持轻量尺寸。 */
@media (max-width: 639px), (pointer: coarse) {
  .theme-toggle {
    min-width: 44px;
    min-height: 44px;
  }
}

/* 减弱动态效果适配 */
@media (prefers-reduced-motion: reduce) {
  .theme-toggle,
  .theme-glyph-sun,
  .theme-glyph-moon,
  .theme-glyph-system {
    transition: none !important;
  }
}
</style>
