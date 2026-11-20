<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

const props = defineProps<{
  label: string
  active: string | symbol | null | undefined
  tabs: readonly { name: string; label: string; to: RouteLocationRaw }[]
  compact?: boolean
}>()

const navigation = ref<HTMLElement>()
const track = ref<HTMLElement>()
const indicator = shallowRef<{ left: number; width: number; animate: boolean }>()
let observer: ResizeObserver | undefined
const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)')

function alignCurrent(animate: boolean) {
  const scroller = navigation.value
  const row = track.value
  if (!scroller?.clientWidth || !row) return

  const active = row.querySelector<HTMLElement>('[aria-current="page"]')
  if (!active) {
    indicator.value = undefined
    return
  }

  const label = active.querySelector<HTMLElement>('.route-tab-label')
  if (!label) return
  // 固定槽位只决定中心，短线宽度对应完整图文及各侧 3px 留白。
  const center = active.offsetLeft + active.offsetWidth / 2
  const width = Math.min(active.offsetWidth - 10, label.offsetWidth + 28)
  const left = center - width / 2
  const smooth = animate && !reduceMotion.matches
  const previous = indicator.value
  if (!previous || previous.left !== left || previous.width !== width) {
    indicator.value = { left, width, animate: smooth && !!previous }
  }
  scroller.scrollTo({
    left: Math.max(0, center - scroller.clientWidth / 2),
    behavior: smooth ? 'smooth' : 'instant',
  })
}

watch(() => props.active, () => alignCurrent(true), { flush: 'post' })
watch(() => props.compact, () => alignCurrent(false), { flush: 'post' })
onMounted(() => {
  alignCurrent(false)
  observer = new ResizeObserver(() => alignCurrent(false))
  if (navigation.value) observer.observe(navigation.value)
  if (track.value) observer.observe(track.value)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <nav ref="navigation" class="route-tabs" :class="{ 'route-tabs-compact': compact }" :aria-label="label">
    <div ref="track" class="route-tab-track">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.name"
        :to="tab.to"
        class="route-tab"
        :aria-current="active === tab.name ? 'page' : undefined"
      >
        <span class="route-tab-label" :data-label="tab.label">
          <span>{{ tab.label }}</span>
          <span class="route-tab-icon" aria-hidden="true"><slot name="icon" :tab="tab" /></span>
        </span>
      </RouterLink>
      <span
        v-if="indicator"
        class="route-tab-indicator"
        :class="{ 'route-tab-indicator-animated': indicator.animate }"
        :style="{ transform: `translateX(${indicator.left}px)`, width: `${indicator.width}px` }"
        aria-hidden="true"
      />
    </div>
  </nav>
</template>

<style scoped>
.route-tabs {
  --route-tab-slot-width: 120px;
  --route-tab-travel: 11px;
  --route-tab-duration: 280ms;
  --route-tab-ease: cubic-bezier(.22, .71, .29, 1);
  overflow-x: auto;
  overscroll-behavior-inline: contain;
  scrollbar-width: none;
}
.route-tabs-compact {
  --route-tab-slot-width: 106px;
  border-bottom: 1px solid rgb(var(--line-default) / .7);
}
.route-tab-track {
  position: relative;
  display: flex;
  align-items: center;
  width: max-content;
}
.route-tab {
  position: relative;
  display: inline-flex;
  flex: none;
  justify-content: center;
  align-items: center;
  width: var(--route-tab-slot-width);
  min-height: 46px;
  color: rgb(var(--content-secondary));
  font-size: 14px;
  white-space: nowrap;
  transition: color var(--route-tab-duration) var(--route-tab-ease);
}
.route-tab[aria-current="page"] {
  color: rgb(var(--selection-content));
  font-weight: 600;
}
.route-tab:focus-visible {
  outline: 2px solid rgb(var(--focus));
  outline-offset: -2px;
  border-radius: 4px;
}
.route-tab-label {
  position: relative;
  display: inline-grid;
  transition: transform var(--route-tab-duration) var(--route-tab-ease);
}
.route-tab-label > span:first-child,
.route-tab-label::after {
  grid-area: 1 / 1;
  text-align: center;
}
.route-tab-label::after {
  content: attr(data-label);
  visibility: hidden;
  font-weight: 600;
}
.route-tab[aria-current="page"] .route-tab-label {
  transform: translateX(var(--route-tab-travel));
}
.route-tab-icon {
  position: absolute;
  inset: 50% auto auto 0;
  width: 16px;
  height: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transform: translateY(-50%) scale(.68);
  transition: opacity 105ms ease, transform var(--route-tab-duration) var(--route-tab-ease);
}
.route-tab[aria-current="page"] .route-tab-icon {
  opacity: 1;
  transform: translate(calc(-2 * var(--route-tab-travel)), -50%) scale(1);
  transition: opacity 145ms ease 45ms, transform var(--route-tab-duration) var(--route-tab-ease);
}
.route-tab-indicator {
  position: absolute;
  inset: auto auto 0 0;
  height: 2.5px;
  border-radius: 2px;
  pointer-events: none;
  background: rgb(var(--selection-content));
}
.route-tab-indicator-animated {
  transition: transform var(--route-tab-duration) var(--route-tab-ease), width var(--route-tab-duration) var(--route-tab-ease);
}
@media (hover: hover) {
  .route-tab:hover { color: rgb(var(--selection-content)); }
}
@media (prefers-reduced-motion: reduce) {
  .route-tab,
  .route-tab-label,
  .route-tab-icon,
  .route-tab[aria-current="page"] .route-tab-icon,
  .route-tab-indicator-animated { transition: none; }
}
</style>
