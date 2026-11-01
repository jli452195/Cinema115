<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { adminAccountGroups, isAccountLinkActive } from '../utils/accountNavigation'
import type { AccountLink } from '../utils/accountNavigation'
import { isPortalAdmin } from '../composables/usePortalAuth'
import { managementNavigationScroll } from '../composables/useManagementSidebar'
import NavigationIcon from './NavigationIcon.vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ mobile?: boolean; collapsed?: boolean }>()
const route = useRoute(), navigation = ref<HTMLElement>()
const groups = computed(() => [
  { label: '个人', links: [
    ...(!isPortalAdmin.value ? [{ label: '我的播放', to: '/account/playback', icon: 'playback' as const }] : []),
    { label: '我的追更', to: '/follows', icon: 'follows' as const },
  ] as AccountLink[] },
  ...(isPortalAdmin.value ? adminAccountGroups : []),
])
const currentCategory = computed(() => groups.value.flatMap(group => group.links)
  .filter(link => isAccountLinkActive(route, link)).sort((a, b) => b.to.length - a.to.length)[0])
const tooltip = ref<{ label: string; top: number; left: number }>()
const tooltipID = useId()

function showTooltip(event: MouseEvent | FocusEvent, label: string) {
  if (event.currentTarget instanceof HTMLElement) positionTooltip(event.currentTarget, label)
}
function positionTooltip(element: HTMLElement, label: string) {
  if (!props.collapsed || props.mobile) return
  const bounds = element.getBoundingClientRect()
  const right = navigation.value?.getBoundingClientRect().right ?? bounds.right
  tooltip.value = { label, left: right + 12, top: Math.max(22, Math.min(window.innerHeight - 22, bounds.top + bounds.height / 2)) }
}
function hideTooltip() { tooltip.value = undefined }
function onScroll() {
  rememberScroll()
  const focused = document.activeElement
  // Keyboard focus can scroll a link into view after its focus event.
  if (focused instanceof HTMLElement && navigation.value?.contains(focused) && focused.matches('a:focus-visible')) positionTooltip(focused, focused.getAttribute('aria-label') || '')
  else hideTooltip()
}

function rememberScroll() { if (navigation.value && !props.mobile) managementNavigationScroll.top = navigation.value.scrollTop }
async function restoreScroll() {
  await nextTick()
  const element = navigation.value
  if (!element) return
  if (!props.mobile) element.scrollTop = managementNavigationScroll.top
  const current = element.querySelector<HTMLElement>('[aria-current]')
  if (!current) return
  const bounds = element.getBoundingClientRect(), item = current.getBoundingClientRect()
  // Scroll only the navigation, leaving the business page's saved scroll intact.
  if (item.top < bounds.top) element.scrollTop += item.top - bounds.top
  else if (item.bottom > bounds.bottom) element.scrollTop += item.bottom - bounds.bottom
}
watch(() => route.path, () => { hideTooltip(); void restoreScroll() })
watch(() => props.collapsed, () => { hideTooltip(); void restoreScroll() })
onMounted(() => { void restoreScroll(); window.addEventListener('resize', hideTooltip) })
onBeforeUnmount(() => { rememberScroll(); window.removeEventListener('resize', hideTooltip) })
</script>

