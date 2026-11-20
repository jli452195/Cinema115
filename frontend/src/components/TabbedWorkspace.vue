<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import ManagementLayout from './ManagementLayout.vue'
import RouteTabs, { type RouteTab } from './RouteTabs.vue'

const props = defineProps<{
  title: string
  description: string
  label: string
  tabs: readonly RouteTab[]
  fillTabsOnMobile?: boolean
  mobileBackToOrigin?: boolean
}>()
defineEmits<{ logout: [] }>()

const route = useRoute()
const router = useRouter()
const standaloneQuery = window.matchMedia('(display-mode: standalone)')
const phoneQuery = window.matchMedia('(max-width: 639px)')
const compact = ref(standaloneQuery.matches || phoneQuery.matches)
function updateDisplayMode() {
  compact.value = standaloneQuery.matches || phoneQuery.matches
  cancelSwipe()
}
onMounted(() => {
  standaloneQuery.addEventListener('change', updateDisplayMode)
  phoneQuery.addEventListener('change', updateDisplayMode)
})
onBeforeUnmount(() => {
  standaloneQuery.removeEventListener('change', updateDisplayMode)
  phoneQuery.removeEventListener('change', updateDisplayMode)
})

const activeIndex = computed(() => props.tabs.findIndex(tab => tab.name === route.name))
const panelTransition = ref('workspace-slide-next')
watch(activeIndex, (index, previous) => {
  panelTransition.value = index > previous ? 'workspace-slide-next' : 'workspace-slide-previous'
})

function deactivatePanel(element: Element) {
  if (!compact.value) return
  element.setAttribute('inert', '')
  element.setAttribute('aria-hidden', 'true')
}

const swipeControls = 'a, button, label, input, textarea, select, summary, [contenteditable], [draggable="true"], [data-sortable-item], [data-drag-handle], [role="button"], [role="slider"], [role="option"], .cm-editor'
let swipe: { id: number; x: number; y: number; time: number; index: number } | undefined
let swipePending = false

function startSwipe(event: TouchEvent) {
  swipe = undefined
  const target = event.target
  if (!compact.value || swipePending || event.touches.length !== 1 || !(target instanceof Element) || target.closest(swipeControls)) return
  const touch = event.touches[0]
  if (touch.clientX < 20 || touch.clientX > window.innerWidth - 20) return

  // 保留横向容器自身的触摸滚动。
  for (let node: Element | null = target; node && node !== event.currentTarget; node = node.parentElement) {
    if (node.scrollWidth > node.clientWidth && ['auto', 'scroll'].includes(getComputedStyle(node).overflowX)) return
  }
  swipe = { id: touch.identifier, x: touch.clientX, y: touch.clientY, time: event.timeStamp, index: activeIndex.value }
}

function moveSwipe(event: TouchEvent) {
  if (!swipe) return
  const touch = event.touches[0]
  if (event.touches.length !== 1 || !touch || touch.identifier !== swipe.id) {
    swipe = undefined
    return
  }
  const deltaY = Math.abs(touch.clientY - swipe.y)
  if (deltaY > 12 && deltaY >= Math.abs(touch.clientX - swipe.x)) swipe = undefined
}

function cancelSwipe() {
  swipe = undefined
}

async function finishSwipe(event: TouchEvent) {
  const start = swipe
  cancelSwipe()
  if (!start || event.touches.length || start.index !== activeIndex.value || event.timeStamp - start.time > 700) return
  const touch = Array.from(event.changedTouches).find(item => item.identifier === start.id)
  if (!touch) return
  const deltaX = touch.clientX - start.x
  if (Math.abs(deltaX) < 56 || Math.abs(deltaX) <= Math.abs(touch.clientY - start.y) * 1.5) return
  const target = props.tabs[start.index + (deltaX < 0 ? 1 : -1)]
  if (!target) return

  // 手势与标签点击共用路由守卫，确认离开后才触发切页动画。
  swipePending = true
  try {
    await router.push(target.to)
  } finally {
    swipePending = false
  }
}
</script>

