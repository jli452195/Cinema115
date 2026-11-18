<script setup lang="ts">
import { IconCalendar, IconHeart, IconHome, IconPlus } from '@tabler/icons-vue'
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import NavigationIcon from './NavigationIcon.vue'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'
import { animateFloatingPanel, readFloatingPanelMotion } from '../lib/floatingPanelMotion'
import { isPortalAdmin } from '../composables/usePortalAuth'
import { followPendingCount } from '../composables/useFollowPendingCount'
import { rememberedDashboardHome } from '../utils/dashboardPreferences'
import { mobileFollowTabs, mobileWorkspaceLabels, mobileWorkspaceToolGroups, type MobileWorkspaceSection } from '../utils/mobileWorkspace'

const props = defineProps<{ section?: MobileWorkspaceSection; activeTool?: string }>()
const route = useRoute()
const tabs = computed(() => [
  { key: 'home', label: mobileWorkspaceLabels.home, icon: IconHome, to: rememberedDashboardHome.value },
  { key: 'follows', label: mobileWorkspaceLabels.follows, icon: IconHeart, to: props.section === 'follows' ? route.fullPath : mobileFollowTabs[0].to },
  { key: 'updates', label: mobileWorkspaceLabels.updates, icon: IconCalendar, to: '/updates' },
])
const selectedIndex = computed(() => tabs.value.findIndex(tab => tab.key === props.section))
function selectTab(event: MouseEvent, key: string, navigate: (event: MouseEvent) => unknown) {
  // 再点当前追更区时保留页面和滚动位置，同时保留链接的修饰键操作。
  if (key === 'follows' && props.section === key && event.button === 0 &&
      !event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey) {
    event.preventDefault()
    return
  }
  void navigate(event)
}
const pending = computed(() => isPortalAdmin.value ? followPendingCount.value || 0 : 0)
const currentToolPath = computed(() => route.path.toLowerCase().replace(/\/$/, ''))
const dialog = ref<HTMLDialogElement>(), panel = ref<HTMLElement>(), closeButton = ref<HTMLButtonElement>()
const opened = ref(false), dialogId = useId(), titleId = useId()
const lifecycle = createDialogLifecycle()
const mobile = window.matchMedia('(max-width: 639px)'), reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
let animation: Animation | undefined
let backdropStarted = false

function openTools(event: MouseEvent) {
  if (opened.value || !dialog.value || !isPortalAdmin.value || !mobile.matches) return
  lifecycle.acquire(event.currentTarget as HTMLElement)
  opened.value = true
  backdropStarted = false
  if (panel.value) panel.value.inert = false
  dialog.value.showModal()
  closeButton.value?.focus({ preventScroll: true })
  if (!panel.value || reducedMotion.matches || typeof panel.value.animate !== 'function') return
  const opening = animateFloatingPanel(panel.value, true, 'bottom')
  animation = opening
  opening.onfinish = () => { if (animation === opening) { animation = undefined; opening.cancel() } }
}

function dismissTools(returnFocus = true) {
  const current = animation
  animation = undefined
  current?.cancel()
  if (!opened.value) return
  dialog.value?.close()
  opened.value = false
  lifecycle.release(returnFocus)
}

async function closeTools() {
  if (!opened.value || !panel.value || reducedMotion.matches || typeof panel.value.animate !== 'function') { dismissTools(); return }
  if (animation?.id === 'closing-tools') return
  // 退场时禁用卡片，保留模态遮罩，直到动画结束。
  panel.value.inert = true
  closeButton.value?.focus({ preventScroll: true })
  const from = animation ? readFloatingPanelMotion(panel.value) : undefined
  animation?.cancel()
  const closing = animateFloatingPanel(panel.value, false, 'bottom', from)
  closing.id = 'closing-tools'
  animation = closing
  try { await closing.finished } catch { return }
  if (animation === closing) dismissTools()
}

function pickTool(event: MouseEvent, to: string, navigate: (event: MouseEvent) => unknown) {
  if (!opened.value || animation?.id === 'closing-tools') { event.preventDefault(); return }
  if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.button !== 0) return
  if (currentToolPath.value === to) {
    event.preventDefault()
    void closeTools()
    return
  }
  // 先释放原生模态，再让路由守卫决定是否离开，未保存确认可以直接接管焦点。
  dismissTools()
  void navigate(event)
}

