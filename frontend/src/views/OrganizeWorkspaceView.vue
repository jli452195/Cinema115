<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import RouteTabs from '../components/RouteTabs.vue'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()

const sections = [
  { name: 'media-organize', label: '整理规则' },
  { name: 'media-organize-history', label: '整理记录' },
  { name: 'naming-settings', label: '命名模板' },
  { name: 'classification-settings', label: '分类规则' },
  { name: 'recognition-settings', label: '识别词表' },
]

const route = useRoute()
const router = useRouter()
const standaloneQuery = window.matchMedia('(display-mode: standalone)')
const phoneQuery = window.matchMedia('(max-width: 639px)')
const pwa = ref(standaloneQuery.matches || phoneQuery.matches)
function updateDisplayMode() {
  pwa.value = standaloneQuery.matches || phoneQuery.matches
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

const historyLocation = ref('/media/organize/history')
const tabs = computed(() => sections.map(section => ({
  ...section,
  to: route.name === section.name ? route.fullPath
    : section.name === 'media-organize-history' ? historyLocation.value
      : { name: section.name },
})))
watch(() => route.fullPath, value => {
  if (route.name === 'media-organize-history') historyLocation.value = value
}, { immediate: true })

const activeIndex = computed(() => sections.findIndex(section => section.name === route.name))
const panelTransition = ref('organize-slide-next')
watch(activeIndex, (index, previous) => {
  panelTransition.value = index > previous ? 'organize-slide-next' : 'organize-slide-previous'
})

function deactivatePanel(element: Element) {
  if (!pwa.value) return
  element.setAttribute('inert', '')
  element.setAttribute('aria-hidden', 'true')
}

const swipeControls = 'a, button, label, input, textarea, select, summary, [contenteditable], [draggable="true"], [role="button"], [role="slider"], [role="option"], .cm-editor'
let swipe: { id: number; x: number; y: number; time: number; index: number } | undefined
let swipePending = false

function startSwipe(event: TouchEvent) {
  swipe = undefined
  const target = event.target
  if (!pwa.value || swipePending || event.touches.length !== 1 || !(target instanceof Element) || target.closest(swipeControls)) return
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
  const target = tabs.value[start.index + (deltaX < 0 ? 1 : -1)]
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
  <ManagementLayout :class="{ 'organize-pwa': pwa }" title="自动整理" description="管理目录监控、整理记录、命名模板、分类规则与识别词表。" @logout="$emit('logout')">
    <RouteTabs class="organize-sections" label="整理功能" :active="route.name" :tabs="tabs" :compact="pwa">
      <template #icon="{ tab }">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
          <template v-if="tab.name === 'media-organize'">
            <path d="M2 9V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H20a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-1" />
            <path d="M2 13h10m-3 3 3-3-3-3" />
          </template>
          <template v-else-if="tab.name === 'media-organize-history'">
            <path d="M16 22h2a2 2 0 0 0 2-2V7l-5-5H6a2 2 0 0 0-2 2v3" />
            <path d="M14 2v4a2 2 0 0 0 2 2h4" />
            <circle cx="8" cy="16" r="6" />
            <path d="M9.5 17.5 8 16.25V14" />
          </template>
          <template v-else-if="tab.name === 'naming-settings'">
            <path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1" />
            <path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1" />
          </template>
          <template v-else-if="tab.name === 'classification-settings'">
            <path d="m15 5 6.3 6.3a2.4 2.4 0 0 1 0 3.4L17 19" />
            <path d="M9.586 5.586A2 2 0 0 0 8.172 5H3a1 1 0 0 0-1 1v5.172a2 2 0 0 0 .586 1.414L8.29 18.29a2.426 2.426 0 0 0 3.42 0l3.58-3.58a2.426 2.426 0 0 0 0-3.42z" />
            <circle cx="6.5" cy="9.5" r=".5" fill="currentColor" />
          </template>
          <template v-else-if="tab.name === 'recognition-settings'">
            <path d="M3 7V5a2 2 0 0 1 2-2h2m10 0h2a2 2 0 0 1 2 2v2m0 10v2a2 2 0 0 1-2 2h-2M7 21H5a2 2 0 0 1-2-2v-2M7 8h8m-8 4h10m-10 4h6" />
          </template>
        </svg>
      </template>
    </RouteTabs>
    <div
      class="organize-panels"
      @touchstart.passive="startSwipe"
      @touchmove.passive="moveSwipe"
      @touchend.passive="finishSwipe"
      @touchcancel.passive="cancelSwipe"
    >
      <RouterView v-slot="{ Component }">
        <Transition :name="panelTransition" :css="pwa" @before-leave="deactivatePanel">
          <div :key="route.name" class="organize-panel">
            <component :is="Component" :token="token" />
          </div>
        </Transition>
      </RouterView>
    </div>
  </ManagementLayout>
</template>

<style scoped>
.organize-sections { margin-bottom: 12px; }
.organize-pwa { --organize-page-inset: 1rem; }
.organize-pwa :deep(.management-content) {
  max-width: none;
  padding-inline: var(--organize-page-inset);
}
/* 滑动轨道贴边，每页内部保持原有边距，切换时不重新排版。 */
.organize-pwa :is(.organize-sections, .organize-panels) {
  margin-inline: calc(-1 * var(--organize-page-inset));
}
.organize-pwa .organize-panel {
  padding-inline: var(--organize-page-inset);
}
.organize-pwa .organize-panels {
  position: relative;
  overflow-x: clip;
}
.organize-panel:is(.organize-slide-next-enter-active, .organize-slide-previous-enter-active, .organize-slide-next-leave-active, .organize-slide-previous-leave-active) {
  transition: transform 320ms cubic-bezier(.22, .72, .25, 1);
}
.organize-panel:is(.organize-slide-next-leave-active, .organize-slide-previous-leave-active) {
  position: absolute;
  inset: 0 0 auto;
  pointer-events: none;
}
.organize-slide-next-enter-from,
.organize-slide-previous-leave-to {
  transform: translateX(100%);
}
.organize-slide-previous-enter-from,
.organize-slide-next-leave-to {
  transform: translateX(-100%);
}
@media (min-width: 640px) {
  .organize-pwa { --organize-page-inset: 2rem; }
}
@media (min-width: 1024px) {
  .organize-pwa { --organize-page-inset: 2.5rem; }
}
@media (max-width: 639px) {
  .organize-pwa {
    --workbench-toolbar-height: max(52px, 3.25rem);
    --workbench-header-height: calc(var(--workbench-toolbar-height) + var(--app-safe-top));
  }
  .organize-pwa :deep(.management-shell) {
    --management-content-top: calc(var(--workbench-header-height) + 1px);
  }
  /* 顶栏和标签使用同一底色，避免渐隐层盖住标签或露出滚动中的正文。 */
  .organize-pwa :deep(header.workbench-header),
  .organize-sections {
    background: rgb(var(--app-chrome-color));
  }
  .organize-pwa :deep(header.workbench-header::before),
  .organize-pwa :deep(header.workbench-header::after),
  .organize-pwa :deep(header.workbench-header :is(.header-surface, .header-background)) {
    display: none;
  }
  :deep(.management-heading) { margin-bottom: 0; }
  :deep(.management-description) { display: none; }
  .organize-sections {
    position: sticky;
    top: var(--management-content-top);
    z-index: 30;
    margin-bottom: 10px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .organize-panel:is(.organize-slide-next-enter-active, .organize-slide-previous-enter-active) {
    transform: none;
    transition: none;
  }
  .organize-panel[inert] { display: none; }
}
</style>