<template>
  <ManagementLayout
    :class="{ 'tabbed-workspace-compact': compact }"
    :title="title"
    :description="description"
    :mobile-back-to-origin="mobileBackToOrigin"
    @logout="$emit('logout')"
  >
    <RouteTabs class="workspace-tabbar" :label="label" :active="route.name" :tabs="tabs" :compact="compact" :fill-on-mobile="fillTabsOnMobile">
      <template #icon="{ tab }"><slot name="icon" :tab="tab" /></template>
    </RouteTabs>
    <div
      class="workspace-panels"
      @touchstart.passive="startSwipe"
      @touchmove.passive="moveSwipe"
      @touchend.passive="finishSwipe"
      @touchcancel.passive="cancelSwipe"
    >
      <RouterView v-slot="{ Component }">
        <Transition :name="panelTransition" :css="compact" @before-leave="deactivatePanel">
          <div :key="route.name" class="workspace-panel">
            <slot :component="Component"><component :is="Component" /></slot>
          </div>
        </Transition>
      </RouterView>
    </div>
  </ManagementLayout>
</template>

<style scoped>
.workspace-tabbar { margin-bottom: 12px; }
.tabbed-workspace-compact { --workspace-page-inset: 1rem; }
.tabbed-workspace-compact :deep(.management-content) {
  max-width: none;
  padding-inline: var(--workspace-page-inset);
}
/* 滑动轨道贴边，每页内部保持原有边距，切换时不重新排版。 */
.tabbed-workspace-compact :is(.workspace-tabbar, .workspace-panels) {
  margin-inline: calc(-1 * var(--workspace-page-inset));
}
.tabbed-workspace-compact .workspace-panel {
  padding-inline: var(--workspace-page-inset);
}
.tabbed-workspace-compact .workspace-panels {
  position: relative;
  overflow-x: clip;
}
.workspace-panel:is(.workspace-slide-next-enter-active, .workspace-slide-previous-enter-active, .workspace-slide-next-leave-active, .workspace-slide-previous-leave-active) {
  transition: transform 320ms cubic-bezier(.22, .72, .25, 1);
}
.workspace-panel:is(.workspace-slide-next-leave-active, .workspace-slide-previous-leave-active) {
  position: absolute;
  inset: 0 0 auto;
  pointer-events: none;
}
.workspace-slide-next-enter-from,
.workspace-slide-previous-leave-to {
  transform: translateX(100%);
}
.workspace-slide-previous-enter-from,
.workspace-slide-next-leave-to {
  transform: translateX(-100%);
}
@media (min-width: 640px) {
  .tabbed-workspace-compact { --workspace-page-inset: 2rem; }
}
@media (min-width: 1024px) {
  .tabbed-workspace-compact { --workspace-page-inset: 2.5rem; }
}
@media (max-width: 639px) {
  .tabbed-workspace-compact {
    --workbench-toolbar-height: max(52px, 3.25rem);
    --workbench-header-height: calc(var(--workbench-toolbar-height) + var(--app-safe-top));
  }
  .tabbed-workspace-compact :deep(.management-shell) {
    --management-content-top: calc(var(--workbench-header-height) + 1px);
  }
  /* 顶栏和标签使用同一底色，避免渐隐层盖住标签或露出滚动中的正文。 */
  .tabbed-workspace-compact :deep(header.workbench-header),
  .workspace-tabbar {
    background: rgb(var(--app-chrome-color));
  }
  .tabbed-workspace-compact :deep(header.workbench-header::before),
  .tabbed-workspace-compact :deep(header.workbench-header::after),
  .tabbed-workspace-compact :deep(header.workbench-header :is(.header-surface, .header-background)) {
    display: none;
  }
  .tabbed-workspace-compact :deep(.management-heading) { margin-bottom: 0; }
  .tabbed-workspace-compact :deep(.management-description) { display: none; }
  .workspace-tabbar {
    position: sticky;
    top: var(--management-content-top);
    z-index: 30;
    margin-bottom: 10px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .workspace-panel:is(.workspace-slide-next-enter-active, .workspace-slide-previous-enter-active) {
    transform: none;
    transition: none;
  }
  .workspace-panel[inert] { display: none; }
}
</style>
