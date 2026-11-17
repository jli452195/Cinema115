<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { IconCheck, IconCopy, IconRestore } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import DetailSearchButtons from './DetailSearchButtons.vue'
import SegmentedControl from './SegmentedControl.vue'
import SettingSwitch from './SettingSwitch.vue'
import { useGlassSettings } from '../composables/useGlassSettings'
import { glassPresets, glassSliders, type GlassPreset } from '../utils/glassSettings'
import { detailSearchSources } from '../utils/detailSearchSources'
import { copyText } from '../utils/clipboard'

const emit = defineEmits<{ close: [] }>()
const { settings, preset, refractionSupported, refractionActive, transparencyReduced, storageAvailable, cssSnippet, updateSettings, applyPreset, resetSettings, saveSettings } = useGlassSettings()
const previewSources = detailSearchSources.filter(source => !source.animationOnly)
const presetOptions = glassPresets.map(({ value, label }) => ({ value, label }))
const copyState = ref<'idle' | 'copied' | 'manual'>('idle')
watch(cssSnippet, () => { if (copyState.value === 'copied') copyState.value = 'idle' })
const manualCopy = ref<HTMLTextAreaElement>()
const refractionDescription = computed(() => transparencyReduced.value
  ? '当前已按系统偏好减少透明效果，折射暂停。'
  : !refractionSupported.value ? '仅在桌面 Chromium 浏览器生效；当前设备使用普通玻璃效果。'
  : '仅用于搜索胶囊背景，默认关闭。开启会增加绘制开销。')

function choosePreset(value: GlassPreset | 'custom') { if (value !== 'custom') applyPreset(value) }
async function copyCss() {
  try {
    await copyText(cssSnippet.value)
    copyState.value = 'copied'
  } catch {
    copyState.value = 'manual'
  }
}
onBeforeUnmount(saveSettings)
</script>

<template>
  <AnimeFloatingDialog class="appearance-dialog" title="外观设置" close-label="关闭外观设置" mobile-presentation="sheet" @close="emit('close')">
    <div class="appearance-layout">
      <section class="appearance-preview" aria-label="玻璃实时预览">
        <div class="preview-caption"><span>实时预览</span><span>{{ preset === 'custom' ? '自定义' : glassPresets.find(item => item.value === preset)?.label }}</span></div>
        <div class="preview-stage">
          <div class="preview-pattern" aria-hidden="true"></div>
          <DetailSearchButtons :sources="previewSources" :interactive="false" />
        </div>
        <p>搜索按钮、浮层和顶栏共用这组参数。内容较多的面板会自动加强底色，手机抽屉保留实色以便阅读和拖动。</p>
      </section>

      <section class="appearance-controls" aria-label="玻璃参数">
        <div class="appearance-presets">
          <div class="setting-heading"><h3>玻璃质感</h3><span v-if="preset === 'custom'">已自定义</span></div>
          <SegmentedControl :model-value="preset" :options="presetOptions" label="玻璃质感预设" appearance="subtle" density="compact" @change="choosePreset" />
        </div>
        <div class="appearance-sliders">
          <label v-for="slider in glassSliders" :key="slider.key" class="appearance-slider">
            <span class="slider-heading"><span>{{ slider.label }}</span><output>{{ settings[slider.key] }}<span>{{ slider.unit }}</span></output></span>
            <input type="range" :aria-label="slider.label" :min="slider.min" :max="slider.max" step="1" :value="settings[slider.key]" :aria-valuetext="`${settings[slider.key]}${slider.unit}`" @input="updateSettings({ [slider.key]: Number(($event.target as HTMLInputElement).value) })" />
          </label>
        </div>
        <div class="appearance-refraction">
          <SettingSwitch :model-value="refractionActive" label="边缘微折射（实验性）" :description="refractionDescription" :disabled="!refractionSupported || transparencyReduced" @update:model-value="updateSettings({ refraction: $event })" />
        </div>
        <p class="appearance-save-status" role="status">{{ storageAvailable ? '即时生效，自动保存在当前浏览器。' : '当前浏览器无法保存设置，本次调整仍然生效。' }}</p>
        <p v-if="transparencyReduced" class="appearance-save-status">系统已要求减少透明或提高对比度，参数已保留。</p>
        <div v-if="copyState === 'manual'" class="appearance-manual-copy">
          <label for="glass-css-copy">复制未成功，可手动选择以下 CSS 参数。</label>
          <textarea id="glass-css-copy" ref="manualCopy" readonly :value="cssSnippet" @focus="manualCopy?.select()"></textarea>
        </div>
      </section>
    </div>
    <template #footer>
      <div class="appearance-actions">
        <button type="button" class="appearance-button" @click="resetSettings"><IconRestore :size="16" aria-hidden="true" /><span>恢复默认</span></button>
        <button type="button" class="appearance-button appearance-copy" @click="copyCss"><component :is="copyState === 'copied' ? IconCheck : IconCopy" :size="16" aria-hidden="true" /><span>{{ copyState === 'copied' ? '已复制' : '复制 CSS' }}</span></button>
      </div>
    </template>
  </AnimeFloatingDialog>
