<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { isNavigationFailure, NavigationFailureType, useRouter } from 'vue-router'
import ManagementNavigation from './ManagementNavigation.vue'
import NavigationIcon from './NavigationIcon.vue'
import CinemaLogo from './CinemaLogo.vue'

const props = defineProps<{ desktop: boolean; collapsed?: boolean }>()
defineEmits<{ toggle: [] }>()
const router = useRouter(), drawer = ref<HTMLDialogElement>(), opened = ref(false), closeButton = ref<HTMLButtonElement>(), desktopNavigation = ref<HTMLElement>()
const titleID = useId()
let trigger: HTMLElement | undefined, previousOverflow = '', backdropStarted = false
async function openDrawer(event: MouseEvent) {
  if (props.desktop || opened.value) return
  trigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'
  opened.value = true
  await nextTick()
  if (!opened.value) return
  drawer.value?.showModal(); closeButton.value?.focus({ preventScroll: true })
}
function closeDrawer(returnFocus = true) {
  if (!opened.value) return
  drawer.value?.close(); opened.value = false
  document.body.style.overflow = previousOverflow
  if (returnFocus) void nextTick(() => {
    const target = trigger?.isConnected && trigger.getClientRects().length ? trigger
      : desktopNavigation.value?.querySelector<HTMLElement>('a[aria-current]') || desktopNavigation.value?.querySelector<HTMLElement>('a[href]')
    target?.focus({ preventScroll: true })
  })
}
function outside(event: PointerEvent | MouseEvent) {
  if (event.target !== drawer.value || !drawer.value) return false
  const box = drawer.value.getBoundingClientRect()
  return event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom
}
function pointerDown(event: PointerEvent) { backdropStarted = outside(event) }
function backdropClick(event: MouseEvent) {
  if (backdropStarted && outside(event)) closeDrawer()
  backdropStarted = false
}
function trapFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !drawer.value) return
  const controls = [...drawer.value.querySelectorAll<HTMLElement>('button:not(:disabled), a[href]')].filter(el => el.getClientRects().length)
  const first = controls[0], last = controls[controls.length - 1]
  if (!controls.some(el => el === document.activeElement)) { event.preventDefault(); (event.shiftKey ? last : first)?.focus() }
  else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}
const removeNavigationHook = router.afterEach((_to, _from, failure) => {
  if (!failure) closeDrawer(false)
  else if (isNavigationFailure(failure, NavigationFailureType.duplicated)) closeDrawer()
})
watch(() => props.desktop, desktop => { if (desktop) closeDrawer() })
// A rejected route change leaves the drawer open so the current page is usable.
onBeforeUnmount(() => { removeNavigationHook(); closeDrawer(false) })
defineExpose({ openDrawer, opened })
</script>

