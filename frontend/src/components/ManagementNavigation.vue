<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { adminAccountGroups, isAccountLinkActive } from '../utils/accountNavigation'
import { managementNavigationScroll } from '../composables/useManagementSidebar'
import NavigationIcon from './NavigationIcon.vue'

const props = defineProps<{ mobile?: boolean }>()
const route = useRoute(), navigation = ref<HTMLElement>()
const currentCategory = computed(() => adminAccountGroups.flatMap(group => group.links).find(link => isAccountLinkActive(route, link)))

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
watch(() => route.path, () => { void restoreScroll() })
onMounted(restoreScroll)
onBeforeUnmount(rememberScroll)
</script>

<template>
  <nav ref="navigation" class="management-navigation" aria-label="管理功能" @scroll.passive="rememberScroll">
    <section v-for="group in adminAccountGroups" :key="group.label" class="management-nav-group" :aria-label="group.label">
      <p class="management-nav-label">{{ group.label }}</p>
      <div v-for="link in group.links" :key="link.to" class="management-nav-item" :data-section="link.to">
        <div class="management-nav-row ambient-glow" :class="{ 'is-section-active': currentCategory?.to === link.to }">
          <span class="management-nav-marker" aria-hidden="true" />
          <RouterLink :to="link.to" class="management-nav-link" :title="link.label" :aria-label="link.label" :aria-current="route.path === link.to ? 'page' : currentCategory?.to === link.to ? 'location' : undefined">
            <NavigationIcon :name="link.icon" /><span>{{ link.label }}</span>
          </RouterLink>
        </div>
      </div>
    </section>
  </nav>
</template>

<style scoped>
.management-navigation { min-height: 0; flex: 1; overflow-x: hidden; overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin; padding: 0 8px 12px; color: rgb(var(--color-slate-600)); }
.management-nav-group + .management-nav-group { margin-top: 18px; }
.management-nav-label { margin: 0 12px 7px; color: rgb(var(--color-slate-500)); font-size: 11px; font-weight: 400; }
.management-nav-item + .management-nav-item { margin-top: 4px; }
.management-nav-row { display: flex; min-width: 0; border-radius: 11px; }
.management-nav-marker { position: absolute; z-index: 2; left: 2px; top: 50%; width: 2px; height: 18px; border-radius: 2px; background: #739bc9; opacity: 0; transform: translateY(-50%) scaleY(0); transform-origin: center; transition: transform 180ms ease-out, opacity 180ms ease-out; pointer-events: none; }
.management-nav-row:has(:focus-visible) > .management-nav-marker { opacity: 1; transform: translateY(-50%) scaleY(1); }
.dark .management-nav-marker { background: #94b8e5; }
.management-nav-link { display: flex; flex: 1; min-width: 0; min-height: 44px; align-items: center; gap: 10px; padding: 0 12px; border-radius: 11px; font-size: 13px; font-weight: 500; }
.management-nav-link > svg { width: 19px; height: 19px; flex-shrink: 0; }
.management-nav-link > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.management-nav-row.is-section-active { background: #e8eff9; color: #315e99; }
.management-navigation .ambient-glow { --ambient-glow-strength: .45; --ambient-glow-step: 1px; --ambient-glow-wide-step: 1px; }
.management-nav-link:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; }
.dark .management-navigation { color: rgb(var(--color-slate-300)); }.dark .management-nav-label { color: rgb(var(--color-slate-500)); }.dark .management-nav-row.is-section-active { background: rgb(104 150 210 / 13%); color: #bad2f4; }
@media (hover: hover) { .management-nav-row:hover { background-color: rgb(116 143 180 / 7%); }.management-nav-row.is-section-active:hover { background-color: rgb(116 155 210 / 17%); } }
@media (hover: hover) and (pointer: fine) { .management-nav-row:hover > .management-nav-marker { opacity: 1; transform: translateY(-50%) scaleY(1); } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .management-nav-link > span { transform-origin: left center; transition: transform 180ms ease-out; }
  .management-nav-link > svg { transform-origin: center; transition: transform 180ms ease-out; }
  .management-nav-row:hover .management-nav-link > span { transform: scale(1.035); }
  .management-nav-row:hover .management-nav-link > svg { transform: scale(1.06); }
}
@media (prefers-reduced-motion: reduce) { .management-nav-marker { transition: none; } }
</style>