<template>
  <nav v-bind="$attrs" ref="navigation" class="management-navigation" :class="{ 'is-collapsed': collapsed && !mobile, 'is-mobile': mobile }" aria-label="功能导航" @scroll.passive="onScroll" @keydown.esc="hideTooltip">
    <section v-for="group in groups" :key="group.label" class="management-nav-group" :aria-label="group.label">
      <p class="management-nav-label">{{ group.label }}</p>
      <div v-for="link in group.links" :key="link.to" class="management-nav-item" :data-section="link.to">
        <div class="management-nav-row ambient-glow" :class="{ 'is-section-active': currentCategory?.to === link.to }">
          <span class="management-nav-marker" aria-hidden="true" />
          <RouterLink :to="link.to" class="management-nav-link" :title="collapsed ? undefined : link.label" :aria-label="link.label" :aria-describedby="tooltip?.label === link.label ? tooltipID : undefined" :aria-current="route.path === link.to ? 'page' : currentCategory?.to === link.to ? 'location' : undefined" @mouseenter="showTooltip($event, link.label)" @mouseleave="hideTooltip" @focus="showTooltip($event, link.label)" @blur="hideTooltip" @click="hideTooltip">
            <NavigationIcon :name="link.icon" :stroke="collapsed && !mobile && currentCategory?.to === link.to ? 2.25 : 1.75" /><span>{{ link.label }}</span>
          </RouterLink>
        </div>
      </div>
    </section>
  </nav>
  <Teleport to="body">
    <span v-if="tooltip" :id="tooltipID" role="tooltip" class="management-nav-tooltip" :style="{ left: `${tooltip.left}px`, top: `${tooltip.top}px` }">{{ tooltip.label }}</span>
  </Teleport>
</template>