<template>
  <aside v-if="desktop" ref="desktopNavigation" class="management-sidebar" :class="{ 'is-collapsed': collapsed }" aria-label="管理侧栏">
    <div class="management-sidebar-brand">
      <RouterLink to="/" class="management-brand-link" aria-label="Cinema115 首页" title="Cinema115 首页"><CinemaLogo sidebar :compact="collapsed" /></RouterLink>
      <button type="button" class="management-sidebar-control management-collapse-control" :aria-label="collapsed ? '展开侧栏' : '收起侧栏'" :title="collapsed ? '展开侧栏' : '收起侧栏'" :aria-expanded="!collapsed" aria-controls="management-desktop-navigation" @click="$emit('toggle')"><NavigationIcon name="sidebar" /></button>
    </div>
    <ManagementNavigation id="management-desktop-navigation" :collapsed="collapsed" />
  </aside>
  <Teleport to="body">
    <dialog v-if="opened" ref="drawer" class="management-drawer" :aria-labelledby="titleID" @cancel.prevent="closeDrawer()" @keydown="trapFocus" @pointerdown="pointerDown" @click="backdropClick">
      <div class="management-drawer-body">
        <header class="management-sidebar-heading"><h2 :id="titleID">功能导航</h2><button ref="closeButton" type="button" class="management-sidebar-control" aria-label="关闭功能导航" @click="closeDrawer()"><NavigationIcon name="close" /></button></header>
        <ManagementNavigation mobile />
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.management-sidebar { position: fixed; z-index: 45; inset: 0 auto 0 0; display: flex; width: var(--management-sidebar-width); height: 100dvh; min-height: 0; flex-direction: column; border-right: 1px solid rgb(var(--color-slate-200) / 60%); background: #f1f4f9; padding-bottom: env(safe-area-inset-bottom, 0px); }
.management-sidebar-brand { display: flex; min-height: var(--workbench-header-height); flex-shrink: 0; align-items: center; gap: 8px; padding: 0 12px 0 18px; }
.management-brand-link { display: inline-flex; min-width: 0; min-height: 44px; flex: 1; align-items: center; border-radius: 10px; }
.management-brand-link:focus-visible { outline: 2px solid #78a2db; outline-offset: 2px; }
.management-sidebar.is-collapsed .management-sidebar-brand { flex-direction: column; gap: 0; padding: 14px 9px 10px; }
.management-sidebar.is-collapsed .management-brand-link { flex: none; justify-content: center; width: 44px; }
.management-sidebar.is-collapsed .management-collapse-control { margin-top: 4px; }
.management-sidebar-heading { display: flex; min-height: 52px; flex-shrink: 0; align-items: center; justify-content: space-between; gap: 8px; padding: 0 8px 0 20px; color: rgb(var(--color-slate-600)); font-size: 12px; font-weight: 600; }
.management-sidebar-control { display: grid; place-items: center; width: 44px; height: 44px; flex-shrink: 0; border-radius: 10px; color: rgb(var(--color-slate-500)); cursor: pointer; }.management-sidebar-control svg { width: 19px; height: 19px; }.management-sidebar-control:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; }
.management-collapse-control { width: 32px; height: 36px; }
.is-collapsed .management-collapse-control { width: 44px; height: 40px; }
.management-drawer { box-sizing: border-box; position: fixed; inset: 0 auto 0 0; width: min(288px, calc(100vw - 32px)); max-width: none; height: 100dvh; max-height: none; margin: 0; padding: 0; border: 0; border-radius: 0 20px 20px 0; overflow: hidden; background: rgb(var(--color-slate-50)); color: rgb(var(--color-slate-700)); box-shadow: 12px 0 60px rgb(15 23 42 / 14%); }
.management-drawer[open] { animation: management-drawer-in 180ms ease-out; }.management-drawer::backdrop { background: rgb(15 23 42 / 32%); animation: management-backdrop-in 180ms ease-out; }
.management-drawer-body { display: flex; height: 100%; flex-direction: column; padding: env(safe-area-inset-top, 0px) 4px env(safe-area-inset-bottom, 0px); }.management-drawer .management-sidebar-heading { min-height: 68px; padding-inline: 20px 8px; font-size: 14px; color: rgb(var(--color-slate-700)); }
.dark .management-sidebar { background: #1d2025; border-color: rgb(var(--color-slate-700) / 55%); }.dark .management-sidebar .management-sidebar-heading { color: rgb(var(--color-slate-300)); }.dark .management-drawer { background: rgb(var(--color-slate-900)); color-scheme: dark; }.dark .management-drawer::backdrop { background: rgb(0 0 0 / 45%); }.dark .management-drawer .management-sidebar-heading { color: rgb(var(--color-slate-200)); }
@media (hover: hover) { .management-sidebar-control:hover { background: rgb(116 143 180 / 9%); color: #4775b0; } }
@keyframes management-drawer-in { from { transform: translateX(-100%); } to { transform: translateX(0); } }@keyframes management-backdrop-in { from { opacity: 0; } to { opacity: 1; } }
@media (prefers-reduced-motion: reduce) { .management-drawer[open], .management-drawer::backdrop { animation: none; } }
</style>
