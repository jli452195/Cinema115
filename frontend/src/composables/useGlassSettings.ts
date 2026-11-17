import { computed, readonly, ref, shallowRef } from 'vue'
import { defaultGlassSettings, glassCssSnippet, glassCssVariables, glassPresets, glassStorageKey, identifyGlassPreset, normalizeGlassSettings, type GlassPreset, type GlassSettings } from '../utils/glassSettings'

const settings = shallowRef<GlassSettings>({ ...defaultGlassSettings })
const refractionSupported = ref(false)
const transparencyReduced = ref(false)
const storageAvailable = ref(true)
let initialized = false
let saveTimer: ReturnType<typeof setTimeout> | undefined
let pendingSave = false

function loadSettings() {
  try {
    const stored = localStorage.getItem(glassStorageKey)
    return normalizeGlassSettings(stored ? JSON.parse(stored) : null)
  } catch {
    return { ...defaultGlassSettings }
  }
}

function applyTokens() {
  const root = document.documentElement
  for (const [key, value] of Object.entries(glassCssVariables(settings.value))) root.style.setProperty(key, value)
  // 偏好可保留，实际生效范围随设备及无障碍设置变化。
  root.dataset.glassRefraction = settings.value.refraction && refractionSupported.value && !transparencyReduced.value ? 'on' : 'off'
}

function saveSettings() {
  clearTimeout(saveTimer)
  if (!pendingSave) return
  pendingSave = false
  try {
    localStorage.setItem(glassStorageKey, JSON.stringify(settings.value))
    storageAvailable.value = true
  } catch {
    storageAvailable.value = false
  }
}

function updateSettings(patch: Partial<GlassSettings>) {
  settings.value = normalizeGlassSettings({ ...settings.value, ...patch })
  applyTokens()
  // 拖动滑杆即时更新材质，存储写入合并执行，离开页面前补齐。
  pendingSave = true
  clearTimeout(saveTimer)
  saveTimer = setTimeout(saveSettings, 180)
}

export function useGlassSettings() {
  if (!initialized) {
    initialized = true
    settings.value = loadSettings()
    const desktopPointer = window.matchMedia('(min-width: 640px) and (hover: hover) and (pointer: fine)')
    const reduced = window.matchMedia('(prefers-reduced-transparency: reduce)')
    const contrast = window.matchMedia('(prefers-contrast: more)')
    const forcedColors = window.matchMedia('(forced-colors: active)')
    // CSS.supports 只能验证语法；保守限定已采用该渲染路径的桌面 Chromium。
    const chromium = /(?:Chrome|Chromium|Edg|OPR)\//.test(navigator.userAgent) && !/Android|iPhone|iPad|iPod/.test(navigator.userAgent)
    const syncEnvironment = () => {
      refractionSupported.value = chromium && desktopPointer.matches && CSS.supports('backdrop-filter', 'url(#cinema-glass-refraction) blur(1px)')
      transparencyReduced.value = reduced.matches || contrast.matches || forcedColors.matches
      applyTokens()
    }
    for (const query of [desktopPointer, reduced, contrast, forcedColors]) query.addEventListener('change', syncEnvironment)
    syncEnvironment()
    window.addEventListener('pagehide', saveSettings)
    document.addEventListener('visibilitychange', () => { if (document.hidden) saveSettings() })
    window.addEventListener('storage', event => {
      if (event.storageArea !== localStorage || (event.key !== glassStorageKey && event.key !== null)) return
      // 外部标签页的变动直接应用，避免监听器互相回写造成循环。
      clearTimeout(saveTimer)
      pendingSave = false
      settings.value = loadSettings()
      applyTokens()
    })
  }
  return {
    settings: readonly(settings),
    preset: computed(() => identifyGlassPreset(settings.value)),
    refractionSupported: readonly(refractionSupported),
    refractionActive: computed(() => settings.value.refraction && refractionSupported.value && !transparencyReduced.value),
    transparencyReduced: readonly(transparencyReduced),
    storageAvailable: readonly(storageAvailable),
    cssSnippet: computed(() => glassCssSnippet(settings.value)),
    updateSettings,
    applyPreset: (id: GlassPreset) => {
      const preset = glassPresets.find(item => item.value === id)
      if (preset) updateSettings({ ...preset.settings, refraction: settings.value.refraction })
    },
    resetSettings: () => updateSettings(defaultGlassSettings),
    saveSettings,
  }
}