<style scoped>
.management-navigation { min-height: 0; flex: 1; overflow-x: hidden; overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin; padding: 14px 10px 16px; color: rgb(var(--color-slate-600)); }
.management-nav-group + .management-nav-group { margin-top: 18px; }
.management-nav-label { margin: 0 12px 8px; color: rgb(var(--color-slate-500)); font-size: 11px; font-weight: 500; }
.management-nav-item + .management-nav-item { margin-top: 4px; }
.management-nav-row { display: flex; min-width: 0; border-radius: 11px; }
.management-nav-marker { position: absolute; z-index: 2; left: 2px; top: 50%; width: 2px; height: 18px; border-radius: 2px; background: #2563eb; opacity: 0; transform: translateY(-50%) scaleY(0); transform-origin: center; transition: transform 180ms ease-out, opacity 180ms ease-out; pointer-events: none; }
.management-nav-row:has(:focus-visible) > .management-nav-marker { opacity: 1; transform: translateY(-50%) scaleY(1); }
.dark .management-nav-marker { background: #93c5fd; }
.management-nav-link { display: flex; flex: 1; min-width: 0; min-height: 44px; align-items: center; gap: 10px; padding: 0 12px; border-radius: 11px; font-size: 13px; font-weight: 500; }
.management-nav-link > svg { width: 19px; height: 19px; flex-shrink: 0; }
.management-nav-link > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.management-nav-row.is-section-active { background: #dfe9fb; color: #2456a6; }
.management-nav-row.is-section-active > .management-nav-marker { background: #2563eb; }
.management-navigation .ambient-glow { --ambient-glow-strength: .45; --ambient-glow-step: 1px; --ambient-glow-wide-step: 1px; }
.management-nav-link:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; }
.management-navigation:not(.is-mobile) .management-nav-link { gap: 18px; padding-inline: 12.5px; }
.management-navigation:not(.is-mobile) .management-nav-row { transition: color 180ms ease-out, background-color 180ms ease-out; }
.management-navigation:not(.is-mobile) .management-nav-link > svg { transition: stroke-width 150ms ease-out, transform 180ms ease-out; }
.management-navigation:not(.is-mobile) .management-nav-link > span { min-width: 0; opacity: 1; visibility: visible; transition: opacity 150ms linear 100ms, visibility 0s; }
.management-navigation:not(.is-mobile) .management-nav-label { height: 16px; overflow: hidden; line-height: 16px; opacity: 1; visibility: visible; transition: height var(--management-sidebar-motion), margin-bottom var(--management-sidebar-motion), opacity 150ms linear 100ms, visibility 0s; }
.management-navigation.is-collapsed { scrollbar-width: none; }
.is-collapsed::-webkit-scrollbar { display: none; }
.management-navigation.is-collapsed .management-nav-label { height: 0; margin-bottom: 0; opacity: 0; visibility: hidden; transition: height var(--management-sidebar-motion), margin-bottom var(--management-sidebar-motion), opacity 100ms linear, visibility 0s linear 100ms; }
.management-navigation.is-collapsed .management-nav-link > span { opacity: 0; visibility: hidden; transition: opacity 100ms linear, visibility 0s linear 100ms; }
.is-collapsed .management-nav-group + .management-nav-group { margin-top: 18px; }
.management-nav-tooltip { position: fixed; z-index: 60; transform: translateY(-50%); max-width: 220px; padding: 7px 11px; border-radius: 8px; background: #263346; color: #fff; box-shadow: 0 4px 14px rgb(15 23 42 / 12%); font-size: 12px; line-height: 20px; pointer-events: none; }
.dark .management-navigation { color: rgb(var(--color-slate-300)); }.dark .management-nav-label { color: rgb(var(--color-slate-500)); }.dark .management-nav-row.is-section-active { background: #293b56; color: #b9d3ff; }
.dark .management-nav-tooltip { background: #dce5f3; color: #243247; }
@media (hover: hover) { .management-navigation:not(.is-collapsed) .management-nav-row:hover { background-color: rgb(116 143 180 / 9%); }.management-navigation:not(.is-collapsed) .management-nav-row.is-section-active:hover { background-color: #d5e3fa; }.dark .management-navigation:not(.is-collapsed) .management-nav-row.is-section-active:hover { background-color: #314665; } }
@media (hover: hover) and (pointer: fine) { .management-nav-row:hover > .management-nav-marker { opacity: 1; transform: translateY(-50%) scaleY(1); } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .management-nav-link > span { transform-origin: left center; }
  .management-nav-link > svg { transform-origin: center; transition: transform 180ms ease-out; }
  .management-nav-row:hover .management-nav-link > span { transform: scale(1.035); }
  .management-nav-row:hover .management-nav-link > svg { transform: scale(1.06); }
}
.management-navigation.is-collapsed .management-nav-row,
.dark .management-navigation.is-collapsed .management-nav-row { background: transparent; box-shadow: none; }
.management-navigation.is-collapsed .management-nav-row:not(.is-section-active) { color: rgb(var(--color-slate-500)); }
.dark .management-navigation.is-collapsed .management-nav-row:not(.is-section-active) { color: rgb(var(--color-slate-400)); }
.management-navigation.is-collapsed .management-nav-row.is-section-active { color: #2563eb; }
.dark .management-navigation.is-collapsed .management-nav-row.is-section-active { color: #60a5fa; }
.is-collapsed .management-nav-marker, .is-collapsed .management-nav-row::before, .is-collapsed .management-nav-row::after { display: none; }
.management-navigation.is-collapsed .management-nav-link:focus-visible { outline-color: #3b82f6; outline-offset: -4px; }
.dark .management-navigation.is-collapsed .management-nav-link:focus-visible { outline-color: #60a5fa; }
@media (hover: hover) {
  .management-navigation.is-collapsed .management-nav-row:not(.is-section-active):hover { color: rgb(var(--color-slate-800)); }
  .dark .management-navigation.is-collapsed .management-nav-row:not(.is-section-active):hover { color: rgb(var(--color-slate-100)); }
  .management-navigation.is-collapsed .management-nav-row.is-section-active:hover { color: #1d4ed8; }
  .dark .management-navigation.is-collapsed .management-nav-row.is-section-active:hover { color: #93c5fd; }
}
@media (prefers-reduced-motion: reduce) {
  .management-navigation:not(.is-mobile) .management-nav-label, .management-navigation:not(.is-mobile) .management-nav-link > span, .management-nav-marker, .management-nav-link > svg { transition: none; }
  .management-navigation:not(.is-mobile) .management-nav-row, .management-navigation:not(.is-mobile) .management-nav-link > svg { transition: none; }
}
</style>