</template>

<style scoped>
.appearance-dialog { width: min(880px, calc(100vw - 32px)); }
.appearance-layout { display: grid; grid-template-columns: minmax(0, .85fr) minmax(0, 1.15fr); gap: 28px; padding-block: 8px; }
.appearance-preview, .appearance-controls { min-width: 0; }
.preview-caption, .setting-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 12px; line-height: 20px; }
.preview-caption { margin-bottom: 10px; color: var(--glass-muted); }
.preview-caption > :first-child, .setting-heading h3 { color: var(--glass-text); font-weight: 600; }
.preview-caption > :last-child { font-size: 11px; }
.preview-stage { position: relative; overflow: hidden; display: grid; align-items: center; min-height: 232px; padding: 20px 16px; border: 1px solid var(--glass-inset-border); border-radius: 20px; background: linear-gradient(135deg, #d6e2f8, #e9edf3 60%, #becde3); }
.preview-pattern { position: absolute; inset: 0; background: repeating-linear-gradient(125deg, transparent 0 40px, rgb(255 255 255 / .5) 40px 43px, transparent 43px 82px), radial-gradient(ellipse at 18% 30%, rgb(59 130 246 / .42), transparent 70%); }
.dark .preview-stage { background: linear-gradient(135deg, #233755, #151f30 60%, #334155); }
.dark .preview-pattern { background: repeating-linear-gradient(125deg, transparent 0 40px, rgb(148 163 184 / .2) 40px 43px, transparent 43px 82px), radial-gradient(ellipse at 18% 30%, rgb(59 130 246 / .4), transparent 70%); }
.preview-stage :deep(.detail-search-buttons) { position: relative; }
.preview-stage :deep(.search-pill) { padding-inline: 8px; }
.appearance-preview > p { margin-top: 12px; color: var(--glass-muted); font-size: 12px; line-height: 21px; }
.setting-heading { margin-bottom: 10px; }
.setting-heading > span { color: rgb(var(--action-link)); font-size: 11px; }
.appearance-presets :deep(.segmented-control) { display: flex; width: 100%; }
.appearance-presets :deep(.segmented-control-option) { flex: 1 0 auto; min-width: 0; padding-inline: 10px; font-size: 12px; }
.appearance-sliders { display: grid; gap: 8px; margin-top: 18px; }
.appearance-slider { display: block; }
.slider-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: var(--glass-text); font-size: 13px; line-height: 20px; }
.slider-heading output { color: var(--glass-muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.slider-heading output > span { margin-left: 3px; }
.appearance-slider input { display: block; width: 100%; height: 36px; margin: 0; accent-color: #2563eb; cursor: pointer; }
.appearance-slider input:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; border-radius: 8px; }
.dark .appearance-slider input { accent-color: #93b4ef; }
.appearance-refraction { margin-top: 14px; padding-top: 16px; border-top: 1px solid var(--glass-inset-border); }
.appearance-refraction :deep(.setting-switch-label) { font-size: 13px; }
.appearance-refraction :deep(.setting-switch-description) { line-height: 20px; }
.appearance-save-status { margin-top: 14px; color: var(--glass-muted); font-size: 12px; line-height: 20px; }
.appearance-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; }
.appearance-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 44px; padding: 8px 14px; border: 1px solid transparent; border-radius: 999px; color: var(--glass-muted); font-size: 12px; font-weight: 500; }
.appearance-copy { border-color: var(--glass-inset-border); background: var(--glass-inset-surface); color: var(--glass-text); }
.appearance-button:hover { background: var(--glass-inset-surface); }
.appearance-manual-copy { margin-top: 12px; color: var(--glass-muted); font-size: 12px; line-height: 20px; }
.appearance-manual-copy textarea { display: block; width: 100%; min-height: 160px; margin-top: 8px; padding: 10px; border: 1px solid var(--glass-inset-border); border-radius: 10px; color: var(--glass-text); background: var(--glass-solid); font-family: monospace; font-size: 12px; }
@media (max-width: 719px) {
  .appearance-layout { grid-template-columns: minmax(0, 1fr); gap: 20px; }
  .preview-stage { min-height: 168px; padding-block: 10px; }
  .appearance-preview > p { margin-top: 8px; }
}
@media (max-width: 639px) {
  .appearance-layout { padding: 2px 8px 8px; }
  .appearance-slider input { height: 44px; }
  .appearance-sliders { gap: 6px; margin-top: 16px; }
  .appearance-presets :deep(.segmented-control-option) { padding-inline: 8px; }
}
</style>