function outside(event: MouseEvent | PointerEvent) {
  if (event.target !== dialog.value || !dialog.value) return false
  const { left, right, top, bottom } = dialog.value.getBoundingClientRect()
  return event.clientX < left || event.clientX > right || event.clientY < top || event.clientY > bottom
}
function onPointerDown(event: PointerEvent) { backdropStarted = outside(event) }
function onBackdropClick(event: MouseEvent) {
  if (backdropStarted && outside(event)) void closeTools()
  backdropStarted = false
}
function onViewportChange() { if (!mobile.matches) dismissTools(false) }
watch(() => route.fullPath, () => dismissTools(false))
watch(isPortalAdmin, admin => { if (!admin) dismissTools(false) })
onMounted(() => mobile.addEventListener('change', onViewportChange))
onBeforeUnmount(() => { mobile.removeEventListener('change', onViewportChange); dismissTools(false) })
</script>

<template>
  <div class="mobile-dock">
    <nav class="dock-tabs glass-pill-surface" aria-label="手机主导航" :style="{ '--dock-selection': String(selectedIndex) }">
      <span v-if="selectedIndex >= 0" class="dock-selection" aria-hidden="true"></span>
      <RouterLink v-for="tab in tabs" :key="tab.key" :to="tab.to" custom v-slot="{ href, navigate }">
        <a :href="href" class="dock-tab" :aria-current="section === tab.key ? 'page' : undefined" @click="selectTab($event, tab.key, navigate)">
          <span class="dock-icon"><component :is="tab.icon" :size="22" :stroke="section === tab.key ? '2.1' : '1.75'" aria-hidden="true" />
            <span v-if="tab.key === 'follows' && pending" class="dock-badge" :aria-label="`${pending} 项待处理`">{{ pending > 99 ? '99+' : pending }}</span>
          </span>
          <span>{{ tab.label }}</span>
        </a>
      </RouterLink>
    </nav>
    <button v-if="isPortalAdmin" type="button" class="dock-launcher glass-pill-surface" :class="{ 'is-tool-context': activeTool }" aria-label="打开功能导航" aria-haspopup="dialog" :aria-controls="dialogId" :aria-expanded="opened" @click="openTools">
      <IconPlus :size="26" stroke="1.9" aria-hidden="true" />
    </button>
  </div>
  <Teleport to="body">
    <dialog v-if="isPortalAdmin" :id="dialogId" ref="dialog" class="dock-tools" :aria-labelledby="titleId" @cancel.prevent="closeTools" @pointerdown="onPointerDown" @click="onBackdropClick">
      <section ref="panel" class="dock-tools-panel floating-surface">
        <h2 :id="titleId">功能导航</h2>
        <nav aria-label="管理工具">
          <section v-for="group in mobileWorkspaceToolGroups" :key="group.label" class="dock-tool-group" :aria-label="group.label">
            <h3>{{ group.label }}</h3>
            <div class="dock-tool-grid">
              <RouterLink v-for="tool in group.items" :key="tool.to" :to="tool.to" custom v-slot="{ href, navigate }">
                <a :href="href" class="dock-tool" :aria-current="tool.to === activeTool ? (tool.to === currentToolPath ? 'page' : 'location') : undefined" @click="pickTool($event, tool.to, navigate)">
                  <span class="dock-tool-icon"><NavigationIcon :name="tool.icon" :size="20" /></span>
                  <span class="dock-tool-label">{{ tool.label }}</span>
                  <NavigationIcon name="chevron" class="dock-tool-chevron" :size="15" />
                </a>
              </RouterLink>
            </div>
          </section>
        </nav>
      </section>
      <button ref="closeButton" type="button" class="dock-launcher dock-close glass-pill-surface" aria-label="关闭功能导航" @click="closeTools">
        <IconPlus :size="26" stroke="1.9" aria-hidden="true" />
      </button>
    </dialog>
  </Teleport>
</template>

<style>
:root {
  --mobile-dock-height: 54px;
  --mobile-dock-bottom: max(12px, var(--app-safe-bottom));
  --mobile-dock-clearance: calc(var(--mobile-dock-height) + var(--mobile-dock-bottom));
}
</style>

