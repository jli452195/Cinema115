<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { adminAccountGroups, isAccountLinkActive } from '../utils/accountNavigation'
import { managementNavigationScroll } from '../composables/useManagementSidebar'
import NavigationIcon from './NavigationIcon.vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ mobile?: boolean; collapsed?: boolean }>()
const route = useRoute(), navigation = ref<HTMLElement>()
const currentCategory = computed(() => adminAccountGroups.flatMap(group => group.links).find(link => isAccountLinkActive(route, link)))
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
  <nav v-bind="$attrs" ref="navigation" class="management-navigation" :class="{ 'is-collapsed': collapsed && !mobile }" aria-label="管理功能" @scroll.passive="onScroll" @keydown.esc="hideTooltip">
    <section v-for="group in adminAccountGroups" :key="group.label" class="management-nav-group" :aria-label="group.label">
      <p class="management-nav-label">{{ group.label }}</p>
      <div v-for="link in group.links" :key="link.to" class="management-nav-item" :data-section="link.to">
        <div class="management-nav-row ambient-glow" :class="{ 'is-section-active': currentCategory?.to === link.to }">
          <span class="management-nav-marker" aria-hidden="true" />
          <RouterLink :to="link.to" class="management-nav-link" :title="collapsed ? undefined : link.label" :aria-label="link.label" :aria-describedby="tooltip?.label === link.label ? tooltipID : undefined" :aria-current="route.path === link.to ? 'page' : currentCategory?.to === link.to ? 'location' : undefined" @mouseenter="showTooltip($event, link.label)" @mouseleave="hideTooltip" @focus="showTooltip($event, link.label)" @blur="hideTooltip" @click="hideTooltip">
            <NavigationIcon :name="link.icon" /><span>{{ link.label }}</span>
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
.is-collapsed .management-nav-label, .is-collapsed .management-nav-link > span { display: none; }
.is-collapsed .management-nav-link { justify-content: center; gap: 0; padding: 0; }
.is-collapsed .management-nav-group + .management-nav-group { margin-top: 16px; padding-top: 16px; border-top: 1px solid rgb(var(--color-slate-300) / 55%); }
.management-nav-tooltip { position: fixed; z-index: 60; transform: translateY(-50%); max-width: 220px; padding: 7px 11px; border-radius: 8px; background: #263346; color: #fff; box-shadow: 0 4px 14px rgb(15 23 42 / 12%); font-size: 12px; line-height: 20px; pointer-events: none; }
.dark .management-navigation { color: rgb(var(--color-slate-300)); }.dark .management-nav-label { color: rgb(var(--color-slate-500)); }.dark .management-nav-row.is-section-active { background: #293b56; color: #b9d3ff; }
.dark .is-collapsed .management-nav-group + .management-nav-group { border-color: rgb(var(--color-slate-700) / 70%); }
.dark .management-nav-tooltip { background: #dce5f3; color: #243247; }
@media (hover: hover) { .management-nav-row:hover { background-color: rgb(116 143 180 / 9%); }.management-nav-row.is-section-active:hover { background-color: #d5e3fa; }.dark .management-nav-row.is-section-active:hover { background-color: #314665; } }
@media (hover: hover) and (pointer: fine) { .management-nav-row:hover > .management-nav-marker { opacity: 1; transform: translateY(-50%) scaleY(1); } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .management-nav-link > span { transform-origin: left center; transition: transform 180ms ease-out; }
  .management-nav-link > svg { transform-origin: center; transition: transform 180ms ease-out; }
  .management-nav-row:hover .management-nav-link > span { transform: scale(1.035); }
  .management-nav-row:hover .management-nav-link > svg { transform: scale(1.06); }
}
@media (prefers-reduced-motion: reduce) { .management-nav-marker { transition: none; } }
</style>