<style scoped>
.mobile-dock { display: none; }
@media (max-width: 639px) {
  .mobile-dock {
    position: fixed; z-index: 39; display: flex; align-items: center; gap: 9px;
    inset: auto max(12px, var(--app-safe-right)) var(--mobile-dock-bottom) max(12px, var(--app-safe-left));
    width: auto; max-width: 460px; margin-inline: auto; pointer-events: none;
  }
}
.dock-tabs, .dock-launcher { box-shadow: 0 8px 24px rgb(15 23 42 / .13); pointer-events: auto; }
.dark :is(.dock-tabs, .dock-launcher) { box-shadow: 0 8px 24px rgb(0 0 0 / .28); }
.dock-tabs {
  --dock-inset: 4px;
  display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); flex: 1;
  min-width: 0; height: var(--mobile-dock-height); padding: var(--dock-inset);
}
.dock-selection {
  position: absolute; inset: var(--dock-inset) auto var(--dock-inset) var(--dock-inset); z-index: 0;
  width: calc((100% - 2 * var(--dock-inset)) / 3); border-radius: 999px;
  background: rgb(var(--selection-surface)); box-shadow: inset 0 0 0 1px var(--glass-edge);
  transform: translateX(calc(var(--dock-selection) * 100%));
  transition: transform 360ms cubic-bezier(.22, 1, .36, 1); pointer-events: none;
}
.dock-tab {
  position: relative; display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 2px; min-width: 0; border-radius: 999px; color: rgb(var(--content-secondary));
  font-size: 11px; line-height: 1.2; font-weight: 600; -webkit-tap-highlight-color: transparent;
}
.dock-tab[aria-current] { color: rgb(var(--selection-content)); }
.dock-icon { position: relative; display: flex; transition: transform 160ms ease-out; }
.dock-tab:active .dock-icon { transform: scale(.88); }
.dock-badge {
  position: absolute; inset: -3px auto auto 14px; min-width: 15px; padding: 0 3px; border-radius: 999px;
  background: rgb(var(--action-primary)); color: #fff; font-size: 9px; line-height: 15px; text-align: center;
}
.dock-launcher {
  display: grid; flex: none; place-items: center; width: var(--mobile-dock-height); height: var(--mobile-dock-height);
  padding: 0; color: rgb(var(--selection-content)); cursor: pointer; -webkit-tap-highlight-color: transparent;
}
.dock-launcher.is-tool-context::before { border-color: color-mix(in srgb, rgb(var(--selection-content)) 35%, var(--glass-edge)); }
.dock-launcher > svg { transition: transform 200ms cubic-bezier(.22, 1, .36, 1); }
.dock-launcher:active > svg { transform: scale(.86); }
.dock-tools {
  position: fixed; inset: auto max(12px, var(--app-safe-right)) var(--mobile-dock-bottom) max(12px, var(--app-safe-left));
  width: auto; max-width: 460px; max-height: none; margin: 0 auto; padding: 0; overflow: visible;
  border: 0; background: transparent; color: var(--glass-text);
}
.dock-tools::backdrop { background: var(--glass-scrim); }
.dock-tools-panel {
  max-height: calc(100dvh - var(--app-safe-top) - var(--mobile-dock-clearance) - 28px);
  overflow: auto; overscroll-behavior: contain; padding: 15px; border-radius: 25px; transform-origin: calc(100% - 28px) 100%;
  box-shadow: 0 12px 40px rgb(15 23 42 / .19), var(--glass-dialog-shadow);
}
.dock-tools-panel h2 { margin: 0 0 11px; font-size: 16px; font-weight: 700; }
.dock-tool-group + .dock-tool-group { margin-top: 12px; }
.dock-tool-group h3 { margin: 0 0 6px 3px; color: var(--glass-muted); font-size: 12px; font-weight: 600; }
.dock-tool-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 7px; }
.dock-tool {
  display: flex; align-items: center; gap: 7px; min-width: 0; min-height: 52px; padding: 5px 8px;
  border: 1px solid var(--glass-edge); border-radius: 13px; background: var(--glass-inset-surface);
  color: var(--glass-text); font-size: 12px; font-weight: 600;
}
.dock-tool-icon { display: grid; flex: none; width: 30px; height: 30px; place-items: center; border-radius: 10px; background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); }
.dock-tool[aria-current] { border-color: rgb(var(--selection-content) / .48); background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); }
.dock-tool-label { min-width: 0; overflow-wrap: anywhere; }
.dock-tool-chevron { flex: none; margin-left: auto; color: var(--glass-muted); }
.dock-close { margin: 9px 0 0 auto; }
.dock-close > svg { transform: rotate(45deg); }
.dock-close:active > svg { transform: rotate(45deg) scale(.86); }
:is(.dock-tab, .dock-launcher, .dock-tool):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: -2px; }
@media (hover: hover) { .dock-tool:hover { background: rgb(var(--selection-surface)); } }
@media (max-width: 359px) { .dock-tool-chevron { display: none; } }
@media (prefers-reduced-motion: reduce) {
  .dock-selection, .dock-icon, .dock-launcher > svg { transition: none; }
  .dock-tab:active .dock-icon, .dock-launcher:active > svg { transform: none; }
  .dock-close:active > svg { transform: rotate(45deg); }
}
</style>
